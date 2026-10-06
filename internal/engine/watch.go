package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// verdictKind is how watching a task ended.
type verdictKind int

const (
	verdictStop verdictKind = iota // the run stops; the task stays in progress
	verdictDone                    // the task is finished
	verdictSkip                    // the task is skipped
)

type verdict struct {
	kind  verdictKind
	note  string        // done note or skip reason
	sig   *state.Signal // the signal that ended the watch; nil for an owner command
	owner bool          // the owner decided (CmdDone/CmdSkip/confirmed skip)
}

// episode tracks one stretch of the agent sitting idle without a signal
// (SPEC §6.3). It ends when the agent works again.
type episode struct {
	since    time.Time // first settled observation; zero while working
	needsYou bool      // NeedsYou was raised for this episode
}

// watch polls l's session and the signals until the task is finished, the
// owner decides, or the run stops (SPEC §6.3). Igris advances on a signal or
// an owner action only, never on what the session looks like.
func (e *Engine) watch(ctx context.Context, l *launch) (verdict, error) {
	t := l.t
	var ep episode
	var skip *state.Signal // the skip request waiting for the owner's answer
	stateErr := ""         // the last State error reported
	for {
		e.checkConfig(ctx) // before the signal, so a config edit is seen before the task is accepted
		if e.stopping(ctx) {
			return verdict{kind: verdictStop}, nil
		}
		for _, c := range e.takeCommands() {
			switch c.Kind {
			case CmdDone:
				return verdict{kind: verdictDone, note: c.Text, owner: true}, nil
			case CmdSkip:
				return verdict{kind: verdictSkip, note: c.Text, owner: true}, nil
			case CmdRetry:
				if err := e.retry(ctx, l, c.Continue); err != nil {
					return verdict{}, err
				}
				ep, stateErr = episode{}, ""
			case CmdAnswer:
				if skip == nil {
					e.reject(c, "no question is waiting for an answer")
					continue
				}
				if c.Yes {
					return verdict{kind: verdictSkip, note: skip.Note, sig: skip, owner: true}, nil
				}
				if err := e.dir.RemoveSignal(t.ID); err != nil {
					return verdict{}, err
				}
				skip = nil
			}
		}

		switch sig := e.scanSignals(t, l.cur.StartedAt); {
		case sig == nil:
			skip = nil // a skip request that disappeared is withdrawn
		case state.Classify(*sig, t.ID, t.Owner) == state.Apply:
			return verdict{kind: verdictDone, note: sig.Note, sig: sig}, nil
		case skip == nil || !skip.At.Equal(sig.At):
			// A skip from an agent session is only a request (SPEC §6.2).
			skip = sig
			e.needsYou(ctx, notifyNeedsInput, "the session asks to skip the task")
			e.emit(Event{Kind: Asked, Question: QuestionConfirmSkip, Detail: fmt.Sprintf("skip %s? the session's reason: %s", t.ID, sig.Note)})
		}

		if !l.lost {
			st, err := l.sess.State(ctx)
			switch {
			case err != nil:
				if ctx.Err() == nil && err.Error() != stateErr {
					stateErr = err.Error()
					e.warn(fmt.Sprintf("read the state of %s: %v", t.ID, err))
				}
			case st == backend.Exited:
				e.lose(ctx, l)
			default:
				stateErr = ""
				e.observe(ctx, st, &ep)
			}
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}

// observe updates the idle episode with the agent state st and raises Needs
// you once per episode after needs_input_after without a signal.
func (e *Engine) observe(ctx context.Context, st backend.AgentState, ep *episode) {
	switch {
	case st == backend.Working:
		if ep.needsYou {
			e.emit(Event{Kind: NeedsYouClear})
		}
		*ep = episode{}
	case st.Settled():
		now := e.clock.Now()
		if ep.since.IsZero() {
			ep.since = now
		}
		if after := e.cfg.NeedsInputAfter.Std(); !ep.needsYou && now.Sub(ep.since) >= after {
			ep.needsYou = true
			e.needsYou(ctx, notifyNeedsInput, fmt.Sprintf("the agent is %s and has not run `igris done` for %s", st, after))
		}
	}
	// Unknown tells nothing about the agent; the episode stays as it is.
}

// needsYou marks the task Needs you and sends the notification event. why
// is shown in the UI; the notification only says that igris waits.
func (e *Engine) needsYou(ctx context.Context, event, why string) {
	e.emit(Event{Kind: NeedsYou, Detail: why})
	what := "needs you"
	if event == notifyVerifyLimit {
		what = "verification keeps failing; needs you"
	}
	e.toast(ctx, event, what)
}

// lose marks l's session lost and asks the owner how to go on (SPEC §6.3).
func (e *Engine) lose(ctx context.Context, l *launch) {
	l.lost = true
	e.emit(Event{Kind: SessionLost, Detail: "the session is gone without `igris done`"})
	e.toast(ctx, notifySessionLost, "session lost")
	e.emit(Event{Kind: Asked, Question: QuestionSessionLost, Detail: "the session of " + l.t.ID + " is gone: continue its conversation, start a fresh session, mark the task done, skip it, or stop"})
}

// scanSignals reads every pending signal, reports the ones that are not
// applied (for another task, from before the task started, unreadable) once
// each, and returns t's own signal if it counts (SPEC §6.2).
func (e *Engine) scanSignals(t *plan.Task, started time.Time) *state.Signal {
	sigs, bad, err := e.dir.ListSignals()
	if err != nil {
		e.reportOnce("list|"+err.Error(), Warning, err.Error())
		return nil
	}
	for _, b := range bad {
		e.reportOnce("bad|"+b.Error(), Warning, b.Error())
	}
	var own *state.Signal
	for i := range sigs {
		s := sigs[i]
		key := s.ID + "|" + s.Action + "|" + s.At.String()
		switch {
		case s.ID != t.ID:
			e.reportOnce("stray|"+key, StraySignal, fmt.Sprintf("kept, not applied: a %s signal for %s, which is not the current task", s.Action, s.ID))
		case s.At.Before(started):
			e.reportOnce("stale|"+key, StaleSignal, fmt.Sprintf("ignoring a %s signal for %s written before the task started; run `igris %s %s` again if it is meant", s.Action, t.ID, s.Action, t.ID))
		default:
			own = &s
		}
	}
	return own
}

// reportOnce emits an event of kind once per key for the whole run.
func (e *Engine) reportOnce(key string, kind EventKind, detail string) {
	if e.reported[key] {
		return
	}
	e.reported[key] = true
	e.emit(Event{Kind: kind, Detail: detail})
}
