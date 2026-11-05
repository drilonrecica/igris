package main

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// sinceProject is goldenPlan after a run and some edits: the baseline is
// the plan as the run 20261023-090000-ab12 took it; the run started and
// finished M1-02; someone else then gave M1-03 a mode, reworded M1-01 and
// added M1-04.
func sinceProject(t *testing.T) string {
	t.Helper()
	edited := strings.NewReplacer(
		"| M1-01 | **Config loader** |", "| M1-01 | **Config loader**, reworded |",
		"| M1-02 | **Lock file** | M1-01 | ready |", "| M1-02 | **Lock file** | M1-01 | done |",
		"| M1-03 | **Pick a license** | — | ready | — | user |", "| M1-03 | **Pick a license** | — | ready | — | user | auto |",
	).Replace(goldenPlan)
	edited = strings.Replace(edited, "| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n",
		"| ID | Task | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|---|\n", 1)
	edited = strings.NewReplacer("| agent |\n", "| agent | — |\n").Replace(edited)
	edited += "| M1-04 | **Release** | M1-02 | ready | sonnet | agent | — |\n"
	root := writeProject(t, map[string]string{"tasks.md": edited})

	at := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	base := plan.Parse("tasks.md", []byte(goldenPlan), plan.Options{})
	if err := dir.WritePlanCells(state.PlanCells{Run: "20261023-090000-ab12", At: at, Tasks: state.CellsOf(base)}); err != nil {
		t.Fatal(err)
	}
	for i, typ := range []state.EventType{state.EventTaskStarted, state.EventTaskDone} {
		if err := dir.Append(state.Event{At: at.Add(time.Duration(i+1) * time.Minute), Type: typ, Run: "20261023-090000-ab12", Task: "M1-02"}); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestStatusSinceLastRun(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		sinceProject(t)
		code, out, errb := runCmd("status", "--since-last-run")
		checkGolden(t, "status_since_last_run", code, out, errb)
	})
	t.Run("json", func(t *testing.T) {
		goldenDir := mustAbs("testdata/golden")
		dir := sinceProject(t)
		code, out, errb := runCmd("status", "--since-last-run", "--json")
		compareGolden(t, goldenDir, "since_status_json", dir, code, out, errb)
	})
	t.Run("no baseline", func(t *testing.T) {
		writeProject(t, map[string]string{"tasks.md": goldenPlan, ".igris/runs.jsonl": ""})
		code, out, _ := runCmd("status", "--since-last-run")
		if code != exitOK || !strings.Contains(out, "Since the last run\n  no baseline yet") {
			t.Errorf("exit %d:\n%s", code, out)
		}
	})
	t.Run("unchanged", func(t *testing.T) {
		root := writeProject(t, map[string]string{"tasks.md": goldenPlan})
		dir, err := state.Open(root, state.Options{})
		if err != nil {
			t.Fatal(err)
		}
		base := plan.Parse("tasks.md", []byte(goldenPlan), plan.Options{})
		if err := dir.WritePlanCells(state.PlanCells{Run: "20261023-090000-ab12", At: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC), Tasks: state.CellsOf(base)}); err != nil {
			t.Fatal(err)
		}
		code, out, _ := runCmd("status", "--since-last-run")
		if code != exitOK || !strings.Contains(out, "Since the last run (20261023-090000-ab12 · 09:00)\n  no changes") {
			t.Errorf("exit %d:\n%s", code, out)
		}
	})
}
