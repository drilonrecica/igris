package engine

import (
	"context"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// runUserTask runs a task the owner does outside igris (SPEC §8): no pane,
// no session, no verify and no commit. It is marked in progress, shown as
// Your turn, and finished by the owner's done or skip.
func (e *Engine) runUserTask(ctx context.Context, l *launch) (stopped bool, err error) {
	l.cur = &state.Current{TaskID: l.t.ID, StartedAt: e.clock.Now()}
	if started, err := e.markInProgress(ctx, l, "user task"); err != nil || !started {
		return false, err
	}
	return e.yourTurn(ctx, l)
}

// yourTurn tells the owner that l's user task is theirs and waits until
// they finish it.
func (e *Engine) yourTurn(ctx context.Context, l *launch) (stopped bool, err error) {
	e.emit(Event{Kind: YourTurn, Detail: l.t.Text})
	e.toast(ctx, notifyNeedsInput, "your turn")
	v := e.waitForOwner(ctx, l)
	switch v.kind {
	case verdictDone:
		return false, e.finish(ctx, l, plan.Done, v.note)
	case verdictSkip:
		return false, e.finish(ctx, l, plan.Skipped, v.note)
	}
	return true, nil
}

// waitForOwner waits for the owner to finish l's user task: `d`/`s` (or
// done/skip on stdin) or a done/skip signal from any terminal. Both kinds
// of signal apply directly, since only the owner acts on user tasks
// (SPEC §6.2).
func (e *Engine) waitForOwner(ctx context.Context, l *launch) verdict {
	t := l.t
	for {
		e.checkConfig(ctx)
		if e.stopping(ctx) {
			return verdict{kind: verdictStop}
		}
		for _, c := range e.takeCommands() {
			switch c.Kind {
			case CmdDone:
				return verdict{kind: verdictDone, note: c.Text, owner: true}
			case CmdSkip:
				return verdict{kind: verdictSkip, note: c.Text, owner: true}
			default:
				e.reject(c, t.ID+" is a user task: mark it done or skip it")
			}
		}
		if sig := e.scanSignals(t, l.cur.StartedAt); sig != nil && state.Classify(*sig, t.ID, t.Owner) == state.Apply {
			if sig.Action == state.ActionSkip {
				return verdict{kind: verdictSkip, note: sig.Note, sig: sig}
			}
			return verdict{kind: verdictDone, note: sig.Note, sig: sig}
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}
