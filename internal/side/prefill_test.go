package side

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs (run with -update and review the diff):\n--- got ---\n%s--- want ---\n%s", path, got, want)
	}
}

func TestTaskPrefill(t *testing.T) {
	got := TaskPrefill(TaskInfo{
		ID: "M2-04", Title: "Widget cache", Phase: "M2", PhaseName: "Widgets",
		PlanPath: "/home/owner/project/tasks.md",
		Text:     "**Widget cache** — keep the last 100 widgets\x1b[31m in memory; tests",
		Running:  true,
	})
	checkGolden(t, "prefill-task", got)
	if strings.ContainsRune(got, 0x1b) {
		t.Error("an escape sequence from the plan reached the prompt")
	}
	idle := TaskPrefill(TaskInfo{ID: "M2-05", Title: "x", Phase: "M2", PlanPath: "/p/tasks.md", Text: "x"})
	if !strings.Contains(idle, "Running now: no\n") || !strings.Contains(idle, "Phase: M2\n") || !strings.HasSuffix(idle, "Question: ") {
		t.Errorf("not running, no phase name:\n%s", idle)
	}
}

// A long task text is cut so the prompt fits MaxPrompt with room to type,
// and still ends in "Question: ".
func TestTaskPrefillLong(t *testing.T) {
	got := TaskPrefill(TaskInfo{ID: "M2-04", Title: "Big", Phase: "M2", PlanPath: "/p/tasks.md", Text: strings.Repeat("é", MaxPrompt)})
	if len(got) > MaxPrompt-promptRoom || !utf8.ValidString(got) || !strings.HasSuffix(got, "…\n\nQuestion: ") {
		t.Errorf("%d bytes, valid %v, ends %q", len(got), utf8.ValidString(got), got[max(len(got)-20, 0):])
	}
}

func TestIgrisPrefill(t *testing.T) {
	var events []state.Event
	for i := range 12 {
		events = append(events, state.Event{Type: state.EventType("task_started"), Task: fmt.Sprintf("M1-%02d", i+1)})
	}
	events = append(events, state.Event{Type: "verify_failed", Task: "M1-12", Detail: "exit 1\x1b]0;evil\x07: make test"})
	got := IgrisPrefill(IgrisInfo{
		Version: "1.2.0", Backend: "tmux",
		Run:    &RunInfo{Phase: "M1", Task: "M1-12", Agent: "working"},
		Events: events,
		Doctor: []checks.Result{
			{ID: "git", Level: checks.OK, Message: "git 2.47"},
			{ID: "claude", Level: checks.Warn, Message: "Claude Code 2.0.1 is older than the verified 2.1.0"},
			{ID: "config", Level: checks.Fail, Message: "unknown key", File: "igris.toml", Line: 3},
		},
	})
	checkGolden(t, "prefill-igris", got)
	if strings.Contains(got, "M1-03") || !strings.Contains(got, "M1-04") {
		t.Error("not the last 10 events")
	}
	if strings.Contains(got, "unknown key") || strings.Contains(got, "2.0.1") {
		t.Error("doctor's messages, which may quote igris.toml, reached the prompt")
	}
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Error("an escape sequence from the run log reached the prompt")
	}
	none := IgrisPrefill(IgrisInfo{Backend: "herdr"})
	for _, want := range []string{"igris version: unknown", "Run: none", "no events", "Doctor: not run.", "don't change the plan", "Problem: "} {
		if !strings.Contains(none, want) {
			t.Errorf("no run, no log, no doctor: missing %q in\n%s", want, none)
		}
	}
	ok := IgrisPrefill(IgrisInfo{Doctor: []checks.Result{{Level: checks.OK}}})
	if !strings.Contains(ok, "Doctor: ok.") {
		t.Errorf("all ok:\n%s", ok)
	}
}
