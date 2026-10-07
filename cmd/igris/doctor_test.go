package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/runner"
)

// doctorIn runs `igris doctor args…` in dir with no real claude, herdr or
// git around, and returns the exit code, stdout and stderr.
func doctorIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	savedGetenv, savedRunner, savedCompat := ariseGetenv, ariseRunner, compatWarnings
	t.Cleanup(func() { ariseGetenv, ariseRunner, compatWarnings = savedGetenv, savedRunner, savedCompat })
	ariseGetenv = func(string) string { return "" }
	ariseRunner = &runner.Fake{} // unscripted commands (git, herdr) fail
	compatWarnings = func(context.Context, runner.Runner) []checks.Result {
		return []checks.Result{{ID: checks.IDClaude, Level: checks.OK, Message: "Claude Code 2.1.292"}}
	}
	t.Chdir(dir)
	var out, errb bytes.Buffer
	code := run(append([]string{"doctor"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

// snapshot maps every path under dir to its modification time and size.
func treeStamp(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		m[path] = fi.ModTime().Format(time.RFC3339Nano) + " " + fi.Mode().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDoctorOutsideProject(t *testing.T) {
	dir := t.TempDir()
	before := treeStamp(t, dir)
	code, out, errb := doctorIn(t, dir)
	if code != exitOK {
		t.Fatalf("exit %d, want 0 outside a project\n%s%s", code, out, errb)
	}
	if !strings.Contains(out, "no igris.toml") || !strings.Contains(out, "next: igris init") {
		t.Errorf("output lacks the no-project note:\n%s", out)
	}
	if after := treeStamp(t, dir); len(after) != len(before) {
		t.Errorf("doctor created files: %v", after)
	}
	if _, err := os.Stat(filepath.Join(dir, ".igris")); err == nil {
		t.Error(".igris/ was created")
	}
}

func TestDoctorReadOnly(t *testing.T) {
	dir := t.TempDir()
	if code, _, errb := initIn(t, dir); code != exitOK {
		t.Fatalf("init: %s", errb)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte("## A — First\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| a-1 | — | ready | sonnet |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeStamp(t, dir)
	time.Sleep(10 * time.Millisecond)
	code, out, errb := doctorIn(t, dir)
	if code != exitOK {
		t.Fatalf("exit %d\n%s%s", code, out, errb)
	}
	if after := treeStamp(t, dir); !equalMaps(before, after) {
		t.Errorf("doctor changed the project:\nbefore %v\nafter  %v", before, after)
	}
	for _, want := range []string{"✓ ok   igris.toml is valid", "✓ ok   `igris done` is allowed", "✓ ok   tasks.md is valid"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestDoctorFailureExitsOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "igris.toml"), []byte("default_mode = \"nope\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := doctorIn(t, dir)
	if code != exitFail {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "⨯ fail") || !strings.Contains(out, "next: edit igris.toml") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".igris")); err == nil {
		t.Error(".igris/ was created")
	}
}

func TestDoctorJSON(t *testing.T) {
	dir := t.TempDir()
	code, out, errb := doctorIn(t, dir, "--json")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errb)
	}
	var rs []checks.Result
	if err := json.Unmarshal([]byte(out), &rs); err != nil {
		t.Fatalf("not a JSON array of results: %v\n%s", err, out)
	}
	if len(rs) == 0 || rs[0].ID != checks.IDClaude {
		t.Errorf("results = %+v", rs)
	}
}

func TestDoctorUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"doctor", "extra"}, &out, &errb); code != exitUsage {
		t.Errorf("exit %d, want 2", code)
	}
	if !strings.Contains(usageText, "doctor") {
		t.Error("usage text doesn't list doctor")
	}
}
