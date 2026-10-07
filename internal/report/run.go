package report

import (
	"errors"
	"fmt"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Lock states of a RunInfo. They are the --json values.
const (
	LockNone       = "none"
	LockHere       = "running here"
	LockElsewhere  = "running elsewhere"
	LockStale      = "stale"
	LockUnreadable = "unreadable"
)

// runModes are the run modes state.json may hold. engine.ValidMode is the
// source, but engine imports this package, so the list is repeated here.
var runModes = map[string]bool{"default": true, "accept": true, "auto": true, "plan": true, "yolo": true}

// RunInfo is the current run as `igris status` shows it (SPEC §13, §14).
type RunInfo struct {
	// Unreadable is why state.json can't be shown, or "". Then only the
	// lock and signals are filled in.
	Unreadable string   `json:"unreadable,omitempty"`
	Phases     []string `json:"phases,omitempty"`
	Through    string   `json:"through,omitempty"`
	StartedAt  string   `json:"started_at,omitempty"` // RFC 3339, UTC
	Task       string   `json:"task,omitempty"`       // "" between tasks
	Mode       string   `json:"mode,omitempty"`
	Since      string   `json:"since,omitempty"` // the task's start, RFC 3339, UTC
	Session    string   `json:"session,omitempty"`
	Lock       string   `json:"lock"`
	LockDetail string   `json:"lock_detail,omitempty"` // holder, or why it's stale or unreadable
	Signals    []string `json:"signals"`               // "<ID> done|skip", sorted by task ID
}

// RunInput is what NewRunInfo reads. Run and RunErr come from state.PeekRun,
// Lock and LockErr from state.PeekLock, Signals from state.PeekSignals.
type RunInput struct {
	Run        *state.Run
	RunErr     error
	Lock       state.LockState
	LockErr    error
	Signals    []state.Signal
	SignalsBad int // signal files that couldn't be read
}

// NewRunInfo describes the current run, or returns nil when there is none:
// no state.json and no lock. Whatever is wrong with the state files becomes
// text in the result; it never fails.
func NewRunInfo(in RunInput) *RunInfo {
	noRun := errors.Is(in.RunErr, state.ErrNoRun)
	if noRun && !in.Lock.Held && in.LockErr == nil {
		return nil
	}
	r := &RunInfo{Signals: []string{}}
	switch {
	case noRun:
	case in.RunErr != nil:
		r.Unreadable = textsafe.Line(in.RunErr.Error())
	default:
		if why := checkShape(in.Run); why != "" {
			r.Unreadable = why
		} else {
			r.fillRun(in.Run)
		}
	}
	r.fillLock(in.Lock, in.LockErr)
	for _, s := range in.Signals {
		r.Signals = append(r.Signals, textsafe.Line(s.ID)+" "+s.Action)
	}
	if in.SignalsBad > 0 {
		r.Signals = append(r.Signals, fmt.Sprintf("%d unreadable", in.SignalsBad))
	}
	return r
}

// checkShape returns why a state.json that parsed can't be trusted, or "".
// A session or the owner may have written it, so IDs and the mode are checked
// before they are shown.
func checkShape(run *state.Run) string {
	for _, id := range append([]string{run.Through}, run.Phases...) {
		if id != "" && !plan.ValidID(id) {
			return "state unreadable: malformed phase ID in state.json"
		}
	}
	if c := run.Current; c != nil {
		if !plan.ValidID(c.TaskID) {
			return "state unreadable: malformed task ID in state.json"
		}
		// No mode is valid: a user task has no session, and an agent task has
		// none until its session launches.
		if c.Mode != "" && !runModes[c.Mode] {
			return "state unreadable: unknown mode in state.json"
		}
	}
	return ""
}

func (r *RunInfo) fillRun(run *state.Run) {
	r.Phases = append([]string{}, run.Phases...)
	r.Through = run.Through
	r.StartedAt = stamp(run.StartedAt)
	if c := run.Current; c != nil {
		r.Task, r.Mode, r.Since = c.TaskID, c.Mode, stamp(c.StartedAt)
		if c.Session != nil {
			r.Session = textsafe.Line(c.Session.Backend)
			for _, part := range []string{c.Session.TabID, c.Session.PaneID} {
				if part != "" {
					r.Session += " " + textsafe.Line(part)
				}
			}
		}
	}
}

func (r *RunInfo) fillLock(l state.LockState, err error) {
	switch {
	case err != nil:
		r.Lock, r.LockDetail = LockUnreadable, textsafe.Line(err.Error())
	case !l.Held:
		r.Lock = LockNone
	case l.Unreadable:
		r.Lock, r.LockDetail = LockUnreadable, textsafe.Line(l.Reason)
	case l.Stale:
		r.Lock, r.LockDetail = LockStale, fmt.Sprintf("pid %d: %s", l.Info.PID, textsafe.Line(l.Reason))
	case l.Remote:
		r.Lock, r.LockDetail = LockElsewhere, fmt.Sprintf("pid %d on %s", l.Info.PID, textsafe.Line(l.Info.Host))
	default:
		r.Lock, r.LockDetail = LockHere, fmt.Sprintf("pid %d", l.Info.PID)
	}
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
