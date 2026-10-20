package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The engine reads a split plan (SPEC §3.7), but until the writer writes
// Status cells across files it refuses to run one: the run stops at its
// first write, before any session opens, and the plan stays as it was.
func TestSplitPlanRefusedBeforeASession(t *testing.T) {
	main := "<!-- igris:format 2 -->\n\n## A — First\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| A-1 | — | ready | sonnet |\n\n<!-- igris:include phases/b.md -->\n"
	h := newHarness(t, main, "")
	if err := os.MkdirAll(h.path("phases"), 0o750); err != nil {
		t.Fatal(err)
	}
	h.write(filepath.Join("phases", "b.md"), "## B — Second\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| B-1 | A-1 | blocked | sonnet |\n")
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), "split plan") {
		t.Fatalf("run: err = %v, want the split-plan refusal", err)
	}
	if n := len(h.be.Opened()); n != 0 {
		t.Errorf("%d session(s) opened", n)
	}
	if got, err := os.ReadFile(h.path("tasks.md")); err != nil || string(got) != main {
		t.Errorf("plan changed: %q (%v)", got, err)
	}
}
