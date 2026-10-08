package plan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writePlan(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	return path
}

func readPlan(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertNoTempFiles fails if anything but the plan is left in its directory.
func assertNoTempFiles(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("leftover files: %v", names)
	}
}

func set(id string, s Status) UpdateFunc {
	return func(p *Plan) ([]Change, error) {
		if err := p.Apply([]Change{{ID: id, To: s}}); err != nil {
			return nil, err
		}
		return []Change{{ID: id, To: s}}, nil
	}
}

func TestWriterPreservesBytes(t *testing.T) {
	const head = "# Plan\n\nIntro with | pipes and `code`.\n\n## M0 — Foundation\n\n" +
		"| ID | Task | Deps | Spec | Status | Model | Owner |\n|:---|---|---|--:|:-:|---|---|\n"
	tests := []struct {
		name     string
		row      string
		to       Status
		wantCell string // the row's Status cell after the write, padding included
		oldCell  string
	}{
		{"plain", "| a | **A** — x | — | 1 | ready | sonnet | agent |", InProgress, " in progress ", " ready "},
		{"wide padding", "| a | x | — | 1 |    ready     | sonnet | agent |", Done, "    done     ", "    ready     "},
		{"no padding", "| a | x | — | 1 |ready| sonnet | agent |", Done, "done", "ready"},
		{"tabs", "| a | x | — | 1 |\tready\t| sonnet | agent |", Done, "\tdone\t", "\tready\t"},
		{"backticks", "| a | x | — | 1 | `ready` | sonnet | agent |", Done, " `done` ", " `ready` "},
		{"suffix dropped", "| a | x | — | 1 | blocked (waits on vendor) | sonnet | agent |", Ready, " ready ", " blocked (waits on vendor) "},
		{"backtick suffix dropped", "| a | x | — | 1 | `skipped` (not needed) | sonnet | agent |", Done, " `done` ", " `skipped` (not needed) "},
		{"backticked suffix dropped", "| a | x | — | 1 | `skipped (not needed)` | sonnet | agent |", Ready, " `ready` ", " `skipped (not needed)` "},
		{"case normalized", "| a | x | — | 1 | READY | sonnet | agent |", Done, " done ", " READY "},
		{"escaped pipes elsewhere", "| a | `v1 \\| nonce` | — | 1 | ready | sonnet | agent |", Done, " done ", " ready "},
		{"same status rewritten", "| a | x | — | 1 | Done (by hand) | sonnet | agent |", Done, " done ", " Done (by hand) "},
	}
	const tail = "| b | other ready row | a | 2 | blocked | opus | agent |\n\n## Notes\n\n| Status | ready |\n|---|---|\n"
	for _, ending := range []struct{ name, nl string }{{"lf", "\n"}, {"crlf", "\r\n"}} {
		for _, trailing := range []bool{true, false} {
			for _, tt := range tests {
				name := fmt.Sprintf("%s/%s/trailing=%v", ending.name, tt.name, trailing)
				t.Run(name, func(t *testing.T) {
					in := head + tt.row + "\n" + tail
					if !trailing {
						in = strings.TrimSuffix(in, "\n")
					}
					in = strings.ReplaceAll(in, "\n", ending.nl)
					path := writePlan(t, in)

					got, err := NewWriter(path, Options{}, testRules).Update(context.Background(), set("a", tt.to))
					if err != nil {
						t.Fatal(err)
					}
					if len(got) != 1 {
						t.Fatalf("changes = %v", got)
					}
					wantRow := strings.Replace(tt.row, "|"+tt.oldCell+"|", "|"+tt.wantCell+"|", 1)
					want := strings.Replace(in, tt.row, wantRow, 1)
					if out := readPlan(t, path); out != want {
						t.Fatalf("output differs:\n got %q\nwant %q", out, want)
					}
					assertNoTempFiles(t, path)
				})
			}
		}
	}
}

// TestWriterEveryCellIsolated rewrites each task to every status and checks
// that only that cell changed.
func TestWriterEveryCellIsolated(t *testing.T) {
	in := specExample + "\n## M1 — Extra\n\n| ID | Status | Model | Owner |\n|---|---|---|---|\n| M1-01 | `done` | — | user |\n| M1-02 |ready|opus|agent|"
	p := Parse("tasks.md", []byte(in), Options{})
	for _, task := range p.Tasks {
		for _, s := range []Status{Ready, Blocked, InProgress, Done, Skipped} {
			path := writePlan(t, in)
			if _, err := NewWriter(path, Options{}, testRules).Update(context.Background(), set(task.ID, s)); err != nil {
				t.Fatalf("%s → %v: %v", task.ID, s, err)
			}
			out := readPlan(t, path)
			if out[:task.statusStart] != in[:task.statusStart] {
				t.Fatalf("%s → %v: bytes before the cell changed", task.ID, s)
			}
			rest := in[task.statusEnd:]
			if !strings.HasSuffix(out, rest) {
				t.Fatalf("%s → %v: bytes after the cell changed", task.ID, s)
			}
			if cell := out[task.statusStart : len(out)-len(rest)]; cell != statusCell(task.StatusText, s) {
				t.Fatalf("%s → %v: cell = %q", task.ID, s, cell)
			}
		}
	}
}

func TestWriterSyncMultipleCells(t *testing.T) {
	in := "## M0\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n" +
		"| a | — | in progress | sonnet |\n| b | a | blocked | sonnet |\n| c | a, b | `blocked` | sonnet |\n" +
		"\n## M1\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| d | a | blocked | sonnet |\n"
	path := writePlan(t, in)
	got, err := NewWriter(path, Options{}, testRules).Update(context.Background(), func(p *Plan) ([]Change, error) {
		return p.Sync("a", Done)
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a: in progress → done", "b: blocked → ready", "d: blocked → ready"}; !reflect.DeepEqual(changeStrings(got), want) {
		t.Fatalf("changes = %q", changeStrings(got))
	}
	want := strings.NewReplacer(
		"| a | — | in progress |", "| a | — | done |",
		"| b | a | blocked |", "| b | a | ready |",
		"| d | a | blocked |", "| d | a | ready |",
	).Replace(in)
	if out := readPlan(t, path); out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
}

func TestWriterNoChangesLeavesFileAlone(t *testing.T) {
	path := writePlan(t, specExample)
	before, _ := os.Stat(path)
	got, err := NewWriter(path, Options{}, testRules).Update(context.Background(), func(*Plan) ([]Change, error) { return nil, nil })
	if err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(before, after) || readPlan(t, path) != specExample {
		t.Fatal("file must not be replaced")
	}
}

func TestWriterKeepsPermissions(t *testing.T) {
	path := writePlan(t, specExample)
	if err := os.Chmod(path, 0o640); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	if _, err := NewWriter(path, Options{}, testRules).Update(context.Background(), set("M0-01", Done)); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestWriterRefusesInvalidPlan(t *testing.T) {
	in := strings.Replace(specExample, "| ready | sonnet |", "| todo | sonnet |", 1)
	path := writePlan(t, in)
	called := false
	_, err := NewWriter(path, Options{}, testRules).Update(context.Background(), func(*Plan) ([]Change, error) {
		called = true
		return nil, nil
	})
	var inv *Invalid
	if !errors.As(err, &inv) || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
	if readPlan(t, path) != in {
		t.Fatal("invalid plan must not be written")
	}
}

func TestWriterVanishedRow(t *testing.T) {
	path := writePlan(t, specExample)
	_, err := NewWriter(path, Options{}, testRules).Update(context.Background(), func(p *Plan) ([]Change, error) {
		return []Change{{ID: "M0-99", To: Done}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "task M0-99 is no longer in") {
		t.Fatalf("err = %v", err)
	}
	if readPlan(t, path) != specExample {
		t.Fatal("file must be unchanged")
	}
	assertNoTempFiles(t, path)
}

func TestWriterRowVanishesDuringWrite(t *testing.T) {
	path := writePlan(t, specExample)
	w := NewWriter(path, Options{}, testRules)
	edited := strings.Replace(specExample, "| M0-02 | **Entrypoint** — main.go with subcommands | M0-01 | blocked | sonnet | agent |\n", "", 1)
	edited = strings.Replace(edited, "M0-02…M0-03", "M0-03", 1)
	w.beforeRename = func() {
		w.beforeRename = nil
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil { //nolint:gosec // test file
			t.Error(err)
		}
	}
	_, err := w.Update(context.Background(), func(p *Plan) ([]Change, error) { return p.Sync("M0-02", InProgress) })
	if err == nil || !strings.Contains(err.Error(), "task M0-02 is not in the plan") {
		t.Fatalf("err = %v", err)
	}
	if readPlan(t, path) != edited {
		t.Fatal("the concurrent edit must survive")
	}
	assertNoTempFiles(t, path)
}

func TestWriterConcurrentEditRetried(t *testing.T) {
	path := writePlan(t, specExample)
	w := NewWriter(path, Options{}, testRules)
	// While igris writes M0-01, the owner marks M0-02 done by hand and adds a
	// note at the end of the file.
	const m02 = "| M0-02 | **Entrypoint** — main.go with subcommands | M0-01 | blocked |"
	edited := strings.Replace(specExample, m02, strings.Replace(m02, "blocked", "done", 1), 1) + "\nOwner note.\n"
	calls := 0
	w.beforeRename = func() {
		calls++
		if calls == 1 {
			if err := os.WriteFile(path, []byte(edited), 0o644); err != nil { //nolint:gosec // test file
				t.Error(err)
			}
		}
	}
	got, err := w.Update(context.Background(), func(p *Plan) ([]Change, error) { return p.Sync("M0-01", Done) })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("attempts = %d, want 2", calls)
	}
	// The retry worked on the owner's version: M0-02 is already done, so it
	// is not a change, and the gate is still waiting on M0-03.
	if want := []string{"M0-01: ready → done", "M0-03: blocked → ready"}; !reflect.DeepEqual(changeStrings(got), want) {
		t.Fatalf("changes = %q", changeStrings(got))
	}
	want := strings.NewReplacer(
		"| M0-01 | **Go module** — init module, pin Go version | — | ready |", "| M0-01 | **Go module** — init module, pin Go version | — | done |",
		"| M0-03 | **Crypto envelope** — `v1 \\| nonce \\| ciphertext` | M0-01 | blocked |", "| M0-03 | **Crypto envelope** — `v1 \\| nonce \\| ciphertext` | M0-01 | ready |",
	).Replace(edited)
	if out := readPlan(t, path); out != want {
		t.Fatalf("got %q\nwant %q", out, want)
	}
	assertNoTempFiles(t, path)
}

func TestWriterGivesUpAfterThreeAttempts(t *testing.T) {
	path := writePlan(t, specExample)
	w := NewWriter(path, Options{}, testRules)
	n := 0
	w.beforeRename = func() {
		n++
		if err := os.WriteFile(path, []byte(specExample+fmt.Sprintf("\nedit %d\n", n)), 0o644); err != nil { //nolint:gosec // test file
			t.Error(err)
		}
	}
	_, err := w.Update(context.Background(), set("M0-01", Done))
	if !errors.Is(err, ErrConcurrentEdit) || n != 3 {
		t.Fatalf("err = %v, attempts = %d", err, n)
	}
	if readPlan(t, path) != specExample+"\nedit 3\n" {
		t.Fatal("igris must not overwrite concurrent edits")
	}
	assertNoTempFiles(t, path)
}

func TestWriterErrors(t *testing.T) {
	path := writePlan(t, specExample)
	w := NewWriter(path, Options{}, testRules)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Update(ctx, set("M0-01", Done)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}

	boom := errors.New("boom")
	if _, err := w.Update(context.Background(), func(*Plan) ([]Change, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("fn error: %v", err)
	}

	missing := NewWriter(filepath.Join(t.TempDir(), "nope.md"), Options{}, testRules)
	if _, err := missing.Update(context.Background(), set("a", Done)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if readPlan(t, path) != specExample {
		t.Fatal("file must be unchanged")
	}
}

// A plan saved by a Windows editor keeps its UTF-8 byte order mark and CRLF
// line endings; only the Status cell changes.
func TestWriterKeepsBOM(t *testing.T) {
	in := "\ufeff## M0 — Foundation\r\n\r\n| ID | Task | Status | Model |\r\n|---|---|---|---|\r\n| a | x | ready | sonnet |\r\n"
	path := writePlan(t, in)
	if _, err := NewWriter(path, Options{}, testRules).Update(context.Background(), set("a", Done)); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(in, "| ready |", "| done |", 1)
	if out := readPlan(t, path); out != want {
		t.Fatalf("output differs:\n got %q\nwant %q", out, want)
	}
}

// A plan that is a symlink stays one: the write replaces the file it points
// to, byte for byte except the Status cell.
func TestWriterKeepsSymlink(t *testing.T) {
	const in = "## M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n"
	target := writePlan(t, in)
	link := filepath.Join(t.TempDir(), "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWriter(link, Options{}, testRules).Update(context.Background(), set("a", Done)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the plan path is no longer a symlink: %v, %v", fi, err)
	}
	want := strings.Replace(in, "| ready |", "| done |", 1)
	if got := readPlan(t, target); got != want {
		t.Errorf("target = %q, want %q", got, want)
	}
	assertNoTempFiles(t, target)
	assertNoTempFiles(t, link)

	// Replace behaves the same.
	if err := Replace(link, []byte(want), []byte(in)); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("Replace turned the symlink into a file")
	}
	if got := readPlan(t, target); got != in {
		t.Errorf("target after Replace = %q", got)
	}
}

func TestReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	was := []byte("# old\r\nkeep\ttabs \n")
	if err := os.WriteFile(path, was, 0o640); err != nil { //nolint:gosec // test file: a mode Replace must keep
		t.Fatal(err)
	}
	data := []byte("## M0\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n")
	if err := Replace(path, was, data); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("plan = %q, want the proposal byte for byte", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestReplaceRefusesChangedPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte("edited since the review\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Replace(path, []byte("reviewed\n"), []byte("proposal\n"))
	if !errors.Is(err, ErrConcurrentEdit) {
		t.Fatalf("err = %v, want ErrConcurrentEdit", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "edited since the review\n" { //nolint:gosec // test temp dir
		t.Errorf("plan = %q, want it untouched", got)
	}
	if err := Replace(filepath.Join(t.TempDir(), "nope.md"), nil, nil); err == nil {
		t.Error("want an error for a missing plan")
	}
}
