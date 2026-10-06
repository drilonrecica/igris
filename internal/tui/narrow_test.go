package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
)

func TestNarrowLayoutGolden(t *testing.T) {
	for _, size := range [][2]int{{50, 20}, {80, 24}} {
		for _, st := range layoutStates {
			name := fmt.Sprintf("narrow_%dx%d_%s", size[0], size[1], st.name)
			t.Run(name, func(t *testing.T) {
				hs := newHarness(t, size[0], size[1])
				hs.m.loc = time.UTC
				hs.withPlan(demoPlan)
				st.setup(hs)
				view := hs.m.View()
				checkFits(t, view, size[0], size[1])
				golden(t, name, view)
			})
		}
	}
}

func TestNarrowFitsSmallScreens(t *testing.T) {
	for _, size := range [][2]int{{50, 20}, {99, 40}, {120, 12}, {30, 10}, {12, 4}} {
		for _, st := range layoutStates {
			hs := newHarness(t, size[0], size[1])
			hs.withPlan(demoPlan)
			st.setup(hs)
			for range 10 {
				hs.events(engine.Event{Kind: engine.Warning, Detail: strings.Repeat("a long warning ", 8)})
			}
			view := hs.m.View()
			if strings.HasPrefix(view, "┌") {
				t.Fatalf("%v %s: wide layout on a small screen", size, st.name)
			}
			checkFits(t, view, size[0], size[1])
		}
	}
}

func TestNarrowShowsWhatMatters(t *testing.T) {
	hs := newHarness(t, 50, 20)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	for i := range 5 {
		hs.events(engine.Event{Kind: engine.Warning, Detail: fmt.Sprintf("log %d", i)})
	}
	v := hs.m.View()
	for _, want := range []string{"M0-03 Makefile", "● M0-03 sonnet", "⨯ M0-13 sonnet", "log 2", "log 3", "log 4", "[Open session]", "[Done]"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "log 1") {
		t.Errorf("more than the last %d log lines:\n%s", narrowLog, v)
	}
	if strings.Contains(v, "More…") {
		t.Errorf("More… shown although everything fits:\n%s", v)
	}
	hs.click(isAct(actDone))
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdDone {
		t.Errorf("sent %+v", got)
	}
}

func TestNarrowFoldsIntoMore(t *testing.T) {
	hs := newHarness(t, 16, 20)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	v := hs.m.View()
	if !strings.Contains(v, "[More…]") || strings.Contains(v, "[Pause]") {
		t.Fatalf("bar not folded:\n%s", v)
	}
	hs.click(isAct(actMore))
	if hs.m.dialog == nil || !strings.Contains(hs.m.View(), "Pause") {
		t.Fatalf("More… shows no folded actions:\n%s", hs.m.View())
	}
	hs.key("1")
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdPause {
		t.Errorf("sent %+v, want pause", got)
	}
	if hs.m.dialog != nil {
		t.Error("More… stays open after a pick")
	}
	hs.click(isAct(actMore))
	hs.key("esc")
	if hs.m.dialog != nil || len(hs.s.take()) != 0 {
		t.Error("esc doesn't just close More…")
	}
}

func TestFitBar(t *testing.T) {
	btns := []option{{"Open session", actOpen}, {"Pause", actPause}, {"Done", actDone}, {"Quit", actQuit}}
	labels := func(l barLayout) [][]string {
		var out [][]string
		for _, r := range l.rows {
			var row []string
			for _, p := range r {
				row = append(row, p.text)
			}
			out = append(out, row)
		}
		return out
	}
	tests := []struct {
		w      int
		rows   [][]string
		folded int
	}{
		{50, [][]string{{"[Open session]", "[Pause]", "[Done]", "[Quit]"}}, 0},
		{24, [][]string{{"[Open session]", "[Pause]"}, {"[Done]", "[Quit]"}}, 0},
		{16, [][]string{{"[Open session]"}, {"[Done]", "[More…]"}}, 2},
	}
	for _, tt := range tests {
		l := fitBar(btns, tt.w, barRows)
		if got := labels(l); !reflect.DeepEqual(got, tt.rows) || len(l.folded) != tt.folded {
			t.Errorf("w=%d: rows %q folded %d, want %q %d", tt.w, got, len(l.folded), tt.rows, tt.folded)
		}
		for i, r := range l.rows {
			for _, p := range r {
				if !strings.Contains(l.text[i], p.text) {
					t.Errorf("w=%d: row text %q lacks %q", tt.w, l.text[i], p.text)
				}
			}
			if textWidth(l.text[i]) > tt.w {
				t.Errorf("w=%d: row %q too wide", tt.w, l.text[i])
			}
		}
	}
}

func TestNarrowDialogTakesTheScreen(t *testing.T) {
	hs := newHarness(t, 50, 20)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"),
		asked(engine.QuestionSessionLost, strings.Repeat("the pane of M0-03 is gone and Claude Code exited without a signal. ", 8)))
	v := hs.m.View()
	checkFits(t, v, 50, 20)
	if strings.Contains(v, "TASKS") || !strings.HasPrefix(v, "╭") {
		t.Errorf("dialog doesn't take the whole screen:\n%s", v)
	}
	for i, o := range []string{"Start fresh", "Continue conversation", "Mark done", "Stop igris"} {
		if !strings.Contains(v, fmt.Sprintf("%d. %s", i+1, o)) {
			t.Errorf("option %q cut off:\n%s", o, v)
		}
	}
	if !strings.Contains(v, "…") {
		t.Errorf("long detail not shortened:\n%s", v)
	}
	hs.click(isOption(3))
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdStop {
		t.Errorf("sent %+v, want stop", got)
	}
}

// clickText presses the left button on the first cell of text as it
// appears in the rendered frame, so the zones are checked against what
// the owner sees.
func (hs *harness) clickText(text string) {
	hs.t.Helper()
	for y, l := range strings.Split(hs.m.View(), "\n") {
		if i := strings.Index(l, text); i >= 0 {
			x := textWidth(l[:i])
			_, cmd := hs.m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			run(cmd)
			return
		}
	}
	hs.t.Fatalf("%q not on screen:\n%s", text, hs.m.View())
}

func TestClicksLandWhereTheTextIs(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {50, 20}, {16, 20}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := newHarness(t, size[0], size[1])
			hs.withPlan(demoPlan)
			hs.events(started("M0-03"), opened("M0-03"), asked(engine.QuestionCommit, "commit?"))
			hs.clickText("2. Leave")
			if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdAnswer}) {
				t.Fatalf("dialog option: sent %+v", got)
			}
			hs.clickText("[Done]")
			if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdDone {
				t.Fatalf("bar: sent %+v", got)
			}
			hs.clickText("[o] Open session")
			if len(hs.focus) != 1 {
				t.Fatal("card button: not focused")
			}
		})
	}
}
