package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// reattach picks up cur, a task the previous run was working on (SPEC §13):
// its live session is reattached, else its pending signal counts, else the
// owner chooses how to go on. The task is active afterwards; the phase loop
// drives it (runPhase).
func (e *Engine) reattach(ctx context.Context, cur *state.Current) error {
	p, err := e.loadPlan()
	if err != nil {
		return err
	}
	t := p.Task(cur.TaskID)
	switch {
	case t == nil:
		e.warn(fmt.Sprintf("the interrupted task %s is no longer in the plan; igris forgets it", cur.TaskID))
		return e.forget(cur.TaskID)
	case t.Status != plan.InProgress:
		// The owner settled it while igris was not running.
		e.warn(fmt.Sprintf("the interrupted task %s is %s in the plan now; igris leaves it", t.ID, t.Status))
		return e.forget(cur.TaskID)
	}
	if t.Phase != nil {
		e.phase = t.Phase.ID
	}
	if !e.rng.In(t) {
		e.warn(fmt.Sprintf("the interrupted task %s is outside this run's slice (%s); igris picks it up first", t.ID, e.rng.Selection))
	}
	l := &launch{t: t, cur: cur, mode: cur.Mode}
	e.round++
	e.addLaunch(l)
	e.focus(l)
	defer e.unfocus()
	if t.Owner.IsAgent() {
		var ok bool
		if l.model, ok = e.cfg.Models[t.Rank]; !ok {
			return fmt.Errorf("task %s: unknown model rank %q; add it to [models] in igris.toml", t.ID, t.Rank)
		}
	}
	e.logResumed(l, "")
	if !t.Owner.IsAgent() {
		e.emit(Event{Kind: TaskResumed, Detail: "user task"})
		e.yourTurn(ctx, l)
		return nil
	}

	movedFrom := ""
	if cur.Session != nil && cur.Session.Backend != e.be.Name() {
		// It ran on another backend (e.g. herdr, and igris now runs in
		// tmux): it can't be reattached from here (SPEC §11.3).
		movedFrom = cur.Session.Backend
	} else if cur.Session != nil {
		sess, err := e.be.Attach(ctx, *cur.Session, t.ID)
		switch {
		case errors.Is(err, backend.ErrSessionForeign):
			// state.json named a pane igris did not start for the task:
			// a session can write the file (SPEC §13). Never typed into.
			e.warn(fmt.Sprintf("the recorded session of %s was not started for it (%v); igris treats the session as gone", t.ID, err))
		case errors.Is(err, backend.ErrSessionGone):
		case err != nil:
			return fmt.Errorf("reattach the session of %s: %w; check that %s is running, then run `igris arise` again", t.ID, err, e.be.Name())
		default:
			if st, err := sess.State(ctx); err != nil || st != backend.Exited {
				l.sess = sess
				// The last run logged the usage up to its stop.
				e.startUsage(l, e.clock.Now())
				detail := "reattached to its session"
				held := false
				if cur.PendingPrompt != "" {
					// The session never got its task: it was held at a
					// startup prompt when the last run stopped. The
					// recorded text is only a marker: the prompt is built
					// again from the plan, since state.json is a
					// session's to write (SPEC §13).
					text, err := e.firstPrompt(l)
					if err != nil {
						return err
					}
					cur.PendingPrompt = text
					detail = "reattached to its session; its task prompt goes out once Claude Code is ready"
					if h, ok := sess.(backend.PromptHolder); ok {
						h.HoldPrompt(text)
						held = true // the clock starts at its delivery
					} else if err := sess.Prompt(ctx, text); err != nil && !errors.Is(err, backend.ErrSessionGone) {
						return fmt.Errorf("send the task prompt to %s: %w; its session is still open in %s: run `igris arise` to retry", t.ID, err, e.be.Name())
					}
				}
				if !held {
					l.startAttempt(e.clock.Now()) // elapsed time is not saved (SPEC §6.3)
				}
				// The reattached session, as SessionOpened carries a new
				// one: the run view opens it (SPEC §15.3).
				ev := Event{Kind: TaskResumed, Detail: detail, Session: new(backend.SessionRef)}
				*ev.Session = *cur.Session
				if backend.ValidClaudeSession(cur.ClaudeSession) { // state.json is untrusted
					ev.ClaudeSession = cur.ClaudeSession
				}
				e.emit(ev)
				return nil
			}
		}
	}
	// The session is gone. A signal it left is processed first (the next
	// step finds it); only without one does the owner have to choose.
	l.lost = true
	if sig := e.scanSignals(t, cur.StartedAt); sig != nil && state.Classify(*sig, t.ID, t.Owner) == state.Apply {
		e.emit(Event{Kind: TaskResumed, Detail: "its session is gone; processing its done signal"})
		return nil
	}
	if movedFrom != "" {
		e.emit(Event{Kind: TaskResumed, Detail: "its session ran on " + textsafe.Line(movedFrom) + ", igris now runs on " + e.be.Name() + "; it can't be reattached from here"})
	} else {
		e.emit(Event{Kind: TaskResumed, Detail: "its session is gone"})
	}
	e.lose(ctx, l)
	return nil
}

// adoptTask takes over t, a task the plan says is in progress although no
// run recorded it (e.g. state.json was deleted): there is no session to go
// back to, so the owner decides how to go on. parallel says the phase runs
// its tasks at once: the task takes a slot (SPEC §6.11).
func (e *Engine) adoptTask(ctx context.Context, t *plan.Task, parallel bool) error {
	l := &launch{t: t}
	l.cur = &state.Current{TaskID: t.ID, StartedAt: e.clock.Now()}
	if parallel {
		l.cur.Slot = e.freeSlot()
	}
	e.run.Activate(l.cur)
	if err := e.dir.SaveRun(e.run); err != nil {
		return err
	}
	e.addLaunch(l)
	e.focus(l)
	defer e.unfocus()
	if t.Owner.IsAgent() {
		var ok bool
		if l.model, ok = e.cfg.Models[t.Rank]; !ok {
			return fmt.Errorf("task %s: unknown model rank %q; add it to [models] in igris.toml", t.ID, t.Rank)
		}
	}
	e.logResumed(l, "no record of an earlier session")
	if !t.Owner.IsAgent() {
		e.emit(Event{Kind: TaskResumed, Detail: "user task"})
		e.yourTurn(ctx, l)
		return nil
	}
	e.emit(Event{Kind: TaskResumed, Detail: "in progress without a recorded session"})
	l.lost = true
	e.lose(ctx, l)
	return nil
}

// logResumed logs that this run picks up l's task: its first attempt in
// this run, with the conversation state.json recorded for it, if any.
func (e *Engine) logResumed(l *launch, detail string) {
	l.since = e.clock.Now()
	ev := taskInfo(state.Event{Type: state.EventTaskResumed, Detail: detail, Slot: l.cur.Slot}, l.t)
	if l.t.Owner.IsAgent() {
		e.nextAttempt(l)
		if s := l.cur.ClaudeSession; backend.ValidClaudeSession(s) {
			ev.Session = s
		}
	}
	e.log(ev)
}

// forget drops the interrupted task id from state.json.
func (e *Engine) forget(id string) error {
	e.run.Deactivate(id)
	e.dropLaunch(id)
	return e.dir.SaveRun(e.run)
}
