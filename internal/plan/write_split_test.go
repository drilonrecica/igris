package plan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// splitMain is a main plan including phases/b.md between its two phases;
// C-1 waits on {B}, a task of the included file.
const splitMain = "\ufeff# Plan\n" + format2 + "\nIntro | with pipes.\n\n## A — First\n\n" +
	"| ID | Deps | Status | Model |\n|---|---|---|---|\n| A-1 | — | done | sonnet |\n\n" +
	"<!--  igris:include phases/b.md  -->\n\n" +
	"## C — Last\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| C-1 | {B} | blocked | opus |\n"

// assertOnly fails unless dir holds exactly names: no temp file is left.
func assertOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	slices.Sort(names)
	if !slices.Equal(got, names) {
		t.Fatalf("%s holds %v, want %v", dir, got, names)
	}
}

// stat returns the file info of path, failing the test on an error.
func stat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// Every byte-preservation case of TestWriterPreservesBytes, with the row in
// an included file: the cell changes in that file, and the main plan is
// neither changed nor replaced (SPEC §4 rule 7).
func TestSplitWriterPreservesBytes(t *testing.T) {
	for _, ending := range []struct{ name, nl string }{{"lf", "\n"}, {"crlf", "\r\n"}} {
		for _, trailing := range []bool{true, false} {
			for _, tt := range preserveCases {
				t.Run(fmt.Sprintf("%s/%s/trailing=%v", ending.name, tt.name, trailing), func(t *testing.T) {
					inc := "<!-- igris:format 1 -->\n" + preserveHead + tt.row + "\n" + preserveTail
					if !trailing {
						inc = strings.TrimSuffix(inc, "\n")
					}
					inc = strings.ReplaceAll(inc, "\n", ending.nl)
					main := strings.ReplaceAll(strings.Replace(splitMain, "{B}", "b", 1), "\n", ending.nl)
					dir := writeFiles(t, map[string]string{"tasks.md": main, "phases/b.md": inc})
					before := stat(t, "tasks.md")

					got, err := NewWriter("tasks.md", Options{}, testRules).Update(context.Background(), set("a", tt.to))
					if err != nil {
						t.Fatal(err)
					}
					if len(got) != 1 {
						t.Fatalf("changes = %v", got)
					}
					wantRow := strings.Replace(tt.row, "|"+tt.oldCell+"|", "|"+tt.wantCell+"|", 1)
					if out, want := readPlan(t, "phases/b.md"), strings.Replace(inc, tt.row, wantRow, 1); out != want {
						t.Fatalf("included file differs:\n got %q\nwant %q", out, want)
					}
					if out := readPlan(t, "tasks.md"); out != main || !os.SameFile(before, stat(t, "tasks.md")) {
						t.Fatalf("main plan changed or replaced: %q", out)
					}
					assertOnly(t, dir, "phases", "tasks.md")
					assertOnly(t, filepath.Join(dir, "phases"), "b.md")
				})
			}
		}
	}
}

// splitFiles is a plan of three files, each with tasks.
var splitFiles = map[string]string{
	"tasks.md": strings.Replace(splitMain, "{B}", "B-2", 1) + "\n<!-- igris:include d.md -->\n",
	"phases/b.md": "# Phase B\r\n\r\n## B — Middle\r\n\r\n| ID | Task | Deps | Status | Model | Owner |\r\n|---|---|---|:-:|---|---|\r\n" +
		"| B-1 | `x \\| y` | A-1 |  `ready`  | sonnet | agent |\r\n| B-2 | **Two** | B-1 |blocked| — | user |\r\n",
	"d.md": "## D — Apart\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| D-1 | B-1 | Blocked (soon) | opus |",
}

// Each task of each file set to each status changes only its own cell, in
// its own file; the plan's other files are not replaced.
func TestSplitWriterEveryCellIsolated(t *testing.T) {
	writeFiles(t, splitFiles)
	p := loadSplit(t)
	if err := p.Check(testRules); err != nil {
		t.Fatal(err)
	}
	for _, task := range p.Tasks {
		for _, s := range fuzzStatuses {
			writeFiles(t, splitFiles)
			infos := map[string]os.FileInfo{}
			for name := range splitFiles {
				infos[name] = stat(t, name)
			}
			if _, err := NewWriter("tasks.md", Options{}, testRules).Update(context.Background(), set(task.ID, s)); err != nil {
				t.Fatalf("%s → %v: %v", task.ID, s, err)
			}
			for name, in := range splitFiles {
				out := readPlan(t, name)
				if filepath.FromSlash(name) != task.Phase.File {
					if out != in || !os.SameFile(infos[name], stat(t, name)) {
						t.Fatalf("%s → %v: %s changed or replaced", task.ID, s, name)
					}
					continue
				}
				want := in[:task.statusStart] + statusCell(task.StatusText, s) + in[task.statusEnd:]
				if out != want {
					t.Fatalf("%s → %v in %s:\n got %q\nwant %q", task.ID, s, name, out, want)
				}
			}
		}
	}
}

// A readiness sync writes each cell in the file its row is in: the done
// task in one file, the tasks it unblocks in two others.
func TestSplitWriterSyncAcrossFiles(t *testing.T) {
	files := map[string]string{
		"tasks.md":    strings.Replace(strings.Replace(splitFiles["tasks.md"], "| A-1 | — | done |", "| A-1 | — | in progress |", 1), "| C-1 | B-2 |", "| C-1 | A-1 |", 1),
		"phases/b.md": splitFiles["phases/b.md"],
		"d.md":        "## D — Apart\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| D-1 | B-1 | blocked | opus |\n| D-2 | A-1 | blocked | opus |\n",
	}
	files["phases/b.md"] = strings.Replace(files["phases/b.md"], "`ready`", "`blocked`", 1)
	writeFiles(t, files)
	got, err := NewWriter("tasks.md", Options{}, testRules).Update(context.Background(), func(p *Plan) ([]Change, error) {
		return p.Sync("A-1", Done)
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A-1: in progress → done", "B-1: blocked → ready", "C-1: blocked → ready", "D-2: blocked → ready"}; !reflect.DeepEqual(changeStrings(got), want) {
		t.Fatalf("changes = %q", changeStrings(got))
	}
	want := map[string]string{
		"tasks.md":    strings.NewReplacer("| A-1 | — | in progress |", "| A-1 | — | done |", "| C-1 | A-1 | blocked |", "| C-1 | A-1 | ready |").Replace(files["tasks.md"]),
		"phases/b.md": strings.Replace(files["phases/b.md"], "`blocked`", "`ready`", 1),
		"d.md":        strings.Replace(files["d.md"], "| D-2 | A-1 | blocked |", "| D-2 | A-1 | ready |", 1),
	}
	for name, w := range want {
		if out := readPlan(t, name); out != w {
			t.Errorf("%s:\n got %q\nwant %q", name, out, w)
		}
	}
}

// A forced reset in the main plan re-blocks a dependent in an included file.
func TestSplitWriterResetAcrossFiles(t *testing.T) {
	files := map[string]string{
		"tasks.md":    strings.Replace(splitMain, "{B}", "B-1", 1),
		"phases/b.md": "## B — Middle\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| B-1 | A-1 |\tready\t| sonnet |\n",
	}
	writeFiles(t, files)
	got, err := NewWriter("tasks.md", Options{}, testRules).Update(context.Background(), func(p *Plan) ([]Change, error) {
		return p.Reset("A-1", true)
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"A-1: done → ready", "B-1: ready → blocked"}; !reflect.DeepEqual(changeStrings(got), want) {
		t.Fatalf("changes = %q", changeStrings(got))
	}
	if out, want := readPlan(t, "tasks.md"), strings.Replace(files["tasks.md"], "| A-1 | — | done |", "| A-1 | — | ready |", 1); out != want {
		t.Errorf("main plan:\n got %q\nwant %q", out, want)
	}
	if out, want := readPlan(t, "phases/b.md"), strings.Replace(files["phases/b.md"], "\tready\t", "\tblocked\t", 1); out != want {
		t.Errorf("included file:\n got %q\nwant %q", out, want)
	}
}

// An included file that is a symlink stays one, and keeps its mode: the
// write replaces the file it points to.
func TestSplitWriterKeepsSymlinkAndMode(t *testing.T) {
	const inc = "## B — Middle\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| B-1 | A-1 | ready | sonnet |\n"
	dir := writeFiles(t, map[string]string{"tasks.md": strings.Replace(splitMain, "{B}", "B-1", 1), "kept/real.md": inc})
	if err := os.Mkdir("phases", 0o755); err != nil { //nolint:gosec // test dir
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "kept", "real.md"), filepath.Join("phases", "b.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join("kept", "real.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWriter("tasks.md", Options{}, testRules).Update(context.Background(), set("B-1", Done)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join("phases", "b.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the include is no longer a symlink: %v, %v", fi, err)
	}
	if out, want := readPlan(t, "kept/real.md"), strings.Replace(inc, "| ready |", "| done |", 1); out != want {
		t.Errorf("target = %q, want %q", out, want)
	}
	if m := stat(t, "kept/real.md").Mode().Perm(); m != 0o600 {
		t.Errorf("mode = %v, want 0600", m)
	}
	assertOnly(t, filepath.Join(dir, "kept"), "real.md")
	assertOnly(t, filepath.Join(dir, "phases"), "b.md")
}

// A change to any file of the plan before the renames, also one the write
// doesn't touch, discards the write and retries from the read; the retry
// works on the owner's version.
func TestSplitWriterConcurrentEditRetried(t *testing.T) {
	dir := writeFiles(t, splitFiles)
	w := NewWriter("tasks.md", Options{}, testRules)
	edited := strings.Replace(splitFiles["d.md"], "Blocked (soon)", "done", 1) + "\nOwner note.\n"
	calls := 0
	w.beforeRename = func() {
		calls++
		if calls == 1 {
			if err := os.WriteFile("d.md", []byte(edited), 0o644); err != nil { //nolint:gosec // test file
				t.Error(err)
			}
		}
	}
	got, err := w.Update(context.Background(), set("A-1", InProgress))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(got) != 1 {
		t.Fatalf("attempts = %d, changes = %v", calls, got)
	}
	if out := readPlan(t, "d.md"); out != edited {
		t.Errorf("the owner's edit was lost: %q", out)
	}
	if out, want := readPlan(t, "tasks.md"), strings.Replace(splitFiles["tasks.md"], "| A-1 | — | done |", "| A-1 | — | in progress |", 1); out != want {
		t.Errorf("main plan = %q", out)
	}
	assertOnly(t, dir, "d.md", "phases", "tasks.md")
}

// An include that resolves to another file by the renames is a change: the
// retry reads and writes the file it names now.
func TestSplitWriterIncludeRetargeted(t *testing.T) {
	const inc = "## B — Middle\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| B-1 | A-1 | ready | sonnet |\n"
	dir := writeFiles(t, map[string]string{"tasks.md": strings.Replace(splitMain, "{B}", "B-1", 1), "one.md": inc, "two.md": inc})
	if err := os.Mkdir("phases", 0o755); err != nil { //nolint:gosec // test dir
		t.Fatal(err)
	}
	link := filepath.Join("phases", "b.md")
	if err := os.Symlink(filepath.Join(dir, "one.md"), link); err != nil {
		t.Fatal(err)
	}
	w := NewWriter("tasks.md", Options{}, testRules)
	calls := 0
	w.beforeRename = func() {
		calls++
		if calls == 1 {
			if err := os.Remove(link); err != nil {
				t.Error(err)
			}
			if err := os.Symlink(filepath.Join(dir, "two.md"), link); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := w.Update(context.Background(), set("B-1", Done)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("attempts = %d, want 2", calls)
	}
	if out := readPlan(t, "one.md"); out != inc {
		t.Errorf("the old target was written: %q", out)
	}
	if out := readPlan(t, "two.md"); out != strings.Replace(inc, "| ready |", "| done |", 1) {
		t.Errorf("the new target = %q", out)
	}
	assertOnly(t, dir, "one.md", "phases", "tasks.md", "two.md")
}

// An included file edited during every attempt: igris gives up and writes
// no file, leaving no temp file anywhere.
func TestSplitWriterGivesUpAfterThreeAttempts(t *testing.T) {
	dir := writeFiles(t, splitFiles)
	w := NewWriter("tasks.md", Options{}, testRules)
	n := 0
	w.beforeRename = func() {
		n++
		if err := os.WriteFile(filepath.Join("phases", "b.md"), []byte(splitFiles["phases/b.md"]+fmt.Sprintf("\r\nedit %d\r\n", n)), 0o644); err != nil { //nolint:gosec // test file
			t.Error(err)
		}
	}
	// The write touches the main plan only; the edits land in phases/b.md.
	_, err := w.Update(context.Background(), set("A-1", InProgress))
	if !errors.Is(err, ErrConcurrentEdit) || n != 3 {
		t.Fatalf("err = %v, attempts = %d", err, n)
	}
	if readPlan(t, "tasks.md") != splitFiles["tasks.md"] {
		t.Error("the main plan was written")
	}
	assertOnly(t, dir, "d.md", "phases", "tasks.md")
	assertOnly(t, filepath.Join(dir, "phases"), "b.md")
}

// An included file removed before the renames: the retry's validation
// reports it, and no file is written.
func TestSplitWriterIncludedFileRemoved(t *testing.T) {
	dir := writeFiles(t, splitFiles)
	w := NewWriter("tasks.md", Options{}, testRules)
	w.beforeRename = func() {
		w.beforeRename = nil
		if err := os.Remove("d.md"); err != nil {
			t.Error(err)
		}
	}
	_, err := w.Update(context.Background(), set("A-1", InProgress))
	if err == nil || !strings.Contains(err.Error(), "d.md does not exist") {
		t.Fatalf("err = %v", err)
	}
	if readPlan(t, "tasks.md") != splitFiles["tasks.md"] {
		t.Error("the main plan was written")
	}
	assertOnly(t, dir, "phases", "tasks.md")
}

// A problem in an included file stops the write as one in the main plan
// does, and each Status write needs the whole plan valid.
func TestSplitWriterRefusesInvalidIncludedFile(t *testing.T) {
	files := map[string]string{
		"tasks.md":    splitFiles["tasks.md"],
		"phases/b.md": splitFiles["phases/b.md"],
		"d.md":        strings.Replace(splitFiles["d.md"], "| opus |", "| gpt |", 1),
	}
	writeFiles(t, files)
	_, err := NewWriter("tasks.md", Options{}, testRules).Update(context.Background(), set("A-1", InProgress))
	if err == nil || !strings.Contains(err.Error(), "d.md:5:") {
		t.Fatalf("err = %v", err)
	}
	for name, in := range files {
		if readPlan(t, name) != in {
			t.Errorf("%s changed", name)
		}
	}
}
