package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// resetAsk is a reset request the owner was asked to confirm (SPEC §6.2).
// Sessions can write into .igris/signals/ too, so a reset signal is only a
// request while igris runs: it is applied once the owner says yes.
type resetAsk struct {
	sig      state.Signal
	answered bool
	yes      bool
}

// resets handles the pending reset signals at one of the engine's polls
// (SPEC §6.2). A new request is checked against the plan and put to the
// owner (QuestionConfirmReset); a request whose file is gone or was
// replaced is withdrawn; a declined one is dropped. With apply, the
// confirmed ones are applied: the task is put back to ready/blocked by
// readiness, and resetting the current task (or, at start, the interrupted
// one) also closes its session without the idle wait, clears it from
// state.json and turns pause-after-task on. current reports that the
// current task was reset; the caller then lets go of it.
//
// Nothing is applied in the middle of a verify or a commit: the callers
// that may not apply pass apply false, and the request waits. A plan that
// can't be read leaves the requests pending.
func (e *Engine) resets(ctx context.Context, apply bool) (current bool, err error) {
	sigs, _, err := e.dir.ListSignals()
	if err != nil {
		return false, nil // scanSignals reports it
	}
	pending := map[string]state.Signal{}
	for _, s := range sigs {
		if s.Action == state.ActionReset {
			pending[s.ID] = s
		}
	}
	kept := e.resetAsks[:0]
	for _, a := range e.resetAsks {
		if s, ok := pending[a.sig.ID]; ok && s.At.Equal(a.sig.At) && s.Force == a.sig.Force {
			kept = append(kept, a)
			delete(pending, a.sig.ID)
			continue
		}
		// Gone, or replaced by a newer request, which is asked about below.
		e.emit(Event{Kind: ResetDropped, Task: a.sig.ID, Detail: "the reset request for " + a.sig.ID + " was withdrawn"})
	}
	e.resetAsks = kept
	for _, s := range sigs {
		if _, ok := pending[s.ID]; ok && s.Action == state.ActionReset {
			if err := e.askReset(ctx, s); err != nil {
				return false, err
			}
		}
	}
	// An answer sent at once (or long ago) counts at this poll.
	e.takeResetAnswers()

	kept = e.resetAsks[:0]
	var confirmed []state.Signal
	for _, a := range e.resetAsks {
		switch {
		case !a.answered:
			kept = append(kept, a)
		case !a.yes:
			if err := e.dir.RemoveReset(a.sig.ID); err != nil {
				return false, err
			}
			e.emit(Event{Kind: ResetDropped, Task: a.sig.ID, Detail: "not reset: you declined; " + a.sig.ID + " is left as it is"})
		case apply:
			confirmed = append(confirmed, a.sig)
		default:
			kept = append(kept, a) // confirmed; applied at a later poll
		}
	}
	e.resetAsks = kept
	for _, s := range confirmed {
		cur, err := e.applyReset(ctx, s)
		if err != nil {
			return current, err
		}
		current = current || cur
	}
	return current, nil
}

// askReset checks a new reset request and asks the owner to confirm it. A
// request for a task that isn't in the plan, or has nothing to reset, is
// reported and dropped without a question.
func (e *Engine) askReset(ctx context.Context, s state.Signal) error {
	p, err := e.loadPlan()
	if err != nil {
		e.reportOnce("reset|"+err.Error(), Warning, fmt.Sprintf("the reset of %s waits until the plan is valid: %v", s.ID, err))
		return nil
	}
	t := p.Task(s.ID)
	if ok, err := e.resettable(t, s); !ok || err != nil {
		return err
	}
	e.resetAsks = append(e.resetAsks, &resetAsk{sig: s})
	what := "reset " + t.ID + " (" + t.Status.String() + ") to ready/blocked"
	if s.Force {
		what += " (forced)"
	}
	if e.run.Current != nil && e.run.Current.TaskID == t.ID {
		what += "; its session is closed and the run pauses"
	}
	e.emit(Event{Kind: Asked, Question: QuestionConfirmReset, Task: t.ID, Title: t.Title, Rank: t.Rank, Phase: phaseOf(t),
		Detail: what + "? `igris reset` asks for it, but a session can write the request too: confirm only if you ran it"})
	e.toast(ctx, notifyNeedsInput, "a reset of "+t.ID+" waits for your confirmation")
	return nil
}

// resettable checks s against t, its task in the plan as read now (nil if
// it isn't there). A request that can't be applied is reported and its
// signal removed; ok is false then.
func (e *Engine) resettable(t *plan.Task, s state.Signal) (ok bool, err error) {
	var why string
	phase := ""
	if t == nil {
		why = fmt.Sprintf("dropped the reset of %s: the task is not in the plan", s.ID)
	} else {
		phase = phaseOf(t)
		// The status is checked again: it may have changed since `igris reset`.
		switch ok, err := plan.Resettable(t, s.Force); {
		case err != nil:
			why = fmt.Sprintf("not reset: %v", err)
		case !ok:
			why = fmt.Sprintf("%s is %s: nothing to reset", t.ID, t.Status)
		default:
			return true, nil
		}
	}
	// Named explicitly: the event is about that task, not the current one.
	e.emit(Event{Kind: Warning, Task: s.ID, Phase: phase, Detail: why})
	return false, e.dir.RemoveReset(s.ID)
}

// takeResetAnswers records the owner's answers to reset requests: each
// names its task, or "" for the oldest unanswered request.
func (e *Engine) takeResetAnswers() {
	e.mu.Lock()
	queue := e.queue[:0]
	for _, c := range e.queue {
		if isResetAnswer(c) {
			e.resetAnswers = append(e.resetAnswers, c)
		} else {
			queue = append(queue, c)
		}
	}
	e.queue = queue
	e.mu.Unlock()
	answers := e.resetAnswers
	e.resetAnswers = nil
	for _, c := range answers {
		var a *resetAsk
		for _, r := range e.resetAsks {
			if !r.answered && (c.Task == "" || r.sig.ID == c.Task) {
				a = r
				break
			}
		}
		if a == nil {
			e.reject(c, "no reset request waits for an answer")
			continue
		}
		a.answered, a.yes = true, c.Yes
	}
}

// resetWaiting reports whether a reset request waits for the owner's
// answer: for task id, or for any task when id is "".
func (e *Engine) resetWaiting(id string) bool {
	for _, a := range e.resetAsks {
		if !a.answered && (id == "" || a.sig.ID == id) {
			return true
		}
	}
	return false
}

// settleResets waits until no reset request for task id ("" = for any
// task) waits for an answer, then applies the confirmed ones. It runs
// before a task is accepted, so a reset requested while it was verified
// or committed is never lost behind its done, and at start, before the
// interrupted task is picked up again. current is as for resets; stopped
// reports that the run stopped while it waited.
func (e *Engine) settleResets(ctx context.Context, id string) (current, stopped bool, err error) {
	for {
		if e.stopping(ctx) {
			return false, true, nil
		}
		if _, err := e.resets(ctx, false); err != nil {
			return false, false, err
		}
		if !e.resetWaiting(id) {
			break
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
	current, err = e.resets(ctx, true)
	return current, false, err
}

// applyReset applies a confirmed reset request.
func (e *Engine) applyReset(ctx context.Context, s state.Signal) (current bool, err error) {
	p, err := e.loadPlan()
	if err != nil {
		e.reportOnce("reset|"+err.Error(), Warning, fmt.Sprintf("the reset of %s waits until the plan is valid: %v", s.ID, err))
		e.resetAsks = append(e.resetAsks, &resetAsk{sig: s, answered: true, yes: true})
		return false, nil
	}
	t := p.Task(s.ID)
	if ok, err := e.resettable(t, s); !ok || err != nil {
		return false, err
	}

	current = e.run.Current != nil && e.run.Current.TaskID == t.ID
	model := ""
	if current {
		// Closed first, then forgotten, then rewritten: a crash in between
		// leaves the task in progress with its reset still pending, which
		// the next run asks about before anything else.
		if e.task != nil {
			model = e.task.model
		}
		e.closeForReset(ctx)
		e.run.Current = nil
		if err := e.dir.SaveRun(e.run); err != nil {
			return false, err
		}
	}
	changes, err := e.writer.Update(ctx, func(p *plan.Plan) ([]plan.Change, error) {
		return p.Reset(t.ID, s.Force)
	})
	if err != nil {
		return false, err
	}
	e.plans.wrote(changes)
	e.log(state.Event{Type: state.EventTaskReset, Task: t.ID, Rank: t.Rank, Model: model, Detail: t.Status.String()})
	if err := e.dir.RemoveReset(t.ID); err != nil {
		return false, err
	}
	detail := "nothing to change"
	if len(changes) > 0 {
		detail = changes[0].String()
	}
	e.emit(Event{Kind: TaskReset, Task: t.ID, Title: t.Title, Rank: t.Rank, Phase: phaseOf(t), Detail: detail, Changes: changes})
	if current {
		e.task = nil
		if !e.pause {
			// Nothing relaunches the task behind the owner's back.
			e.pause = true
			e.emit(Event{Kind: PauseOn})
		}
	}
	return current, nil
}

// phaseOf is the ID of t's phase.
func phaseOf(t *plan.Task) string {
	if t.Phase == nil {
		return ""
	}
	return t.Phase.ID
}

// closeForReset closes the session of the task being reset, without the
// idle wait (SPEC §6.2). At start there is no launch yet: the interrupted
// task's session is reattached to be closed, if it is still there.
func (e *Engine) closeForReset(ctx context.Context) {
	id := e.run.Current.TaskID
	var sess backend.Session
	switch {
	case e.task != nil:
		if !e.task.lost {
			sess = e.task.sess
		}
	case e.run.Current.Session != nil && e.run.Current.Session.Backend == e.be.Name():
		s, err := e.be.Attach(ctx, *e.run.Current.Session)
		switch {
		case errors.Is(err, backend.ErrSessionGone):
		case err != nil:
			e.warn(fmt.Sprintf("reattach the session of %s to close it: %v; close its pane by hand", id, err))
		default:
			sess = s
		}
	case e.run.Current.Session != nil:
		e.warn(fmt.Sprintf("the session of %s ran on %s; close its pane by hand", id, e.run.Current.Session.Backend))
	}
	if sess == nil {
		return
	}
	if err := sess.Close(ctx); err != nil {
		e.warn(fmt.Sprintf("close the session of %s: %v; close its pane by hand", id, err))
	}
}

// resetPending reports whether a reset signal waits for task id.
func (e *Engine) resetPending(id string) bool {
	s, err := e.dir.ReadReset(id)
	return err == nil && s != nil
}
