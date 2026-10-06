package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

const userPlan = `## A

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **Buy a domain** — any registrar | — | ready | — | user |
| A-2 | **Two** | A-1 | blocked | sonnet | agent |
`

func TestUserTask(t *testing.T) {
	tests := []struct {
		name       string
		finish     func(h *harness) // at 10s
		wantStatus string
		wantLog    string
		wantNote   string
	}{
		{
			name: "igris done",
			finish: func(h *harness) {
				_ = h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionDone, Note: "bought"})
			},
			wantStatus: "A-1=done A-2=done", wantLog: "task_done", wantNote: "bought",
		},
		{
			name: "igris skip applies directly",
			finish: func(h *harness) {
				_ = h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionSkip, Note: "have one"})
			},
			wantStatus: "A-1=skipped A-2=done", wantLog: "task_skipped", wantNote: "have one",
		},
		{
			name:       "owner done",
			finish:     func(h *harness) { h.eng.Send(Command{Kind: CmdDone, Text: "bought"}) },
			wantStatus: "A-1=done A-2=done", wantLog: "task_done", wantNote: "bought",
		},
		{
			name:       "owner skip",
			finish:     func(h *harness) { h.eng.Send(Command{Kind: CmdSkip, Text: "have one"}) },
			wantStatus: "A-1=skipped A-2=done", wantLog: "task_skipped", wantNote: "have one",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, userPlan, "")
			h.clock.At(5*time.Second, func() {
				if got := h.statuses(); got != "A-1=in progress A-2=blocked" {
					t.Errorf("while waiting: %s", got)
				}
				run, err := h.dir.LoadRun()
				if err != nil || run.Current == nil || run.Current.TaskID != "A-1" || run.Current.Session != nil || run.Current.ClaudeSession != "" {
					t.Errorf("state.json while waiting: %+v, %v", run, err)
				}
			})
			h.clock.At(10*time.Second, func() { tt.finish(h) })
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.statuses(); got != tt.wantStatus {
				t.Errorf("statuses = %s, want %s", got, tt.wantStatus)
			}
			if got := h.opened(); got != "A-2" {
				t.Errorf("sessions opened for %q, want only the agent task", got)
			}
			turn := h.event(YourTurn, "A-1")
			if turn.Detail != "**Buy a domain** — any registrar" || !turn.At.Equal(t0) {
				t.Errorf("your_turn = %+v", turn)
			}
			if got := h.toasts(); len(got) < 1 || got[0] != "request: phase A · A-1 Buy a domain: your turn" {
				t.Errorf("toasts = %q", got)
			}
			if got := h.logged(); !strings.HasPrefix(got, "run_started task_started notification "+tt.wantLog) {
				t.Errorf("run log = %s", got)
			}
			kind := TaskDone
			if tt.wantLog == "task_skipped" {
				kind = TaskSkipped
			}
			if got := h.event(kind, "A-1"); got.Detail != tt.wantNote || !got.At.Equal(t0.Add(10*time.Second)) {
				t.Errorf("%s = %+v", kind, got)
			}
			if got := h.count(Asked); got != 0 {
				t.Errorf("%d questions for a user task", got)
			}
			// A user task is never verified or committed: git only runs for A-2.
			if got := len(h.calls("git")); got != 1 {
				t.Errorf("%d git calls, want 1 (for A-2)", got)
			}
		})
	}
}

func TestUserTaskIgnoresOldSignalsAndSessionCommands(t *testing.T) {
	h := newHarness(t, userPlan, "")
	early := state.Signal{ID: "A-1", Action: state.ActionDone, At: t0.Add(-time.Hour)}
	if err := h.dir.WriteSignal(early); err != nil {
		t.Fatal(err)
	}
	h.onEvent = func(ev Event) {
		if ev.Kind == YourTurn {
			h.eng.Send(Command{Kind: CmdRetry})
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true})
		}
	}
	h.clock.At(time.Minute, func() { h.eng.Send(Command{Kind: CmdDone}) })
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.event(TaskDone, "A-1").At; !got.Equal(t0.Add(time.Minute)) {
		t.Errorf("A-1 done at %v, want at 1m by the owner", got)
	}
	if got := h.count(StaleSignal); got != 1 {
		t.Errorf("%d stale_signal events, want 1", got)
	}
	rejected := 0
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, "A-1 is a user task") {
			rejected++
		}
	}
	if rejected != 2 {
		t.Errorf("%d rejected commands, want 2 in %s", rejected, h.kinds())
	}
}
