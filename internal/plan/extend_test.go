package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// extendBase is a plan with prose, two phases and a closing section; the
// M0 table has an extra column, Spec, and a delimiter row with alignment.
const extendBase = "# Plan\n\nIntro with | pipes.\n\n## M0 — Foundation\n\n" +
	"| ID | Task | Deps | Spec | Status | Model | Owner |\n|:---|---|---|--:|:-:|---|---|\n" +
	"| M0-01 | **Module** — `go \\| x` | — | 1 |  `done`  | sonnet | agent |\n" +
	"| M0-02 | **CI** | M0-01 | 2 | ready | sonnet | agent |\n\n" +
	"Text after the table.\n\n## M1 — Core\n\n" +
	"| ID | Task | Deps | Status | Model |\n|---|---|---|---|---|\n" +
	"| M1-01 | **Parser** | M0-02 | blocked | opus |\n\n## Notes\n\nKept as written.\n"

// extend loads the plan at path and extends it with fragment.
func extend(t *testing.T, path, fragment string) (*Extension, []Issue) {
	t.Helper()
	p, err := Load(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Check(testRules); err != nil {
		t.Fatal(err)
	}
	return Extend(p, Options{}, ".igris/draft/tasks.extend.md", []byte(fragment))
}

// mustExtend is extend for a fragment without problems.
func mustExtend(t *testing.T, path, fragment string) *Extension {
	t.Helper()
	x, issues := extend(t, path, fragment)
	if len(issues) > 0 {
		t.Fatalf("issues: %v", issueMsgs(issues))
	}
	return x
}

// Every insert keeps every existing byte (SPEC §4, §9.1): rows land after
// the last row of their phase's table in its column order, missing cells
// empty, new phases at the end of the file after one blank line, in the
// file's line ending, with or without a trailing newline and a BOM.
func TestExtendPreservesBytes(t *testing.T) {
	tests := []struct {
		name, fragment string
		// edit turns the base into the wanted result, both with "\n".
		edit func(string) string
	}{
		{
			"rows in another column order",
			"# Ignored title\n\n## M0 — whatever the session wrote\n\nIgnored prose.\n\n" +
				"| Status | ID | Model | Task | Deps |\n|---|---|---|---|---|\n" +
				"| blocked | M0-03 | `opus` | **Docs** — a \\| b | M0-02 |\n| ready | M0-04 | sonnet | **Lint** | — |\n",
			func(s string) string {
				return strings.Replace(s, "| M0-02 | **CI** | M0-01 | 2 | ready | sonnet | agent |\n",
					"| M0-02 | **CI** | M0-01 | 2 | ready | sonnet | agent |\n"+
						"| M0-03 | **Docs** — a \\| b | M0-02 |  | blocked | `opus` |  |\n"+
						"| M0-04 | **Lint** | — |  | ready | sonnet |  |\n", 1)
			},
		},
		{
			"rows in two phases",
			"## M1\n\n| ID | Task | Deps | Status | Model |\n|---|---|---|---|---|\n| M1-02 | **Printer** | M1-01 | blocked | sonnet |\n\n" +
				"## M0\n\n| ID | Status | Model |\n|---|---|---|\n| M0-03 | ready | sonnet |\n",
			func(s string) string {
				s = strings.Replace(s, "| M1-01 | **Parser** | M0-02 | blocked | opus |\n",
					"| M1-01 | **Parser** | M0-02 | blocked | opus |\n| M1-02 | **Printer** | M1-01 | blocked | sonnet |\n", 1)
				return strings.Replace(s, "| M0-02 | **CI** | M0-01 | 2 | ready | sonnet | agent |\n",
					"| M0-02 | **CI** | M0-01 | 2 | ready | sonnet | agent |\n| M0-03 |  |  |  | ready | sonnet |  |\n", 1)
			},
		},
		{
			"a new phase",
			"<!-- igris:format 1 -->\n## M2 — Release\n\nShip it.\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n" +
				"| M2-01 | **Tag** | M1-01 | blocked | sonnet | agent |\n| M2-G | **Gate** | M2-01 | blocked | sonnet | agent + user |\n\n\n" +
				"## Draft notes\n\n- M2-01 sonnet: well specified\n",
			func(s string) string {
				return s + "\n## M2 — Release\n\nShip it.\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n" +
					"| M2-01 | **Tag** | M1-01 | blocked | sonnet | agent |\n| M2-G | **Gate** | M2-01 | blocked | sonnet | agent + user |\n"
			},
		},
		{
			"rows and two new phases",
			"## M3\n\n| ID | Status | Model |\n|---|---|---|\n| M3-01 | blocked | opus |\n\n" +
				"## M1\n\n| ID | Status | Model | Deps |\n|---|---|---|---|\n| M1-02 | blocked | opus | M3-01 |\n\n" +
				"## M4\n\n| ID | Status | Model |\n|---|---|---|\n| M4-01 | ready | opus |\n",
			func(s string) string {
				s = strings.Replace(s, "| M1-01 | **Parser** | M0-02 | blocked | opus |\n",
					"| M1-01 | **Parser** | M0-02 | blocked | opus |\n| M1-02 |  | M3-01 | blocked | opus |\n", 1)
				return s + "\n## M3\n\n| ID | Status | Model |\n|---|---|---|\n| M3-01 | blocked | opus |\n" +
					"\n## M4\n\n| ID | Status | Model |\n|---|---|---|\n| M4-01 | ready | opus |\n"
			},
		},
	}
	for _, nl := range []string{"\n", "\r\n"} {
		for _, trailing := range []bool{true, false} {
			for _, bom := range []bool{false, true} {
				for _, tt := range tests {
					t.Run(fmt.Sprintf("%s/crlf=%v/trailing=%v/bom=%v", tt.name, nl != "\n", trailing, bom), func(t *testing.T) {
						base, want := extendBase, tt.edit(extendBase)
						if !trailing {
							base = strings.TrimSuffix(base, "\n")
							if strings.HasSuffix(want, "Kept as written.\n") {
								want = strings.TrimSuffix(want, "\n")
							}
						}
						base, want = strings.ReplaceAll(base, "\n", nl), strings.ReplaceAll(want, "\n", nl)
						if bom {
							base, want = "\ufeff"+base, "\ufeff"+want
						}
						path := writePlan(t, base)
						x := mustExtend(t, path, tt.fragment)
						files := x.Files()
						if len(files) != 1 || string(files[0].Old) != base {
							t.Fatalf("files = %d, want the plan", len(files))
						}
						if got := string(files[0].New); got != want {
							t.Fatalf("extended plan differs:\n got %q\nwant %q", got, want)
						}
						if readPlan(t, path) != base {
							t.Fatal("Extend wrote the plan")
						}
						if err := x.Write(); err != nil {
							t.Fatal(err)
						}
						if got := readPlan(t, path); got != want {
							t.Fatalf("written plan differs:\n got %q\nwant %q", got, want)
						}
						assertNoTempFiles(t, path)
						if err := Parse(path, []byte(want), Options{}).Check(testRules); err != nil {
							t.Fatalf("extended plan: %v", err)
						}
					})
				}
			}
		}
	}
}

// A new phase after a file that ends in a blank line gets no second one.
func TestExtendAfterBlankLine(t *testing.T) {
	path := writePlan(t, extendBase+"\n")
	x := mustExtend(t, path, "## M2\n\n| ID | Status | Model |\n|---|---|---|\n| M2-01 | ready | opus |\n")
	if got, want := string(x.Files()[0].New), extendBase+"\n## M2\n\n| ID | Status | Model |\n|---|---|---|\n| M2-01 | ready | opus |\n"; got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

// What the review lists: each addition with its file and its tasks as the
// extended plan has them; the draft notes are kept apart, never written.
func TestExtendAdditions(t *testing.T) {
	path := writePlan(t, extendBase)
	x := mustExtend(t, path, "## M1\n\n| ID | Task | Status | Model |\n|---|---|---|---|\n| M1-02 | **Printer** | blocked | ? |\n\n"+
		"## M2 — Release\n\nShip it.\n\n| ID | Task | Status | Model |\n|---|---|---|---|\n| M2-01 | **Tag** | ready | sonnet |\n\nAfter.\n\n"+
		"## Draft notes\n\n- M1-02 ?: unclear\n- M2-01 sonnet: easy\n")
	type add struct {
		file, phase, heading string
		isNew                bool
		tasks                []string
		prose                []string
	}
	var got []add
	for _, a := range x.Additions {
		var ids []string
		for _, t := range a.Tasks {
			ids = append(ids, fmt.Sprintf("%s:%d:%s", t.ID, t.Line, t.Phase.ID))
		}
		got = append(got, add{a.File, a.Phase, a.Heading, a.New, ids, a.Prose})
	}
	want := []add{
		{path, "M1", "M1 — Core", false, []string{"M1-02:19:M1"}, nil},
		{path, "M2", "M2 — Release", true, []string{"M2-01:31:M2"}, []string{"", "Ship it.", "", "", "After."}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("additions:\n got %v\nwant %v", got, want)
	}
	if want := []string{"- M1-02 ?: unclear", "- M2-01 sonnet: easy"}; !slices.Equal(x.Notes, want) {
		t.Fatalf("notes = %q, want %q", x.Notes, want)
	}
	if strings.Contains(string(x.Files()[0].New), "Draft notes") {
		t.Fatal("draft notes written to the plan")
	}
	// The `?` rank is the extended plan's only problem.
	issues := x.Plan.Validate(testRules)
	if len(issues) != 1 || issues[0].Line != 19 || !strings.Contains(issues[0].Msg, `"?"`) {
		t.Fatalf("issues = %v", issueMsgs(issues))
	}
}

// Fragment problems are refused with the fragment's lines: igris never
// offers to write them.
func TestExtendRefusesFragment(t *testing.T) {
	const m2 = "## M2\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n"
	tests := []struct {
		name, fragment string
		want           []string
	}{
		{"empty", "# Nothing\n\nprose only\n", []string{"the fragment adds no tasks; write ## phase sections with task tables"}},
		{"taken ID", m2 + "| M1-01 | — | ready | opus |\n", []string{":5: task M1-01 is already in the plan (" /* path */}},
		{"twice in the fragment", m2 + "| M2-01 | — | ready | opus |\n| M2-01 | — | ready | opus |\n", []string{":6: task M2-01 is in the fragment twice (first at line 5)"}},
		{"invalid ID", m2 + "| **M2-01** | — | ready | opus |\n", []string{`:5: "**M2-01**" is not a valid task ID`}},
		{"done", m2 + "| M2-01 | — | done | opus |\n| M2-02 | — | in progress | opus |\n",
			[]string{`:5: task M2-01 has Status "done"; a new task is ready or blocked`, `:6: task M2-02 has Status "in progress"`}},
		{"unknown dep", m2 + "| M2-01 | M1-01, M9-01 | blocked | opus |\n", []string{":5: task M2-01 depends on M9-01, which is neither in the plan nor in the fragment"}},
		{"unknown range end", m2 + "| M2-01 | M0-01…M9-02 | blocked | opus |\n", []string{":5: task M2-01 depends on M9-02"}},
		{"extra column", "## M1\n\n| ID | Status | Model | Owner | Spec |\n|---|---|---|---|---|\n| M1-02 | ready | opus | agent | 3 |\n",
			[]string{":3: column Owner is not in the table of phase M1 (", ":3: column Spec is not in the table of phase M1 ("}},
		{"section without a table", "## Ideas\n\nlater\n\n" + m2 + "| M2-01 | — | ready | opus |\n", []string{`:1: section "Ideas" has no task table`}},
		{"phase without rows", m2, []string{":1: phase M2 adds no tasks"}},
		{"include line", "<!-- igris:format 2 -->\n<!-- igris:include x.md -->\n" + m2 + "| M2-01 | — | ready | opus |\n", []string{":2: includes are not read here"}},
		{"two sections of a phase", m2 + "| M2-01 | — | ready | opus |\n\n" + m2 + "| M2-02 | — | ready | opus |\n", []string{":7: duplicate phase ID M2"}},
		{"escape sequence", "## M2\n\nbad \x1b[31m\n\n| ID | Status | Model |\n|---|---|---|\n| M2-01 | ready | opus |\n", []string{":1: the section of phase M2 contains a control character"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePlan(t, extendBase)
			x, issues := extend(t, path, tt.fragment)
			if x != nil {
				t.Fatal("got an extension")
			}
			msgs := issueMsgs(issues)
			if len(msgs) != len(tt.want) {
				t.Fatalf("issues = %q, want %d", msgs, len(tt.want))
			}
			for i, w := range tt.want {
				if !strings.Contains(msgs[i], w) {
					t.Errorf("issue %d = %q, want %q in it", i, msgs[i], w)
				}
			}
		})
	}
}

// New phases are refused when the plan ends inside a fence, where they
// would be hidden.
func TestExtendRefusesOpenFence(t *testing.T) {
	path := writePlan(t, extendBase+"\n```\nopen\n")
	_, issues := extend(t, path, "## M2\n\n| ID | Status | Model |\n|---|---|---|\n| M2-01 | ready | opus |\n")
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "ends inside a fenced code block") {
		t.Fatalf("issues = %v", issueMsgs(issues))
	}
	// Rows for an existing phase are fine.
	mustExtend(t, path, "## M1\n\n| ID | Status | Model |\n|---|---|---|\n| M1-02 | ready | opus |\n")
}

// An extension that takes the plan over MaxSize is refused: igris
// couldn't read the result.
func TestExtendRefusesTooLarge(t *testing.T) {
	path := writePlan(t, extendBase)
	big := "## M2\n\n" + strings.Repeat("x", MaxSize) + "\n\n| ID | Status | Model |\n|---|---|---|\n| M2-01 | ready | opus |\n"
	if _, issues := extend(t, path, big); len(issues) != 1 || !strings.Contains(issues[0].Msg, "would be over 16 MiB") {
		t.Fatalf("issues = %v", issueMsgs(issues))
	}
}

// splitBase is a split plan: phase M1 lives in phases/m1.md.
const splitBase = "<!-- igris:format 2 -->\n# Plan\n\n## M0\n\n| ID | Status | Model |\n|---|---|---|\n| M0-01 | done | sonnet |\n\n" +
	"<!-- igris:include phases/m1.md -->\n\n## Notes\n\nKept.\n"

const splitM1 = "# M1 kept apart\n\n## M1 — Core\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| M1-01 | M0-01 | ready | opus |\n\nAfter.\n"

// On a split plan rows go into the file their phase lives in and new
// phases into the main file; a file nothing goes into is left alone.
func TestExtendSplitPlan(t *testing.T) {
	dir := writeFiles(t, map[string]string{"tasks.md": splitBase, "phases/m1.md": splitM1})
	path := filepath.Join(dir, "tasks.md")
	p, err := Load(path, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	frag := "## M1\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| M1-02 | M1-01 | blocked | opus |\n\n" +
		"## M2\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| M2-01 | M1-02 | blocked | sonnet |\n"
	x, issues := Extend(p, Options{Root: dir}, "frag.md", []byte(frag))
	if len(issues) > 0 {
		t.Fatal(issueMsgs(issues))
	}
	inc := filepath.Join(dir, "phases", "m1.md")
	if got := []string{x.Additions[0].File, x.Additions[1].File}; !slices.Equal(got, []string{inc, path}) {
		t.Fatalf("files = %v", got)
	}
	wantMain := splitBase + "\n## M2\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| M2-01 | M1-02 | blocked | sonnet |\n"
	wantInc := strings.Replace(splitM1, "| M1-01 | M0-01 | ready | opus |\n", "| M1-01 | M0-01 | ready | opus |\n| M1-02 | M1-01 | blocked | opus |\n", 1)
	if err := x.Plan.Check(testRules); err != nil {
		t.Fatal(err)
	}
	if t2 := x.Plan.Task("M2-01"); t2 == nil || t2.Phase.File != path {
		t.Fatalf("M2-01 = %+v", t2)
	}
	if err := x.Write(); err != nil {
		t.Fatal(err)
	}
	if got := readPlan(t, path); got != wantMain {
		t.Fatalf("main:\n got %q\nwant %q", got, wantMain)
	}
	if got := readPlan(t, inc); got != wantInc {
		t.Fatalf("included:\n got %q\nwant %q", got, wantInc)
	}
	assertOnly(t, dir, "phases", "tasks.md")
	assertOnly(t, filepath.Join(dir, "phases"), "m1.md")

	// Rows only: the main plan is neither changed nor replaced.
	before := stat(t, path)
	p, err = Load(path, Options{Root: dir})
	if err != nil {
		t.Fatal(err)
	}
	x, issues = Extend(p, Options{Root: dir}, "frag.md", []byte("## M1\n\n| ID | Status | Model |\n|---|---|---|\n| M1-03 | ready | opus |\n"))
	if len(issues) > 0 {
		t.Fatal(issueMsgs(issues))
	}
	if files := x.Files(); len(files) != 1 || files[0].Name != inc {
		t.Fatalf("files = %v", files)
	}
	if err := x.Write(); err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, stat(t, path)) || readPlan(t, path) != wantMain {
		t.Fatal("main plan changed or replaced")
	}
}

// Nothing is written when any file of the plan changed since it was read,
// also one the extension doesn't change.
func TestExtendWriteRefusesChangedPlan(t *testing.T) {
	frag := "## M1\n\n| ID | Status | Model |\n|---|---|---|\n| M1-02 | ready | opus |\n"
	t.Run("before", func(t *testing.T) {
		dir := writeFiles(t, map[string]string{"tasks.md": splitBase, "phases/m1.md": splitM1})
		p, err := Load("tasks.md", Options{Root: dir})
		if err != nil {
			t.Fatal(err)
		}
		x, issues := Extend(p, Options{Root: dir}, "frag.md", []byte(frag))
		if len(issues) > 0 {
			t.Fatal(issueMsgs(issues))
		}
		if err := os.WriteFile("tasks.md", []byte(splitBase+"edited\n"), 0o644); err != nil { //nolint:gosec // test file
			t.Fatal(err)
		}
		if err := x.Write(); !errors.Is(err, ErrConcurrentEdit) {
			t.Fatalf("err = %v, want ErrConcurrentEdit", err)
		}
		if readPlan(t, "phases/m1.md") != splitM1 {
			t.Fatal("included file written")
		}
	})
	t.Run("between temp files and renames", func(t *testing.T) {
		path := writePlan(t, extendBase)
		x := mustExtend(t, path, frag)
		x.beforeRename = func() {
			if err := os.WriteFile(path, []byte(extendBase+"edited\n"), 0o644); err != nil { //nolint:gosec // test file
				t.Fatal(err)
			}
		}
		if err := x.Write(); !errors.Is(err, ErrConcurrentEdit) {
			t.Fatalf("err = %v, want ErrConcurrentEdit", err)
		}
		if readPlan(t, path) != extendBase+"edited\n" {
			t.Fatal("plan overwritten")
		}
		assertNoTempFiles(t, path)
	})
}

// Write keeps the file mode and a plan that is a symlink stays one.
func TestExtendWriteKeepsModeAndSymlink(t *testing.T) {
	dir := writeFiles(t, map[string]string{"real/tasks.md": extendBase})
	if err := os.Chmod("real/tasks.md", 0o640); err != nil { //nolint:gosec // test file: a mode the write must keep
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("real", "tasks.md"), "tasks.md"); err != nil {
		t.Fatal(err)
	}
	x := mustExtend(t, filepath.Join(dir, "tasks.md"), "## M1\n\n| ID | Status | Model |\n|---|---|---|\n| M1-02 | ready | opus |\n")
	if err := x.Write(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat("tasks.md"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("tasks.md is no longer a symlink: %v", err)
	}
	if fi := stat(t, "real/tasks.md"); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", fi.Mode().Perm())
	}
	if !strings.Contains(readPlan(t, "real/tasks.md"), "| M1-02 |  |  | ready | opus |\n") {
		t.Fatal("row not written")
	}
}

// FuzzExtend checks the extend splicer's byte-preservation property on any
// valid plan and any fragment it accepts: every byte of the plan stays, in
// order (only insertions), and the extended plan has the plan's tasks
// unchanged plus the fragment's.
func FuzzExtend(f *testing.F) {
	f.Add([]byte(extendBase), []byte("## M1\n\n| ID | Status | Model |\n|---|---|---|\n| M1-02 | ready | opus |\n"))
	f.Add([]byte(strings.ReplaceAll(extendBase, "\n", "\r\n")), []byte("## M2\n\nx\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| M2-01 | M1-01 | blocked | opus |\n"))
	f.Add([]byte(strings.TrimSuffix(extendBase, "\n")), []byte("## M0\n\n| Model | ID | Status |\n|---|---|---|\n| sonnet | M0-03 | ready |\n\n## Draft notes\n\n- x\n"))
	f.Fuzz(func(t *testing.T, data, fragment []byte) {
		p := Parse("tasks.md", data, Options{})
		if p.Check(testRules) != nil {
			return
		}
		x, issues := Extend(p, Options{}, "frag.md", fragment)
		if x == nil {
			if len(issues) == 0 {
				t.Fatal("no extension and no issues")
			}
			return
		}
		for _, fc := range x.Files() {
			if !subsequence(fc.Old, fc.New) {
				t.Fatalf("%s: the extended file drops or changes bytes of the plan", fc.Name)
			}
		}
		for _, a := range p.Tasks {
			b := x.Plan.Task(a.ID)
			if b == nil || a.Text != b.Text || a.StatusText != b.StatusText || a.DepsText != b.DepsText || a.Rank != b.Rank || a.OwnerText != b.OwnerText {
				t.Fatalf("task %s changed: %+v → %+v", a.ID, a, b)
			}
		}
	})
}

// subsequence reports whether a's bytes appear in b in order.
func subsequence(a, b []byte) bool {
	i := 0
	for j := 0; i < len(a) && j < len(b); j++ {
		if a[i] == b[j] {
			i++
		}
	}
	return i == len(a)
}
