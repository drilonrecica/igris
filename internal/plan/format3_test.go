package plan

import (
	"context"
	"slices"
	"strings"
	"testing"
)

const format3 = "<!-- igris:format 3 -->\n"

// groupTable is a phase with a Group column; its rows start at line 5
// after the heading, or at line 7 with a marker line and a blank before
// the table.
func groupTable(id, marker string, rows ...string) string {
	head := "## " + id + " — Phase " + id + "\n\n"
	if marker != "" {
		head += marker + "\n\n"
	}
	return head + "| ID | Task | Deps | Status | Model | Owner | Group |\n|---|---|---|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
}

// The parallel marker (SPEC §3.8): in a format-3 plan, in the section of
// a phase with a task table, once, with no value. It marks that phase.
func TestParallelMarker(t *testing.T) {
	const tbl = "| ID | Status | Model |\n|---|---|---|\n"
	tests := []struct {
		name, plan string
		parallel   []string // the parallel phases
		issues     []string // every issue wanted, in order
	}{
		{"under the heading", format3 + "## M0\n\n<!-- igris:parallel -->\n\n" + tbl + "| a | ready | sonnet |\n## M1\n\n" + tbl + "| b | ready | sonnet |\n",
			[]string{"M0"}, nil},
		{"after the table, no spaces", format3 + "## M0\n\n" + tbl + "| a | ready | sonnet |\n\n  <!--igris:parallel-->  \n", []string{"M0"}, nil},
		{"under a ### heading", format3 + "## M0\n\n### Notes\n\n<!-- igris:parallel -->\n\n" + tbl + "| a | ready | sonnet |\n", []string{"M0"}, nil},
		{"in a fence", format3 + "## M0\n\n```\n<!-- igris:parallel -->\n```\n\n" + tbl + "| a | ready | sonnet |\n", nil, nil},
		{"another word", format3 + "## M0\n\n<!-- igris:parallelism -->\n\n" + tbl + "| a | ready | sonnet |\n", nil, nil},
		{"format 1", "## M0\n\n<!-- igris:parallel -->\n\n" + tbl + "| a | ready | sonnet |\n", nil,
			[]string{"tasks.md:3: a parallel marker needs plan format 3; add <!-- igris:format 3 --> on a line of its own before the first ## heading"}},
		{"format 2", format2 + "## M0\n\n<!-- igris:parallel -->\n\n" + tbl + "| a | ready | sonnet |\n", nil,
			[]string{"tasks.md:4: a parallel marker needs plan format 3"}},
		{"a value", format3 + "## M0\n\n<!-- igris:parallel 4 -->\n\n" + tbl + "| a | ready | sonnet |\n", nil,
			[]string{"tasks.md:4: the parallel marker takes no value; write <!-- igris:parallel --> and set how many tasks run at once with [parallel] max in igris.toml"}},
		{"before the first heading", format3 + "<!-- igris:parallel -->\n## M0\n\n" + tbl + "| a | ready | sonnet |\n", nil,
			[]string{"tasks.md:2: the parallel marker must come after a phase's ## heading, in the section of the phase it marks"}},
		{"twice", format3 + "## M0\n\n<!-- igris:parallel -->\n<!-- igris:parallel -->\n\n" + tbl + "| a | ready | sonnet |\n", []string{"M0"},
			[]string{"tasks.md:5: a second parallel marker in this section (the first is on line 4)"}},
		{"no task table", format3 + "## Notes\n\n<!-- igris:parallel -->\n\n## M0\n\n" + tbl + "| a | ready | sonnet |\n", nil,
			[]string{"tasks.md:4: the parallel marker is in a section without a task table; put it under the ## heading of the phase it marks"}},
		{"no task table at the end", format3 + "## M0\n\n" + tbl + "| a | ready | sonnet |\n\n## Legend\n\n<!-- igris:parallel -->\n", nil,
			[]string{"tasks.md:10: the parallel marker is in a section without a task table"}},
		{"after an include line", format3 + "## M0\n\n" + tbl + "| a | ready | sonnet |\n<!-- igris:include x.md -->\n<!-- igris:parallel -->\n", nil,
			[]string{"tasks.md:7: includes are not read here", "tasks.md:8: the parallel marker after the include line at line 7 marks no phase"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Parse("tasks.md", []byte(tt.plan), Options{})
			var parallel []string
			for _, ph := range p.Phases {
				if ph.Parallel {
					parallel = append(parallel, ph.ID)
				}
			}
			if !slices.Equal(parallel, tt.parallel) {
				t.Errorf("parallel phases %v, want %v", parallel, tt.parallel)
			}
			issues := p.Validate(testRules)
			if len(issues) != len(tt.issues) {
				t.Fatalf("issues %v, want %d: %q", issues, len(tt.issues), tt.issues)
			}
			for i, want := range tt.issues {
				if !strings.HasPrefix(issues[i].Error(), want) {
					t.Errorf("issue %d = %q, want %q", i, issues[i].Error(), want)
				}
			}
		})
	}
}

// Group is a canonical column in format 3 only; before it, it stays the
// extra column it always was, so a format-1 or -2 plan keeps its meaning.
func TestGroupColumn(t *testing.T) {
	rows := []string{"| a | **A** | — | ready | sonnet | agent | ` db ` |", "| b | **B** | — | ready | sonnet | agent | — |"}
	p := Parse("tasks.md", []byte(format3+groupTable("M0", "<!-- igris:parallel -->", rows...)), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatal(issues)
	}
	if a, b := p.Task("a"), p.Task("b"); a.Group != "db" || b.Group != "" || len(a.Extra) != 0 {
		t.Errorf("groups %q, %q, extra %v", a.Group, b.Group, a.Extra)
	}
	for _, marker := range []string{"", format2} {
		p := Parse("tasks.md", []byte(marker+groupTable("M0", "", rows...)), Options{})
		if a := p.Task("a"); a.Group != "" || a.Extra["Group"] != "` db `" {
			t.Errorf("%q: group %q, extra %v", marker, a.Group, a.Extra)
		}
		if hints := p.Hints(); len(hints) > 0 {
			t.Errorf("%q: hints %v", marker, hints)
		}
	}
	// An alias reaches the column too.
	alias := strings.Replace(groupTable("M0", "", rows...), "| Group |", "| Lane |", 1)
	if p := Parse("tasks.md", []byte(format3+alias), Options{Columns: map[string]string{"lane": "Group"}}); p.Task("a").Group != "db" {
		t.Errorf("aliased group %q", p.Task("a").Group)
	}
}

func TestGroupValidation(t *testing.T) {
	p := Parse("tasks.md", []byte(format3+groupTable("M0", "<!-- igris:parallel -->",
		"| a | **A** | — | ready | sonnet | agent | db, cache |",
		"| b | **B** | — | done | sonnet | agent | db cache |",
		"| c | **C** | — | ready | — | user | `x.y_z-1` |",
		"| d | **D** | — | ready | sonnet | agent | -x |",
	)), Options{})
	var got []string
	for _, is := range p.Validate(testRules) {
		got = append(got, is.Error())
	}
	want := []string{
		`tasks.md:8: a: Group "db, cache" is not one name; a task is in at most one group, named with letters, digits, '.', '_' and '-'`,
		`tasks.md:11: d: Group "-x" is not one name; a task is in at most one group, named with letters, digits, '.', '_' and '-'`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("issues\n%q\nwant\n%q", got, want)
	}
}

// A Group in a phase without the marker changes nothing: one warning per
// phase, none for finished tasks.
func TestGroupHint(t *testing.T) {
	in := format3 + groupTable("M0", "", "| a | **A** | — | ready | sonnet | agent | db |", "| b | **B** | — | ready | sonnet | agent | db |") +
		"\n" + groupTable("M1", "", "| c | **C** | — | done | sonnet | agent | db |") +
		"\n" + groupTable("M2", "<!-- igris:parallel -->", "| d | **D** | — | ready | sonnet | agent | db |")
	p := Parse("tasks.md", []byte(in), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatal(issues)
	}
	var got []string
	for _, h := range p.Hints() {
		got = append(got, h.Error())
	}
	want := []string{"tasks.md:4: phase M0 is not parallel, so its Group cells change nothing; add <!-- igris:parallel --> under its heading, or clear them"}
	if !slices.Equal(got, want) {
		t.Errorf("hints %q, want %q", got, want)
	}
}

// In a parallel phase a task naming an earlier unfinished one that neither
// depends on the other gets a warning (SPEC §3.8); a sequential phase,
// finished tasks, real dependencies (also through another task) and IDs
// inside longer words get none.
func TestImpliedDeps(t *testing.T) {
	in := format3 + groupTable("M1", "<!-- igris:parallel -->",
		"| M1-01 | **Schema** | — | ready | sonnet | agent | — |",
		"| M1-02 | **Store** — on the schema of M1-01. | — | ready | sonnet | agent | — |",
		"| M1-03 | **API** — uses M1-02 and M1-01 | M1-04 | blocked | sonnet | agent | — |",
		"| M1-04 | **Client** — after M1-02 | M1-02 | blocked | sonnet | agent | — |",
		"| M1-05 | **Old** — replaces M1-021, x.M1-01 and M1-01.5 | — | ready | sonnet | agent | — |",
		"| M1-06 | **Done** — after M1-01 | — | done | sonnet | agent | — |",
		"| M1-07 | **Later** — before M1-08 | — | ready | sonnet | agent | — |",
		"| M1-08 | **Last** — after M1-06 | — | ready | sonnet | agent | — |",
	) + "\n" + groupTable("M2", "", "| M2-01 | **A** | — | ready | sonnet | agent | — |", "| M2-02 | **B** — after M2-01 | — | ready | sonnet | agent | — |")
	p := Parse("tasks.md", []byte(in), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatal(issues)
	}
	var got []string
	for _, h := range p.Hints() {
		got = append(got, h.Error())
	}
	want := []string{
		"tasks.md:9: M1-02 names M1-01, which comes before it in parallel phase M1, but neither depends on the other, so they may run at the same time; add M1-01 to its Deps if it must finish first",
		"tasks.md:10: M1-03 names M1-01, which comes before it in parallel phase M1, but neither depends on the other, so they may run at the same time; add M1-01 to its Deps if it must finish first",
	}
	if !slices.Equal(got, want) {
		t.Errorf("hints\n%q\nwant\n%q", got, want)
	}
}

func TestNamesID(t *testing.T) {
	tests := []struct {
		text, id string
		want     bool
	}{
		{"M1-02", "M1-02", true},
		{"after M1-02.", "M1-02", true},
		{"(M1-02)", "M1-02", true},
		{"M1-02, M1-03", "M1-03", true},
		{"M1-02…M1-04", "M1-02", true},
		{"M1-02..M1-04", "M1-02", true},
		{"`M1-02`", "M1-02", true},
		{"M1-021", "M1-02", false},
		{"M1-02.5", "M1-02", false},
		{"xM1-02", "M1-02", false},
		{"x.M1-02", "M1-02", false},
		{"m1-02", "M1-02", false},
		{"M1-021 and M1-02", "M1-02", true},
		{"", "M1-02", false},
	}
	for _, tt := range tests {
		if got := namesID(tt.text, tt.id); got != tt.want {
			t.Errorf("namesID(%q, %q) = %v, want %v", tt.text, tt.id, got, tt.want)
		}
	}
}

// A parallel marker in an included file marks its phase when the main
// plan declares format 3, and an included file may declare format 3.
func TestParallelInIncludedFile(t *testing.T) {
	writeFiles(t, map[string]string{
		"tasks.md":   format3 + phaseTable("A", "| A-1 | — | done | sonnet |") + "\n<!-- igris:include b.md -->\n",
		"b.md":       format3 + "## B — Phase B\n\n<!-- igris:parallel -->\n\n| ID | Deps | Status | Model | Group |\n|---|---|---|---|---|\n| B-1 | A-1 | ready | sonnet | db |\n",
		"format2.md": format2 + phaseTable("A", "| A-1 | — | done | sonnet |") + "\n<!-- igris:include b.md -->\n",
	})
	p := loadSplit(t)
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatal(issues)
	}
	if !p.Phase("B").Parallel || p.Phase("A").Parallel || p.Task("B-1").Group != "db" {
		t.Errorf("B parallel %v, A parallel %v, group %q", p.Phase("B").Parallel, p.Phase("A").Parallel, p.Task("B-1").Group)
	}
	p, err := Load("format2.md", Options{})
	if err != nil {
		t.Fatal(err)
	}
	issues := p.Validate(testRules)
	if len(issues) != 1 || issues[0].Error() != "b.md:4: a parallel marker needs plan format 3; add <!-- igris:format 3 --> on a line of its own before the first ## heading" {
		t.Errorf("format-2 main plan: %v", issues)
	}
	if p.Task("B-1").Extra["Group"] != "db" {
		t.Errorf("format-2 main plan: Group is %v", p.Task("B-1").Extra)
	}
}

// The writer changes only Status cells in a format-3 plan too: the markers
// and the Group cells stay as they are.
func TestWriterFormat3EveryCellIsolated(t *testing.T) {
	in := "# Plan\r\n" + strings.ReplaceAll(format3+groupTable("M0", "  <!--igris:parallel-->  ",
		"| a | **A** | — | ready | sonnet | agent | `db` |",
		"| b | **B** | a | blocked | sonnet | agent | db |",
		"| c | **C** | — | `in progress` | opus | agent | — |",
	)+"\n"+groupTable("M1", "", "| d | **D** | a…c | blocked | — | user | x |"), "\n", "\r\n")
	p := Parse("tasks.md", []byte(in), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 || !p.Phase("M0").Parallel {
		t.Fatalf("issues %v, parallel %v", issues, p.Phase("M0").Parallel)
	}
	for _, task := range p.Tasks {
		for _, s := range []Status{Ready, Blocked, InProgress, Done, Skipped} {
			path := writePlan(t, in)
			if _, err := NewWriter(path, Options{}, testRules).Update(context.Background(), set(task.ID, s)); err != nil {
				t.Fatalf("%s → %v: %v", task.ID, s, err)
			}
			out := readPlan(t, path)
			rest := in[task.statusEnd:]
			if out[:task.statusStart] != in[:task.statusStart] || !strings.HasSuffix(out, rest) {
				t.Fatalf("%s → %v: bytes outside the cell changed:\n%q", task.ID, s, out)
			}
			if cell := out[task.statusStart : len(out)-len(rest)]; cell != statusCell(task.StatusText, s) {
				t.Fatalf("%s → %v: cell = %q", task.ID, s, cell)
			}
		}
	}
}

// A fragment for a format-3 plan may add a parallel phase, but may not
// mark a phase of the plan that isn't parallel (SPEC §9.1).
func TestExtendParallelMarker(t *testing.T) {
	base := format3 + groupTable("M0", "", "| M0-01 | **A** | — | done | sonnet | agent | — |")
	p := Parse("tasks.md", []byte(base), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatal(issues)
	}
	frag := groupTable("M1", "<!-- igris:parallel -->", "| M1-01 | **B** | M0-01 | ready | sonnet | agent | db |")
	x, issues := Extend(p, Options{}, "fragment.md", []byte(frag))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	out := Parse("tasks.md", x.Files()[0].New, Options{})
	if issues := out.Validate(testRules); len(issues) > 0 || !out.Phase("M1").Parallel || out.Task("M1-01").Group != "db" {
		t.Errorf("extended: issues %v, parallel %v\n%s", issues, out.Phase("M1").Parallel, x.Files()[0].New)
	}
	frag = groupTable("M0", "<!-- igris:parallel -->", "| M0-02 | **C** | — | ready | sonnet | agent | — |")
	if _, issues := Extend(p, Options{}, "fragment.md", []byte(frag)); len(issues) != 1 || !strings.Contains(issues[0].Msg, "phase M0 is not parallel in the plan") {
		t.Errorf("marking M0: %v", issues)
	}
}
