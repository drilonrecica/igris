package adapt

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/plan"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// reviewText writes a review as the TUI's markers would, without layout.
func reviewText(r Review) string {
	var b strings.Builder
	mark := map[Op]string{Equal: "  ", Add: "+ ", Del: "- ", Mod: "~ "}
	b.WriteString(r.Summary() + "\n")
	for _, s := range r.Sections {
		b.WriteString(mark[s.Op] + "phase " + s.Heading + "\n")
		for _, c := range s.Changes {
			b.WriteString("  " + mark[c.Op] + c.String() + "\n")
		}
	}
	b.WriteString("prose:\n")
	for _, l := range r.Prose {
		b.WriteString(mark[l.Op] + l.Text + "\n")
	}
	return b.String()
}

func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
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
		t.Errorf("%s differs (run with -update and review the diff):\n--- got ---\n%s--- want ---\n%s", path, got, want)
	}
}

func TestCompareReorderedColumns(t *testing.T) {
	opts := plan.Options{Columns: map[string]string{"Depends on": "Deps"}}
	r := Compare([]byte(readFixture(t, "reorder/original.md")), []byte(readFixture(t, "reorder/proposed.md")), opts)
	if !r.Tables {
		t.Fatal("want a table-by-table review")
	}
	checkGolden(t, filepath.Join("testdata", "reorder", "review.golden"), reviewText(r))

	// The line diff marks every row changed; the review only what changed.
	added, removed := Counts(LineReview([]byte(readFixture(t, "reorder/original.md")), []byte(readFixture(t, "reorder/proposed.md"))).Prose)
	if added < 20 || removed < 15 {
		t.Errorf("line diff +%d −%d; the fixture should change every row", added, removed)
	}
}

func TestCompare(t *testing.T) {
	const table = "| ID | Task | Status | Model |\n|---|---|---|---|\n"
	tests := []struct {
		name   string
		a, b   string
		tables bool
		want   []string // reviewText lines that must be there
		not    []string // and lines that must not
	}{
		{
			name:   "identical",
			a:      "## A — x\n\n" + table + "| A-1 | **One** | ready | sonnet |\n",
			b:      "## A — x\n\n" + table + "| A-1 | **One** | ready | sonnet |\n",
			tables: true,
			want:   []string{"0 table change(s) · +0 −0 lines outside the tables"},
			not:    []string{"phase"},
		},
		{
			name:   "original without task tables falls back",
			a:      readFixture(t, "renamed-columns.orig.md"),
			b:      readFixture(t, "renamed-columns.proposed.md"),
			tables: false,
			want:   []string{"+3 −3 lines", "- | Key | What | State | LLM |", "+ | ID | Task | Status | Model |"},
		},
		{
			name:   "proposal without task tables falls back",
			a:      "## A\n\n" + table + "| A-1 | x | ready | sonnet |\n",
			b:      "# nothing\n",
			tables: false,
		},
		{
			name:   "duplicate IDs fall back",
			a:      "## A\n\n" + table + "| A-1 | x | ready | sonnet |\n| A-1 | y | ready | sonnet |\n",
			b:      "## A\n\n" + table + "| A-1 | x | ready | sonnet |\n",
			tables: false,
		},
		{
			name:   "empty ID falls back",
			a:      "## A\n\n" + table + "| A-1 | x | ready | sonnet |\n",
			b:      "## A\n\n" + table + "| | x | ready | sonnet |\n",
			tables: false,
		},
		{
			name:   "rewritten phase heading is matched by its tasks",
			a:      "## Milestone 1\n\n" + table + "| A-1 | **One** | ready | sonnet |\n| A-2 | **Two** | ready | sonnet |\n",
			b:      "## M1 — Setup\n\n" + table + "| A-1 | **One** | ready | sonnet |\n| A-2 | **Two** | ready | sonnet |\n",
			tables: true,
			want:   []string{`~ phase M1 — Setup`, `  ~ heading: "Milestone 1" → "M1 — Setup"`},
			not:    []string{"moved", "A-1 phase", "+ phase", "- phase"},
		},
		{
			name:   "added and removed column; a missing cell is empty",
			a:      "## A\n\n| ID | Task | Status | Model | Spec |\n|---|---|---|---|---|\n| A-1 | x | ready | sonnet | 3 |\n| A-2 | y | ready | sonnet | |\n",
			b:      "## A\n\n| ID | Task | Status | Model | Owner |\n|---|---|---|---|---|\n| A-1 | x | ready | sonnet | |\n| A-2 | y | ready | sonnet | user |\n",
			tables: true,
			want:   []string{"  + column Owner", "  - column Spec", `  ~ A-1 Spec: "3" → ""`, `  ~ A-2 Owner: "" → "user"`},
			not:    []string{"reordered", "A-1 Owner", "A-2 Spec"},
		},
		{
			name: "v0.4 columns are compared by name like any other",
			a: "## A\n\n| ID | Task | Status | Model | Verify | Timeout | Context |\n|---|---|---|---|---|---|---|\n" +
				"| A-1 | x | ready | sonnet | fast | 45m | docs/a.md |\n| A-2 | y | ready | sonnet | go test ./... | | |\n",
			b: "## A\n\n| ID | Task | Status | Model | Verify notes | Timeout | Verify |\n|---|---|---|---|---|---|---|\n" +
				"| A-1 | x | ready | sonnet | | 1h | fast |\n| A-2 | y | ready | sonnet | go test ./... | | |\n",
			tables: true,
			want: []string{
				"  + column Verify notes", "  - column Context",
				`  ~ A-1 Timeout: "45m" → "1h"`, `  ~ A-1 Context: "docs/a.md" → ""`,
				`  ~ A-2 Verify notes: "" → "go test ./..."`, `  ~ A-2 Verify: "go test ./..." → ""`,
			},
			not: []string{"A-1 Verify:"},
		},
		{
			name:   "extra columns match case-insensitively",
			a:      "## A\n\n| ID | Task | Status | Model | spec |\n|---|---|---|---|---|\n| A-1 | x | ready | sonnet | 3 |\n",
			b:      "## A\n\n| ID | Task | Status | Model | Spec |\n|---|---|---|---|---|\n| A-1 | x | ready | sonnet | 3 |\n",
			tables: true,
			want:   []string{`  ~ column renamed: "spec" → "Spec"`},
			not:    []string{"A-1"},
		},
		{
			name:   "prose outside tables is a line diff; table rows are not",
			a:      "# P\n\nintro\n\n## A\n\n" + table + "| A-1 | x | todo | sonnet |\n\nouter\n",
			b:      "# P\n\nintro changed\n\n## A\n\n" + table + "| A-1 | x | ready | sonnet |\n\nouter\n",
			tables: true,
			want:   []string{"- intro", "+ intro changed", "  outer", `  ~ A-1 Status: "todo" → "ready"`},
			not:    []string{"| A-1"},
		},
		{
			name:   "byte order mark and CRLF are not changes",
			a:      "\xef\xbb\xbf# P\r\n\r\n## A\r\n\r\n" + strings.ReplaceAll(table, "\n", "\r\n") + "| A-1 | x | ready | sonnet |\r\n",
			b:      "# P\n\n## A\n\n" + table + "| A-1 | x | ready | sonnet |\n",
			tables: true,
			want:   []string{"0 table change(s) · +0 −0 lines outside the tables"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Compare([]byte(tt.a), []byte(tt.b), plan.Options{})
			if r.Tables != tt.tables {
				t.Fatalf("Tables = %v, want %v", r.Tables, tt.tables)
			}
			got := reviewText(r)
			lines := strings.Split(got, "\n")
			for _, w := range tt.want {
				if !contains(lines, w) {
					t.Errorf("missing line %q in:\n%s", w, got)
				}
			}
			for _, n := range tt.not {
				if strings.Contains(got, n) {
					t.Errorf("unexpected %q in:\n%s", n, got)
				}
			}
		})
	}
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if l == s {
			return true
		}
	}
	return false
}

func TestChangeString(t *testing.T) {
	tests := []struct {
		c    Change
		want string
	}{
		{Change{Op: Mod, What: "M2-04 Deps", Old: "M2-01", New: "M2-01, M2-03"}, `M2-04 Deps: "M2-01" → "M2-01, M2-03"`},
		{Change{Op: Add, What: "M2-05", New: "Parse widgets"}, "M2-05 Parse widgets"},
		{Change{Op: Del, What: "column", Old: "Spec"}, "column Spec"},
		{Change{Op: Mod, What: "M2-04 Model", Old: "", New: "?"}, `M2-04 Model: "" → "?"`},
	}
	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("%+v = %q, want %q", tt.c, got, tt.want)
		}
	}
}
