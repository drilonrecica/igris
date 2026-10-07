package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/state"
)

const signalPlan = `## M0 — Test

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| M0-01 | **First** | — | ready | sonnet |
`

// signalProject creates a project with a plan and an igris.toml, and makes sub
// (a directory below the root) the working directory.
func signalProject(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte(signalPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "igris.toml"), []byte("plan = \"tasks.md\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "internal", "x")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	return root
}

func TestDoneAndSkipWriteSignals(t *testing.T) {
	root := signalProject(t)

	var out, errb bytes.Buffer
	if got := run([]string{"done", "--note", "all good", "M0-01"}, &out, &errb); got != exitOK {
		t.Fatalf("done exit = %d, stderr: %s", got, errb.String())
	}
	if !strings.Contains(out.String(), "M0-01") || !strings.Contains(out.String(), "no igris run is active") {
		t.Errorf("stdout = %q, want task ID and the no-run hint", out.String())
	}
	path := filepath.Join(root, ".igris", "signals", "M0-01.json")
	data, err := os.ReadFile(path) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	var s state.Signal
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s.ID != "M0-01" || s.Action != "done" || s.Note != "all good" || s.At.IsZero() {
		t.Errorf("signal = %+v", s)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}

	out.Reset()
	if got := run([]string{"skip", "M0-01", "--reason", "n/a"}, &out, &errb); got != exitOK {
		t.Fatalf("skip exit = %d, stderr: %s", got, errb.String())
	}
	data, _ = os.ReadFile(path) //nolint:gosec // test temp dir
	if !strings.Contains(string(data), `"action":"skip"`) || !strings.Contains(string(data), `"note":"n/a"`) {
		t.Errorf("signal after skip = %s", data)
	}
}

func TestSignalErrors(t *testing.T) {
	signalProject(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown task", []string{"done", "M9-99"}, "igris status"},
		{"bad id", []string{"done", "../evil"}, "invalid task ID \"../evil\": IDs are letters, digits"},
		{"skip unknown task", []string{"skip", "M9-99", "--reason", "x"}, "not in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(tt.args, &out, &errb); got != exitFail {
				t.Errorf("exit = %d, want %d", got, exitFail)
			}
			if !strings.Contains(errb.String(), tt.want) {
				t.Errorf("stderr = %q, want %q", errb.String(), tt.want)
			}
		})
	}
}

func TestSignalOutsideProject(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errb bytes.Buffer
	if got := run([]string{"done", "M0-01"}, &out, &errb); got != exitFail {
		t.Errorf("exit = %d, want %d", got, exitFail)
	}
	if !strings.Contains(errb.String(), "igris init") {
		t.Errorf("stderr = %q, want hint to run igris init", errb.String())
	}
}

func TestDoneAdaptNeedsNoPlan(t *testing.T) {
	root := signalProject(t)
	// The adapt session's plan doesn't validate, or isn't the configured one.
	if err := os.Remove(filepath.Join(root, "tasks.md")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if got := run([]string{"done", "ADAPT", "--note", "converted"}, &out, &errb); got != exitOK {
		t.Fatalf("done ADAPT exit = %d, stderr: %s", got, errb.String())
	}
	d, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	s, err := d.ReadSignal("ADAPT")
	if err != nil || s == nil || s.Action != state.ActionDone || s.Note != "converted" {
		t.Fatalf("signal = %+v, %v", s, err)
	}

	// skip ADAPT is not an adapt signal: the plan has to know the task.
	if got := run([]string{"skip", "ADAPT", "--reason", "x"}, &out, &errb); got != exitFail {
		t.Errorf("skip ADAPT exit = %d, want %d", got, exitFail)
	}
}

// While a run holds the lock, done says nothing about applying it later.
func TestDoneDuringARun(t *testing.T) {
	root := signalProject(t)
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := dir.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	var out, errb bytes.Buffer
	if got := run([]string{"done", "M0-01"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if out.String() != "igris: done signal recorded for M0-01\n" {
		t.Errorf("stdout = %q", out.String())
	}
}
