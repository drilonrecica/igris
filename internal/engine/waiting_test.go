package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
)

// An agent waiting on background work (a Stop with a running background
// task or a wakeup) needs nobody: no Needs you until waiting_limit. Once
// it is idle the usual needs_input_after counts from then (SPEC §6.3).
func TestWaitingIsNotIdle(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	// Polled every 2s: waiting until 58s, working 60–62s, idle from 64s.
	script := append(states(backend.Waiting, 30), states(backend.Working, 2)...)
	h.be.Script("A-1", append(script, backend.Idle)...)
	h.signalAt(200*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := durations(h.times(AgentWaiting)...), "0s"; got != want {
		t.Errorf("agent_waiting at %s, want %s", got, want)
	}
	if got, want := durations(h.times(AgentWaitingOver)...), "1m0s"; got != want {
		t.Errorf("agent_waiting_over at %s, want %s", got, want)
	}
	if got, want := durations(h.times(NeedsYou)...), "1m34s"; got != want {
		t.Errorf("needs_you at %s, want %s (30s after the agent went idle at 1m4s)", got, want)
	}
	if got, want := h.waits(), "A-1#1 idle, clear A-1#1 idle"; got != want {
		t.Errorf("run log waits = %q, want %q", got, want)
	}
}

// Waiting longer than waiting_limit needs the owner once, with its own
// reason; the done signal ends the wait.
func TestWaitingLimit(t *testing.T) {
	h := newHarness(t, chainPlan, "waiting_limit = \"1m\"\n")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Waiting)
	h.signalAt(200*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := durations(h.times(NeedsYou)...), "1m0s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
	if got, want := h.event(NeedsYou, "A-1").Detail, "the agent has been waiting on background work for 1m"; got != want {
		t.Errorf("needs_you detail %q, want %q", got, want)
	}
	if got, want := h.waits(), "A-1#1 waiting, clear A-1#1 waiting"; got != want {
		t.Errorf("run log waits = %q, want %q", got, want)
	}
	found := false
	for _, toast := range h.toasts() {
		found = found || strings.Contains(toast, "A-1 One: waiting on background work for 1m")
	}
	if !found {
		t.Errorf("toasts = %q, want the waiting one", h.toasts())
	}
}

// An idle agent task is reminded of igris done once, after
// needs_input_after; Needs you follows needs_input_after later if it stays
// idle (SPEC §6.3).
func TestNudgeOnceThenNeedsYou(t *testing.T) {
	h := newHarness(t, chainPlan, "nudge = true\n")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Idle)
	h.signalAt(200*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := durations(h.times(Nudged)...), "30s"; got != want {
		t.Errorf("nudged at %s, want %s", got, want)
	}
	if got, want := durations(h.times(NeedsYou)...), "1m0s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
	prompts := h.be.Prompts("A-1")
	if len(prompts) != 2 || !strings.Contains(prompts[1], "igris done A-1") || !strings.HasPrefix(prompts[1], "igris: you ended your turn") {
		t.Errorf("prompts to A-1 = %q, want the task prompt and one reminder", prompts)
	}
}

// One reminder per attempt: an agent that answers it and stops again
// without igris done needs the owner (no reminder loop).
func TestNudgeOncePerAttempt(t *testing.T) {
	h := newHarness(t, chainPlan, "nudge = true\n")
	h.autoSignalExcept("A-1")
	// Idle until 38s (reminded at 30s), working 40–48s, idle from 50s.
	script := append(states(backend.Idle, 20), states(backend.Working, 5)...)
	h.be.Script("A-1", append(script, backend.Idle)...)
	h.signalAt(200*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := durations(h.times(Nudged)...), "30s"; got != want {
		t.Errorf("nudged at %s, want %s", got, want)
	}
	if got, want := durations(h.times(NeedsYou)...), "1m20s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
	if got := len(h.be.Prompts("A-1")); got != 2 {
		t.Errorf("%d prompts to A-1, want the task prompt and one reminder", got)
	}
}

// A blocked agent waits on a permission or an answer: no reminder.
func TestNoNudgeWhenBlocked(t *testing.T) {
	h := newHarness(t, chainPlan, "nudge = true\n")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Blocked)
	h.signalAt(100*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(Nudged); got != 0 {
		t.Errorf("%d reminders, want none", got)
	}
	if got, want := durations(h.times(NeedsYou)...), "30s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
}

// A reminder that can't be sent doesn't delay Needs you.
func TestNudgeNotSent(t *testing.T) {
	h := newHarness(t, chainPlan, "nudge = true\n")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Idle)
	h.be.FailLaterPrompts("A-1", errors.New("pane busy"))
	h.signalAt(100*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(Nudged); got != 0 {
		t.Errorf("%d reminders reported, want none", got)
	}
	if got, want := durations(h.times(NeedsYou)...), "30s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
	if got := h.event(Warning, "A-1").Detail; !strings.Contains(got, "remind A-1 of igris done: pane busy") {
		t.Errorf("warning = %q", got)
	}
}

// The reminder works: the agent runs igris done, nobody is needed.
func TestNudgeThenDone(t *testing.T) {
	h := newHarness(t, chainPlan, "nudge = true\n")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Idle)
	h.signalAt(40*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.count(Nudged) != 1 || h.count(NeedsYou) != 0 {
		t.Errorf("%d reminders, %d needs_you; want 1 and 0", h.count(Nudged), h.count(NeedsYou))
	}
	if got := h.waits(); got != "" {
		t.Errorf("run log waits = %q, want none", got)
	}
}
