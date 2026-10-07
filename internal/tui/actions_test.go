package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
)

func userStarted(id string) engine.Event {
	return engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: id, Title: "Buy the domain"}
}

func TestBarShowsOnlyWhatApplies(t *testing.T) {
	tests := []struct {
		name  string
		setup func(hs *harness)
		want  []action
	}{
		{"no task", func(*harness) {}, []action{actMode, actPause, actStopAsk, actHelp, actQuit}},
		{"agent task", func(hs *harness) { hs.events(started("M0-03"), opened("M0-03")) },
			[]action{actOpen, actMode, actPause, actDone, actRetry, actSkip, actStopAsk, actHelp, actQuit}},
		{"agent task, no session yet", func(hs *harness) { hs.events(started("M0-03")) },
			[]action{actMode, actPause, actDone, actRetry, actSkip, actStopAsk, actHelp, actQuit}},
		{"user task", func(hs *harness) { hs.events(userStarted("M0-05")) },
			[]action{actMode, actPause, actDone, actSkip, actStopAsk, actHelp, actQuit}},
		{"question put aside", func(hs *harness) {
			hs.events(started("M0-03"), asked(engine.QuestionCommit, "commit?"))
			hs.key("esc")
		}, []action{actAnswer, actMode, actPause, actDone, actRetry, actSkip, actStopAsk, actHelp, actQuit}},
		{"plan loaded", func(hs *harness) { hs.withPlan(demoPlan); hs.events(started("M0-03")) },
			[]action{actMode, actTaskMode, actPause, actDone, actRetry, actSkip, actStopAsk, actHelp, actQuit}},
		{"user task, plan loaded", func(hs *harness) { hs.withPlan(demoPlan); hs.events(userStarted("M0-05")) },
			[]action{actMode, actPause, actDone, actSkip, actStopAsk, actHelp, actQuit}},
		{"run over", func(hs *harness) {
			hs.events(started("M0-03"), engine.Event{Kind: engine.RunStopped, Detail: "completed"})
		}, []action{actHelp, actQuit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := newHarness(t, 120, 30)
			tt.setup(hs)
			var got []action
			for _, b := range hs.m.buttons() {
				got = append(got, b.act)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buttons %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStopAsksFirst(t *testing.T) {
	tests := []struct {
		name  string
		keys  []string
		click int // option to click, when keys is empty
		stop  bool
	}{
		{name: "enter keeps running", keys: []string{"enter"}},
		{name: "esc keeps running", keys: []string{"esc"}},
		{name: "number stops", keys: []string{"2"}, stop: true},
		{name: "arrows stop", keys: []string{"down", "enter"}, stop: true},
		{name: "click stops", click: 1, stop: true},
		{name: "click keeps running", click: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := newHarness(t, 80, 24)
			hs.events(started("M0-03"), opened("M0-03"))
			hs.key("x")
			if hs.m.dialog == nil || hs.m.dialog.options[hs.m.dialog.selected].label != "Keep running" {
				t.Fatalf("x opened %+v, want the stop dialog on Keep running", hs.m.dialog)
			}
			if len(tt.keys) == 0 {
				hs.click(isOption(tt.click))
			}
			hs.keys(tt.keys...)
			got := hs.s.take()
			if tt.stop != (len(got) == 1 && got[0].Kind == engine.CmdStop) || (!tt.stop && len(got) != 0) {
				t.Errorf("sent %+v, want stop %v", got, tt.stop)
			}
			if hs.m.dialog != nil {
				t.Error("stop dialog still open")
			}
		})
	}
	hs := newHarness(t, 80, 24)
	hs.click(isAct(actStopAsk))
	if hs.m.dialog == nil || hs.m.dialog.title != "Stop igris?" {
		t.Fatalf("Stop button opened %+v", hs.m.dialog)
	}
}

func TestSkipAsksForAReason(t *testing.T) {
	tests := []struct {
		name string
		keys []string
		want string // the reason sent; "" for nothing
		open bool   // the dialog stays open
	}{
		{name: "enter twice cancels", keys: []string{"enter", "enter"}},
		{name: "esc cancels in the field", keys: []string{"d", "u", "p", "esc"}},
		{name: "esc cancels on the options", keys: []string{"d", "enter", "esc"}},
		{name: "an empty reason can't skip", keys: []string{"enter", "2"}, open: true},
		{name: "a blank reason can't skip", keys: []string{" ", " ", "enter", "down", "enter"}, open: true},
		{name: "reason then Skip", keys: []string{"d", "u", "p", "e", "enter", "down", "enter"}, want: "dupe"},
		{name: "reason then number", keys: []string{"o", "k", "tab", "2"}, want: "ok"},
		{name: "shortcut keys type", keys: []string{"q", "p", "d", "1", "j", "k", "?", "enter", "2"}, want: "qpd1jk?"},
		{name: "backspace", keys: []string{"a", "b", "backspace", "c", "enter", "2"}, want: "ac"},
		{name: "back to the field", keys: []string{"a", "enter", "up", "b", "enter", "2"}, want: "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := newHarness(t, 80, 24)
			hs.events(started("M0-03"), opened("M0-03"))
			hs.key("s")
			d := hs.m.dialog
			if d == nil || !d.inField || d.input == nil {
				t.Fatalf("s opened %+v, want the skip dialog typing", d)
			}
			for _, k := range tt.keys {
				if _, ok := hs.key(k).(tea.QuitMsg); ok {
					t.Fatalf("%q quit", k)
				}
			}
			got := hs.s.take()
			switch {
			case tt.want != "":
				if len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdSkip, Text: tt.want}) {
					t.Errorf("sent %+v, want skip %q", got, tt.want)
				}
			case len(got) != 0:
				t.Errorf("sent %+v, want nothing", got)
			}
			if tt.open != (hs.m.dialog != nil) {
				t.Errorf("dialog open %v, want %v", hs.m.dialog != nil, tt.open)
			}
			if tt.open && (!hs.m.dialog.inField || !strings.Contains(hs.m.View(), "Type a reason")) {
				t.Errorf("an empty skip doesn't send the owner back to the field:\n%s", hs.m.View())
			}
		})
	}
}

func TestSkipByMouse(t *testing.T) {
	hs := newHarness(t, 50, 20)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.click(isAct(actSkip))
	hs.click(isOption(0)) // leaves the field for Cancel
	if hs.m.dialog != nil || len(hs.s.take()) != 0 {
		t.Fatal("Cancel did not just close")
	}
	hs.click(isAct(actSkip))
	hs.keys("x")
	hs.click(isOption(1))
	if got := hs.s.take(); len(got) != 1 || got[0].Text != "x" {
		t.Fatalf("sent %+v", got)
	}
	hs.click(isAct(actSkip))
	hs.keys("enter")
	hs.click(isAct(actField))
	if !hs.m.dialog.inField {
		t.Error("clicking the field doesn't focus it")
	}
	if v := hs.m.View(); !strings.Contains(v, "Reason: [") || !strings.Contains(v, "session is closed.") {
		t.Errorf("skip dialog:\n%s", v)
	}
}

func TestRetryAsksHow(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.keys("r", "enter")
	hs.keys("r", "2")
	hs.keys("r", "esc")
	got := hs.s.take()
	want := []engine.Command{{Kind: engine.CmdRetry}, {Kind: engine.CmdRetry, Continue: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sent %+v, want %+v", got, want)
	}
	user := newHarness(t, 80, 24)
	user.events(userStarted("M0-05"))
	user.key("r")
	if user.m.dialog != nil {
		t.Error("retry offered for a user task")
	}
}

func TestSessionLostSkip(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"), asked(engine.QuestionSessionLost, "gone"))
	hs.key("4")
	if hs.m.dialog == nil || hs.m.dialog.input == nil {
		t.Fatalf("Skip… opened %+v", hs.m.dialog)
	}
	hs.key("esc")
	if hs.m.asked == nil {
		t.Fatal("cancelling the skip dropped the pending question")
	}
	hs.click(isAct(actAnswer))
	hs.keys("4", "w", "o", "n", "t", "enter", "2")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdSkip, Text: "wont"}) {
		t.Fatalf("sent %+v", got)
	}
	if hs.m.asked != nil || hs.m.dialog != nil {
		t.Error("the skip didn't answer the question")
	}
}

func TestTaskDialogsCloseWhenTheTaskEnds(t *testing.T) {
	for _, k := range []string{"s", "r"} {
		hs := newHarness(t, 80, 24)
		hs.events(started("M0-03"), opened("M0-03"))
		hs.key(k)
		hs.events(engine.Event{Kind: engine.TaskDone, Task: "M0-03"})
		if hs.m.dialog != nil {
			t.Errorf("%s dialog outlived its task", k)
		}
	}
}

func TestFocusMovesAndActivates(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	if v := hs.m.View(); !strings.Contains(v, "[›Open session‹] [Mode]") {
		t.Fatalf("first bar button not focused:\n%s", v)
	}
	hs.keys("right", "right", "right", "enter") // Mode, Task mode, Pause
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdPause {
		t.Fatalf("enter on Pause sent %+v", got)
	}
	hs.keys("left", "left", "left", "left") // wraps around to Quit
	if v := hs.m.View(); !strings.Contains(v, "[›Quit‹]") {
		t.Fatalf("left doesn't wrap:\n%s", v)
	}
	if _, ok := hs.key(" ").(tea.QuitMsg); !ok {
		t.Fatal("space on Quit doesn't quit")
	}

	// tab: bar → log → tasks → bar.
	hs.key("tab")
	if v := hs.m.View(); hs.m.focus != focusLog || !strings.Contains(v, "› LOG") || strings.Contains(v, "‹]") {
		t.Fatalf("tab didn't move to the log:\n%s", v)
	}
	hs.key("tab")
	v := hs.m.View()
	if hs.m.focus != focusTasks || !strings.Contains(v, "› TASKS") || !strings.Contains(v, "›● M0-03") {
		t.Fatalf("tab didn't move to the task list on the current task:\n%s", v)
	}
	hs.keys("down", "down", "j")
	if v := hs.m.View(); !strings.Contains(v, "›⨯ M0-13") {
		t.Fatalf("down didn't move the selection:\n%s", v)
	}
	hs.keys("k", "up")
	if hs.m.selID != "M0-04" {
		t.Errorf("selected %q, want M0-04", hs.m.selID)
	}
	hs.key("shift+tab")
	if hs.m.focus != focusLog {
		t.Errorf("shift+tab went to %v, want the log", hs.m.focus)
	}
	hs.key("shift+tab")
	if hs.m.focus != focusBar {
		t.Errorf("focus %v, want the bar", hs.m.focus)
	}
	// Shortcuts work wherever the focus is.
	hs.keys("tab", "d")
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdDone {
		t.Errorf("d from the log sent %+v", got)
	}
}

func TestFocusSurvivesButtonChanges(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"))
	hs.m.View()
	hs.keys("right", "right") // Mode, Pause, Done
	hs.events(opened("M0-03"))
	if v := hs.m.View(); !strings.Contains(v, "[›Done‹]") {
		t.Errorf("focus moved off Done when Open session appeared:\n%s", v)
	}
	hs.events(engine.Event{Kind: engine.TaskDone, Task: "M0-03"})
	if v := hs.m.View(); !strings.Contains(v, "[›Mode‹]") {
		t.Errorf("focus not back on the first button once Done went:\n%s", v)
	}
}

func TestNarrowBarFocus(t *testing.T) {
	hs := newHarness(t, 16, 20)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.m.View()
	hs.keys("right", "right") // Open session, Done, More…
	if v := hs.m.View(); hs.focusedButtonIs(actMore) && !strings.Contains(v, "[›More…‹]") {
		t.Fatalf("More… not marked:\n%s", v)
	}
	if !hs.focusedButtonIs(actMore) {
		t.Fatalf("focus on %v, want More…", hs.m.focusedButton())
	}
	hs.key("enter")
	if hs.m.dialog == nil {
		t.Fatal("enter on More… opened nothing")
	}
}

func (hs *harness) focusedButtonIs(a action) bool { return hs.m.focusedButton() == a }

func TestLogFocusScrolls(t *testing.T) {
	hs := newHarness(t, 60, 12)
	for i := range 20 {
		hs.events(engine.Event{Kind: engine.Warning, Detail: "line " + string(rune('a'+i))})
	}
	hs.key("tab")
	hs.keys("up", "up", "k")
	if hs.m.scroll != 3 {
		t.Errorf("scroll %d after three ups, want 3", hs.m.scroll)
	}
	hs.key("down")
	if hs.m.scroll != 2 {
		t.Errorf("scroll %d, want 2", hs.m.scroll)
	}
}

func TestHelpPage(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {50, 20}} {
		hs := newHarness(t, size[0], size[1])
		hs.events(started("M0-03"), opened("M0-03"))
		hs.key("?")
		if hs.m.page == nil {
			t.Fatal("? opened no page")
		}
		var all []string
		for _, l := range hs.m.page.body(size[0]) {
			all = append(all, strings.TrimSpace(l))
		}
		text := strings.Join(all, "\n")
		for k := range shortcuts {
			if k == "ctrl+c" {
				continue
			}
			if !strings.Contains(text, k+" ") && !strings.Contains(text, "\n"+k) {
				t.Errorf("%v: help doesn't list %q:\n%s", size, k, text)
			}
		}
		v := hs.m.View()
		checkFits(t, v, size[0], size[1])
		if !strings.Contains(v, "Help") || !strings.Contains(v, "[Close]") {
			t.Fatalf("%v: help not drawn:\n%s", size, v)
		}
		// The page is modal: shortcut keys don't act under it.
		hs.keys("d", "p", "end", "pgup", "down")
		if got := hs.s.take(); len(got) != 0 {
			t.Errorf("keys under the help sent %+v", got)
		}
		hs.key("esc")
		if hs.m.page != nil {
			t.Fatal("esc doesn't close the help")
		}
		hs.key("?")
		hs.click(isAct(actClose))
		if hs.m.page != nil {
			t.Fatal("Close doesn't close the help")
		}
	}
}

func TestHelpScrolls(t *testing.T) {
	hs := newHarness(t, 50, 8)
	hs.key("?")
	hs.m.View()
	if hs.m.page.top != 0 {
		t.Fatal("help doesn't start at the top")
	}
	hs.key("down")
	hs.m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	hs.m.View()
	if hs.m.page.top != 4 {
		t.Errorf("top %d after down and a wheel notch, want 4", hs.m.page.top)
	}
	hs.key("end")
	v := hs.m.View()
	if !strings.Contains(v, "esc") || !strings.Contains(v, "of ") {
		t.Errorf("end doesn't show the last lines:\n%s", v)
	}
	checkFits(t, v, 50, 8)
}

func TestQuestionOpensOverThePage(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"))
	hs.key("?")
	hs.events(asked(engine.QuestionCommit, "commit?"))
	if v := hs.m.View(); !strings.Contains(v, "Commit M0-03?") {
		t.Fatalf("question hidden under the help:\n%s", v)
	}
	hs.key("enter")
	if hs.m.page == nil || !strings.Contains(hs.m.View(), "Help") {
		t.Error("help gone after answering")
	}
}

// A user task has no session, so M does nothing on it.
func TestTaskModeIgnoresUserTasks(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(userStarted("M0-05"))
	hs.key("M")
	if hs.m.dialog != nil {
		t.Errorf("M opened %+v on a user task", hs.m.dialog)
	}
	for _, b := range hs.m.buttons() {
		if b.act == actTaskMode {
			t.Errorf("the bar offers Task mode on a user task")
		}
	}
	if got := hs.s.take(); len(got) != 0 {
		t.Errorf("sent %+v", got)
	}
}
