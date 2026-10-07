package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/plan"
)

const testPlan = "## A — First\n\n" +
	"| ID | Task | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|---|\n" +
	"| A-1 | **Start** | — | done | sonnet | agent | — |\n" +
	"| A-2 | **Next** | A-1 | blocked | sonnet | agent | — |\n" +
	"| A-3 | **Later** | A-2 | ready | — | user | — |\n"

func load(t *testing.T, src string) *plan.Plan {
	t.Helper()
	return plan.Parse("tasks.md", []byte(src), plan.Options{})
}

func TestPhasesAndStatus(t *testing.T) {
	p := load(t, testPlan)
	// Phases doesn't validate: a plan that igris would reject still must not
	// carry an escape sequence out of the report.
	ph := Phases(load(t, strings.Replace(testPlan, "First", "First \x1b[31mred\x1b[0m", 1)))
	if got := ph.Phases[0]; got.ID != "A" || got.Total != 3 || got.Counts["done"] != 1 || got.Counts["blocked"] != 1 || got.Counts["ready"] != 1 || got.Counts["skipped"] != 0 {
		t.Errorf("phase = %+v", got)
	}
	if strings.Contains(ph.Phases[0].Title, "\x1b") {
		t.Errorf("title keeps an escape sequence: %q", ph.Phases[0].Title)
	}

	st, err := Status(p, "A")
	if err != nil {
		t.Fatal(err)
	}
	s := st.Phases[0]
	if s.Next != "A-2" {
		t.Errorf("next = %q", s.Next)
	}
	if got := s.Tasks[2]; got.Rank != "—" || got.Owner != "user" || len(got.WaitsOn) != 1 || got.WaitsOn[0] != (Wait{ID: "A-2", Status: "blocked", Phase: "A"}) {
		t.Errorf("task = %+v", got)
	}
	if got := s.Tasks[0]; got.WaitsOn == nil || len(got.WaitsOn) != 0 {
		t.Errorf("a finished task waits on %#v, want an empty, non-nil list", got.WaitsOn)
	}
	if _, err := Status(p, "Z"); err == nil || !strings.Contains(err.Error(), "Z") {
		t.Errorf("unknown phase: %v", err)
	}
}

func TestCheckKeepsJSONKeyOrderAndNeverNull(t *testing.T) {
	p := load(t, testPlan)
	r := Check(CheckInput{Plan: p, Models: map[string]string{"sonnet": "sonnet"}, Leading: []Warning{{Message: "x\x1b[2Jy"}}})
	if !r.Valid || len(r.Issues) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if r.Warnings[0].Message != "xy" {
		t.Errorf("leading warning not cleaned: %q", r.Warnings[0].Message)
	}
	out, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), `{"issues":[],"phases":1,"plan":"tasks.md","tasks":3,"valid":true,"warnings":[`) {
		t.Errorf("json = %s", out)
	}
	if w := r.Warnings[1]; w.Task != "A-2" || w.From != "blocked" || w.To != "ready" {
		t.Errorf("drift warning = %+v", w)
	}
}

func TestInvalidPlanHasNoDrift(t *testing.T) {
	p := load(t, "## M0 — P\n\n| ID | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|\n| a | b | blocked | sonnet | agent | |\n| b | a | blocked | sonnet | agent | |\n")
	r := Check(CheckInput{Plan: p, Models: map[string]string{"sonnet": "sonnet"}})
	if r.Valid || len(r.Issues) != 1 || len(r.Warnings) != 0 {
		t.Fatalf("report = %+v", r)
	}
	if got := r.Issues[0].String(); !strings.HasPrefix(got, "tasks.md:") || !strings.Contains(got, "dependency cycle") {
		t.Errorf("issue = %q", got)
	}
	inv, _ := json.Marshal(NewInvalid(p.Path, nil))
	if string(inv) != `{"issues":[],"plan":"tasks.md","valid":false}` {
		t.Errorf("invalid json = %s", inv)
	}
}
