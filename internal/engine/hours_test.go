package engine

// Working hours ([run] hours, SPEC §6.9; decisions.md V110-P1), end to end
// on the fake backend and the fake clock: a start outside the window waits
// for its opening, a user task starts meanwhile, Start anyway lifts the
// hold for one start, a running session goes on past the close, and the
// usage hold takes over when the window opens.

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/state"
)

// hoursConfig is [run] hours from t0+from to t0+to in local time, the
// engine's clock: the window as igris.toml writes it and as the run log
// names it.
func hoursConfig(from, to time.Duration) (toml, shown string) {
	a, b := t0.Add(from).Local().Format("15:04"), t0.Add(to).Local().Format("15:04")
	return "[run]\nhours = \"" + a + "-" + b + "\"\n", a + "–" + b
}

// hoursLines returns the run log's hold lines, hours_* and limit_*, as
// "type window detail".
func (h *harness) hoursLines() string {
	var out []string
	for _, e := range h.logEvents() {
		switch e.Type {
		case state.EventHoursHold, state.EventHoursResume, state.EventLimitHold, state.EventLimitResume:
			out = append(out, strings.TrimSpace(string(e.Type)+" "+e.Window+" "+e.Detail))
		}
	}
	return strings.Join(out, "; ")
}

// Outside the window the first agent task waits for its opening: a run
// hold, not Needs you, logged and shown as the usage hold is, with no
// notification.
func TestHoursHoldWaitsForTheWindow(t *testing.T) {
	cfg, shown := hoursConfig(2*time.Hour, 10*time.Hour)
	h := newHarness(t, chainPlan, cfg)
	if res, err := h.run(); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	open := t0.Add(2 * time.Hour)
	hold := h.event(LimitHold, "")
	if hold.Task != "A-1" || hold.Limit == nil || hold.Limit.Window != state.WindowHours || !hold.Limit.ResetsAt.Equal(open) || hold.Detail != "outside hours "+shown {
		t.Fatalf("limit_hold = %+v (limit %+v)", hold, hold.Limit)
	}
	// Every screen draws `Paused · outside hours · resumes 14:00 (≈ 2h)`
	// from these two: no grace after the opening.
	if hold.Limit.Label() != "outside hours" || !hold.Limit.ResumesAt().Equal(open) {
		t.Errorf("label %q, resumes %s", hold.Limit.Label(), hold.Limit.ResumesAt())
	}
	if ev := h.event(LimitResume, ""); ev.Detail != "window opened" {
		t.Errorf("limit_resume = %+v", ev)
	}
	if d := h.event(TaskStarted, "A-1").At.Sub(open); d < 0 || d > h.cfg.PollInterval.Std() {
		t.Errorf("A-1 started %s after the opening, want within a poll", d)
	}
	if got, want := h.hoursLines(), "hours_hold hours outside hours "+shown+"; hours_resume hours window opened"; got != want {
		t.Errorf("hold lines = %s, want %s", got, want)
	}
	line := logged1(t, h.logEvents(), state.EventHoursHold, "")
	if line.ResetsAt != open.UTC().Format(time.RFC3339) || line.Task != "" {
		t.Errorf("hours_hold line = %+v", line)
	}
	if h.count(NeedsYou) != 0 || h.count(Paused) != 0 {
		t.Errorf("events: %s", h.kinds())
	}
	if got := h.limitToasts(); len(got) != 0 {
		t.Errorf("toasts = %q, want none", got)
	}
}

// A user task needs no session and starts outside the window; the agent
// task beside it waits for the opening.
func TestHoursHoldStartsUserTasks(t *testing.T) {
	cfg, _ := hoursConfig(time.Hour, 9*time.Hour)
	h := newHarness(t, userFirstPlan, cfg)
	h.on(func(ev Event) {
		if ev.Kind == YourTurn {
			h.signalAt(ev.At.Sub(t0)+time.Minute, "A-1")
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d := h.event(TaskStarted, "A-1").At.Sub(t0); d > 5*time.Second {
		t.Errorf("the user task started %s in, want at once", d)
	}
	if got := h.event(LimitHold, "").Task; got != "A-2" {
		t.Errorf("held task = %q", got)
	}
	if d := h.event(TaskStarted, "A-2").At.Sub(t0.Add(time.Hour)); d < 0 || d > h.cfg.PollInterval.Std() {
		t.Errorf("A-2 started %s after the opening", d)
	}
}

// Start anyway lifts the hold for one start: the next agent task outside
// the window is held again.
func TestHoursHoldStartAnyway(t *testing.T) {
	cfg, shown := hoursConfig(3*time.Hour, 11*time.Hour)
	h := newHarness(t, chainPlan, cfg)
	h.on(func(ev Event) {
		if ev.Kind == LimitHold && ev.Task == "A-1" {
			h.eng.Send(Command{Kind: CmdLimitStart})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d := h.event(TaskStarted, "A-1").At.Sub(h.event(LimitHold, "A-1").At); d > h.cfg.PollInterval.Std() {
		t.Errorf("A-1 started %s after Start anyway", d)
	}
	if got := h.event(LimitHold, "A-2").Task; got != "A-2" {
		t.Errorf("A-2 was not held again: %s", h.kinds())
	}
	if d := h.event(TaskStarted, "A-2").At.Sub(t0.Add(3 * time.Hour)); d < 0 || d > h.cfg.PollInterval.Std() {
		t.Errorf("A-2 started %s after the opening", d)
	}
	hold := "hours_hold hours outside hours " + shown
	if got, want := h.hoursLines(), hold+"; hours_resume hours started anyway; "+hold+"; hours_resume hours window opened"; got != want {
		t.Errorf("hold lines = %s, want %s", got, want)
	}
}

// Within the window nothing is held; a session still running at the close
// goes on, and only the next start waits, here for the next day's opening:
// the window crosses midnight in no case the engine needs to know.
func TestHoursHoldLetsRunningSessionsGoOn(t *testing.T) {
	cfg, _ := hoursConfig(-time.Hour, 30*time.Minute)
	h := newHarness(t, chainPlan, cfg)
	h.autoSignalExcept("A-1")
	h.signalAt(time.Hour, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if d := h.event(TaskStarted, "A-1").At.Sub(t0); d > 5*time.Second {
		t.Errorf("A-1 started %s in, want at once", d)
	}
	if h.event(TaskDone, "A-1").At.Before(t0.Add(time.Hour)) {
		t.Error("A-1 ended before its signal")
	}
	hold := h.event(LimitHold, "")
	if hold.Task != "A-2" || !hold.Limit.ResetsAt.Equal(t0.Add(23*time.Hour)) {
		t.Errorf("limit_hold = %+v (limit %+v)", hold, hold.Limit)
	}
	if d := h.event(TaskStarted, "A-2").At.Sub(t0.Add(23 * time.Hour)); d < 0 || d > h.cfg.PollInterval.Std() {
		t.Errorf("A-2 started %s after the next opening", d)
	}
}

// When the window opens while a usage limit holds, the usage hold takes
// over: the run log ends the one before it begins the other.
func TestHoursHoldHandsOverToTheUsageHold(t *testing.T) {
	cfg, shown := hoursConfig(time.Hour, 9*time.Hour)
	h := newHarness(t, chainPlan, cfg+statuslineOn)
	reset := t0.Add(2 * time.Hour)
	h.writeQuota(state.WindowFiveHour, 97, reset)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "hours_hold hours outside hours " + shown + "; hours_resume hours window opened; limit_hold five_hour 5h at 97%; limit_resume five_hour reset"
	if got := h.hoursLines(); got != want {
		t.Errorf("hold lines = %s, want %s", got, want)
	}
	if d := h.event(TaskStarted, "A-1").At.Sub(look.ResumeAt(reset)); d < 0 || d > h.cfg.PollInterval.Std() {
		t.Errorf("A-1 started %s after the reset's resume", d)
	}
	// The hours hold notifies nobody; the usage hold once.
	if got := h.limitToasts(); len(got) != 1 {
		t.Errorf("toasts = %q", got)
	}
}
