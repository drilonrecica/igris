package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
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
	waiting  stretch   // the agent waits on background work instead
}

// stretch tracks the agent waiting on background work or a wakeup that
// Claude Code resumes it for (SPEC §6.3): not idle, but not working either.
type stretch struct {
	since    time.Time // zero while not waiting
	needsYou bool      // NeedsYou was raised: waited longer than waiting_limit
}

// watchStep is one poll of l's session and its signals (SPEC §6.3): the
// owner's commands for the task, its signal, then its session's state.
// decided reports a verdict: the task is finished, skipped or reset. Igris
// advances on a signal or an owner action only, never on what the session
// looks like.
func (e *Engine) watchStep(ctx context.Context, l *launch) (v verdict, decided bool, err error) {
	t := l.t
	for _, c := range e.takeCommands(l) {
		switch c.Kind {
		case CmdDone:
			e.endEpisode(&l.ep)
			return verdict{kind: verdictDone, note: c.Text, owner: true}, true, nil
		case CmdSkip:
			return verdict{kind: verdictSkip, note: c.Text, owner: true}, true, nil
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
				return verdict{}, false, err
			}
			l.ep, l.stateErr = episode{}, ""
		case CmdAnswer:
			if l.skip == nil {
				e.reject(c, "no question is waiting for an answer")
				continue
			}
			if c.Yes {
				return verdict{kind: verdictSkip, note: l.skip.Note, sig: l.skip, owner: true}, true, nil
			}
			if err := e.dir.RemoveSignal(t.ID); err != nil {
				return verdict{}, false, err
			}
			l.skip = nil
			e.waitOver(state.ReasonSkipRequest)
		case CmdPlan:
			e.decidePlan(l, c)
		default:
			e.reject(c, t.ID+" takes no "+c.Kind.String()+" now")
		}
	}

	switch sig := e.scanSignals(t, l.cur.StartedAt); {
	case sig == nil:
		if l.skip != nil {
			// A skip request that disappeared is withdrawn.
			l.skip = nil
			e.waitOver(state.ReasonSkipRequest)
		}
	case state.Classify(*sig, t.ID, t.Owner) == state.Apply:
		// igris no longer waits on the owner: the session said it is
		// done. If its verify fails, a new episode starts.
		e.endEpisode(&l.ep)
		return verdict{kind: verdictDone, note: sig.Note, sig: sig}, true, nil
	case l.skip == nil || !l.skip.At.Equal(sig.At):
		// A skip from an agent session is only a request (SPEC §6.2).
		l.skip = sig
		e.needsYouAsking(ctx, notifyNeedsInput, state.ReasonSkipRequest, "the session asks to skip the task", "the session asks to skip",
			&Event{Kind: Asked, Question: QuestionConfirmSkip, Detail: fmt.Sprintf("skip %s? the session's reason: %s", t.ID, sig.Note)})
	}

	e.watchSession(ctx, l)
	return verdict{}, false, nil
}

// watchSession is the session part of a poll of l (SPEC §6.3): its
// state, the idle episode, the usage and the rank audit, the Timeout. A
// lost session is watched by nobody: the owner was asked.
func (e *Engine) watchSession(ctx context.Context, l *launch) {
	if l.lost {
		return
	}
	t := l.t
	st, err := l.sess.State(ctx)
	switch {
	case err != nil:
		if ctx.Err() == nil && err.Error() != l.stateErr {
			l.stateErr = err.Error()
			e.warn(fmt.Sprintf("read the state of %s: %v", t.ID, err))
		}
	case st == backend.Exited:
		e.lose(ctx, l)
	default:
		l.stateErr = ""
		delivered := e.promptDelivered(l)
		e.checkSubmitted(ctx, l, st, delivered)
		e.checkPlanRequest(ctx, l)
		e.observe(ctx, l, st, &l.ep)
		e.readUsage(ctx, l, false)
		e.readQuota(false)
		e.auditRank(ctx, l, false)
	}
	e.checkOverdue(ctx, l)
}

// observe updates the idle episode with the agent state st and raises Needs
// you once per episode after needs_input_after without a signal. An idle
// agent task is first reminded of `igris done` once per attempt (nudge).
// An agent waiting on background work needs nobody until waiting_limit.
func (e *Engine) observe(ctx context.Context, l *launch, st backend.AgentState, ep *episode) {
	now := e.clock.Now()
	lim := l.limit
	// The usage-limit wait (SPEC §6.3): the hooks say limited, or the
	// transcript's hit says the session waits while the hooks say idle;
	// over once the fixed prompt went out, when the idle rules apply.
	limited := lim == nil || !lim.prompted
	limited = limited && (st == backend.Limited || (lim != nil && st.Settled()))
	switch {
	case st == backend.Working:
		e.limitOver(l)
		e.endEpisode(ep)
		// Working again, the agent waits on nobody (Decision W, V05-P1):
		// the owner told it about the failed verify or the verify limit
		// themselves, and an overdue task, or one on another model, that
		// works is not waiting.
		e.waitOver(state.ReasonVerifyNotSent)
		e.waitOver(state.ReasonVerifyLimit)
		e.waitOver(state.ReasonTaskOverdue)
		e.waitOver(state.ReasonRankMismatch)
	case limited:
		e.endEpisode(ep)
		e.limited(ctx, l)
	case st == backend.Waiting:
		e.endIdle(ep)
		w := &ep.waiting
		if w.since.IsZero() {
			w.since = now
			e.emit(Event{Kind: AgentWaiting, Detail: "the agent waits on background work; Claude Code resumes it when that is done"})
		}
		if limit := e.cfg.WaitingLimitOrDefault(); !w.needsYou && now.Sub(w.since) >= limit {
			w.needsYou = true
			what := "waiting on background work for " + config.Duration(limit).Short()
			e.needsYou(ctx, notifyNeedsInput, state.ReasonWaiting, "the agent has been "+what, what)
		}
	case st.Settled(), st == backend.Limited:
		// Limited here: the fixed prompt went out and the session still
		// hasn't continued; the idle rules apply (SPEC §6.3).
		e.endWaiting(ep)
		if ep.since.IsZero() {
			ep.since = now
		}
		after := e.cfg.NeedsInputAfter.Std()
		if ep.needsYou || now.Sub(ep.since) < after || l.plan.n != "" {
			// A plan shown for approval has its own Needs you (SPEC §6.3).
			return
		}
		if st != backend.Blocked && e.nudge(ctx, l) {
			ep.since = now // the agent gets needs_input_after to answer
			return
		}
		ep.needsYou, ep.reason = true, state.ReasonIdle
		words := fmt.Sprintf("idle %s without igris done", after)
		switch {
		case st == backend.Blocked:
			ep.reason, words = state.ReasonBlocked, "waiting for a permission or an answer"
		case lim != nil:
			ep.reason, words = state.ReasonUsageLimit, "idle after the usage limit reset"
		}
		e.needsYou(ctx, notifyNeedsInput, ep.reason, fmt.Sprintf("the agent is %s and has not run `igris done` for %s", st, after), words)
	}
	// Unknown tells nothing about the agent; the episode stays as it is.
}

// nudge reminds l's idle agent of `igris done` once per attempt, before it
// needs the owner (SPEC §6.3), and reports whether the reminder went out.
// User tasks, nudge = false and a session that can't take the prompt get
// none: Needs you follows at once.
func (e *Engine) nudge(ctx context.Context, l *launch) bool {
	if l.nudged || !e.cfg.NudgeOn() || !l.t.Owner.IsAgent() || l.sess == nil {
		return false
	}
	l.nudged = true
	switch err := l.sess.Prompt(ctx, prompt.Nudge(l.t.ID)); {
	case errors.Is(err, backend.ErrSessionGone):
		return false // the next poll finds it gone
	case err != nil:
		if ctx.Err() == nil {
			e.warn(fmt.Sprintf("remind %s of igris done: %v", l.t.ID, err))
		}
		return false
	}
	e.emit(Event{Kind: Nudged, Detail: "the agent stopped without igris done; igris reminded it once"})
	return true
}

// submitAfter is how long a delivered prompt may sit unsubmitted, the
// agent idle, before igris presses Enter once (SPEC §6.3).
const submitAfter = 15 * time.Second

// checkSubmitted presses Enter once when l's first prompt seems pasted but
// never submitted (SPEC §6.3): the session is idle at least submitAfter
// after the prompt went out, and its hook record still holds the event
// from before the prompt (SessionStart, or the idle_prompt Notification).
// A submitted prompt always fires UserPromptSubmit, and a turn ends with
// Stop. Without a hook record there is no evidence, and nothing is done.
// The prompt text is never sent again; Needs you follows as usual.
// delivered says st comes from the poll that just delivered a held prompt:
// that poll reports Working for the delivery itself, not for the agent
// working on the prompt.
func (e *Engine) checkSubmitted(ctx context.Context, l *launch, st backend.AgentState, delivered bool) {
	if l.promptAt.IsZero() || l.cur.ClaudeSession == "" {
		return
	}
	now := e.clock.Now()
	if st == backend.Working && !delivered && now.After(l.promptAt) {
		l.promptAt = time.Time{} // the agent works on it
		return
	}
	rec, ok := state.PeekAgentState(e.dir.Root(), l.cur.ClaudeSession)
	if !ok {
		return
	}
	if rec.State != backend.Idle || (rec.Event != "SessionStart" && rec.Event != "Notification") {
		// Submitted (UserPromptSubmit and on), or blocked at a question:
		// Enter must not answer it.
		if rec.State != backend.Blocked {
			l.promptAt = time.Time{}
		}
		return
	}
	if (st != backend.Idle && st != backend.Done) || now.Sub(l.promptAt) < submitAfter {
		return
	}
	l.promptAt = time.Time{} // once per prompt
	sub, ok := l.sess.(backend.Submitter)
	if !ok {
		return
	}
	if err := sub.Submit(ctx); err != nil {
		if ctx.Err() == nil && !errors.Is(err, backend.ErrSessionGone) {
			e.warn(fmt.Sprintf("the prompt of %s didn't seem submitted; pressing Enter failed: %v; press Enter in its session", l.t.ID, err))
		}
		return
	}
	e.warn("the prompt didn't seem submitted; sent Enter once")
}

// endEpisode ends the idle episode ep and any waiting stretch, clearing
// their waits if Needs you was raised.
func (e *Engine) endEpisode(ep *episode) {
	e.endWaiting(ep)
	e.endIdle(ep)
}

// endIdle ends the idle part of ep, clearing its wait if Needs you was
// raised for it.
func (e *Engine) endIdle(ep *episode) {
	if ep.needsYou {
		e.emit(Event{Kind: NeedsYouClear})
		e.waitOver(ep.reason)
	}
	ep.since, ep.needsYou, ep.reason = time.Time{}, false, ""
}

// endWaiting ends ep's waiting stretch, if any, clearing its wait if it ran
// past waiting_limit.
func (e *Engine) endWaiting(ep *episode) {
	w := &ep.waiting
	if w.since.IsZero() {
		return
	}
	if w.needsYou {
		e.emit(Event{Kind: NeedsYouClear})
		e.waitOver(state.ReasonWaiting)
	}
	e.emit(Event{Kind: AgentWaitingOver})
	*w = stretch{}
}

// checkOverdue raises the task's Timeout once per attempt (SPEC §6.3): an
// overdue event, Needs you, the run log and the task_overdue notification.
// The session is left running.
func (e *Engine) checkOverdue(ctx context.Context, l *launch) {
	limit := l.t.Timeout
	if limit <= 0 || l.overdue || l.attemptAt.IsZero() || !l.t.Owner.IsAgent() {
		return
	}
	if l.limit != nil && !l.limit.since.IsZero() {
		return // the clock stops while the session waits at a usage limit (SPEC §6.3)
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
	e.needsYou(ctx, notifyTaskOverdue, state.ReasonTaskOverdue, what+"; igris leaves its session running", what)
}

// needsYou marks the task Needs you, logs it with reason (a state.Reason*)
// and sends the notification event. why is shown in the UI; the
// notification only says that igris waits, in words.
func (e *Engine) needsYou(ctx context.Context, event notify.Event, reason, why, words string) {
	e.needsYouAsking(ctx, event, reason, why, words, nil)
}

// needsYouAsking is needsYou for a wait that asks a question: asked, the
// Asked event, is emitted after Needs you and before the notification,
// which then carries its answer buttons (SPEC §10). Nil asks nothing.
func (e *Engine) needsYouAsking(ctx context.Context, event notify.Event, reason, why, words string, asked *Event) {
	e.emit(Event{Kind: NeedsYou, Detail: why, Reason: reason})
	what := "needs you"
	if event == notifyVerifyLimit {
		what = "verification keeps failing; needs you"
	} else if words != "" {
		// A few words, so two notifications in a row can be told apart.
		what += " (" + words + ")"
	}
	e.waitOn(reason, what)
	var q Question
	if asked != nil {
		e.emit(*asked)
		q = asked.Question
	}
	if event == notifyTaskOverdue || event == notifyRankMismatch {
		// The title already says ▲ Overdue / ▲ Rank mismatch: a warning,
		// worded as one, not "needs you" (SPEC §10).
		what = words
	}
	e.toastAsking(ctx, event, what, q)
}

// lose marks l's session lost and asks the owner how to go on (SPEC §6.3).
func (e *Engine) lose(ctx context.Context, l *launch) {
	l.lost = true
	e.endUsage(ctx, l)
	e.dropPlan(l, "the session is gone")
	e.emit(Event{Kind: SessionLost, Detail: "the session is gone without `igris done`"})
	e.waitOn(state.ReasonSessionLost, "session lost")
	choices := "continue its conversation, start a fresh session"
	if l.cur.Session == nil {
		choices = "start a fresh session" // no conversation was ever started
	}
	e.emit(Event{Kind: Asked, Question: QuestionSessionLost, Detail: "the session of " + l.t.ID + " is gone: " + choices + ", mark the task done, skip it, or stop"})
	e.toastAsking(ctx, notifySessionLost, "session lost", QuestionSessionLost)
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
		// Once per task and action: a rewritten signal (a new timestamp
		// each time) must not grow the once-map or repeat the report.
		key := s.ID + "|" + s.Action
		switch {
		case s.Action == state.ActionReset:
			// Asked about and applied by resets, for any task.
		case s.ID != t.ID:
			// Another active task's signal is its own step's (SPEC §5.1).
			if e.run.ActiveTask(s.ID) == nil {
				e.reportOnce("stray|"+key, StraySignal, fmt.Sprintf("kept, not applied: a %s signal for %s, which is not the current task", s.Action, s.ID))
			}
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
