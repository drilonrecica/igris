package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run a target for longer with `make fuzz` (FUZZTIME=10m) or
// `go test -run '^$' -fuzz '^FuzzParse$' ./internal/plan`. A failing input
// lands in testdata/fuzz/<Target>/; commit it with the fix.

// fuzzPlanSeeds are the shapes the parser and writer must survive: the
// fixtures plus inline edge cases (CRLF, a UTF-8 BOM, no trailing newline,
// escaped pipes, backticks, fenced fake tables, non-ASCII, a very long cell).
func fuzzPlanSeeds(t testing.TB) [][]byte {
	t.Helper()
	seeds := [][]byte{[]byte(specExample)}
	paths, err := filepath.Glob("testdata/invalid/*.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(paths, "testdata/large.md") {
		b, err := os.ReadFile(p) //nolint:gosec // test fixture
		if err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, b)
	}
	const head = "## M0 — One\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n"
	for _, s := range []string{
		strings.ReplaceAll(head+"| a | **A** | — | ready | sonnet | agent |\n| b | B | a | blocked | opus | agent |\n", "\n", "\r\n"),
		head + "| a | **A** | — | ready | sonnet | agent |",
		head + "| a | pipe \\| inside, `code \\| too` | — | `ready` | sonnet | agent |\n",
		head + "| a | x | — | `done` (merged) | — | agent |\n| b | y | a | in progress | haiku | agent + user |\n",
		"```\n## M9 — Fake\n| ID | Status | Model |\n|---|---|---|\n| z | ready | opus |\n```\n" + head + "| a | A | — | ready | sonnet | agent |\n",
		"## Fase 1 — Größe ✓\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n| ü-1 | 日本語 **Tâche** | — | ready | sonnet | agent |\n",
		head + "| a | " + strings.Repeat("long ", 2000) + " | — | ready | sonnet | agent |\n",
		head + "| a | A | — | ready | sonnet | agent |\n| b | B | a…a | blocked | sonnet | user |\n| c | | |\n",
		"## Phase 2\n\n| ID | Status | Model | Depends on |\n|---|---|---|---|\n| 1 | ready | sonnet | — |\r",
		"\ufeff" + strings.ReplaceAll(head+"| a | A | — | ready | sonnet | agent |\n", "\n", "\r\n"),
	} {
		seeds = append(seeds, []byte(s))
	}
	return seeds
}

func FuzzSplitRow(f *testing.F) {
	for _, s := range []string{"", "|", "||", "| a | b |", `a \| b | c`, `\\|x`, "|  |  |", "| x\\", "\t|\t a\t|"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		prev := 0
		for i, c := range splitRow(s) {
			if c.start < prev || c.end < c.start || c.end > len(s) {
				t.Fatalf("cell %d span [%d,%d) out of order or bounds (prev end %d, len %d)", i, c.start, c.end, prev, len(s))
			}
			prev = c.end
		}
	})
}

// planSummary renders everything Parse and Validate decide, so two parses
// of the same input can be compared.
func planSummary(p *Plan, issues []Issue) string {
	var b strings.Builder
	for _, ph := range p.Phases {
		fmt.Fprintf(&b, "phase %q %q %q %d\n", ph.ID, ph.Title, ph.Columns, len(ph.Tasks))
	}
	for _, t := range p.Tasks {
		fmt.Fprintf(&b, "task %q %q %v %q %q %q %q %q %q %q %q %q %s %q %d [%d,%d)\n",
			t.ID, t.Title, t.Status, t.StatusText, t.Suffix, t.OwnerText, t.Rank, t.Mode, t.DepsText, t.Deps,
			t.Verify, t.TimeoutText, t.Timeout, t.Context, t.Line, t.statusStart, t.statusEnd)
	}
	for _, is := range issues {
		fmt.Fprintf(&b, "issue %s\n", is)
	}
	return b.String()
}

// FuzzParse: for arbitrary bytes and one arbitrary column alias, parsing,
// validation and scheduling never panic, the same input always gives the
// same result, and every Status span lies inside the file.
func FuzzParse(f *testing.F) {
	for _, s := range fuzzPlanSeeds(f) {
		f.Add(s, "Depends on", "Deps")
	}
	f.Add([]byte("## A\n\n| Key | State | Rank |\n|---|---|---|\n| a | ready | opus |\n"), "Key", "ID")
	f.Fuzz(func(t *testing.T, data []byte, aliasKey, aliasVal string) {
		opts := Options{Columns: map[string]string{aliasKey: aliasVal}}
		p := Parse("tasks.md", data, opts)
		issues := p.Validate(testRules)
		_ = p.Check(testRules)
		_ = p.Hints()
		for _, t := range p.Tasks {
			if t.statusStart < 0 || t.statusEnd < t.statusStart || t.statusEnd > len(data) {
				panic(fmt.Sprintf("task %q: status span [%d,%d) outside the file (%d bytes)", t.ID, t.statusStart, t.statusEnd, len(data)))
			}
		}
		if len(issues) == 0 {
			// Scheduling is defined only for valid plans.
			_ = p.Readiness()
			for _, ph := range p.Phases {
				_, _ = p.Select(ph.ID)
				_, _ = p.PhasesThrough(ph.ID, p.Phases[len(p.Phases)-1].ID)
			}
			for _, t := range p.Tasks {
				_ = p.WaitingOn(t)
			}
		}

		again := Parse("tasks.md", data, opts)
		if a, b := planSummary(p, issues), planSummary(again, again.Validate(testRules)); a != b {
			t.Fatalf("parsing twice differs:\n--- first ---\n%s--- second ---\n%s", a, b)
		}
	})
}
