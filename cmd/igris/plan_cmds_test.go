package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixture paths are absolute because the tests change directory.
var (
	largePlan   = mustAbs("../../internal/plan/testdata/large.md")
	invalidPlan = mustAbs("../../internal/plan/testdata/invalid/cycle.md")
)

func mustAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		panic(err)
	}
	return abs
}

// inDir runs the test in a fresh directory so igris.toml lookups are isolated.
func inDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func runCmd(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestCheck(t *testing.T) {
	inDir(t)
	code, out, _ := runCmd("check", "--plan", largePlan)
	if code != exitOK {
		t.Fatalf("code = %d, out: %s", code, out)
	}
	for _, want := range []string{"OK (12 phases, 168 tasks, 14 warnings)", "large.md:40: F1-13 is blocked but all its dependencies are satisfied; igris will set it to ready"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	code, out, _ = runCmd("check", "--plan", invalidPlan)
	if code != exitFail || !strings.Contains(out, "dependency cycle a → b → a") || !strings.Contains(out, "1 problem(s)") {
		t.Errorf("invalid plan: code %d, out: %s", code, out)
	}
}

func TestCheckJSON(t *testing.T) {
	inDir(t)
	code, out, _ := runCmd("check", "--json", "--plan", largePlan)
	if code != exitOK {
		t.Fatalf("code = %d", code)
	}
	var got struct {
		Valid    bool              `json:"valid"`
		Tasks    int               `json:"tasks"`
		Issues   []json.RawMessage `json:"issues"`
		Warnings []struct {
			Task, From, To string
			Line           int
		} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !got.Valid || got.Tasks != 168 || got.Issues == nil || len(got.Issues) != 0 || len(got.Warnings) != 14 {
		t.Errorf("unexpected report: %+v", got)
	}
	if w := got.Warnings[0]; w.Task != "F1-13" || w.From != "blocked" || w.To != "ready" || w.Line != 40 {
		t.Errorf("first warning = %+v", w)
	}

	code, out, _ = runCmd("check", "--json", "--plan", invalidPlan)
	if code != exitFail || !strings.Contains(out, `"valid": false`) || !strings.Contains(out, `"warnings": []`) {
		t.Errorf("invalid plan: code %d, out: %s", code, out)
	}
}

func TestCheckUsesConfig(t *testing.T) {
	dir := inDir(t)
	plan := "## M0 — One\n\n| ID | Depends on | Status | Model |\n|---|---|---|---|\n| a | — | ready | gold |\n"
	if err := os.WriteFile(filepath.Join(dir, "my-plan.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := "plan = \"my-plan.md\"\n[models]\ngold = \"opus\"\n[columns]\n\"Depends on\" = \"Deps\"\n"
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runCmd("check"); code != exitOK {
		t.Fatalf("code %d, out %s, err %s", code, out, errs)
	}
}

func TestPlanErrors(t *testing.T) {
	dir := inDir(t)
	for _, cmd := range []string{"check", "phases", "status"} {
		code, _, errs := runCmd(cmd)
		if code != exitFail || !strings.Contains(errs, "tasks.md") || !strings.Contains(errs, "--plan") {
			t.Errorf("%s with no plan: code %d, stderr %q", cmd, code, errs)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("bogus = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runCmd("check", "--plan", largePlan); code != exitFail || !strings.Contains(errs, "unknown key") {
		t.Errorf("bad config: code %d, stderr %q", code, errs)
	}
}

func TestPhases(t *testing.T) {
	inDir(t)
	code, out, _ := runCmd("phases", "--plan", largePlan)
	if code != exitOK {
		t.Fatalf("code = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 13 || !strings.HasPrefix(lines[0], "PHASE") {
		t.Fatalf("want header + 12 phases, got:\n%s", out)
	}
	if f := strings.Fields(lines[3]); strings.Join(f, " ") != "F3 Ingest 2 6 1 4 1 14" {
		t.Errorf("F3 row = %q", lines[3])
	}

	_, out, _ = runCmd("phases", "--json", "--plan", largePlan)
	var got struct {
		Phases []struct {
			ID     string
			Total  int
			Counts map[string]int
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Phases) != 12 || got.Phases[2].Counts["in progress"] != 1 || got.Phases[2].Total != 14 {
		t.Errorf("unexpected phases: %+v", got.Phases[:3])
	}

	if code, _, errs := runCmd("phases", "--plan", invalidPlan); code != exitFail || !strings.Contains(errs, "dependency cycle") {
		t.Errorf("invalid plan: code %d, stderr %q", code, errs)
	}
}

func TestStatus(t *testing.T) {
	inDir(t)
	code, out, _ := runCmd("status", "f3", "--plan", largePlan) // phase IDs match case-insensitively
	if code != exitOK {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, "F3 — Ingest: 5/14 finished, next (F3-06)") {
		t.Errorf("header missing:\n%s", out)
	}
	if strings.Contains(out, "F2 — ") {
		t.Errorf("only the requested phase should be listed:\n%s", out)
	}

	_, out, _ = runCmd("status", "Phase-4", "--plan", largePlan)
	if !strings.Contains(out, "stuck") || !strings.Contains(out, "F3-G (blocked, phase F3)") {
		t.Errorf("stuck phase should name what it waits on:\n%s", out)
	}

	_, out, _ = runCmd("status", "--json", "--plan", largePlan)
	var got struct {
		Phases []struct {
			ID, Outcome, Next string
			Tasks             []struct {
				ID      string
				WaitsOn []struct{ ID, Status, Phase string } `json:"waits_on"`
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Phases) != 12 || got.Phases[0].Outcome != "next" || got.Phases[0].Next != "F1-13" {
		t.Errorf("unexpected phases: %+v", got.Phases[0])
	}
	if w := got.Phases[3].Tasks[0].WaitsOn; len(w) != 1 || w[0].ID != "F3-G" || w[0].Phase != "F3" {
		t.Errorf("Phase-4 first task waits on %+v", w)
	}

	code, _, errs := runCmd("status", "nope", "--plan", largePlan)
	if code != exitFail || !strings.Contains(errs, `unknown phase "nope"`) || !strings.Contains(errs, "F1") {
		t.Errorf("unknown phase: code %d, stderr %q", code, errs)
	}
	if code, _, _ := runCmd("status", "--plan", invalidPlan); code != exitFail {
		t.Errorf("invalid plan: code %d", code)
	}
}
