package plan

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestLintFixtures loads one fixture per lint hint from testdata/lint and
// checks it reports exactly that hint (SPEC §14 `check --strict`).
func TestLintFixtures(t *testing.T) {
	tests := []struct {
		file string
		want []string // "line name: message"
	}{
		{"clean.md", nil},
		{"title.md", []string{`7 title: M1-03: the Task cell has no **bold** title, so igris shows the whole cell as its title; start the cell with **Title**`}},
		{"long.md", []string{`6 long: M1-03: the Task cell is 406 characters (over 400); keep the row short and point to a spec for the details`}},
		{"owner-step.md", []string{`7 owner-step: M1-03: agent + user task, but its row never says what the owner does (no "owner", "approve" or "decide"); say what needs the owner's sign-off`}},
		{"gate.md", []string{`10 gate: M1-G: the gate does not depend on M1-04, M1-05 of phase M1; add them to Deps (e.g. M1-01…M1-05)`}},
		{"gate-after.md", []string{`7 gate: M1-G: the gate does not depend on M1-02 of phase M1; add them to Deps (e.g. M1-01…M1-02)`}},
		{"yolo.md", []string{`6 yolo: M1-03: Mode yolo runs this task with --dangerously-skip-permissions; prefer auto unless it must run unattended`}},
		{"fable.md", []string{`6 fable: M1-03: the only heavy-rank task of phase M1 is fable; check that this task needs fable`}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			p, err := Load(filepath.Join("testdata", "lint", tt.file), Options{})
			if err != nil {
				t.Fatal(err)
			}
			if issues := p.Validate(testRules); len(issues) > 0 {
				t.Fatalf("fixture is invalid: %v", issueMsgs(issues))
			}
			if drift := p.Readiness(); len(drift) > 0 {
				t.Fatalf("fixture drifted: %v", drift)
			}
			var got []string
			for _, l := range p.Lint() {
				got = append(got, fmt.Sprintf("%d %s: %s", l.Line, l.Name, l.Msg))
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("lint =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// Done and skipped tasks are never linted; a gate counts deps through
// ranges and other tasks.
func TestLintSkipsFinishedTasks(t *testing.T) {
	p := Parse("tasks.md", []byte("## M1\n\n| ID | Task | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|---|\n"+
		"| M1-01 | no title | — | done | fable | agent + user | yolo |\n"+
		"| M1-02 | **Two** | M1-01 | skipped | sonnet | agent | yolo |\n"+
		"| M1-03 | **Three** | M1-02 | ready | sonnet | agent | — |\n"+
		"| m1-g | **Gate** | M1-03 | blocked | sonnet | agent | — |\n"), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatalf("invalid: %v", issueMsgs(issues))
	}
	if got := p.Lint(); len(got) != 0 {
		t.Errorf("lint = %+v, want none", got)
	}
}

// The title hint says what igris shows instead, and needs a Task column.
func TestLintTitle(t *testing.T) {
	long := strings.Repeat("word ", 20)
	tests := []struct {
		name, plan string
		want       []string
	}{
		{"long cell", "| ID | Task | Status | Model |\n|---|---|---|---|\n| M1-01 | " + long + " | ready | sonnet |\n",
			[]string{`M1-01: the Task cell has no **bold** title, so igris shows its first 80 characters; start the cell with **Title**`}},
		{"empty cell", "| ID | Task | Status | Model |\n|---|---|---|---|\n| M1-01 | | ready | sonnet |\n",
			[]string{`M1-01: the Task cell is empty, so the task has no title; start the cell with **Title**`}},
		{"no Task column", "| ID | Status | Model |\n|---|---|---|\n| M1-01 | ready | sonnet |\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Parse("tasks.md", []byte("## M1\n\n"+tt.plan), Options{})
			if issues := p.Validate(testRules); len(issues) > 0 {
				t.Fatalf("invalid: %v", issueMsgs(issues))
			}
			var got []string
			for _, l := range p.Lint() {
				if l.Name == LintTitle {
					got = append(got, l.Msg)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("title hints = %q, want %q", got, tt.want)
			}
		})
	}
}
