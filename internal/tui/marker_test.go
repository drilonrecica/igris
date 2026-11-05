package tui

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/plan"
)

// markerPlan has a parallel phase whose short titles are under minTitle:
// A-1 and A-2 run together, A-3 waits on A-1 (SPEC §3.8).
const markerPlan = "<!-- igris:format 3 -->\n\n## A — Side by side\n\n<!-- igris:parallel -->\n\n" +
	"| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n" +
	"| A-1 | **Go** | — | ready | sonnet | agent |\n" +
	"| A-2 | **Docs** | — | ready | opus | agent |\n" +
	"| A-3 | **Ship** | A-1 | blocked | sonnet | agent |\n"

// The task list is as wide as its rows with their parallel markers, and at
// that width it draws every marker (SPEC §15.4), however short the titles.
func TestParallelMarkerFitsTheNaturalWidth(t *testing.T) {
	p := plan.Parse("tasks.md", []byte(markerPlan), plan.Options{})
	r := rows{plan: p, phase: "A", th: &theme{}}
	lines, _, _ := r.lines(r.natural(), -1)
	want := map[string]string{"A-1": "with A-2", "A-2": "with A-1"}
	for _, l := range lines {
		for id, mark := range want {
			if strings.Contains(l, " "+id+" ") && !strings.HasSuffix(strings.TrimRight(l, " "), mark) {
				t.Errorf("row of %s lacks %q: %q", id, mark, l)
			}
		}
		if strings.Contains(l, " A-3 ") && strings.Contains(l, "with") {
			t.Errorf("A-3 runs with nothing: %q", l)
		}
	}
	cells, _, _, _ := r.compact(80, -1)
	if got := strings.Join(cells, "\n"); !strings.Contains(got, "A-1 Go with A-2") {
		t.Errorf("compact cells lack the marker:\n%s", got)
	}
	// Too narrow for the marker: the title keeps the room.
	narrow, _, _ := r.lines(3+3+2+4+1+6, -1)
	if got := strings.Join(narrow, "\n"); strings.Contains(got, "with") {
		t.Errorf("a list with no room still draws markers:\n%s", got)
	}
}
