package engine

import (
	"errors"
	"fmt"
	"io/fs"
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
}

// endUsage reads l's transcript one last time and logs the attempt's usage
// (SPEC §6.9, §13): when it is done, skipped, retried, lost, or the run
// stops while it runs. Once per attempt; nothing without a session.
func (e *Engine) endUsage(l *launch) {
	u := l.usage
	if u == nil {
		return
	}
	e.readUsage(l, true)
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
	e.log(ev)
}
