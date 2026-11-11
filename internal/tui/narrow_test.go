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
				hs.withPlan(layoutPlan(st.name))
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
			hs.withPlan(layoutPlan(st.name))
			st.setup(hs)
			for range 10 {
				hs.events(engine.Event{Kind: engine.Warning, Detail: strings.Repeat("a long warning ", 8)})
			}
			view := hs.m.View()
			if hs.m.wide() {
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
	checkFits(t, v, 50, 20)
	for _, want := range []string{"M0-03 Makefile", "● M0-03 Makefile", "⨯ M0-13 Static assets", "log 0", "log 2", "log 4", "[›o Open session‹]", "[? More]"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	// TASKS is only as tall as its rows (SPEC §15.2): the LOG rule follows
	// its last row, and the log takes the rest of the screen.
	lines := strings.Split(v, "\n")
	for i, l := range lines {
		if strings.Contains(l, "– M0-14 Old idea") && (i+1 >= len(lines) || !strings.Contains(lines[i+1], "LOG")) {
			t.Errorf("empty TASKS rows under the last task:\n%s", v)
		}
	}
	if strings.Contains(v, "More…") || strings.Contains(v, "session open") {
		t.Errorf("More… shown, or more log lines than rows:\n%s", v)
	}
	hs.key("d") // not on the bar while the task works, but its key works
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdDone {
		t.Errorf("sent %+v", got)
	}
}

// TestOneButtonRow: the bar is one row of the actions that apply now and
// ? More (SPEC §15.4), the whole bar where it fits on one row, the keys
// alone where even the short row doesn't.
func TestOneButtonRow(t *testing.T) {
	needsYou := func(hs *harness) {
		hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.NeedsYou, Phase: "M0", Task: "M0-03", Detail: "idle"})
	}
	working := func(hs *harness) { hs.events(started("M0-03"), opened("M0-03")) }
	for _, tc := range []struct {
		name  string
		w, h  int
		setup func(*harness)
		want  string
	}{
		{"working", 80, 24, working, "[›o Open session‹] [p Pause] [x Stop…] [? More]"},
		{"needs you", 80, 24, needsYou, "[›o Open session‹] [d Done] [r Retry…] [p Pause] [x Stop…] [? More]"},
		{"needs you, keys", 50, 20, needsYou, "[›o‹] [d] [r] [p] [x] [?]"},
		{"working, wide", 100, 24, working, "[›o Open session‹] [p Pause] [x Stop…] [? More]"},
		{"working, room for all", 120, 30, working, "[›o Open session‹] [m Mode] [M Task mode] [p Pause] [d Done] [r Retry…] [s Skip…] [x Stop…] [?] [q Quit]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHarness(t, tc.w, tc.h)
			hs.withPlan(demoPlan)
			tc.setup(hs)
			v := hs.m.View()
			checkFits(t, v, tc.w, tc.h)
			lines := strings.Split(v, "\n")
			if got := lines[len(lines)-1]; got != tc.want {
				t.Errorf("bar %q, want %q:\n%s", got, tc.want, v)
			}
			if strings.Contains(lines[len(lines)-2], "[") {
				t.Errorf("the bar takes two rows:\n%s", v)
			}
		})
	}
	// A user task at its turn has its own actions.
	hs := yourTurn(t, 50, 20)
	lines := strings.Split(hs.m.View(), "\n")
	if got, want := lines[len(lines)-1], "[›d Done…‹] [s Skip…] [p Pause] [x Stop…] [? More]"; got != want {
		t.Errorf("your turn: bar %q, want %q", got, want)
	}
	// ? More lists every key; the keys not on the bar still work.
	hs = newHarness(t, 80, 24)
	hs.withPlan(demoPlan)
	working(hs)
	hs.click(isAct(actHelp))
	if hs.m.page == nil || !strings.Contains(hs.m.View(), "Done — mark the current task done") {
		t.Fatalf("? More shows no help:\n%s", hs.m.View())
	}
	hs.key("esc")
	hs.key("s")
	if hs.m.dialog == nil {
		t.Errorf("s opened no skip dialog:\n%s", hs.m.View())
	}
}

func TestLayoutBar(t *testing.T) {
	btns := []button{{"Open session", "Open", "o", actOpen}, {label: "Pause", key: "p", act: actPause}, {label: "Done", key: "d", act: actDone}, {key: "?", act: actHelp}, {label: "Quit", key: "q", act: actQuit}}
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
		// Everything fits with the full labels.
		{60, [][]string{{"[o Open session]", "[p Pause]", "[d Done]", "[?]", "[q Quit]"}}, 0},
		// Two rows hold the full labels.
		{30, [][]string{{"[o Open session]", "[p Pause]"}, {"[d Done]", "[?]", "[q Quit]"}}, 0},
		// The verbs fit where the labels don't.
		{22, [][]string{{"[o Open]", "[p Pause]"}, {"[d Done]", "[?]", "[q Quit]"}}, 0},
		// Folding: More… goes before ? and Quit, which never fold.
		{16, [][]string{{"[o Open]", "[More…]"}, {"[?]", "[q Quit]"}}, 2},
	}
	for _, tt := range tests {
		l := layoutBar(&theme{}, btns, tt.w, barRows, actNone, false, []action{actPause, actDone, actOpen})
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
	// The focused button is marked; under a dialog every button is
	// inactive and none is marked.
	if l := layoutBar(&theme{}, btns, 60, 1, actDone, false, nil); l.text[0] != "[o Open session] [p Pause] [›d Done‹] [?] [q Quit]" {
		t.Errorf("focused bar = %q", l.text[0])
	}
	if l := layoutBar(&theme{}, btns, 60, 1, actDone, true, nil); strings.Contains(l.text[0], "›") {
		t.Errorf("modal bar marks a button: %q", l.text[0])
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
	for i, o := range []string{"Start fresh", "Continue conversation", "Mark done", "Skip…", "Stop igris"} {
		if !strings.Contains(v, fmt.Sprintf("%d. %s", i+1, o)) {
			t.Errorf("option %q cut off:\n%s", o, v)
		}
	}
	if !strings.Contains(v, "…") {
		t.Errorf("long detail not shortened:\n%s", v)
	}
	hs.click(isOption(4))
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
			if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdAnswer, Task: "M0-03"}) {
				t.Fatalf("dialog option: sent %+v", got)
			}
			// The task needs you: Done is on the one-row bar, by its key
			// alone where the labels don't fit.
			done := "[d]"
			if size[0] >= 100 {
				done = "[d Done]"
			}
			hs.clickText(done)
			if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdDone {
				t.Fatalf("bar: sent %+v", got)
			}
			hs.clickText("[o Open se")
			if len(hs.focus) != 1 {
				t.Fatal("card button: not focused")
			}
		})
	}
}
