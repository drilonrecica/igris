package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
)

// detailPlan is a synthetic plan with extra columns for the details page.
const detailPlan = `## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner | Mode | Spec |
|---|---|---|---|---|---|---|---|
| M0-01 | **Go module** — module path | — | done | sonnet | agent | — | 3.1 |
| M0-02 | **Config loader** — read igris.toml, validate every key and report each problem with the line it is on, so the owner can fix them all at once | M0-01, P0-05 | blocked | opus | agent | plan | 12, 12.1 |
| M0-03 | **Makefile** | M0-01 | in progress | sonnet | agent | — | — |

## P0 — Decisions

| ID | Task | Deps | Status | Model | Owner | Mode | Spec |
|---|---|---|---|---|---|---|---|
| P0-05 | **Create repo** | — | ready | — | user | — | — |
`

func detailHarness(t *testing.T, w, h int) *harness {
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(detailPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	return hs
}

func isRow(i int) func(target) bool {
	return func(t target) bool { return t.act == actTaskRow && t.option == i }
}

func TestClickSelectsThenOpensDetails(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {50, 20}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := detailHarness(t, size[0], size[1])
			hs.click(isRow(1))
			if hs.m.focus != focusTasks || hs.m.selID != "M0-02" || hs.m.page != nil {
				t.Fatalf("first click: focus %v, selected %q, page %v", hs.m.focus, hs.m.selID, hs.m.page != nil)
			}
			if v := hs.m.View(); !strings.Contains(v, "›⨯ M0-02") {
				t.Errorf("selection not shown:\n%s", v)
			}
			hs.click(isRow(0))
			if hs.m.selID != "M0-01" || hs.m.page != nil {
				t.Fatalf("clicking another row: selected %q, page %v", hs.m.selID, hs.m.page != nil)
			}
			hs.click(isRow(0))
			if hs.m.page == nil || !strings.Contains(hs.m.View(), "M0-01 — Go module") {
				t.Fatalf("second click opened no details:\n%s", hs.m.View())
			}
			checkFits(t, hs.m.View(), size[0], size[1])
			hs.key("esc")
			if hs.m.page != nil {
				t.Error("esc doesn't close the details")
			}
		})
	}
}

func TestClickingTheCurrentTaskFirstSelectsIt(t *testing.T) {
	hs := detailHarness(t, 120, 30)
	hs.click(isRow(2)) // M0-03, the current task, but the list has no focus yet
	if hs.m.page != nil {
		t.Fatal("the first click opened the details")
	}
	hs.click(isRow(2))
	if hs.m.page == nil {
		t.Fatal("the second click didn't")
	}
}

func TestEnterOpensDetails(t *testing.T) {
	hs := detailHarness(t, 120, 30)
	hs.keys("tab", "tab", "enter")
	if hs.m.page == nil || !strings.Contains(hs.m.View(), "M0-03 — Makefile") {
		t.Fatalf("enter on the task list didn't open the current task:\n%s", hs.m.View())
	}
	hs.keys("esc", "up", " ")
	if !strings.Contains(hs.m.View(), "M0-02 — Config loader") {
		t.Fatalf("space didn't open the selected task:\n%s", hs.m.View())
	}
}

func TestDetailsShowEverything(t *testing.T) {
	hs := detailHarness(t, 120, 30)
	hs.events(engine.Event{Kind: engine.TaskModeChanged, Task: "M0-01", Detail: "auto"})
	text := func(id string) string { return strings.Join(hs.m.detailPage(id).body(100), "\n") }
	got := text("M0-02")
	for _, want := range []string{
		"M0-02 — Config loader",
		"Config loader — read igris.toml, validate every key and report each problem with the line it is",
		"Status    ⨯ blocked",
		"Rank      opus",
		"Owner     agent",
		"Mode      plan (Mode column)",
		"Phase     M0 Repository foundation",
		"Deps      ✓ M0-01 done · Go module",
		"          · P0-05 ready · Create repo",
		"Waits on  P0-05",
		"Spec      12, 12.1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("details lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "**") {
		t.Errorf("markdown left in the text:\n%s", got)
	}
	if got := text("M0-01"); !strings.Contains(got, "Mode    auto (your override)") || !strings.Contains(got, "Deps    none") {
		t.Errorf("override or no deps not shown:\n%s", got)
	}
	if got := text("M0-03"); !strings.Contains(got, "Mode    plan (run mode)") || !strings.Contains(got, "Status  ● in progress") {
		t.Errorf("run mode or status not shown:\n%s", got)
	}
	if got := text("Z-9"); !strings.Contains(got, "no longer in the plan") {
		t.Errorf("missing task: %q", got)
	}
}

func TestDetailGolden(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {50, 20}} {
		name := fmt.Sprintf("detail_%dx%d", size[0], size[1])
		t.Run(name, func(t *testing.T) {
			hs := detailHarness(t, size[0], size[1])
			hs.click(isRow(1))
			hs.click(isRow(1))
			view := hs.m.View()
			checkFits(t, view, size[0], size[1])
			golden(t, name, view)
		})
	}
}

func TestRowsAreNotClickableUnderADialog(t *testing.T) {
	hs := detailHarness(t, 120, 30)
	hs.key("x")
	hs.m.View()
	for _, z := range hs.m.zones.list {
		if z.t.act == actTaskRow {
			t.Fatal("task rows clickable under a dialog")
		}
	}
}

func TestLogPage(t *testing.T) {
	hs := newHarness(t, 50, 12)
	hs.m.loc = time.UTC
	for i := range 30 {
		hs.events(engine.Event{Kind: engine.Warning, Detail: fmt.Sprintf("line %02d %s", i, strings.Repeat("long ", 12))})
	}
	hs.keys("tab", "enter") // the log, then the whole log
	if hs.m.page == nil {
		t.Fatal("enter on the log opened nothing")
	}
	v := hs.m.View()
	checkFits(t, v, 50, 12)
	if !strings.Contains(v, "long long") || strings.Contains(v, "line 00") || !strings.Contains(hs.m.View(), "of ") {
		t.Fatalf("log page doesn't start at the newest lines, wrapped:\n%s", v)
	}
	last := strings.Split(v, "\n")
	if !strings.Contains(strings.Join(last[len(last)-4:], "\n"), "long") {
		t.Errorf("the newest line isn't at the bottom:\n%s", v)
	}
	for range 40 {
		hs.m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	}
	if v := hs.m.View(); !strings.Contains(v, "line 00") {
		t.Fatalf("wheel didn't scroll to the oldest line:\n%s", v)
	}
	hs.key("end")
	hs.key("pgup")
	if v := hs.m.View(); strings.Contains(v, "line 00") || strings.Contains(v, "line 29") {
		t.Errorf("pgup from the end shows the wrong lines:\n%s", v)
	}
	hs.key("esc")
	if hs.m.page != nil || hs.m.focus != focusLog {
		t.Error("esc doesn't go back to the log")
	}
}
