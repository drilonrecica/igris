package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

// A pasted prompt that Claude Code never submitted (SPEC §6.3): the hook
// record still shows the event from before the prompt, so igris presses
// Enter once.

// hookAtPrompt makes the A-1 session record the hook event at its first
// prompt, the way Claude Code's hooks would; "" records nothing. Every
// other task signals done at once.
func (h *harness) hookAtPrompt(event string, st backend.AgentState) {
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id != "A-1" {
			return h.signal(id)
		}
		if event == "" {
			return nil
		}
		opened := h.be.Opened()
		uuid := opened[len(opened)-1].ClaudeSession
		return state.WriteAgentState(h.root, uuid, state.AgentState{State: st, Event: event, At: h.clock.Now()})
	})
}

// lostEnterWarnings returns the times of the warnings about a re-sent
// Enter.
func (h *harness) lostEnterWarnings() []time.Duration {
	var out []time.Duration
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, "sent Enter once") {
			out = append(out, ev.At.Sub(t0))
		}
	}
	return out
}

// The event stays SessionStart: Enter goes out once, after 15 s idle, the
// prompt text is never sent again, and Needs you follows as usual.
func TestLostEnterSentOnce(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.hookAtPrompt("SessionStart", backend.Idle)
	h.signalAt(100*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.be.Submits("A-1"); got != 1 {
		t.Errorf("Enter sent %d times, want once", got)
	}
	if got, want := durations(h.lostEnterWarnings()...), "16s"; got != want {
		t.Errorf("lost-Enter warnings at %q, want %q", got, want)
	}
	if got := h.event(Warning, "A-1").Detail; got != "the prompt didn't seem submitted; sent Enter once" {
		t.Errorf("warning = %q", got)
	}
	if got := len(h.be.Prompts("A-1")); got != 1 {
		t.Errorf("%d prompts sent to A-1, want only the task prompt", got)
	}
	// Still unsubmitted: Needs you is raised as usual.
	if got, want := h.waits(), "A-1#1 idle, clear A-1#1 idle"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
	if got, want := durations(h.times(NeedsYou)...), "30s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
}

func TestLostEnterNotSent(t *testing.T) {
	tests := []struct {
		name  string
		event string
		st    backend.AgentState // the hook record's state
		be    backend.AgentState // what the backend reports
	}{
		{"prompt submitted", "UserPromptSubmit", backend.Working, backend.Idle},
		{"tool used", "PostToolUse", backend.Working, backend.Idle},
		{"turn finished", "Stop", backend.Idle, backend.Idle},
		{"no hook record", "", "", backend.Idle},
		{"blocked at a prompt", "SessionStart", backend.Idle, backend.Blocked},
		{"working", "SessionStart", backend.Idle, backend.Working},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			h.hookAtPrompt(tt.event, tt.st)
			h.be.Script("A-1", tt.be)
			h.signalAt(100*time.Second, "A-1")
			if _, err := h.run(); err != nil {
				t.Fatal(err)
			}
			if got := h.be.Submits("A-1"); got != 0 {
				t.Errorf("Enter sent %d times, want never", got)
			}
			if got := h.lostEnterWarnings(); len(got) != 0 {
				t.Errorf("lost-Enter warnings at %v, want none", got)
			}
		})
	}
}

// The agent worked after the prompt (the backend saw it) and is idle again
// with a hook record from before: it was submitted, no Enter.
func TestLostEnterNotSentAfterWorking(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.hookAtPrompt("SessionStart", backend.Idle)
	h.be.Script("A-1", append(states(backend.Working, 3), backend.Idle)...)
	h.signalAt(100*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.be.Submits("A-1"); got != 0 {
		t.Errorf("Enter sent %d times, want never", got)
	}
}

// Each prompt gets its own Enter: a fresh retry's prompt is checked again.
func TestLostEnterOncePerPrompt(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.hookAtPrompt("SessionStart", backend.Idle)
	h.clock.At(60*time.Second, func() { h.eng.Send(Command{Kind: CmdRetry}) })
	h.signalAt(150*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.be.Submits("A-1"); got != 2 {
		t.Errorf("Enter sent %d times, want once per session", got)
	}
	if got := len(h.be.Prompts("A-1")); got != 2 {
		t.Errorf("%d prompts sent to A-1, want 2 (task prompt, retry)", got)
	}
}

// A prompt held at a startup prompt is checked from when it goes out.
func TestLostEnterAfterHeldPrompt(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.hookAtPrompt("SessionStart", backend.Idle)
	h.be.StartupPrompt("A-1")
	h.be.Script("A-1", append(states(backend.Blocked, 5), backend.Idle)...)
	h.signalAt(100*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.be.Submits("A-1"); got != 1 {
		t.Errorf("Enter sent %d times, want once", got)
	}
	// Delivered at 10s, idle from 12s: Enter at the first poll 15s on.
	if got, want := durations(h.lostEnterWarnings()...), "26s"; got != want {
		t.Errorf("lost-Enter warnings at %q, want %q", got, want)
	}
}
