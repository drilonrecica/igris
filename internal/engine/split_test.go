package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
)

// splitMainPlan includes phases/b.md between its phases A and C (SPEC
// §3.7); splitIncluded is that file, with CRLF line endings and prose.
const (
	splitMainPlan = "<!-- igris:format 2 -->\n# Plan\n\n## A — First\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| A-1 | — | ready | sonnet |\n\n" +
		"<!-- igris:include phases/b.md -->\n\n## C — Last\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| C-1 | B-2 | blocked | opus |\n"
	splitIncluded = "# Phase B\r\n\r\nKept apart.\r\n\r\n## B — Middle\r\n\r\n| ID | Deps | Status | Model |\r\n|---|---|---|---|\r\n" +
		"| B-1 | A-1 | blocked | sonnet |\r\n| B-2 | B-1 |  `blocked`  | sonnet |\r\n"
)

func newSplitHarness(t *testing.T, tomlText string) *harness {
	t.Helper()
	h := newHarness(t, splitMainPlan, tomlText)
	h.write(filepath.Join("phases", "b.md"), splitIncluded)
	return h
}

func runAC(o *Options) { o.Phase, o.Through = "A", "C" }

// A run over a split plan writes each Status cell in the file its row is
// in, every other byte kept, and names the task's file in its prompt.
func TestSplitPlanRun(t *testing.T) {
	h := newSplitHarness(t, "")
	res, err := h.run(runAC)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.opened(); got != "A-1 B-1 B-2 C-1" {
		t.Errorf("sessions opened for %q", got)
	}
	if got, want := h.read("tasks.md"), strings.NewReplacer("| ready |", "| done |", "| blocked |", "| done |").Replace(splitMainPlan); got != want {
		t.Errorf("main plan:\n got %q\nwant %q", got, want)
	}
	if got, want := h.read("phases/b.md"), strings.NewReplacer("| blocked |", "| done |", "`blocked`", "`done`").Replace(splitIncluded); got != want {
		t.Errorf("included file:\n got %q\nwant %q", got, want)
	}
	if n := h.count(PlanChanged); n != 0 {
		t.Errorf("%d plan_changed events for igris's own writes", n)
	}
	if p := h.be.Prompts("A-1"); len(p) == 0 || !strings.Contains(p[0], "Plan file: `tasks.md` (find the row for `A-1`)") {
		t.Errorf("A-1 prompt: %q", p)
	}
	want := "Plan file: `tasks.md`; the row for `B-1` is in `" + filepath.Join("phases", "b.md") + "`, which it includes"
	if p := h.be.Prompts("B-1"); len(p) == 0 || !strings.Contains(p[0], want) {
		t.Errorf("B-1 prompt doesn't say %q: %q", want, p)
	}
}

// A session editing a row of an included file holds the run as an edit of
// the main plan does (SPEC §5.4).
func TestSplitPlanIncludedEditHoldsTheRun(t *testing.T) {
	h := newSplitHarness(t, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			h.write(filepath.Join("phases", "b.md"), strings.Replace(splitIncluded, "| B-2 | B-1 |  `blocked`  | sonnet |", "| B-2 | B-1 |  `blocked`  | fable |", 1))
		}
	}
	h.clock.At(time.Minute, func() {
		if got := h.opened(); got != "A-1" {
			t.Errorf("while held: sessions opened for %q, want only A-1", got)
		}
		if got := h.count(PlanChanged); got != 1 {
			t.Errorf("while held: %d plan_changed events, want 1", got)
		}
		h.eng.Send(Command{Kind: CmdPause}) // resume
	})
	res, err := h.run(runAC)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if ev := h.event(PlanChanged, ""); !strings.Contains(ev.Detail, "B-2 Model sonnet → fable") {
		t.Errorf("plan_changed detail = %q", ev.Detail)
	}
	if got := h.statuses(); got != "A-1=done B-1=done B-2=done C-1=done" {
		t.Errorf("statuses = %s", got)
	}
}

// A worktree session may read each file of the plan, which is outside the
// worktree, unless the file's path holds pattern characters; its prompt
// names the task's file by its absolute path (SPEC §6.8).
func TestSplitPlanWorktree(t *testing.T) {
	h := newSplitHarness(t, "")
	if _, err := h.run(withWorktree, runAC); err != nil {
		t.Fatal(err)
	}
	want := []string{"Read(/" + h.path("tasks.md") + ")", "Read(/" + h.path(filepath.Join("phases", "b.md")) + ")"}
	if got := h.eng.worktreeAllow(); !slices.Equal(got, want) {
		t.Errorf("allow = %q, want %q", got, want)
	}
	if p := h.be.Prompts("B-1"); len(p) == 0 || !strings.Contains(p[0], "is in `"+h.path(filepath.Join("phases", "b.md"))+"`") {
		t.Errorf("B-1 prompt doesn't name the included file's absolute path: %q", p)
	}
	h.eng.included = []string{h.path(filepath.Join("phases", "[b].md"))}
	if got := h.eng.worktreeAllow(); !slices.Equal(got, want[:1]) {
		t.Errorf("included path with pattern characters: %q", got)
	}
}

// Every poll reads every file of the plan: a row moved to another file, an
// include removed and one added are reported as plan edits (SPEC §5.4).
func TestPlanWatchSplitPlan(t *testing.T) {
	const phaseB = "## B — Middle\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| B-1 | A-1 | blocked | sonnet |\n| B-2 | B-1 | blocked | sonnet |\n"
	inlined := strings.Replace(splitMainPlan, "<!-- igris:include phases/b.md -->\n", phaseB, 1)
	tests := []struct {
		name  string
		main  string
		files map[string]string
		want  string
	}{
		{"unchanged", splitMainPlan, nil, ""},
		{"included row edited", splitMainPlan, map[string]string{"phases/b.md": strings.Replace(splitIncluded, "| B-1 | A-1 | blocked |", "| B-1 | — | blocked |", 1)},
			"B-1 Deps A-1 → —"},
		{"rows moved into the main plan", inlined, nil,
			"B-1 file phases/b.md → tasks.md; B-2 file phases/b.md → tasks.md"},
		{"include removed", strings.Replace(splitMainPlan, "<!-- igris:include phases/b.md -->\n", "", 1), nil,
			"B-1 removed; B-2 removed"},
		{"include added", splitMainPlan + "\n<!-- igris:include d.md -->\n", map[string]string{"d.md": "## D\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| D-1 | — | ready | sonnet |\n"},
			"D-1 added"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, content string) {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			load := func() *plan.Plan {
				p, err := plan.Load(filepath.Join(root, "tasks.md"), plan.Options{Root: root})
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			write("tasks.md", splitMainPlan)
			write("phases/b.md", splitIncluded)
			var w planWatch
			w.reset(load())
			write("tasks.md", tt.main)
			for name, content := range tt.files {
				write(name, content)
			}
			if got := strings.Join(w.diff(load()), "; "); got != tt.want {
				t.Errorf("diff = %q, want %q", got, tt.want)
			}
		})
	}
}
