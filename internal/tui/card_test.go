package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
)

// The CURRENT card says what the task is about, offers its details and
// points at the session when it needs the owner (owner request at the v0.2
// gate: "in the main window I don't see what the task is about").

// cardPlan has a short task, a long one and a user task.
var cardPlan = `## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Makefile** | — | in progress | sonnet | agent |
| M0-02 | **Config loader** — read igris.toml and report every problem with its line | — | ready | opus | agent |
| M0-03 | **Long task** — ` + strings.Repeat("this sentence describes the work in some detail. ", 40) + `| — | ready | sonnet | agent |
| M0-04 | **Pick a license** — your call, not the agent's | — | ready | — | user |
`

func cardHarness(t *testing.T, w, h int, id string) *harness {
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(cardPlan)
	hs.events(engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: id, Title: "title of " + id, Rank: "opus", Model: "opus", Mode: "plan"},
		engine.Event{Kind: engine.SessionOpened, Task: id, Mode: "plan", Session: &backend.SessionRef{Backend: "herdr", PaneID: "p1"}})
	return hs
}

var cardSizes = [][2]int{{120, 40}, {80, 24}, {50, 20}}

func TestCardShowsTheTaskText(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "read igris.toml") {
				t.Errorf("card lacks the task text:\n%s", v)
			}
			if strings.Contains(v, "Config loader — read") || strings.Contains(v, "**") {
				t.Errorf("card repeats the title or shows markdown:\n%s", v)
			}
			if !strings.Contains(v, "[t] Details") || !strings.Contains(v, "[o] Open session") {
				t.Errorf("card lacks its buttons:\n%s", v)
			}
		})
	}
}

func TestCardTrimsLongTextAndKeepsItsButtons(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-03")
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			for _, want := range []string{"this sentence describes", "… t: details", "[t] Details", "[o] Open session"} {
				if !strings.Contains(v, want) {
					t.Errorf("view lacks %q:\n%s", want, v)
				}
			}
		})
	}
}

func TestCardOfATitleOnlyTask(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-01")
	v := hs.m.View()
	if strings.Contains(v, "… t: details") || !strings.Contains(v, "[t] Details") {
		t.Errorf("a title-only task is trimmed or has no details button:\n%s", v)
	}
}

func TestDetailsKey(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-02")
	hs.key("t")
	if hs.m.page == nil || !strings.Contains(hs.m.View(), "M0-02 — Config loader") {
		t.Fatalf("t didn't open the current task's details:\n%s", hs.m.View())
	}
	hs.key("esc")
	hs.keys("tab", "tab", "up") // the task list, then the task above
	hs.key("t")
	if !strings.Contains(hs.m.View(), "M0-01 — Makefile") {
		t.Fatalf("t didn't open the selected task's details:\n%s", hs.m.View())
	}
}

func TestCardDetailsButtonAndTitle(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.m.View()
			n := 0
			for _, z := range hs.m.zones.list {
				if z.t.act == actDetails {
					n++
				}
			}
			if n < 2 {
				t.Fatalf("want the title and the [t] Details button clickable, got %d zones:\n%s", n, hs.m.View())
			}
			hs.click(isAct(actDetails))
			if hs.m.page == nil || !strings.Contains(hs.m.View(), "M0-02 — Config loader") {
				t.Fatalf("clicking the card opened no details:\n%s", hs.m.View())
			}
		})
	}
}

func TestNeedsYouPointsAtTheSession(t *testing.T) {
	// 100 columns is the narrowest wide layout: the hint gets its own line.
	for _, size := range append([][2]int{{100, 30}}, cardSizes...) {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.events(engine.Event{Kind: engine.NeedsYou, Task: "M0-02", Detail: "the agent is waiting"})
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "o opens the session") || !strings.Contains(v, "[o] Open session") {
				t.Errorf("NEEDS YOU doesn't point at o:\n%s", v)
			}
			hs.key("o")
			if len(hs.focus) != 1 {
				t.Errorf("o focused %d sessions", len(hs.focus))
			}
		})
	}
}

func TestUserTaskCardHasNoSession(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.m.loc = time.UTC
	hs.withPlan(cardPlan)
	hs.events(userStarted("M0-04"))
	v := hs.m.View()
	if !strings.Contains(v, "your call, not the agent's") || !strings.Contains(v, "[t] Details") {
		t.Errorf("user task card lacks its text or details:\n%s", v)
	}
	if strings.Contains(v, "Open session") || strings.Contains(v, "o opens the session") {
		t.Errorf("user task card offers a session:\n%s", v)
	}
}

// An overdue task says so on its card until the attempt ends (SPEC §6.3).
func TestCardShowsOverdueUntilTheAttemptEnds(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.events(engine.Event{Kind: engine.TaskOverdue, Task: "M0-02", Detail: "running longer than its Timeout 45m"},
				engine.Event{Kind: engine.NeedsYou, Task: "M0-02", Detail: "the task is running longer than its Timeout 45m; igris leaves its session running"})
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "OVERDUE: running longer") {
				t.Errorf("card lacks the overdue line:\n%s", v)
			}
			hs.events(engine.Event{Kind: engine.NeedsYouClear, Task: "M0-02"})
			if v := hs.m.View(); !strings.Contains(v, "OVERDUE") {
				t.Errorf("overdue cleared while the attempt runs:\n%s", v)
			}
			hs.events(engine.Event{Kind: engine.Retrying, Task: "M0-02", Detail: "fresh"})
			if v := hs.m.View(); strings.Contains(v, "OVERDUE") {
				t.Errorf("overdue kept after the attempt ended:\n%s", v)
			}
		})
	}
}
