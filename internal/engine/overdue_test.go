package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

// timeoutPlan is chainPlan with a Timeout column: A-1 may run a minute.
const timeoutPlan = `# Demo plan

## A — First phase

| ID | Task | Deps | Status | Model | Owner | Timeout |
|---|---|---|---|---|---|---|
| A-1 | **One** — the first thing | — | ready | sonnet | agent | 1m |
| A-2 | **Two** — the second thing | A-1 | blocked | opus | agent | — |
`

// A task past its Timeout is reported once per attempt and its session is
// left alone (SPEC §6.3).
func TestTaskOverdueOncePerAttempt(t *testing.T) {
	h := newHarness(t, timeoutPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working) // busy the whole time: no idle Needs you
	h.signalAt(5*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Polled every 2s from the prompt at 0s: the first poll past 1m.
	if got, want := durations(h.times(TaskOverdue)...), "1m2s"; got != want {
		t.Errorf("task_overdue at %s, want %s", got, want)
	}
	if ev := h.event(TaskOverdue, "A-1"); ev.Detail != "running longer than its Timeout 1m" {
		t.Errorf("task_overdue detail = %q", ev.Detail)
	}
	if got, want := durations(h.times(NeedsYou)...), "1m2s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
	if got := h.toasts(); len(got) == 0 || got[0] != "request: phase A · A-1 One: needs you (running longer than its Timeout 1m)" {
		t.Errorf("toasts = %q", got)
	}
	events, err := h.dir.Events()
	if err != nil {
		t.Fatal(err)
	}
	var overdue []state.Event
	for _, e := range events {
		if e.Type == state.EventTaskOverdue {
			overdue = append(overdue, e)
		}
	}
	if len(overdue) != 1 || overdue[0].Task != "A-1" || overdue[0].Rank != "sonnet" || overdue[0].Detail != "running longer than its Timeout 1m" {
		t.Errorf("task_overdue log entries = %+v", overdue)
	}
	// Closed only once accepted: at the done signal, never for the Timeout.
	if closed := h.be.Closed(); len(closed) != 2 {
		t.Errorf("closed %d sessions, want A-1's and A-2's after their signals", len(closed))
	}
	if ev := h.event(TaskDone, "A-1"); ev.At.Sub(t0) < 5*time.Minute {
		t.Errorf("A-1 done at %s, want after its signal at 5m", ev.At.Sub(t0))
	}
	if got := h.statuses(); got != "A-1=done A-2=done" {
		t.Errorf("statuses = %s", got)
	}
	if h.count(TaskOverdue) != 1 {
		t.Errorf("%d task_overdue events, want 1 (A-2 has no Timeout)", h.count(TaskOverdue))
	}
	// The agent works throughout: the wait ends at the next poll
	// (Decision W, V05-P1).
	if got := h.waits(); got != "A-1#1 task_overdue, clear A-1#1 task_overdue" {
		t.Errorf("run log waits = %q", got)
	}
}

// A retry is a new attempt with a new clock.
func TestTaskOverdueRetryStartsANewClock(t *testing.T) {
	h := newHarness(t, timeoutPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working)
	h.clock.At(90*time.Second, func() { h.eng.Send(Command{Kind: CmdRetry}) })
	h.signalAt(10*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	over := h.times(TaskOverdue)
	if len(over) != 2 || over[0] != 62*time.Second {
		t.Fatalf("task_overdue at %s, want twice: at 1m2s and a minute after the retry", durations(over...))
	}
	opened := h.times(SessionOpened)
	if len(opened) < 2 || over[1]-opened[1] <= time.Minute || over[1]-opened[1] > time.Minute+2*time.Second {
		t.Errorf("second task_overdue at %s, sessions opened at %s", over[1], durations(opened...))
	}
	if !strings.Contains(h.logged(), "task_overdue") || strings.Count(h.logged(), "task_overdue") != 2 {
		t.Errorf("run log = %s, want two task_overdue", h.logged())
	}
}

// A task done within its Timeout is never overdue; a user task ignores it.
func TestTaskOverdueNotRaised(t *testing.T) {
	const userPlan = `## A — First phase

| ID | Task | Deps | Status | Model | Owner | Timeout |
|---|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent | 10m |
| A-2 | **Two** | A-1 | blocked | — | user | 1m |
`
	h := newHarness(t, userPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working)
	h.signalAt(9*time.Minute, "A-1")
	h.on(func(ev Event) {
		if ev.Kind == YourTurn {
			h.clock.At(ev.At.Sub(t0)+5*time.Minute, func() { h.eng.Send(Command{Kind: CmdDone}) })
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := h.count(TaskOverdue); n != 0 {
		t.Errorf("%d task_overdue events, want none", n)
	}
	if got := h.statuses(); got != "A-1=done A-2=done" {
		t.Errorf("statuses = %s", got)
	}
}

// On resume the clock starts again at the reattach: elapsed time is not
// saved.
func TestTaskOverdueResumeRestartsTheClock(t *testing.T) {
	h := newHarness(t, timeoutPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working)
	h.stopMidTask(50 * time.Second)
	if h.count(TaskOverdue) != 0 {
		t.Fatal("overdue before the stop")
	}
	resumedAt := h.clock.Now().Sub(t0)
	h.signalAt(resumedAt+3*time.Minute, "A-1")
	if _, err := h.run(resumeLast); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	over := h.times(TaskOverdue)
	if len(over) != 1 || over[0]-resumedAt <= time.Minute {
		t.Errorf("task_overdue at %s after a resume at %s, want once, over a minute after it", durations(over...), resumedAt)
	}
}

// A first prompt held at a startup prompt (SPEC §6.3) starts the clock
// when it is delivered, not when igris handed it over.
func TestTaskOverdueClockStartsAtDelivery(t *testing.T) {
	h := newHarness(t, timeoutPlan, "")
	h.autoSignalExcept("A-1")
	h.be.StartupPrompt("A-1")
	states := make([]backend.AgentState, 30) // a minute at the folder-trust question
	for i := range states {
		states[i] = backend.Blocked
	}
	h.be.Script("A-1", append(states, backend.Idle, backend.Working)...)
	h.signalAt(5*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := durations(h.times(TaskOverdue)...), "2m2s"; got != want {
		t.Errorf("task_overdue at %s, want %s (a minute after the delivery at 1m0s)", got, want)
	}
}
