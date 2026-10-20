package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

// usageEvery is how often igris reads a running attempt's transcript
// (SPEC §6.9).
const usageEvery = 10 * time.Second

// attemptUsage is the token usage of one attempt of an agent task, read
// from its session's transcript (SPEC §6.9).
type attemptUsage struct {
	from   time.Time     // when the attempt's session opened or was reattached
	r      *usage.Reader // nil until the hooks reported the transcript
	last   time.Time     // the last read
	warned bool          // the attempt's one warning was shown
	// mismatch is set once the rank audit found the main thread on a model
	// its rank doesn't resolve to: raised once per attempt (SPEC §6.9).
	mismatch bool
	// shown is the last counter sent to the UI, so it hears of changes only.
	shown *Tokens
}

// startUsage starts counting the usage of l's attempt from from, unless
// [usage] is off.
func (e *Engine) startUsage(l *launch, from time.Time) {
	l.usage = nil
	if e.cfg.Usage.On() && l.t.Owner.IsAgent() {
		l.usage = &attemptUsage{from: from}
	}
}

// readUsage reads what l's transcript gained, at most every usageEvery
// unless final. A read that fails is warned about once per attempt; the
// task and the run go on.
func (e *Engine) readUsage(l *launch, final bool) {
	u := l.usage
	if u == nil {
		return
	}
	now := e.clock.Now()
	if !final && !u.last.IsZero() && now.Sub(u.last) < usageEvery {
		return
	}
	u.last = now
	if u.r == nil {
		// Only the path the session's hooks reported, named after the
		// session igris started; igris never looks for one.
		rec, ok := state.PeekAgentState(e.dir.Root(), l.cur.ClaudeSession)
		if !ok || rec.Transcript == "" {
			return
		}
		u.r = usage.NewReader(rec.Transcript, u.from)
	}
	var err error
	if final {
		err = u.r.Finish(now)
	} else {
		err = u.r.Read()
	}
	if !final && errors.Is(err, fs.ErrNotExist) {
		return // Claude Code hasn't written it yet
	}
	if err != nil && !u.warned {
		u.warned = true
		e.warn(fmt.Sprintf("can't read the transcript of %s: %v; its token usage may be incomplete", l.t.ID, err))
	}
	e.showUsage(u)
}

// showUsage tells the UI the attempt's counter when it changed (SPEC §15.1):
// the tokens read so far, or that the transcript can't be read.
func (e *Engine) showUsage(u *attemptUsage) {
	if u.r == nil {
		return
	}
	t := &Tokens{Unavailable: !u.r.Available()}
	if !t.Unavailable {
		got := u.r.Usage()
		t.In = sumIn(got.Main) + sumIn(got.Sub)
		t.Out = satAdd(got.Main.Output, got.Sub.Output)
		t.Models = got.Models
	}
	if u.shown != nil && u.shown.In == t.In && u.shown.Out == t.Out &&
		u.shown.Unavailable == t.Unavailable && slices.Equal(u.shown.Models, t.Models) {
		return
	}
	u.shown = t
	e.emit(Event{Kind: UsageRead, Tokens: t})
}

// sumIn is the input side of c: input + cache read + cache write.
func sumIn(c usage.Counts) int64 {
	return satAdd(satAdd(c.Input, c.CacheRead), c.CacheWrite)
}

// satAdd adds two non-negative counts without overflowing.
func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// auditRank checks the model ids the attempt's main thread ran on against
// the model l's rank resolved to (SPEC §6.9). The first one that doesn't
// match raises rank_mismatch once per attempt: the UI event, the run log
// and the notification, and while the attempt runs Needs you. The session
// is never stopped, switched or told anything; the owner decides. final is
// the read at the attempt's end, when nobody is waited on any more.
func (e *Engine) auditRank(ctx context.Context, l *launch, final bool) {
	u := l.usage
	if u == nil || u.r == nil || u.mismatch {
		return
	}
	got := u.r.Usage()
	bad, ok := usage.Mismatch(l.model, got.Models)
	if !ok {
		return
	}
	u.mismatch = true
	want := l.model
	if l.t.Rank != l.model {
		want = l.t.Rank + " → " + l.model
	}
	// bad has the shape of a model id (usage.ValidModel): no markup, no
	// control characters.
	what := "on " + bad + ", not its rank " + want
	e.emit(Event{Kind: RankMismatch, Detail: what})
	e.log(state.Event{Type: state.EventRankMismatch, Models: got.Models, Detail: what})
	if final {
		e.toast(ctx, notifyRankMismatch, "ran "+what)
		return
	}
	e.needsYou(ctx, notifyRankMismatch, state.ReasonRankMismatch, "the session runs "+what+"; igris leaves it running", what)
}

// endUsage reads l's transcript one last time and logs the attempt's usage
// and audit (SPEC §6.9, §13): when it is done, skipped, retried, lost, or
// the run stops while it runs. Once per attempt; nothing without a session.
func (e *Engine) endUsage(ctx context.Context, l *launch) {
	u := l.usage
	if u == nil {
		return
	}
	e.readUsage(l, true)
	e.auditRank(ctx, l, true)
	l.usage = nil
	ev := state.Event{Type: state.EventUsage}
	if s := l.cur.ClaudeSession; backend.ValidClaudeSession(s) {
		ev.Session = s
	}
	if u.r == nil || !u.r.Available() {
		why := "no transcript reported by the session's hooks"
		if u.r != nil {
			why = "the transcript could not be read"
		}
		ev.Audit, ev.Detail = state.AuditUnavailable, why
		if u.r == nil {
			e.emit(Event{Kind: UsageRead, Tokens: &Tokens{Unavailable: true}})
		}
		if !u.warned {
			e.warn(fmt.Sprintf("token usage of %s unavailable: %s", l.t.ID, why))
		}
		e.log(ev)
		return
	}
	got := u.r.Usage()
	ev.Usage, ev.Models, ev.SubModels = &got.Main, got.Models, got.SubModels
	if !got.Sub.IsZero() {
		ev.Sub = &got.Sub
	}
	switch {
	case u.mismatch:
		ev.Audit = state.AuditMismatch
	case !usage.Checkable(l.model):
		ev.Audit = state.AuditUnchecked
	default:
		ev.Audit = state.AuditOK
	}
	e.log(ev)
}
