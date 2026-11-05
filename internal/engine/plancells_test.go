package engine

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// A run keeps the plan baseline (SPEC §5.4, §13): written when it starts,
// after the readiness sync, and again when it ends.
func TestRunKeepsPlanBaseline(t *testing.T) {
	// A-2 drifted: ready although A-1 isn't done. The run fixes it.
	h := newHarness(t, strings.Replace(chainPlan, "| A-1 | blocked | opus", "| A-1 | ready | opus", 1), "")
	h.autoSignalExcept()
	var start state.PlanCells
	h.onEvent = func(ev Event) {
		if ev.Kind != RunStarted {
			return
		}
		c, ok, why := state.ReadPlanCells(h.root)
		if !ok {
			t.Fatalf("no baseline when the run started: %s", why)
		}
		start = c
	}
	if _, err := h.run(func(o *Options) { o.ConfirmedDrift = true }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	runID := h.logEvents()[0].Run
	if start.Run != runID || start.V != 1 || !start.At.Equal(t0) {
		t.Errorf("start baseline run %q v %d at %v; want %q, 1, %v", start.Run, start.V, start.At, runID, t0)
	}
	for id, want := range map[string]string{"A-1": "ready", "A-2": "blocked", "A-3": "blocked", "B-1": "blocked"} {
		if got := start.Tasks[id].Status; got != want {
			t.Errorf("start baseline %s status %q, want %q", id, got, want)
		}
	}
	if a := start.Tasks["A-1"]; a.Rank != "sonnet" || a.Owner != "agent" || len(a.TextSHA256) != 64 {
		t.Errorf("start baseline A-1 = %+v", a)
	}

	end, ok, why := state.ReadPlanCells(h.root)
	if !ok {
		t.Fatalf("no baseline after the run: %s", why)
	}
	if end.Run != runID || end.At.Before(start.At) {
		t.Errorf("end baseline run %q at %v; want %q, not before %v", end.Run, end.At, runID, start.At)
	}
	for _, id := range []string{"A-1", "A-2", "A-3"} {
		if got := end.Tasks[id].Status; got != "done" {
			t.Errorf("end baseline %s status %q, want done", id, got)
		}
	}

	// Against the start baseline, the run's own writes are igris's and
	// nothing else changed.
	p, err := plan.Load(h.path("tasks.md"), plan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	since := report.NewSinceLastRun(start, true, "", h.logEvents(), p)
	if len(since.Others) != 0 {
		t.Errorf("others = %+v, want none", since.Others)
	}
	if len(since.Igris) != 3 || since.Igris[0] != (report.StatusEdit{Task: "A-1", From: "ready", To: "done"}) || since.Igris[1].From != "blocked" {
		t.Errorf("igris = %+v", since.Igris)
	}
}
