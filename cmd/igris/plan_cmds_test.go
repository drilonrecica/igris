package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/runner"
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

// A dependency column under another name, not aliased, is read as an extra
// column: the plan is valid but runs with no dependencies, so check warns.
func TestCheckWarnsAboutIgnoredDepsColumn(t *testing.T) {
	dir := inDir(t)
	plan := "## M0 — One\n\n| ID | Status | Model | Depends |\n|---|---|---|---|\n| a | ready | sonnet | — |\n| b | blocked | sonnet | a |\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	want := `tasks.md:3: column "Depends" looks like dependencies`
	code, out, _ := runCmd("check")
	if code != exitOK || !strings.Contains(out, "warning: "+want) || !strings.Contains(out, "OK (1 phases, 2 tasks, 2 warnings)") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	code, out, _ = runCmd("check", "--json")
	if code != exitOK || !strings.Contains(out, `"message": "column \"Depends\" looks like dependencies`) || strings.Contains(out, `"from": ""`) {
		t.Errorf("json: code %d, out:\n%s", code, out)
	}

	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("[columns]\nDepends = \"Deps\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runCmd("check"); code != exitOK || strings.Contains(out, "warning:") {
		t.Errorf("aliased: code %d, out:\n%s", code, out)
	}
}

func TestPlanErrors(t *testing.T) {
	dir := inDir(t)
	for _, cmd := range []string{"check", "phases", "status"} {
		code, _, errs := runCmd(cmd)
		if code != exitFail || !strings.HasPrefix(errs, "igris "+cmd+": ") || !strings.Contains(errs, "tasks.md") || !strings.Contains(errs, "--plan") {
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

func TestStatusUntitledPhase(t *testing.T) {
	dir := inDir(t)
	plan := "## Overview\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCmd("status")
	if code != exitOK || !strings.HasPrefix(out, "Overview: 0/1 finished, next (a)\n") {
		t.Errorf("code %d, out:\n%s", code, out)
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

// withVersions turns the real version check back on (TestMain stubs it),
// with claude and herdr answering --version from a fake runner; other
// commands (git) succeed with no output.
func withVersions(t *testing.T, claude, herdr string) {
	t.Helper()
	savedCompat, savedRunner := compatWarnings, ariseRunner
	t.Cleanup(func() { compatWarnings, ariseRunner = savedCompat, savedRunner })
	r := &runner.Fake{}
	r.Func(func(c runner.Cmd) (runner.Result, error) {
		switch {
		case c.Name == "claude" && claude == "":
			return runner.Result{}, fmt.Errorf("run claude: %w", exec.ErrNotFound)
		case c.Name == "claude":
			return runner.Result{Stdout: []byte(claude)}, nil
		case c.Name == "herdr":
			return runner.Result{Stdout: []byte(herdr)}, nil
		}
		return runner.Result{}, nil
	})
	compatWarnings, ariseRunner = checks.ToolVersions, r
}

func TestCheckWarnsAboutVersions(t *testing.T) {
	dir := inDir(t)
	plan := "## M0 — One\n\n| ID | Status | Model | Deps |\n|---|---|---|---|\n| a | ready | sonnet | — |\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}

	// Inside a herdr pane, so backend = "auto" checks herdr's version
	// whatever the test machine's environment is (SPEC §11.3).
	savedGetenv := ariseGetenv
	t.Cleanup(func() { ariseGetenv = savedGetenv })
	ariseGetenv = func(k string) string {
		if k == "HERDR_WORKSPACE_ID" {
			return "w1"
		}
		return ""
	}
	withVersions(t, "2.1.292 (Claude Code)\n", "herdr 0.9.1\n")
	if code, out, _ := runCmd("check"); code != exitOK || strings.Contains(out, "warning:") {
		t.Errorf("verified versions: code %d, out:\n%s", code, out)
	}

	withVersions(t, "", "herdr 1.0.0\n")
	code, out, _ := runCmd("check")
	for _, want := range []string{
		"warning: claude not found in PATH; igris arise needs Claude Code",
		"warning: herdr 1.0.0 is a newer major version",
		"OK (1 phases, 1 tasks, 2 warnings)",
	} {
		if code != exitOK || !strings.Contains(out, want) {
			t.Errorf("code %d, output missing %q:\n%s", code, want, out)
		}
	}

	code, out, _ = runCmd("check", "--json")
	var doc struct {
		Warnings []map[string]any `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != exitOK {
		t.Fatalf("json: code %d, %v:\n%s", code, err, out)
	}
	if len(doc.Warnings) != 2 {
		t.Fatalf("json warnings = %v", doc.Warnings)
	}
	for _, w := range doc.Warnings {
		if _, ok := w["file"]; ok {
			t.Errorf("a version warning has a file: %v", w)
		}
		if _, ok := w["line"]; ok {
			t.Errorf("a version warning has a line: %v", w)
		}
	}
}

func TestCheckWarnsAboutClaudeCommand(t *testing.T) {
	dir := inDir(t)
	plan := "## M0 — One\n\n| ID | Status | Model | Deps |\n|---|---|---|---|\n| a | ready | sonnet | — |\n"
	for name, content := range map[string]string{"tasks.md": plan, configFile: "[claude]\ncommand = \"/opt/claude\"\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, out, _ := runCmd("check")
	if code != exitOK || !strings.Contains(out, `warning: igris.toml: claude.command = "/opt/claude" is ignored`) || !strings.Contains(out, "1 warnings") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	code, out, _ = runCmd("check", "--json")
	var doc struct {
		Warnings []map[string]any `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != exitOK || len(doc.Warnings) != 1 {
		t.Fatalf("json: code %d, %v:\n%s", code, err, out)
	}
	if w := doc.Warnings[0]; w["file"] != configFile || w["line"] != nil {
		t.Errorf("warning = %v, want file igris.toml and no line", w)
	}
}

const hintPlan = "## M0 — One\n\n| ID | Status | Model | Deps |\n|---|---|---|---|\n| a | ready | sonnet | — |\n"

func TestCheckWarnsAboutAPIKey(t *testing.T) {
	dir := inDir(t)
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(hintPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := ariseGetenv
	t.Cleanup(func() { ariseGetenv = saved })
	ariseGetenv = func(k string) string {
		if k == checks.APIKeyVar {
			return "sk-secret"
		}
		return ""
	}
	code, out, _ := runCmd("check")
	if code != exitOK || !strings.Contains(out, "warning: ANTHROPIC_API_KEY is set") || !strings.Contains(out, "1 warnings") || strings.Contains(out, "sk-secret") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	code, out, _ = runCmd("check", "--json")
	if code != exitOK || !strings.Contains(out, `"message": "ANTHROPIC_API_KEY is set`) || strings.Contains(out, "sk-secret") {
		t.Errorf("json: code %d, out:\n%s", code, out)
	}
}

// From a subdirectory of a project, check, phases and status read no
// igris.toml; they say where it is.
func TestConfigInParent(t *testing.T) {
	// Resolved: on macOS the temp dir is under /var → /private/var, and the
	// hint names the directory as os.Getwd sees it.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "docs")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, configFile), []byte("plan = \"tasks.md\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	want := "igris.toml found in " + root + "; run igris from there"

	// No plan here: the error carries the hint.
	if code, _, errs := runCmd("status"); code != exitFail || !strings.Contains(errs, want) {
		t.Errorf("status without plan: code %d, stderr %q", code, errs)
	}

	if err := os.WriteFile(filepath.Join(sub, "tasks.md"), []byte(hintPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCmd("check")
	if code != exitOK || !strings.Contains(out, "warning: "+want) || strings.Contains(out, "note:") {
		t.Errorf("check: code %d, out:\n%s", code, out)
	}
	for _, cmd := range []string{"phases", "status"} {
		code, out, errs := runCmd(cmd)
		if code != exitOK || !strings.Contains(errs, "note: "+want) || strings.Contains(out, want) {
			t.Errorf("%s: code %d, stdout %q, stderr %q", cmd, code, out, errs)
		}
	}
}

// A directory with just a plan is fine: a note, not a warning.
func TestCheckNotesMissingConfig(t *testing.T) {
	dir := inDir(t)
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(hintPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCmd("check")
	if code != exitOK || !strings.Contains(out, "note: no igris.toml here; using the defaults") || !strings.Contains(out, "0 warnings") {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	if _, out, _ := runCmd("check", "--json"); strings.Contains(out, "note") || strings.Contains(out, "defaults") {
		t.Errorf("json mentions the note:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := runCmd("check"); strings.Contains(out, "note:") {
		t.Errorf("with igris.toml:\n%s", out)
	}
}

// writeRunState writes .igris/<name> under dir.
func writeRunState(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, ".igris", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const runStateJSON = `{"version":1,"started_at":"2026-10-07T09:30:00Z","phases":["F1","F2"],"config_hash":"h",
"current":{"task_id":"F1-13","mode":"auto","claude_session":"s","session":{"backend":"herdr","tab_id":"t1","pane_id":"p2"},"started_at":"2026-10-07T09:31:00Z"}}`

func TestStatusNoRun(t *testing.T) {
	dir := inDir(t)
	writeRunState(t, dir, "signals/.keep", "")
	_, out, _ := runCmd("status", "--plan", largePlan)
	if strings.Contains(out, "Run\n") {
		t.Errorf("no run, but a Run block:\n%s", out)
	}
	_, out, _ = runCmd("status", "--json", "--plan", largePlan)
	if strings.Contains(out, `"run"`) {
		t.Errorf("no run, but a run object:\n%s", out)
	}
}

func TestStatusCurrentRun(t *testing.T) {
	dir := inDir(t)
	writeRunState(t, dir, "state.json", runStateJSON)
	writeRunState(t, dir, "signals/F1-13.json", `{"id":"F1-13","action":"done","note":"n","at":"2026-10-07T09:32:00Z"}`)
	code, out, _ := runCmd("status", "F1", "--plan", largePlan)
	if code != exitOK {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"Run\n", "F1, F2", "F1-13", "auto", "2026-10-07T09:31:00Z", "herdr t1 p2", "Lock     none", "F1-13 done"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "Run\n") > strings.Index(out, "F1 — ") {
		t.Errorf("Run block should come above the phase table:\n%s", out)
	}

	_, out, _ = runCmd("status", "--json", "--plan", largePlan)
	var got struct {
		Run struct {
			Task, Mode, Lock string
			Phases, Signals  []string
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Run.Task != "F1-13" || got.Run.Mode != "auto" || got.Run.Lock != "none" ||
		len(got.Run.Phases) != 2 || len(got.Run.Signals) != 1 {
		t.Errorf("run = %+v", got.Run)
	}
}

func TestStatusRunFromSubdirAndBadState(t *testing.T) {
	dir := inDir(t)
	writeRunState(t, dir, "state.json", `{"version":1,"phases":["F1"],"current":{"task_id":"F1-13","mode":"turbo"}}`)
	writeRunState(t, dir, "igris.lock", `{"pid":1,"host":"definitely-elsewhere","started_at":"2026-10-07T09:30:00Z"}`)
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	code, out, _ := runCmd("status", "--plan", largePlan)
	if code != exitOK {
		t.Fatalf("a bad state file must not fail status: code %d", code)
	}
	if !strings.Contains(out, "state unreadable") || !strings.Contains(out, "running elsewhere") {
		t.Errorf("bad state not reported:\n%s", out)
	}
}

func TestCheckVerifyProfiles(t *testing.T) {
	dir := inDir(t)
	plan := "## M1 — One\n\n| ID | Status | Model | Owner | Verify |\n|---|---|---|---|---|\n| M1-01 | ready | sonnet | agent | fsat |\n| M1-02 | ready | — | user | fast |\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := "[verify]\nfast = \"go test ./...\"\n[phases.M9]\nverify = \"fast\"\n"
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCmd("check")
	if code != exitFail || !strings.Contains(out, `tasks.md:5: M1-01: unknown verify profile "fsat"; define it under [verify] in igris.toml or use one of: fast, none`) {
		t.Errorf("code %d, out:\n%s", code, out)
	}
	for _, cmd := range []string{"status", "phases"} {
		if code, _, _ := runCmd(cmd); code != exitFail {
			t.Errorf("%s with an unknown profile: code %d", cmd, code)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(strings.Replace(plan, "fsat", "FAST", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCmd("check")
	for _, w := range []string{
		"warning: tasks.md:6: M1-02: user tasks have no session, so its Verify is ignored; clear the cell",
		"warning: igris.toml: [phases.M9] names no phase of tasks.md; fix the phase ID or remove the table",
		"OK (1 phases, 2 tasks, 2 warnings)",
	} {
		if code != exitOK || !strings.Contains(out, w) {
			t.Errorf("code %d, output lacks %q:\n%s", code, w, out)
		}
	}
}

func TestStatusOptionalColumns(t *testing.T) {
	dir := inDir(t)
	write := func(plan string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(plan), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("## M1\n\n| ID | Status | Model | Verify | Timeout | Context |\n|---|---|---|---|---|---|\n| a | ready | sonnet | — | | |\n")
	if code, out, _ := runCmd("status"); code != exitOK || strings.Contains(out, "VERIFY") || strings.Contains(out, "TIMEOUT") || strings.Contains(out, "CONTEXT") {
		t.Errorf("unset columns shown: code %d\n%s", code, out)
	}
	_, out, _ := runCmd("status", "--json")
	if strings.Contains(out, `"verify"`) || strings.Contains(out, `"timeout"`) || strings.Contains(out, `"context"`) {
		t.Errorf("json has unset columns:\n%s", out)
	}

	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("[verify]\nfast = \"true\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	write("## M1\n\n| ID | Status | Model | Verify | Timeout | Context |\n|---|---|---|---|---|---|\n| a | ready | sonnet | fast | | |\n" +
		"\n## M2\n\n| ID | Status | Model | Context |\n|---|---|---|---|\n| b | ready | sonnet | `x.go`, y/ |\n")
	code, out, _ := runCmd("status", "m1")
	if code != exitOK || !strings.Contains(out, "  ID  STATUS  RANK    OWNER  MODE  VERIFY  CONTEXT  WAITS ON\n  a   ready   sonnet  agent  —     fast    —") || strings.Contains(out, "TIMEOUT") {
		t.Errorf("code %d:\n%s", code, out)
	}
	_, out, _ = runCmd("status", "--json")
	var got struct {
		Phases []struct {
			Tasks []struct {
				Verify, Timeout string
				Context         []string
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if a, b := got.Phases[0].Tasks[0], got.Phases[1].Tasks[0]; a.Verify != "fast" || a.Context != nil || strings.Join(b.Context, "|") != "x.go|y/" || b.Verify != "" {
		t.Errorf("json tasks = %+v, %+v", a, b)
	}
}
