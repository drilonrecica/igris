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

// resume picks up the task the previous run was working on (SPEC §13): its
// pending signal first, then its live session, else the owner chooses how
// to go on. stopped reports that the run stopped meanwhile.
func (e *Engine) resume(ctx context.Context) (stopped bool, err error) {
	cur := e.run.Current
	p, err := e.loadPlan()
	if err != nil {
		return false, err
	}
	t := p.Task(cur.TaskID)
	switch {
	case t == nil:
		e.warn(fmt.Sprintf("the interrupted task %s is no longer in the plan; igris forgets it", cur.TaskID))
		return false, e.forget()
	case t.Status != plan.InProgress:
		// The owner settled it while igris was not running.
		e.warn(fmt.Sprintf("the interrupted task %s is %s in the plan now; igris leaves it", t.ID, t.Status))
		return false, e.forget()
	}
	if t.Phase != nil {
		e.phase = t.Phase.ID
	}
	if !e.rng.In(t) {
		e.warn(fmt.Sprintf("the interrupted task %s is outside this run's slice (%s); igris picks it up first", t.ID, e.rng.Selection))
	}
	l := &launch{t: t, cur: cur, mode: cur.Mode}
	e.task = l
	e.log(state.Event{Type: state.EventTaskResumed})
	if !t.Owner.IsAgent() {
		e.emit(Event{Kind: TaskResumed, Detail: "user task"})
		return e.yourTurn(ctx, l)
	}
	var ok bool
	if l.model, ok = e.cfg.Models[t.Rank]; !ok {
		return false, fmt.Errorf("task %s: unknown model rank %q; add it to [models] in igris.toml", t.ID, t.Rank)
	}

	movedFrom := ""
	if cur.Session != nil && cur.Session.Backend != e.be.Name() {
		// It ran on another backend (e.g. herdr, and igris now runs in
		// tmux): it can't be reattached from here (SPEC §11.3).
		movedFrom = cur.Session.Backend
	} else if cur.Session != nil {
		sess, err := e.be.Attach(ctx, *cur.Session)
		switch {
		case errors.Is(err, backend.ErrSessionGone):
		case err != nil:
			return false, fmt.Errorf("reattach the session of %s: %w; check that %s is running, then run `igris arise` again", t.ID, err, e.be.Name())
		default:
			if st, err := sess.State(ctx); err != nil || st != backend.Exited {
				l.sess = sess
				detail := "reattached to its session"
				held := false
				if cur.PendingPrompt != "" {
					// The session never got its task: it was held at a
					// startup prompt when the last run stopped.
					detail = "reattached to its session; its task prompt goes out once Claude Code is ready"
					if h, ok := sess.(backend.PromptHolder); ok {
						h.HoldPrompt(cur.PendingPrompt)
						held = true // the clock starts at its delivery
					} else if err := sess.Prompt(ctx, cur.PendingPrompt); err != nil && !errors.Is(err, backend.ErrSessionGone) {
						return false, fmt.Errorf("send the task prompt to %s: %w; its session is still open in %s: run `igris arise` to retry", t.ID, err, e.be.Name())
					}
				}
				if !held {
					l.startAttempt(e.clock.Now()) // elapsed time is not saved (SPEC §6.3)
				}
				e.emit(Event{Kind: TaskResumed, Detail: detail})
				return e.drive(ctx, l)
			}
		}
	}
	// The session is gone. A signal it left is processed first; only
	// without one does the owner have to choose.
	l.lost = true
	if sig := e.scanSignals(t, cur.StartedAt); sig != nil && state.Classify(*sig, t.ID, t.Owner) == state.Apply {
		e.emit(Event{Kind: TaskResumed, Detail: "its session is gone; processing its done signal"})
		return e.drive(ctx, l)
	}
	if movedFrom != "" {
		e.emit(Event{Kind: TaskResumed, Detail: "its session ran on " + textsafe.Line(movedFrom) + ", igris now runs on " + e.be.Name() + "; it can't be reattached from here"})
	} else {
		e.emit(Event{Kind: TaskResumed, Detail: "its session is gone"})
	}
	e.lose(ctx, l)
	return e.drive(ctx, l)
}

// adopt takes over a task the plan says is in progress although no run
// recorded it (e.g. state.json was deleted): there is no session to go
// back to, so the owner decides how to go on.
func (e *Engine) adopt(ctx context.Context, l *launch) (stopped bool, err error) {
	t := l.t
	l.cur = &state.Current{TaskID: t.ID, StartedAt: e.clock.Now()}
	e.run.Current = l.cur
	if err := e.dir.SaveRun(e.run); err != nil {
		return false, err
	}
	e.log(state.Event{Type: state.EventTaskResumed, Detail: "no record of an earlier session"})
	if !t.Owner.IsAgent() {
		e.emit(Event{Kind: TaskResumed, Detail: "user task"})
		return e.yourTurn(ctx, l)
	}
	var ok bool
	if l.model, ok = e.cfg.Models[t.Rank]; !ok {
		return false, fmt.Errorf("task %s: unknown model rank %q; add it to [models] in igris.toml", t.ID, t.Rank)
	}
	e.emit(Event{Kind: TaskResumed, Detail: "in progress without a recorded session"})
	l.lost = true
	e.lose(ctx, l)
	return e.drive(ctx, l)
}

// forget drops the interrupted task from state.json.
func (e *Engine) forget() error {
	e.run.Current = nil
	e.task = nil
	return e.dir.SaveRun(e.run)
}
