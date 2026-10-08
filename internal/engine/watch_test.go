package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
)

// states repeats s n times, for scripting the fake backend.
func states(s backend.AgentState, n int) []backend.AgentState {
	out := make([]backend.AgentState, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// signalAt writes id's done signal at offset.
func (h *harness) signalAt(offset time.Duration, id string) {
	h.clock.At(offset, func() {
		if err := h.signal(id); err != nil {
			h.t.Error(err)
		}
	})
}

// times returns the offsets from t0 of the events of kind.
func (h *harness) times(kind EventKind) []time.Duration {
	var out []time.Duration
	for _, ev := range h.events {
		if ev.Kind == kind {
			out = append(out, ev.At.Sub(t0))
		}
	}
	return out
}

func durations(ds ...time.Duration) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.String()
	}
	return strings.Join(parts, " ")
}

func TestNeedsYouOncePerIdleEpisode(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	// Polled every 2s: idle until 38s, working 40–48s, idle again from 50s.
	script := append(states(backend.Idle, 20), states(backend.Working, 5)...)
	h.be.Script("A-1", append(script, backend.Idle)...)
	h.signalAt(100*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := durations(h.times(NeedsYou)...), "30s 1m20s"; got != want {
		t.Errorf("needs_you at %s, want %s", got, want)
	}
	// The second episode ends with the done signal at 1m40s.
	if got, want := durations(h.times(NeedsYouClear)...), "40s 1m40s"; got != want {
		t.Errorf("needs_you_clear at %s, want %s", got, want)
	}
	needs := 0
	for _, toast := range h.toasts() {
		if strings.HasPrefix(toast, "request: ") && strings.Contains(toast, "A-1 One: needs you (idle 30s without igris done)") {
			needs++
		}
	}
	if needs != 2 {
		t.Errorf("toasts = %q, want two needs-you toasts for A-1", h.toasts())
	}
	// The second episode ends with the done signal: igris no longer waits.
	if got, want := h.waits(), "A-1#1 idle, clear A-1#1 idle, A-1#1 idle, clear A-1#1 idle"; got != want {
		t.Errorf("run log waits = %q, want %q", got, want)
	}
}

// A blocked agent (a permission prompt, a question) gets its own reason, so
// the owner knows what to look for.
func TestNeedsYouBlockedReason(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", states(backend.Blocked, 20)...)
	h.signalAt(60*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.toasts(); len(got) == 0 || got[0] != "request: phase A · A-1 One: needs you (waiting for a permission or an answer)" {
		t.Errorf("toasts = %q", got)
	}
	if got := h.waits(); got != "A-1#1 blocked, clear A-1#1 blocked" {
		t.Errorf("run log waits = %q", got)
	}
}

// Without herdr's integration the state may stay unknown; that is no reason
// to call the owner.
func TestUnknownStateNeverNeedsYou(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Unknown)
	h.signalAt(5*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(NeedsYou); got != 0 {
		t.Errorf("%d needs_you events, want none", got)
	}
}

func TestSessionLost(t *testing.T) {
	tests := []struct {
		name       string
		answer     Command
		wantStatus string
		wantOpened string
		check      func(t *testing.T, h *harness)
	}{
		{
			name: "retry fresh", answer: Command{Kind: CmdRetry},
			wantStatus: "A-1=done A-2=done A-3=done", wantOpened: "A-1 A-1 A-2 A-3",
			check: func(t *testing.T, h *harness) {
				first, second := h.be.Opened()[0], h.be.Opened()[1]
				if id := arg(second, "--session-id"); id == "" || id == arg(first, "--session-id") {
					t.Errorf("fresh session ID %q, want a new one (first was %q)", id, arg(first, "--session-id"))
				}
				if p := h.be.Prompts("A-1"); len(p) != 2 || !strings.Contains(p[1], "## Resumed task") {
					t.Errorf("prompts = %q, want the resumed task prompt second", p)
				}
				if got := h.event(Retrying, "A-1").Detail; got != "fresh" {
					t.Errorf("retrying detail %q", got)
				}
			},
		},
		{
			name: "retry continue", answer: Command{Kind: CmdRetry, Continue: true},
			wantStatus: "A-1=done A-2=done A-3=done", wantOpened: "A-1 A-1 A-2 A-3",
			check: func(t *testing.T, h *harness) {
				first, second := h.be.Opened()[0], h.be.Opened()[1]
				if got, want := arg(second, "--resume"), arg(first, "--session-id"); got == "" || got != want {
					t.Errorf("--resume %q, want the first session %q", got, want)
				}
				if arg(second, "--session-id") != "" || arg(second, "--model") != "sonnet" {
					t.Errorf("continue args = %q", second.Args)
				}
				if p := h.be.Prompts("A-1"); len(p) != 2 || p[1] != prompt.Continue("A-1") {
					t.Errorf("prompts = %q, want the continue prompt second", p)
				}
				run, err := h.dir.LoadRun()
				if err != nil {
					t.Fatal(err)
				}
				if run.Current != nil {
					t.Errorf("state.json current = %+v after the run", run.Current)
				}
			},
		},
		{
			name: "mark done", answer: Command{Kind: CmdDone, Text: "finished by hand"},
			wantStatus: "A-1=done A-2=done A-3=done", wantOpened: "A-1 A-2 A-3",
			check: func(t *testing.T, h *harness) {
				if got := h.event(TaskDone, "A-1").Detail; got != "finished by hand" {
					t.Errorf("done note %q", got)
				}
				if got := len(h.be.Closed()); got != 2 {
					t.Errorf("closed %d sessions, want 2 (the lost one is already gone)", got)
				}
			},
		},
		{
			name: "skip", answer: Command{Kind: CmdSkip, Text: "not needed"},
			wantStatus: "A-1=skipped A-2=done A-3=done", wantOpened: "A-1 A-2 A-3",
			check: func(t *testing.T, h *harness) {
				if got := h.event(TaskSkipped, "A-1").Detail; got != "not needed" {
					t.Errorf("skip reason %q", got)
				}
				if got := h.logged(); !strings.Contains(got, "task_skipped task_started task_done") {
					t.Errorf("run log = %s", got)
				}
			},
		},
		{
			name: "stop", answer: Command{Kind: CmdStop},
			wantStatus: "A-1=in progress A-2=blocked A-3=blocked", wantOpened: "A-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			opened := 0
			h.be.SetAutoSignal(func(_ context.Context, id string) error {
				if id == "A-1" {
					if opened++; opened == 1 {
						return nil // the first session dies without a signal
					}
				}
				return h.signal(id)
			})
			h.be.Script("A-1", backend.Working, backend.Exited)
			h.onEvent = func(ev Event) {
				if ev.Kind == Asked && ev.Question == QuestionSessionLost {
					h.eng.Send(tt.answer)
				}
			}
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.statuses(); !strings.HasPrefix(got, tt.wantStatus) {
				t.Errorf("statuses = %s, want %s…", got, tt.wantStatus)
			}
			if got := h.opened(); got != tt.wantOpened {
				t.Errorf("sessions opened for %q, want %q", got, tt.wantOpened)
			}
			if got := h.count(SessionLost); got != 1 {
				t.Errorf("%d session_lost events, want 1", got)
			}
			lostToasts := 0
			for _, toast := range h.toasts() {
				if strings.Contains(toast, "A-1 One: session lost") {
					lostToasts++
				}
			}
			if lostToasts != 1 {
				t.Errorf("toasts = %q, want one session-lost toast", h.toasts())
			}
			if tt.check != nil {
				tt.check(t, h)
			}
		})
	}
}

// A session that is gone before its task prompt arrives is lost, not a
// failed run.
func TestSessionGoneBeforeThePrompt(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened && ev.Task == "A-1":
			h.be.Kill(*ev.Session)
		case ev.Kind == Asked && ev.Question == QuestionSessionLost:
			h.eng.Send(Command{Kind: CmdStop})
		}
	}
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v; want stopped", res.Outcome, err)
	}
	if got := h.count(SessionLost); got != 1 {
		t.Errorf("%d session_lost events, want 1", got)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=in progress A-2=blocked") {
		t.Errorf("statuses = %s", got)
	}
}

// A done signal still counts after the session is gone: `igris done` was its
// last action.
func TestSignalAfterTheSessionIsLost(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Exited)
	h.signalAt(10*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=done A-2=done") {
		t.Errorf("statuses = %s", got)
	}
	if got := h.event(TaskDone, "A-1").At; !got.Equal(t0.Add(10 * time.Second)) {
		t.Errorf("A-1 done at %v", got)
	}
}

func TestStraySignalsAreReportedOnce(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	if err := h.signal("B-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir.SignalsDir(), "Z-9.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.signalAt(10*time.Second, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(StraySignal); got != 1 {
		t.Errorf("%d stray_signal events, want 1 in %s", got, h.kinds())
	}
	if ev := h.event(StraySignal, ""); !strings.Contains(ev.Detail, "B-1") {
		t.Errorf("stray_signal detail = %q", ev.Detail)
	}
	bad := 0
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, "Z-9.json") {
			bad++
		}
	}
	if bad != 1 {
		t.Errorf("%d warnings about the unreadable signal, want 1", bad)
	}
	if sig, err := h.dir.ReadSignal("B-1"); err != nil || sig == nil {
		t.Errorf("stray signal not kept: %+v, %v", sig, err)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s; a stray signal was applied", got)
	}
}

func TestSkipSignalNeedsConfirmation(t *testing.T) {
	tests := []struct {
		name       string
		yes        bool
		wantStatus string
	}{
		{"confirmed", true, "A-1=skipped A-2=done"},
		{"declined", false, "A-1=done A-2=done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			h.be.SetAutoSignal(func(_ context.Context, id string) error {
				if id == "A-1" {
					return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionSkip, Note: "not needed"})
				}
				return h.signal(id)
			})
			h.onEvent = func(ev Event) {
				if ev.Kind == Asked && ev.Question == QuestionConfirmSkip {
					h.clock.At(h.clock.Now().Sub(t0)+10*time.Second, func() { h.eng.Send(Command{Kind: CmdAnswer, Yes: tt.yes}) })
				}
			}
			if !tt.yes {
				// After the refusal the session carries on and finishes.
				h.clock.At(30*time.Second, func() {
					if sig, err := h.dir.ReadSignal("A-1"); err != nil || sig != nil {
						t.Errorf("declined skip signal still there: %+v, %v", sig, err)
					}
				})
				h.signalAt(40*time.Second, "A-1")
			}
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.statuses(); !strings.HasPrefix(got, tt.wantStatus) {
				t.Errorf("statuses = %s, want %s…", got, tt.wantStatus)
			}
			if got := h.count(Asked); got != 1 {
				t.Errorf("%d asked events, want 1", got)
			}
			if ev := h.event(NeedsYou, "A-1"); !strings.Contains(ev.Detail, "skip") {
				t.Errorf("needs_you = %q", ev.Detail)
			}
			if ev := h.event(Asked, "A-1"); !strings.Contains(ev.Detail, "not needed") {
				t.Errorf("asked = %q, want the reason", ev.Detail)
			}
			for _, toast := range h.toasts() {
				if strings.Contains(toast, "not needed") {
					t.Errorf("toast %q carries the session's text", toast)
				}
			}
			wantWaits := map[bool]string{true: "A-1#1 skip_request", false: "A-1#1 skip_request, clear A-1#1 skip_request, A-1#1 idle, clear A-1#1 idle"}[tt.yes] // idle until its signal at 40s
			if got := h.waits(); got != wantWaits {
				t.Errorf("run log waits = %q, want %q", got, wantWaits)
			}
			if tt.yes {
				if got := h.event(TaskSkipped, "A-1").Detail; got != "not needed" {
					t.Errorf("skip reason %q", got)
				}
				if got := len(h.be.Closed()); got != 3 {
					t.Errorf("closed %d sessions, want 3", got)
				}
			}
		})
	}
}

func TestCloseWaitsForTheAgentToSettle(t *testing.T) {
	tests := []struct {
		name   string
		script []backend.AgentState
		want   time.Duration
	}{
		{"settles", append(states(backend.Working, 5), backend.Idle), 10 * time.Second},
		{"never settles", []backend.AgentState{backend.Working}, closeIdleWait},
		{"exits", []backend.AgentState{backend.Working, backend.Exited}, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			h.be.Script("A-1", tt.script...)
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.event(TaskDone, "A-1").At.Sub(t0); got != tt.want {
				t.Errorf("A-1 closed after %s, want %s", got, tt.want)
			}
			if got := h.count(SessionLost); got != 0 {
				t.Errorf("%d session_lost events after acceptance", got)
			}
		})
	}
}

func TestCommandsThatDontApplyAreRejected(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-2")
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == TaskStarted && ev.Task == "A-1":
			h.eng.Send(Command{Kind: CmdPause})
		case ev.Kind == Paused:
			h.eng.Send(Command{Kind: CmdDone}) // no task is running
			h.eng.Send(Command{Kind: CmdPause})
		case ev.Kind == SessionOpened && ev.Task == "A-2":
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true}) // nothing was asked
			h.eng.Send(Command{Kind: CmdDone, Text: "owner"})
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var warnings []string
	for _, ev := range h.events {
		if ev.Kind == Warning {
			warnings = append(warnings, ev.Detail)
		}
	}
	if got, want := strings.Join(warnings, "; "), "ignored done: no task is running; ignored answer: no question is waiting for an answer"; got != want {
		t.Errorf("warnings = %q, want %q", got, want)
	}
	if got := h.event(TaskDone, "A-2").Detail; got != "owner" {
		t.Errorf("A-2 done note %q", got)
	}
}
