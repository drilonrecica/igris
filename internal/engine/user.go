package engine

import (
	"context"

	"github.com/drilonrecica/igris/internal/state"
)

// startUserTask starts a task the owner does outside igris (SPEC §8): no
// pane, no session, no verify and no commit. It is marked in progress and
// shown as Your turn; the owner's done or skip finishes it (ownerStep).
func (e *Engine) startUserTask(ctx context.Context, l *launch, parallel bool) (started bool, err error) {
	l.cur = &state.Current{TaskID: l.t.ID, StartedAt: e.clock.Now()}
	if parallel {
		l.cur.Slot = e.freeSlot()
	}
	if started, err := e.markInProgress(ctx, l, "user task"); err != nil || !started {
		return false, err
	}
	e.yourTurn(ctx, l)
	return true, nil
}

// yourTurn tells the owner that l's user task is theirs.
func (e *Engine) yourTurn(ctx context.Context, l *launch) {
	e.emit(Event{Kind: YourTurn, Detail: l.t.Text})
	e.toast(ctx, notifyNeedsInput, "your turn: do it, then mark it done or skipped in igris")
}

// ownerStep is one poll of l's user task, waiting for the owner to finish
// it: `d`/`s` (or done/skip on stdin) or a done/skip signal from any
// terminal. Both kinds of signal apply directly, since only the owner acts
// on user tasks (SPEC §6.2).
func (e *Engine) ownerStep(ctx context.Context, l *launch) (v verdict, decided bool) {
	t := l.t
	for _, c := range e.takeCommands(l) {
		switch c.Kind {
		case CmdDone:
			return verdict{kind: verdictDone, note: c.Text, owner: true}, true
		case CmdSkip:
			return verdict{kind: verdictSkip, note: c.Text, owner: true}, true
		default:
			e.reject(c, t.ID+" is a user task: mark it done or skip it")
		}
	}
	if sig := e.scanSignals(t, l.cur.StartedAt); sig != nil && state.Classify(*sig, t.ID, t.Owner) == state.Apply {
		if sig.Action == state.ActionSkip {
			return verdict{kind: verdictSkip, note: sig.Note, sig: sig}, true
		}
		return verdict{kind: verdictDone, note: sig.Note, sig: sig}, true
	}
	return verdict{}, false
}
