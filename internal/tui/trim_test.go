package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

// The run view spends its rows where they are read (SPEC §15.1, §15.2):
// TASKS only as tall as its rows, a quiet log lending its rows to the live
// tail, the cards by priority, one button row. One set of rules at every
// size, no breakpoint; trimSizes are the sizes the goldens pin, among them
// 90×28, a guess at a laptop's terminal.
var trimSizes = [][2]int{{50, 20}, {80, 24}, {90, 28}, {100, 24}, {100, 30}, {120, 40}}

var multiTails = map[string][]string{
	"M1-03": {"read the input", "parsing tasks.md: 41 rows, 6 phases", "go test ./internal/plan/... ok"},
	"M1-04": {"Plan ready: keep byte offsets per cell", "Approve to continue."},
	"M1-05": {"say how it works", "editing docs/plan-format.md (+18 −4)"},
}

var trimStates = []struct {
	name  string
	setup func(t *testing.T, w, h int) *harness
}{
	{"single", func(t *testing.T, w, h int) *harness {
		hs := cardHarness(t, w, h, "M0-02")
		hs.withTail(sampleTail...)
		return hs
	}},
	{"needs_you", func(t *testing.T, w, h int) *harness {
		hs := cardHarness(t, w, h, "M0-02")
		hs.withTail(sampleTail...)
		hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M0", Task: "M0-02", Reason: state.ReasonIdle, Detail: "the session is idle without a signal"})
		return hs
	}},
	{"blocked", func(t *testing.T, w, h int) *harness {
		hs := cardHarness(t, w, h, "M0-02")
		hs.withTail(sampleTail...)
		hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M0", Task: "M0-02", Reason: state.ReasonBlocked, Cause: "permission", Tool: "Bash", Detail: "the session waits for a permission (Bash)"})
		return hs
	}},
	{"multi", func(t *testing.T, w, h int) *harness {
		hs := multiHarness(t, w, h)
		hs.m.opts.Tail = (&taskTails{lines: multiTails}).tail
		hs.refreshTail()
		hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-04", Detail: "the agent is idle"})
		return hs
	}},
}

func TestTrimGoldens(t *testing.T) {
	for _, st := range trimStates {
		for _, sz := range trimSizes {
			name := fmt.Sprintf("trim_%s_%dx%d", st.name, sz[0], sz[1])
			t.Run(name, func(t *testing.T) {
				hs := st.setup(t, sz[0], sz[1])
				v := hs.m.View()
				checkFits(t, v, sz[0], sz[1])
				golden(t, name, v)
			})
		}
	}
}

// At 100×24 with three active cards the card that needs you keeps its
// full body; the others become one line each, still selectable.
func TestCardThatNeedsYouKeepsItsBody(t *testing.T) {
	hs := multiHarness(t, 100, 24)
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-04", Detail: "the agent is idle"})
	hs.m.View()
	c := hs.m.card("M1-04")
	full := hs.m.cardBody(c, cardInner(columnsRight(hs.m.rows().natural(), 100)), 0, 0, -1, false, true)
	v := hs.m.View()
	checkFits(t, v, 100, 24)
	for _, l := range full {
		if !strings.Contains(v, strings.TrimSpace(strip(l))) {
			t.Errorf("the card that needs you lost %q:\n%s", strip(l), v)
		}
	}
	for _, want := range []string{"● M1-03 title of M1-03  sonnet", "● M1-05 title of M1-05  sonnet"} {
		if !strings.Contains(v, want) {
			t.Errorf("no one-line card %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "┌") != 0 || strings.Count(v, "┏") != 1 {
		t.Errorf("want one box, the card that needs you:\n%s", v)
	}
	// A click on a one-line card selects it, and ] reaches every card.
	hs.click(func(tg target) bool { return tg.act == actCardPick && tg.option == 0 })
	if hs.m.cur.id != "M1-03" {
		t.Errorf("click selected %s, want M1-03", hs.m.cur.id)
	}
	// The card that needs you keeps its box though another is selected.
	if v := hs.m.View(); !strings.Contains(v, "› ● M1-03") || !strings.Contains(v, "M1-04 · slot 2") || strings.Count(v, "┏") != 0 {
		t.Errorf("after the pick:\n%s", v)
	}
	hs.key("]")
	if hs.m.cur.id != "M1-04" {
		t.Errorf("] selected %s, want M1-04", hs.m.cur.id)
	}
}

// With room for every box nothing changes: big screens look as before.
func TestCardsBoxedWithRoom(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-04", Detail: "the agent is idle"})
	if v := hs.m.View(); strings.Count(v, "┌") != 2 || strings.Count(v, "┏") != 1 {
		t.Errorf("want every card boxed:\n%s", v)
	}
}

// A card short of rows puts its rank and mode on the state line; with
// room, the two lines stay.
func TestStateAndModelOnOneLine(t *testing.T) {
	hs := cardHarness(t, 80, 24, "M0-02")
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M0", Task: "M0-02", Reason: state.ReasonIdle, Detail: "the session is idle without a signal"})
	c := hs.m.card("M0-02")
	full := hs.m.cardBody(c, 60, 0, 0, -1, false, true)
	plain := func(lines []string) string { return strip(strings.Join(lines, "\n")) }
	if got := plain(full); !strings.Contains(got, "! Needs you · idle 0s · o opens the session\nopus → opus · mode plan") {
		t.Errorf("with room, want two lines:\n%s", got)
	}
	short := hs.m.cardBody(c, 60, 0, 0, len(full)-1, false, true)
	if got := plain(short); !strings.Contains(got, "! Needs you · idle 0s · opus · mode plan") || strings.Contains(got, "→") {
		t.Errorf("short of rows, want one line:\n%s", got)
	}
}
