package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// modePlan is demoPlan with a Mode column: M0-04 plans and M0-13 skips
// permissions; M0-03 is overridden with M in the test.
const modePlan = `## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M0-01 | **Go module** — module path | — | done | sonnet | agent | |
| M0-02 | **Entrypoint** — subcommand dispatch | M0-01 | done | sonnet | agent | |
| M0-03 | **Makefile** — fmt, lint, test | M0-01 | in progress | sonnet | agent | |
| M0-04 | **Config loader** with a title long enough to be cut in the narrow task pane | M0-01 | ready | opus | agent | plan |
| M0-05 | **Buy the domain** | — | ready | — | user | |
| M0-13 | **Static assets** | P0-05 | blocked | sonnet | agent | yolo |
| M0-14 | **Old idea** | — | skipped | haiku | agent | |

## P0 — Decisions

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| P0-05 | **Create repo** | — | ready | — | user | |
`

// modeRun draws the run view of plan text with the run in mode, the owner
// having set M0-03 to plan when override is set.
// modeParallelPlan is modePlan with M0 a parallel phase: M0-05 and M0-13
// carry the marker, M0-13 a mode as well.
var modeParallelPlan = "<!-- igris:format 3 -->\n\n" + strings.NewReplacer(
	"## M0 — Repository foundation\n", "## M0 — Repository foundation\n\n<!-- igris:parallel -->\n",
	"pane | M0-01 | ready |", "pane | M0-03 | ready |",
).Replace(modePlan)

func modeRun(t *testing.T, w, h int, text, mode string, override bool) *harness {
	t.Helper()
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(text)
	hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.ModeChanged, Detail: mode})
	if override {
		hs.events(engine.Event{Kind: engine.TaskModeChanged, Task: "M0-03", Detail: "plan:auto"})
	}
	hs.now = t0.Add(4*time.Minute + 12*time.Second)
	return hs
}

func TestTaskRowModeGoldens(t *testing.T) {
	cases := []struct {
		name, plan, mode string
		override         bool
	}{
		{"auto", modePlan, engine.ModeAuto, true},
		{"yolo", modePlan, engine.ModeYolo, false},
		{"parallel", modeParallelPlan, engine.ModeAuto, false},
	}
	for _, c := range cases {
		for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 20}} {
			name := "taskmode_" + c.name + "_" + strconv.Itoa(size[0]) + "x" + strconv.Itoa(size[1])
			t.Run(name, func(t *testing.T) {
				hs := modeRun(t, size[0], size[1], c.plan, c.mode, c.override)
				view := hs.m.View()
				checkFits(t, view, size[0], size[1])
				golden(t, name, view)
			})
		}
	}
}

func TestTaskRowModeWords(t *testing.T) {
	hs := modeRun(t, 120, 40, modePlan, engine.ModeAuto, true)
	r := hs.m.rows()
	lines, _, _ := r.lines(60, -1)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"plan:auto", "opus   plan", "yolo"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rows lack %q:\n%s", want, joined)
		}
	}
	for _, l := range lines {
		if (strings.Contains(l, "M0-01") || strings.Contains(l, "M0-14")) && (strings.Contains(l, "plan") || strings.Contains(l, "yolo")) {
			t.Errorf("a done or skipped row names a mode: %q", l)
		}
	}
	// In a yolo run every unfinished row says yolo, and nothing else.
	hs = modeRun(t, 120, 40, demoPlan, engine.ModeYolo, false)
	r = hs.m.rows()
	lines, _, _ = r.lines(60, -1)
	for _, l := range lines {
		done := strings.Contains(l, "M0-01") || strings.Contains(l, "M0-02") || strings.Contains(l, "M0-14") || strings.Contains(l, "waits on")
		if !done && !strings.HasSuffix(strings.TrimRight(l, " "), "yolo") {
			t.Errorf("an unfinished row in a yolo run lacks yolo: %q", l)
		}
	}
	// Narrow: a mode that differs goes, a skip-permissions one never.
	lines, _, _ = hs.m.rows().lines(34, -1)
	if !strings.Contains(strings.Join(lines, "\n"), "yolo") {
		t.Errorf("yolo dropped at 34 cells:\n%s", strings.Join(lines, "\n"))
	}
	hs = modeRun(t, 120, 40, modePlan, engine.ModeAuto, true)
	lines, _, _ = hs.m.rows().lines(34, -1)
	if j := strings.Join(lines, "\n"); strings.Contains(j, "plan:auto") || !strings.Contains(j, "yolo") {
		t.Errorf("at 34 cells a differing mode stays or yolo goes:\n%s", j)
	}
	// compact: only a skip-permissions mode shows.
	cl, _, _, _ := hs.m.rows().compact(60, -1)
	if j := strings.Join(cl, "\n"); strings.Contains(j, "plan") && strings.Contains(j, "plan:auto") || !strings.Contains(j, "yolo") {
		t.Errorf("compact cells:\n%s", j)
	}
}

func TestTaskRowModeNoColor(t *testing.T) {
	hs := modeRun(t, 120, 40, modePlan, engine.ModeYolo, true)
	hs.m.th = noColorTheme(t)
	view := hs.m.View()
	if strings.Contains(view, "38;") || strings.Contains(view, "48;") {
		t.Errorf("NO_COLOR view has colors")
	}
	golden(t, "taskmode_nocolor_120x40", view)
}
