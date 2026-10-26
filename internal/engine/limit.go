package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/hook"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

// Usage limits (SPEC §6.3, §6.9, §10; decisions.md V18-P1). Two sources,
// two jobs: the opt-in status line's quota (quota.json) says how full each
// window is, and holds the next agent task ahead of a hit from [usage]
// hold_at; a session's transcript records a hit, which holds new starts
// until its reset with or without the status line. A session that hit a
// limit waits by itself (Claude Code's autoContinueAtUsageLimit): igris
// marks it limited, stops its Timeout clock and raises no Needs you.

const (
	// resumeGrace is how long after a window's reset held starts resume:
	// Claude Code's own continuation came 56–105 s after the minute.
	resumeGrace = 60 * time.Second
	// continueAfter is how long after the reset a session still not
	// working gets the fixed prompt.
	continueAfter = 2 * time.Minute
	// maxResetAhead bounds a hit's reset: further ahead, or in the past,
	// it is no hit (a session can forge the transcript, SPEC §16).
	maxResetAhead = 8 * 24 * time.Hour
)

// limitWatch is the usage hold: whether new starts are held, by which
// window, and the hits that hold them.
type limitWatch struct {
	on   bool
	cur  Limit  // the window that holds
	held string // the task that would start, named on the hold's events
	// hits are the sessions' hits whose reset is ahead, by window: each
	// holds new starts until then.
	hits map[string]Limit
	// notified keys the limit episodes (Limit.Episode) the usage_limit
	// notification went out for: one per episode (SPEC §10).
	notified map[string]bool
	// startAnyway: the owner's Start anyway; the next gate ignores the
	// quota threshold once.
	startAnyway bool
}

// sessionLimit is one session's wait at a usage limit (SPEC §6.3).
type sessionLimit struct {
	since    time.Time // when the wait began; zero once the Timeout clock runs again
	window   string    // from the transcript's hit; "" until it is read
	resetsAt time.Time
	prompted bool   // the fixed prompt went out; the ordinary idle rules apply
	entered  bool   // Enter was pressed once for quota_auto_resume_stale
	shown    string // the episode last sent as SessionLimited
}

// rfc3339 is t for the run log: RFC 3339, UTC.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// holdDetail is the run log's detail of a hold by l: `5h at 96%`, or
// `after M1-03 hit the 7-day limit`.
func (l Limit) holdDetail() string {
	if l.Task != "" {
		return "after " + l.Task + " hit the " + look.WindowName(l.Window)
	}
	return fmt.Sprintf("%s at %.0f%%", look.Window(l.Window), math.Round(l.Percent))
}

// holding is the window that holds new starts now, if any: of the hits
// whose reset plus the grace is ahead and, with the status line and a
// threshold, the quota windows at or above it, the one that resets first.
// Hits whose reset has passed are forgotten. Start anyway skips the quota.
func (e *Engine) holding(now time.Time) (Limit, bool) {
	var best Limit
	found := false
	consider := func(l Limit) {
		if !found || l.ResetsAt.Before(best.ResetsAt) {
			best, found = l, true
		}
	}
	for w, h := range e.limit.hits {
		if !now.Before(h.ResetsAt.Add(resumeGrace)) {
			delete(e.limit.hits, w)
			continue
		}
		consider(h)
	}
	if at := e.cfg.Usage.HoldAtOrDefault(); at > 0 && e.cfg.Usage.Statusline && !e.limit.startAnyway {
		e.readQuota(true) // the hold goes by the quota as it is now
		if q := e.quota.shown; q != nil {
			for _, w := range q.Windows() {
				reset := time.Unix(w.ResetsAt, 0).UTC()
				if w.Used >= float64(at) && now.Before(reset.Add(resumeGrace)) {
					consider(Limit{Window: w.Name, ResetsAt: reset, Percent: w.Used})
				}
			}
		}
	}
	return best, found
}

// gateStarts holds s's agent tasks while a usage limit holds new starts
// (SPEC §6.9) and returns what may start: user tasks need no session and
// start. It begins and ends the hold as the windows say.
func (e *Engine) gateStarts(ctx context.Context, s schedule) schedule {
	agent := func(t *plan.Task) bool { return t.Owner.IsAgent() }
	i := slices.IndexFunc(s.start, agent)
	if i < 0 {
		return s
	}
	now := e.clock.Now()
	lim, holds := e.holding(now)
	switch {
	case holds:
		if e.limit.startAnyway {
			e.warn(fmt.Sprintf("start anyway: %s still holds new starts until %s", lim.holdDetail(), look.Until(lim.ResetsAt.Local(), now)))
		}
		e.beginHold(ctx, lim, s.start[i])
		s.start = slices.DeleteFunc(slices.Clone(s.start), agent)
	case e.limit.on:
		e.endHold(e.resumeDetail(now))
	}
	e.limit.startAnyway = false
	return s
}

// refreshLimit ends a hold whose windows no longer hold, or moves it to the
// window that holds now; every poll while the hold is on, so a reset or a
// fresh quota is seen with or without a task to start.
func (e *Engine) refreshLimit(ctx context.Context) {
	if !e.limit.on {
		return
	}
	now := e.clock.Now()
	lim, holds := e.holding(now)
	if !holds {
		e.endHold(e.resumeDetail(now))
		return
	}
	e.showHold(ctx, lim)
}

// beginHold starts holding new starts for lim, t being the task that would
// start; a hold already on moves to lim.
func (e *Engine) beginHold(ctx context.Context, lim Limit, t *plan.Task) {
	e.limit.held = t.ID
	if !e.limit.on {
		e.limit.on = true
		e.limit.cur = Limit{} // a fresh episode is logged
	}
	e.showHold(ctx, lim)
}

// showHold records lim as the window that holds: a new episode (another
// window or reset) is logged and notified, a changed use only shown.
func (e *Engine) showHold(ctx context.Context, lim Limit) {
	cur := e.limit.cur
	if cur == lim {
		return
	}
	e.limit.cur = lim
	id := e.limit.held
	if cur.Episode() != lim.Episode() {
		e.logRun(state.Event{Type: state.EventLimitHold, Window: lim.Window, ResetsAt: rfc3339(lim.ResetsAt), Detail: lim.holdDetail()})
	}
	ev := Event{Kind: LimitHold, Task: id, Limit: &lim, Detail: lim.holdDetail()}
	if p, err := plan.Load(e.planPath, e.planOpts); err == nil {
		if t := p.Task(id); t != nil {
			ev.Title, ev.Rank, ev.Phase = t.Title, t.Rank, phaseOf(t)
		}
	}
	e.emit(ev)
	if lim.Task == "" {
		// A hit's episode was notified when the hit was read.
		e.limitNotify(ctx, lim, fmt.Sprintf("%s at %.0f%% · %s held until %s", look.WindowName(lim.Window), math.Round(lim.Percent), id, look.UntilIn(lim.ResetsAt.Local(), e.clock.Now())))
	}
}

// endHold ends the hold; detail says why (SPEC §13: reset, quota fell to
// 71%, started anyway).
func (e *Engine) endHold(detail string) {
	lim := e.limit.cur
	e.limit.on, e.limit.cur, e.limit.held = false, Limit{}, ""
	e.logRun(state.Event{Type: state.EventLimitResume, Window: lim.Window, ResetsAt: rfc3339(lim.ResetsAt), Detail: detail})
	e.emit(Event{Kind: LimitResume, Limit: &lim, Detail: detail})
}

// resumeDetail says why the hold ends now: Start anyway, the quota fell
// below the threshold before the reset, or the reset.
func (e *Engine) resumeDetail(now time.Time) string {
	cur := e.limit.cur
	switch {
	case e.limit.startAnyway:
		return "started anyway"
	case cur.Task == "" && now.Before(cur.ResetsAt):
		if q := e.quota.shown; q != nil {
			for _, w := range q.Windows() {
				if w.Name == cur.Window {
					return fmt.Sprintf("quota fell to %.0f%%", math.Round(w.Used))
				}
			}
		}
		return "quota fell below the threshold"
	}
	return "reset"
}

// limitNotify sends the usage_limit notification for lim's episode, once
// per episode whatever the number of sessions that hit it and holds it
// causes (SPEC §10). The body is what, on its own: the episode is the
// run's, not a task's.
func (e *Engine) limitNotify(ctx context.Context, lim Limit, what string) {
	key := lim.Episode()
	if e.limit.notified[key] {
		return
	}
	e.limit.notified[key] = true
	e.notified(e.notifier.Notify(ctx, notify.Message{
		Event: notifyUsageLimit, Project: filepath.Base(e.dir.Root()), What: what, RunID: e.runID, At: e.clock.Now(),
	}))
}

// noteHits notes the usage-limit hits l's transcript gained since the last
// read (SPEC §6.9).
func (e *Engine) noteHits(ctx context.Context, l *launch) {
	u := l.usage
	if u == nil || u.r == nil {
		return
	}
	hits := u.r.Hits()
	for ; u.hits < len(hits); u.hits++ {
		h := hits[u.hits]
		// A hit from before the attempt (a continued session, a reattached
		// one) still says until when the session waits; only this
		// attempt's hits are logged and notified.
		e.noteHit(ctx, l, h, !h.At.Before(u.from))
	}
}

// noteHit records one hit: it holds new starts until its reset, and a
// fresh one is logged (limit_hit) and notified once per episode. A reset
// in the past or over 8 days ahead is no hit: a warning when fresh.
func (e *Engine) noteHit(ctx context.Context, l *launch, h usage.Hit, fresh bool) {
	now := e.clock.Now()
	if ahead := h.ResetsAt.Sub(now); ahead <= 0 || ahead > maxResetAhead {
		if fresh {
			e.warn(fmt.Sprintf("%s's transcript records a usage limit hit that resets %s; ignored", l.t.ID, look.Date(h.ResetsAt.Local(), now)))
		}
		return
	}
	lim := Limit{Window: h.Window, ResetsAt: h.ResetsAt, Task: l.t.ID}
	if old, ok := e.limit.hits[h.Window]; !ok || old.ResetsAt.Before(h.ResetsAt) {
		e.limit.hits[h.Window] = lim
	}
	if fresh {
		e.log(state.Event{Type: state.EventLimitHit, Window: h.Window, ResetsAt: rfc3339(h.ResetsAt)})
		e.limitNotify(ctx, lim, fmt.Sprintf("%s · %s waits, resumes %s", look.WindowName(h.Window), l.t.ID, look.UntilIn(h.ResetsAt.Local(), now)))
	}
	if fresh || l.limit != nil {
		// The session waits at it.
		if l.limit == nil {
			l.limit = &sessionLimit{since: now}
		}
		if h.ResetsAt.After(l.limit.resetsAt) {
			l.limit.window, l.limit.resetsAt = h.Window, h.ResetsAt
		}
		e.emitLimited(l)
	}
}

// limited handles one poll of l's session seen waiting at a usage limit
// (SPEC §6.3): the card learns of it, the transcript is read for the hit
// that says until when, and Claude Code's own word on its continuation is
// acted on: Enter once when it waits for one, the fixed prompt when it
// gave up or two minutes after the reset.
func (e *Engine) limited(ctx context.Context, l *launch) {
	now := e.clock.Now()
	if l.limit == nil {
		l.limit = &sessionLimit{since: now}
		if l.usage != nil {
			l.usage.last = time.Time{} // read the transcript now: the hit says until when
		}
		e.emitLimited(l)
	}
	lim := l.limit
	if rec, ok := state.PeekAgentState(e.dir.Root(), l.cur.ClaudeSession); ok && rec.State == backend.Limited {
		switch rec.Event {
		case hook.QuotaResumeStale:
			if !lim.entered {
				lim.entered = true
				e.pressEnter(ctx, l)
			}
		case hook.QuotaResumeDisabled:
			e.continueTask(ctx, l, "stopped continuing by itself after the usage limit (Claude Code gave up)")
		}
	}
	if !lim.prompted && !lim.resetsAt.IsZero() && !now.Before(lim.resetsAt.Add(continueAfter)) {
		e.continueTask(ctx, l, "didn't continue two minutes after the usage limit's reset")
	}
}

// emitLimited tells the UI that l's session waits at a usage limit, once
// per window and reset learned.
func (e *Engine) emitLimited(l *launch) {
	lim := l.limit
	key := lim.window + "@" + rfc3339(lim.resetsAt)
	if lim.shown == key {
		return
	}
	lim.shown = key
	ev := Event{Kind: SessionLimited, Detail: "waits at a usage limit"}
	if lim.window != "" {
		ev.Limit = &Limit{Window: lim.window, ResetsAt: lim.resetsAt, Task: l.t.ID}
		ev.Detail = fmt.Sprintf("waits at the %s · continues %s", look.WindowName(lim.window), look.Until(lim.resetsAt.Local(), e.clock.Now()))
	}
	e.emit(ev)
}

// limitWaitOver starts l's Timeout clock again: the wait was nobody's time
// (SPEC §6.3).
func (e *Engine) limitWaitOver(l *launch) {
	lim := l.limit
	if lim == nil || lim.since.IsZero() {
		return
	}
	if !l.attemptAt.IsZero() {
		l.attemptAt = l.attemptAt.Add(e.clock.Now().Sub(lim.since))
	}
	lim.since = time.Time{}
}

// limitOver ends l's wait: the session works again.
func (e *Engine) limitOver(l *launch) {
	if l.limit == nil {
		return
	}
	e.limitWaitOver(l)
	l.limit = nil
	e.emit(Event{Kind: SessionLimitOver, Detail: "works again after the usage limit"})
}

// continueTask sends l's session the fixed prompt once per hit (SPEC §6.3);
// why says what called for it. The ordinary idle rules apply from then on.
func (e *Engine) continueTask(ctx context.Context, l *launch, why string) {
	lim := l.limit
	if lim.prompted || l.sess == nil {
		return
	}
	lim.prompted = true
	e.limitWaitOver(l)
	l.ep.since = e.clock.Now() // the idle episode counts from the prompt
	switch err := l.sess.Prompt(ctx, prompt.LimitContinue()); {
	case errors.Is(err, backend.ErrSessionGone):
		return // the next poll finds it gone
	case err != nil:
		if ctx.Err() == nil {
			e.warn(fmt.Sprintf("tell %s to continue after the usage limit: %v", l.t.ID, err))
		}
		return
	}
	e.emit(Event{Kind: Nudged, Detail: why + "; igris told it to continue"})
	e.emit(Event{Kind: SessionLimitOver, Detail: "told to continue after the usage limit"})
}

// pressEnter presses Enter once in l's pane: Claude Code waits for one
// after a stale resume (the machine slept, SPEC §6.3).
func (e *Engine) pressEnter(ctx context.Context, l *launch) {
	sub, ok := l.sess.(backend.Submitter)
	if !ok {
		return
	}
	if err := sub.Submit(ctx); err != nil {
		if ctx.Err() == nil && !errors.Is(err, backend.ErrSessionGone) {
			e.warn(fmt.Sprintf("Claude Code waits for Enter after the usage limit's reset; pressing it failed: %v; press Enter in %s's session", err, l.t.ID))
		}
		return
	}
	e.warn("Claude Code waited for Enter after the usage limit's reset (the machine slept); sent Enter once")
}
