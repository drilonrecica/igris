package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// A user task says it is the owner's to do outside igris and how to finish
// it, and Done takes an optional note (owner request at the v0.2 gate:
// "your turn" didn't say what to do).

// yourTurn starts user task M0-04 of cardPlan and hands it to the owner.
func yourTurn(t *testing.T, w, h int) *harness {
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(cardPlan)
	hs.events(engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: "M0-04", Title: "Pick a license"},
		engine.Event{Kind: engine.YourTurn, Task: "M0-04", Detail: "**Pick a license** — your call, not the agent's"})
	return hs
}

func TestYourTurnSaysWhatToDo(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := yourTurn(t, size[0], size[1])
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			flat := strings.Join(strings.Fields(v), " ")
			for _, want := range []string{"outside igris", "d done · s skip", "[d] Done…", "[s] Skip…", "[t] Details"} {
				if !strings.Contains(flat, want) {
					t.Errorf("view lacks %q:\n%s", want, v)
				}
			}
			// The log line is cut to the width on small screens; its text says it all.
			if last := hs.m.log[len(hs.m.log)-1].text; last != "M0-04 is your turn: do it outside igris, then d (done) or s (skip)" {
				t.Errorf("log line %q", last)
			}
			if strings.Contains(v, "Open session") {
				t.Errorf("a user task offers a session:\n%s", v)
			}
		})
	}
}

func TestDoneOnAUserTaskTakesANote(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want *engine.Command // nil: nothing sent
	}{
		{name: "enter confirms without a note", keys: []string{"enter"}, want: &engine.Command{Kind: engine.CmdDone}},
		{name: "note then enter", keys: []string{"A", "u", "r", "o", "r", "a", "enter"}, want: &engine.Command{Kind: engine.CmdDone, Text: "Aurora"}},
		{name: "blank note is no note", keys: []string{" ", " ", "enter"}, want: &engine.Command{Kind: engine.CmdDone}},
		{name: "shortcut keys type", keys: []string{"d", "s", "q", "enter"}, want: &engine.Command{Kind: engine.CmdDone, Text: "dsq"}},
		{name: "esc cancels", keys: []string{"x", "esc"}},
		{name: "Cancel by number", keys: []string{"tab", "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := yourTurn(t, 80, 24)
			hs.key("d")
			if d := hs.m.dialog; d == nil || !d.inField || !strings.Contains(hs.m.View(), "Mark M0-04 done?") {
				t.Fatalf("d didn't open the done dialog:\n%s", hs.m.View())
			}
			if got := hs.s.take(); len(got) != 0 {
				t.Fatalf("d sent %+v before the owner confirmed", got)
			}
			hs.keys(tt.keys...)
			got := hs.s.take()
			switch {
			case tt.want == nil && len(got) != 0:
				t.Errorf("sent %+v, want nothing", got)
			case tt.want != nil && (len(got) != 1 || got[0] != *tt.want):
				t.Errorf("sent %+v, want %+v", got, *tt.want)
			}
			if hs.m.dialog != nil {
				t.Errorf("dialog still open")
			}
		})
	}
}

func TestDoneOnAnAgentTaskIsImmediate(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.key("d")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdDone}) || hs.m.dialog != nil {
		t.Errorf("agent d: sent %+v, dialog %v", got, hs.m.dialog != nil)
	}
}

func TestYourTurnCardButtons(t *testing.T) {
	hs := yourTurn(t, 120, 40)
	hs.click(isAct(actDone))
	if hs.m.dialog == nil || !strings.Contains(hs.m.View(), "Mark M0-04 done?") {
		t.Fatalf("[d] Done… opened no dialog:\n%s", hs.m.View())
	}
	hs.key("esc")
	hs.m.View()
	n := 0
	for _, z := range hs.m.zones.list {
		if z.t.act == actSkip {
			n++
		}
	}
	if n < 2 { // the card's [s] Skip… and the bar's Skip
		t.Errorf("want the card's [s] Skip… clickable, %d Skip zones", n)
	}
}

func TestDoneDialogClosesWhenTheTaskEnds(t *testing.T) {
	hs := yourTurn(t, 80, 24)
	hs.key("d")
	hs.events(engine.Event{Kind: engine.TaskDone, Task: "M0-04"})
	if hs.m.dialog != nil {
		t.Error("done dialog outlived its task")
	}
}
