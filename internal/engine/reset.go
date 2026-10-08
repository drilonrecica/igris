package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// applyResets applies every pending reset signal (SPEC §6.2): the task is
// put back to ready/blocked by readiness. Resetting the current task (or,
// at start, the interrupted one) also closes its session without the idle
// wait, clears it from state.json and turns pause-after-task on. current
// reports that the current task was reset; the caller then lets go of it.
//
// It runs at the engine's polls, never in the middle of a verify or a
// commit. A plan that can't be read leaves the signals pending.
func (e *Engine) applyResets(ctx context.Context) (current bool, err error) {
	sigs, _, err := e.dir.ListSignals()
	if err != nil {
		return false, nil // scanSignals reports it
	}
	for _, s := range sigs {
		if s.Action != state.ActionReset {
			continue
		}
		cur, err := e.applyReset(ctx, s)
		if err != nil {
			return current, err
		}
		current = current || cur
	}
	return current, nil
}

func (e *Engine) applyReset(ctx context.Context, s state.Signal) (current bool, err error) {
	p, err := e.loadPlan()
	if err != nil {
		e.reportOnce("reset|"+err.Error(), Warning, fmt.Sprintf("the reset of %s waits until the plan is valid: %v", s.ID, err))
		return false, nil
	}
	t := p.Task(s.ID)
	if t == nil {
		e.warn(fmt.Sprintf("dropped the reset of %s: the task is not in the plan", s.ID))
		return false, e.dir.RemoveSignal(s.ID)
	}
	// The status is checked again: it may have changed since `igris reset`.
	switch ok, err := plan.Resettable(t, s.Force); {
	case err != nil:
		e.warn(fmt.Sprintf("not reset: %v", err))
		return false, e.dir.RemoveSignal(s.ID)
	case !ok:
		e.warn(fmt.Sprintf("%s is %s: nothing to reset", t.ID, t.Status))
		return false, e.dir.RemoveSignal(s.ID)
	}

	current = e.run.Current != nil && e.run.Current.TaskID == t.ID
	model := ""
	if current {
		// Closed first, then forgotten, then rewritten: a crash in between
		// leaves the task in progress with its reset still pending, which
		// the next run applies before anything else.
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
	if err := e.dir.RemoveSignal(t.ID); err != nil {
		return false, err
	}
	detail := "nothing to change"
	if len(changes) > 0 {
		detail = changes[0].String()
	}
	e.emit(Event{Kind: TaskReset, Task: t.ID, Title: t.Title, Rank: t.Rank, Detail: detail, Changes: changes})
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
	s, err := e.dir.ReadSignal(id)
	return err == nil && s != nil && s.Action == state.ActionReset
}
