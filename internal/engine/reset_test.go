package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

// resetAt writes a reset signal for id at offset, like `igris reset` while
// igris runs.
func (h *harness) resetAt(offset time.Duration, id string, force bool) {
	h.clock.At(offset, func() {
		if err := h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionReset, Force: force}); err != nil {
			h.t.Error(err)
		}
	})
}

// continueWhenPaused turns pause-after-task off once the run holds, with
// every session signalling done from then on.
func (h *harness) continueWhenPaused() {
	h.on(func(ev Event) {
		if ev.Kind == Paused {
			h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
			h.eng.Send(Command{Kind: CmdPause})
		}
	})
}

// Resetting the current task closes its session at once, forgets it,
// rewrites its Status and pauses the run (SPEC §6.2).
func TestResetCurrentTask(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working) // busy: a settle wait would run out first
	h.resetAt(9*time.Second, "A-1", false)
	h.on(func(ev Event) {
		if ev.Kind == TaskReset {
			if got := h.statuses(); got != "A-1=ready A-2=blocked A-3=blocked B-1=blocked" {
				t.Errorf("statuses at the reset = %s", got)
			}
			if run, err := h.dir.LoadRun(); err != nil || run.Current != nil {
				t.Errorf("state.json still names a task: %+v, %v", run, err)
			}
			if len(h.be.Closed()) != 1 {
				t.Errorf("closed %d sessions at the reset, want A-1's", len(h.be.Closed()))
			}
		}
	})
	h.continueWhenPaused()
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	ev := h.event(TaskReset, "A-1")
	if ev.Detail != "A-1: in progress → ready" || ev.Title != "One" || len(ev.Changes) != 1 {
		t.Errorf("task_reset = %+v", ev)
	}
	// Polled every 2s: applied at the first poll after 9s, without the idle wait.
	if got := durations(h.times(TaskReset)...); got != "10s" {
		t.Errorf("task_reset at %s, want 10s", got)
	}
	if !strings.Contains(h.kinds(), "task_reset pause_on paused pause_off task_started") {
		t.Errorf("events = %s", h.kinds())
	}
	if got := h.opened(); got != "A-1 A-1 A-2 A-3" {
		t.Errorf("sessions = %s, want A-1 started again after the pause", got)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	events, err := h.dir.Events()
	if err != nil {
		t.Fatal(err)
	}
	var reset []state.Event
	for _, e := range events {
		if e.Type == state.EventTaskReset {
			reset = append(reset, e)
		}
	}
	if len(reset) != 1 || reset[0].Task != "A-1" || reset[0].Detail != "in progress" || reset[0].Rank != "sonnet" || reset[0].Model != "sonnet" {
		t.Errorf("task_reset log = %+v", reset)
	}
	if s, _ := h.dir.ReadSignal("A-1"); s != nil {
		t.Errorf("signal left behind: %+v", s)
	}
	if h.count(TaskDone) != 3 || h.count(StraySignal) != 0 {
		t.Errorf("events = %s", h.kinds())
	}
}

// Another task gets the Status write and the sync; the current task and
// the run go on (SPEC §6.2).
func TestResetOtherTask(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-2")
	h.resetAt(20*time.Second, "A-1", true)
	h.on(func(ev Event) {
		if ev.Kind == TaskReset {
			if got := h.statuses(); got != "A-1=ready A-2=in progress A-3=blocked B-1=blocked" {
				t.Errorf("statuses at the reset = %s", got)
			}
			if h.current() != "A-2" {
				t.Errorf("current task = %q, want A-2 still", h.current())
			}
		}
	})
	h.signalAt(40*time.Second, "A-2")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if ev := h.event(TaskReset, "A-1"); ev.Detail != "A-1: done → ready" {
		t.Errorf("task_reset = %+v", ev)
	}
	if got := h.opened(); got != "A-1 A-2 A-1 A-3" {
		t.Errorf("sessions = %s, want A-1 run again after A-2", got)
	}
	if h.count(PauseOn) != 0 {
		t.Errorf("the run paused: %s", h.kinds())
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
}

// The running igris checks the status again: a done task without force,
// or a ready one, is reported and left alone.
func TestResetRecheckedByTheRun(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-2")
	h.resetAt(20*time.Second, "A-1", false)
	h.resetAt(20*time.Second, "A-3", true) // blocked: nothing to reset
	h.signalAt(40*time.Second, "A-2")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	for _, ev := range h.events {
		if ev.Kind == Warning {
			warnings = append(warnings, ev.Detail)
		}
	}
	want := []string{"not reset: A-1 is done; pass --force to reset it", "A-3 is blocked: nothing to reset"}
	if strings.Join(warnings, "|") != strings.Join(want, "|") {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
	if h.count(TaskReset) != 0 || h.opened() != "A-1 A-2 A-3" {
		t.Errorf("events = %s, sessions %s", h.kinds(), h.opened())
	}
	for _, id := range []string{"A-1", "A-3"} {
		if s, _ := h.dir.ReadSignal(id); s != nil {
			t.Errorf("%s's reset signal left behind", id)
		}
	}
}

// A user task is reset the same way.
func TestResetUserTask(t *testing.T) {
	const userPlan = `## A — First phase

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **Sign** — the owner signs | — | ready | — | user |
| A-2 | **Two** | A-1 | blocked | sonnet | agent |
`
	h := newHarness(t, userPlan, "")
	h.resetAt(5*time.Second, "A-1", false)
	h.on(func(ev Event) {
		if ev.Kind == Paused {
			h.eng.Send(Command{Kind: CmdPause})
		}
		if ev.Kind == YourTurn && h.count(TaskReset) == 1 {
			h.eng.Send(Command{Kind: CmdDone, Text: "signed"})
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if ev := h.event(TaskReset, "A-1"); ev.Detail != "A-1: in progress → ready" {
		t.Errorf("task_reset = %+v", ev)
	}
	if h.count(YourTurn) != 2 || h.count(PauseOn) != 1 {
		t.Errorf("events = %s", h.kinds())
	}
	if got := h.statuses(); got != "A-1=done A-2=done" {
		t.Errorf("statuses = %s", got)
	}
}

// A reset still pending when the next run starts is applied before
// anything is selected: the interrupted task's session is closed and it is
// not picked up again until the owner continues.
func TestResetPendingAtStart(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset}); err != nil {
		t.Fatal(err)
	}
	h.continueWhenPaused()
	res, err := h.run(resumeLast)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed run = %s, %v", res.Outcome, err)
	}
	if h.count(TaskResumed) != 0 {
		t.Errorf("the interrupted task was resumed: %s", h.kinds())
	}
	if !strings.HasPrefix(h.kinds(), "run_started task_reset pause_on") {
		t.Errorf("events = %s", h.kinds())
	}
	if closed := h.be.Closed(); len(closed) == 0 || closed[0].PaneID == "" {
		t.Errorf("closed = %v, want the interrupted session closed first", closed)
	}
	if got := h.opened(); got != "A-1 A-1 A-2 A-3" {
		t.Errorf("sessions = %s", got)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	if got := h.logged(); !strings.Contains(got, "run_started task_reset task_started") {
		t.Errorf("run log = %s", got)
	}
}

// A reset waits while the plan is invalid and is applied once it is
// valid again.
func TestResetWaitsForAValidPlan(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-2")
	h.clock.At(20*time.Second, func() {
		h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| A-3 |", "| A 3 |", 1))
		if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset, Force: true}); err != nil {
			t.Error(err)
		}
	})
	h.clock.At(30*time.Second, func() {
		h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| A 3 |", "| A-3 |", 1))
	})
	h.signalAt(40*time.Second, "A-2")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := durations(h.times(TaskReset)...); got != "30s" {
		t.Errorf("task_reset at %s, want once the plan is valid (30s); events %s", got, h.kinds())
	}
	if w := h.event(Warning, ""); !strings.HasPrefix(w.Detail, "the reset of A-1 waits until the plan is valid") {
		t.Errorf("warning = %q", w.Detail)
	}
}
