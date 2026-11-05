package report

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

const sinceBase = `# P

## M1 — One

| ID | Task | Deps | Status | Model | Mode | Owner |
|---|---|---|---|---|---|---|
| M1-01 | **A** | — | done | sonnet | — | agent |
| M1-02 | **B** | M1-01 | ready | opus | — | agent |
| M1-03 | **C** | M1-02 | blocked | opus | — | agent |
| M1-09 | **Z** | — | ready | — | — | user |
`

func TestNewSinceLastRun(t *testing.T) {
	at := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC)
	base := state.PlanCells{V: 1, Run: "20261105-010000-abcd", At: at, Tasks: state.CellsOf(load(t, sinceBase))}
	ev := func(min int, typ state.EventType, task, detail string) state.Event {
		return state.Event{At: at.Add(time.Duration(min) * time.Minute), Type: typ, Task: task, Detail: detail}
	}
	// The run did M1-02 (ready → in progress → done; M1-03 turned ready on
	// its own); then someone edited M1-03's mode and text, added M1-04 and
	// removed M1-09.
	edited := strings.NewReplacer(
		"| M1-02 | **B** | M1-01 | ready |", "| M1-02 | **B** | M1-01 | done |",
		"| M1-03 | **C** | M1-02 | blocked | opus | — |", "| M1-03 | **C, more** | M1-02 | ready | opus | auto |",
		"| M1-09 | **Z** | — | ready | — | — | user |\n", "| M1-04 | **D** | — | ready | — | — | user |\n",
	).Replace(sinceBase)
	events := []state.Event{
		ev(-5, state.EventTaskDone, "M1-01", ""), // before the baseline: not this diff's
		ev(1, state.EventTaskStarted, "M1-02", ""),
		ev(2, state.EventVerifyPassed, "M1-02", ""),
		ev(3, state.EventTaskDone, "M1-02", "note"),
	}
	got := NewSinceLastRun(base, true, "", events, load(t, edited))
	if !got.Baseline || got.Run != base.Run || !got.At.Equal(at) {
		t.Errorf("header = %v %q %v", got.Baseline, got.Run, got.At)
	}
	if fmt.Sprint(got.Igris) != "[{M1-02 ready done}]" {
		t.Errorf("igris = %v", got.Igris)
	}
	want := "[{M1-03 Mode — → auto} {M1-03 Task text changed} {M1-04 added} {M1-09 removed}]"
	if fmt.Sprint(got.Others) != want {
		t.Errorf("others = %v\nwant     %v", got.Others, want)
	}
	if got.EditedRows() != 3 {
		t.Errorf("edited rows %d, want 3", got.EditedRows())
	}

	// A Status nobody logged is someone else's; a reset is igris's.
	other := strings.Replace(sinceBase, "| M1-02 | **B** | M1-01 | ready |", "| M1-02 | **B** | M1-01 | skipped |", 1)
	other = strings.Replace(other, "| M1-01 | **A** | — | done |", "| M1-01 | **A** | — | ready |", 1)
	got = NewSinceLastRun(base, true, "", []state.Event{ev(1, state.EventTaskReset, "M1-01", "done")}, load(t, other))
	if fmt.Sprint(got.Igris) != "[{M1-01 done ready}]" || fmt.Sprint(got.Others) != "[{M1-02 Status ready → skipped}]" {
		t.Errorf("igris %v, others %v", got.Igris, got.Others)
	}

	// Nothing changed.
	if got := NewSinceLastRun(base, true, "", nil, load(t, sinceBase)); !got.Empty() || !got.Baseline {
		t.Errorf("unchanged plan: %+v", got)
	}
	// No baseline.
	if got := NewSinceLastRun(state.PlanCells{}, false, "plan-cells.json: bad", events, load(t, edited)); got.Baseline || !got.Empty() || got.At != nil || got.Why == "" {
		t.Errorf("no baseline: %+v", got)
	}
}
