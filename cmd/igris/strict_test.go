package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lintDir is absolute because the tests change directory.
var lintDir = mustAbs("../../internal/plan/testdata/lint")

func lintFixture(name string) string { return filepath.Join(lintDir, name) }

// Each lint fixture fails `check --strict` with its hint; plain check
// shows no hint and passes (SPEC §14).
func TestCheckStrictLintFixtures(t *testing.T) {
	for _, c := range []struct{ file, line, msg string }{
		{"title.md", "7", "M1-03: the Task cell has no **bold** title, so igris shows the whole cell as its title; start the cell with **Title**"},
		{"long.md", "6", "M1-03: the Task cell is 406 characters (over 400); keep the row short and point to a spec for the details"},
		{"owner-step.md", "7", `M1-03: agent + user task, but its row never says what the owner does (no "owner", "approve" or "decide"); say what needs the owner's sign-off`},
		{"gate.md", "10", "M1-G: the gate does not depend on M1-04, M1-05 of phase M1; add them to Deps (e.g. M1-01…M1-05)"},
		{"yolo.md", "6", "M1-03: Mode yolo runs this task with --dangerously-skip-permissions; prefer auto unless it must run unattended"},
		{"fable.md", "6", "M1-03: the only heavy-rank task of phase M1 is fable; check that this task needs fable"},
	} {
		t.Run(c.file, func(t *testing.T) {
			inDir(t)
			withVersions(t, "2.1.292 (Claude Code)\n", "herdr 0.9.1\n")
			path := lintFixture(c.file)
			code, out, _ := runCmd("check", "--strict", "--plan", path)
			warning := fmt.Sprintf("warning: %s:%s: %s\n", path, c.line, c.msg)
			summary := fmt.Sprintf("%s: 1 warning(s) under --strict; fix them and run igris check --strict again\n", path)
			if code != exitFail || strings.Count(out, "warning: ") != 1 || !strings.Contains(out, warning) || !strings.HasSuffix(out, summary) {
				t.Errorf("--strict: code %d, out:\n%s\nwant:\n%s%s", code, out, warning, summary)
			}
			code, out, _ = runCmd("check", "--plan", path)
			if code != exitOK || strings.Contains(out, c.msg) || !strings.Contains(out, "OK (") {
				t.Errorf("plain check: code %d, out:\n%s", code, out)
			}
		})
	}
}

// Machine warnings are printed under --strict but never fail it; plan
// warnings other than lint hints do.
func TestCheckStrictWarningKinds(t *testing.T) {
	inDir(t)
	savedGetenv := ariseGetenv
	t.Cleanup(func() { ariseGetenv = savedGetenv })
	ariseGetenv = func(k string) string {
		switch k {
		case "HERDR_WORKSPACE_ID":
			return "w1"
		case "ANTHROPIC_API_KEY":
			return "sk-test"
		}
		return ""
	}
	withVersions(t, "", "herdr 1.0.0\n")
	clean := lintFixture("clean.md")
	code, out, _ := runCmd("check", "--strict", "--plan", clean)
	if code != exitOK || !strings.Contains(out, "warning: claude not found") || !strings.Contains(out, "ANTHROPIC_API_KEY") || !strings.Contains(out, "OK (1 phases, 5 tasks, 3 warnings)") {
		t.Errorf("machine warnings: code %d, out:\n%s", code, out)
	}

	// Drift is a plan warning: it fails --strict.
	ariseGetenv = func(string) string { return "" }
	withVersions(t, "2.1.292 (Claude Code)\n", "herdr 0.9.1\n")
	code, out, _ = runCmd("check", "--strict", "--plan", largePlan)
	if code != exitFail || !strings.Contains(out, "warning(s) under --strict; fix them and run igris check --strict again") {
		t.Errorf("drift: code %d, out:\n%s", code, out)
	}
	// A deprecated or ignored config setting is one too.
	dir := inDir(t)
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("[phases.Z9]\nverify = \"none\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCmd("check", "--strict", "--plan", clean)
	if code != exitFail || !strings.Contains(out, "[phases.Z9] names no phase") || !strings.Contains(out, ": 1 warning(s) under --strict") {
		t.Errorf("config warning: code %d, out:\n%s", code, out)
	}
}

func TestCheckStrictJSON(t *testing.T) {
	inDir(t)
	withVersions(t, "2.1.292 (Claude Code)\n", "herdr 0.9.1\n")
	var doc struct {
		Valid    bool             `json:"valid"`
		Strict   *bool            `json:"strict"`
		Warnings []map[string]any `json:"warnings"`
	}
	code, out, _ := runCmd("check", "--strict", "--json", "--plan", lintFixture("gate.md"))
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if code != exitFail || !doc.Valid || doc.Strict == nil || !*doc.Strict || len(doc.Warnings) != 1 || doc.Warnings[0]["lint"] != "gate" || doc.Warnings[0]["line"] != float64(10) {
		t.Errorf("code %d, doc %+v", code, doc)
	}
	doc.Strict = nil
	code, out, _ = runCmd("check", "--json", "--plan", lintFixture("gate.md"))
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if code != exitOK || doc.Strict != nil || len(doc.Warnings) != 0 || strings.Contains(out, `"lint"`) {
		t.Errorf("plain: code %d, out:\n%s", code, out)
	}
}
