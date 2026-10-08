package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// verdictKind is how watching a task ended.
type verdictKind int

const (
	verdictStop  verdictKind = iota // the run stops; the task stays in progress
	verdictDone                     // the task is finished
	verdictSkip                     // the task is skipped
	verdictReset                    // `igris reset` put the task back; the run lets go of it
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
	reason   string    // its run log reason: state.ReasonIdle or ReasonBlocked
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
				e.endEpisode(&ep)
				return verdict{kind: verdictDone, note: c.Text, owner: true}, nil
			case CmdSkip:
				return verdict{kind: verdictSkip, note: c.Text, owner: true}, nil
			case CmdRetry:
				if c.Continue && l.cur.Session == nil {
					e.reject(c, "there is no earlier conversation of "+t.ID+" to continue; retry fresh")
					continue
				}
				if c.Continue && !ValidSessionID(l.cur.ClaudeSession) {
					e.reject(c, "the conversation ID recorded for "+t.ID+" in state.json is not a session ID; retry fresh")
					continue
				}
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
				e.waitOver(state.ReasonSkipRequest)
			}
		}

		if reset, err := e.resets(ctx, true); err != nil || reset {
			return verdict{kind: verdictReset}, err
		}
		switch sig := e.scanSignals(t, l.cur.StartedAt); {
		case sig == nil:
			if skip != nil {
				// A skip request that disappeared is withdrawn.
				skip = nil
				e.waitOver(state.ReasonSkipRequest)
			}
		case state.Classify(*sig, t.ID, t.Owner) == state.Apply:
			// igris no longer waits on the owner: the session said it is
			// done. If its verify fails, a new episode starts.
			e.endEpisode(&ep)
			return verdict{kind: verdictDone, note: sig.Note, sig: sig}, nil
		case skip == nil || !skip.At.Equal(sig.At):
			// A skip from an agent session is only a request (SPEC §6.2).
			skip = sig
			e.needsYou(ctx, notifyNeedsInput, state.ReasonSkipRequest, "the session asks to skip the task", "the session asks to skip")
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
				e.promptDelivered(l)
				e.observe(ctx, st, &ep)
			}
			e.checkOverdue(ctx, l)
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}

// observe updates the idle episode with the agent state st and raises Needs
// you once per episode after needs_input_after without a signal.
func (e *Engine) observe(ctx context.Context, st backend.AgentState, ep *episode) {
	switch {
	case st == backend.Working:
		e.endEpisode(ep)
		// Working again, the agent waits on nobody (Decision W, V05-P1):
		// the owner told it about the failed verify or the verify limit
		// themselves, and an overdue task that works is not waiting.
		e.waitOver(state.ReasonVerifyNotSent)
		e.waitOver(state.ReasonVerifyLimit)
		e.waitOver(state.ReasonTaskOverdue)
	case st.Settled():
		now := e.clock.Now()
		if ep.since.IsZero() {
			ep.since = now
		}
		if after := e.cfg.NeedsInputAfter.Std(); !ep.needsYou && now.Sub(ep.since) >= after {
			ep.needsYou, ep.reason = true, state.ReasonIdle
			words := fmt.Sprintf("idle %s without igris done", after)
			if st == backend.Blocked {
				ep.reason, words = state.ReasonBlocked, "waiting for a permission or an answer"
			}
			e.needsYou(ctx, notifyNeedsInput, ep.reason, fmt.Sprintf("the agent is %s and has not run `igris done` for %s", st, after), words)
		}
	}
	// Unknown tells nothing about the agent; the episode stays as it is.
}

// endEpisode ends the idle episode ep, clearing its wait if Needs you was
// raised for it.
func (e *Engine) endEpisode(ep *episode) {
	if ep.needsYou {
		e.emit(Event{Kind: NeedsYouClear})
		e.waitOver(ep.reason)
	}
	*ep = episode{}
}

// checkOverdue raises the task's Timeout once per attempt (SPEC §6.3): an
// overdue event, Needs you, the run log and the task_overdue notification.
// The session is left running.
func (e *Engine) checkOverdue(ctx context.Context, l *launch) {
	limit := l.t.Timeout
	if limit <= 0 || l.overdue || l.attemptAt.IsZero() || !l.t.Owner.IsAgent() {
		return
	}
	if e.clock.Now().Sub(l.attemptAt) <= limit {
		return
	}
	l.overdue = true
	// The cell as written (45m, not 45m0s); it parsed as a duration, so it
	// is plain text.
	what := "running longer than its Timeout " + l.t.TimeoutText
	e.emit(Event{Kind: TaskOverdue, Detail: what})
	e.log(state.Event{Type: state.EventTaskOverdue, Detail: what})
	e.needsYou(ctx, notifyTaskOverdue, state.ReasonTaskOverdue, "the task is "+what+"; igris leaves its session running", what)
}

// needsYou marks the task Needs you, logs it with reason (a state.Reason*)
// and sends the notification event. why is shown in the UI; the
// notification only says that igris waits, in words.
func (e *Engine) needsYou(ctx context.Context, event notify.Event, reason, why, words string) {
	e.emit(Event{Kind: NeedsYou, Detail: why})
	what := "needs you"
	if event == notifyVerifyLimit {
		what = "verification keeps failing; needs you"
	} else if words != "" {
		// A few words, so two notifications in a row can be told apart.
		what += " (" + words + ")"
	}
	e.waitOn(reason, what)
	e.toast(ctx, event, what)
}

// lose marks l's session lost and asks the owner how to go on (SPEC §6.3).
func (e *Engine) lose(ctx context.Context, l *launch) {
	l.lost = true
	e.emit(Event{Kind: SessionLost, Detail: "the session is gone without `igris done`"})
	e.waitOn(state.ReasonSessionLost, "session lost")
	e.toast(ctx, notifySessionLost, "session lost")
	choices := "continue its conversation, start a fresh session"
	if l.cur.Session == nil {
		choices = "start a fresh session" // no conversation was ever started
	}
	e.emit(Event{Kind: Asked, Question: QuestionSessionLost, Detail: "the session of " + l.t.ID + " is gone: " + choices + ", mark the task done, skip it, or stop"})
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
		case s.Action == state.ActionReset:
			// Asked about and applied by resets, for any task.
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
