package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
)

func TestPlanWatchDiff(t *testing.T) {
	const base = `## A

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent | — |
| A-2 | **Two** | A-1 | blocked | opus | agent | — |
| A-3 | **Three** | A-2 | blocked | fable | agent + user | — |
`
	tests := []struct {
		name  string
		edit  func(string) string
		wrote []plan.Change
		want  string // "; "-joined changes, "" for none
	}{
		{"unchanged", func(s string) string { return s }, nil, ""},
		{"own write", func(s string) string {
			return strings.Replace(strings.Replace(s, "| ready |", "| done |", 1), "| A-1 | blocked |", "| A-1 | ready |", 1)
		}, []plan.Change{{ID: "A-1", From: plan.Ready, To: plan.Done}, {ID: "A-2", From: plan.Blocked, To: plan.Ready}}, ""},
		{"status by someone else", func(s string) string { return strings.Replace(s, "| ready |", "| done |", 1) }, nil,
			"A-1 Status ready → done"},
		{"mode", func(s string) string {
			return strings.Replace(s, "| opus | agent | — |", "| opus | agent | auto |", 1)
		}, nil,
			"A-2 Mode — → auto"},
		{"model", func(s string) string { return strings.Replace(s, "| opus |", "| fable |", 1) }, nil,
			"A-2 Model opus → fable"},
		{"owner and deps", func(s string) string {
			return strings.Replace(s, "| A-2 | blocked | fable | agent + user |", "| — | blocked | fable | user |", 1)
		}, nil,
			"A-3 Owner agent + user → user; A-3 Deps A-2 → —"},
		{"text", func(s string) string { return strings.Replace(s, "**Two**", "**Two** — rm -rf", 1) }, nil,
			"A-2 Task text changed"},
		{"added and removed", func(s string) string {
			return strings.Replace(s, "| A-3 | **Three** | A-2 | blocked | fable | agent + user | — |\n", "| A-4 | **Four** | — | ready | sonnet | agent | — |\n", 1)
		}, nil, "A-4 added; A-3 removed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w planWatch
			w.reset(plan.Parse("tasks.md", []byte(base), plan.Options{}))
			w.wrote(tt.wrote)
			edited := plan.Parse("tasks.md", []byte(tt.edit(base)), plan.Options{})
			got := strings.Join(w.diff(edited), "; ")
			if got != tt.want {
				t.Errorf("diff = %q, want %q", got, tt.want)
			}
			if again := w.diff(edited); len(again) != 0 {
				t.Errorf("second diff = %q, want none: the edited plan is the new baseline", again)
			}
		})
	}
}

// A session edits a later task while its own runs: igris reports the cells
// and holds until the owner resumes; the next task launches only then.
func TestPlanEditedBySessionHoldsTheRun(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| A-1 | blocked | opus |", "| A-1 | blocked | fable |", 1))
		}
	}
	h.clock.At(time.Minute, func() {
		if got := h.opened(); got != "A-1" {
			t.Errorf("while held: sessions opened for %q, want only A-1", got)
		}
		if got := h.count(PlanChanged); got != 1 {
			t.Errorf("while held: %d plan_changed events, want 1", got)
		}
		h.eng.Send(Command{Kind: CmdPause}) // resume
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.opened(); got != "A-1 A-2 A-3" {
		t.Errorf("sessions opened for %q", got)
	}
	ev := h.event(PlanChanged, "")
	if !strings.Contains(ev.Detail, "A-2 Model opus → fable") || !strings.Contains(ev.Detail, "paused") {
		t.Errorf("plan_changed detail = %q", ev.Detail)
	}
	if got, want := h.event(TaskStarted, "A-2").At, t0.Add(time.Minute); !got.Equal(want) {
		t.Errorf("A-2 started at %v, want %v (when the owner resumed)", got, want)
	}
	if on, off := h.count(PauseOn), h.count(PauseOff); on != 1 || off != 1 {
		t.Errorf("pause on/off = %d/%d, want 1/1 in %s", on, off, h.kinds())
	}
	if toasts := h.toasts(); len(toasts) != 2 || !strings.HasPrefix(toasts[0], "request: ") || !strings.Contains(toasts[0], "plan changed outside igris") {
		t.Errorf("toasts = %q, want a needs_input toast about the plan before the phase-done one", toasts)
	}
	if got := h.count(Paused); got != 0 {
		t.Errorf("%d paused-before-task events, want none: the hold happens before a task is selected", got)
	}
}

// Igris's own status writes are not plan changes.
func TestOwnWritesAreNotPlanChanges(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.count(PlanChanged); got != 0 {
		t.Errorf("%d plan_changed events, want none in %s", got, h.kinds())
	}
}
