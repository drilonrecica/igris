package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/engine"
)

// These tests run the model inside a real Bubble Tea program (teatest), so
// they cover what the hand-driven harness cannot: the Init → listen → batch
// loop, key and mouse messages routed by the program, and the program
// ending when the owner quits.

const waitFor = 3 * time.Second

// program starts the model on a w×h terminal with the run's events already
// queued on the feed.
func program(t *testing.T, w, h int, evs ...engine.Event) (*teatest.TestModel, *sender) {
	t.Helper()
	s := &sender{}
	feed := NewFeed()
	for _, ev := range evs {
		if ev.At.IsZero() {
			ev.At = t0
		}
		feed.Push(ev)
	}
	m := newModel(context.Background(), Options{
		Project: "sinjal", Backend: "herdr", Mode: "plan",
		Feed: feed, Sender: s,
	})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(w, h))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm, s
}

// seen waits until the program's output contains every want.
func seen(t *testing.T, tm *teatest.TestModel, want ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		for _, w := range want {
			if !bytes.Contains(out, []byte(w)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(waitFor), teatest.WithCheckInterval(10*time.Millisecond))
}

// finished waits for the program to end and returns the final model.
func finished(t *testing.T, tm *teatest.TestModel) *model {
	t.Helper()
	return tm.FinalModel(t, teatest.WithFinalTimeout(waitFor)).(*model)
}

func key(tm *teatest.TestModel, ks ...string) {
	for _, k := range ks {
		switch k {
		case "enter":
			tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
		case "esc":
			tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
		case "tab":
			tm.Send(tea.KeyMsg{Type: tea.KeyTab})
		case "down":
			tm.Send(tea.KeyMsg{Type: tea.KeyDown})
		default:
			tm.Type(k)
		}
	}
}

// zoneAt renders the same screen in a hand-driven harness (after pressing
// the key, if any) and returns where the zone matching want sits, so a click can be sent to the real program.
func zoneAt(t *testing.T, w, h int, want func(target) bool, pressed string, evs ...engine.Event) (x, y int) {
	t.Helper()
	hs := newHarness(t, w, h)
	hs.events(evs...)
	if pressed != "" {
		hs.key(pressed)
	}
	hs.m.View()
	for _, z := range hs.m.zones.list {
		if want(z.t) {
			return z.r.x, z.r.y
		}
	}
	t.Fatalf("no zone to click:\n%s", hs.m.View())
	return 0, 0
}

func click(tm *teatest.TestModel, x, y int) {
	tm.Send(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
}

func TestProgramBothLayouts(t *testing.T) {
	tests := []struct {
		name string
		w, h int
		want []string
	}{
		{"wide", 120, 30, []string{"TASKS", "CURRENT", "LOG", "M0-03 Makefile"}},
		{"narrow", 50, 20, []string{"M0-03 Makefile", "[Pause]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm, _ := program(t, tt.w, tt.h, started("M0-03"), opened("M0-03"))
			seen(t, tm, tt.want...)
			key(tm, "q")
			finished(t, tm)
		})
	}
}

func TestProgramResizeSwitchesLayout(t *testing.T) {
	tm, _ := program(t, 120, 30, started("M0-03"), opened("M0-03"))
	seen(t, tm, "│ CURRENT")
	tm.Send(tea.WindowSizeMsg{Width: 50, Height: 20})
	key(tm, "q")
	m := finished(t, tm)
	if m.width != 50 || m.height != 20 {
		t.Fatalf("size %dx%d, want 50x20", m.width, m.height)
	}
	if v := m.View(); strings.Contains(v, "│") || !strings.Contains(v, "M0-03 Makefile") {
		t.Errorf("not the narrow layout after the resize:\n%s", v)
	}
}

func TestProgramQuitLeavesTheRunAlone(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		t.Run(k, func(t *testing.T) {
			tm, s := program(t, 100, 30, started("M0-03"), opened("M0-03"))
			seen(t, tm, "M0-03 Makefile")
			if k == "ctrl+c" {
				tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
			} else {
				key(tm, k)
			}
			finished(t, tm)
			if got := s.take(); len(got) != 0 {
				t.Errorf("quitting sent %+v", got)
			}
		})
	}
}

func TestProgramStopConfirmation(t *testing.T) {
	evs := []engine.Event{started("M0-03"), opened("M0-03")}
	tests := []struct {
		name string
		keys []string
		stop bool
	}{
		{"enter keeps running", []string{"x", "enter"}, false},
		{"esc keeps running", []string{"x", "esc"}, false},
		{"2 stops", []string{"x", "2"}, true},
		{"down enter stops", []string{"x", "down", "enter"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm, s := program(t, 100, 30, evs...)
			seen(t, tm, "M0-03 Makefile")
			key(tm, "x")
			seen(t, tm, "Stop igris?")
			key(tm, tt.keys[1:]...)
			key(tm, "q") // keys are handled in order, so quit proves they ran
			finished(t, tm)
			got := s.take()
			if tt.stop != (len(got) == 1 && got[0].Kind == engine.CmdStop) || (!tt.stop && len(got) != 0) {
				t.Errorf("sent %+v, want stop %v", got, tt.stop)
			}
		})
	}
}

func TestProgramClicks(t *testing.T) {
	evs := []engine.Event{started("M0-03"), opened("M0-03")}
	for _, size := range []struct {
		name string
		w, h int
	}{{"wide", 120, 30}, {"narrow", 60, 24}} {
		t.Run(size.name+" button", func(t *testing.T) {
			tm, s := program(t, size.w, size.h, evs...)
			seen(t, tm, "M0-03 Makefile")
			x, y := zoneAt(t, size.w, size.h, isAct(actPause), "", evs...)
			click(tm, x, y)
			key(tm, "q")
			finished(t, tm)
			if got := s.take(); len(got) != 1 || got[0].Kind != engine.CmdPause {
				t.Errorf("clicking Pause sent %+v", got)
			}
		})
		t.Run(size.name+" dialog option", func(t *testing.T) {
			tm, s := program(t, size.w, size.h, evs...)
			seen(t, tm, "M0-03 Makefile")
			key(tm, "x")
			seen(t, tm, "Stop igris?")
			x, y := zoneAt(t, size.w, size.h, isOption(1), "x", evs...)
			click(tm, x, y)
			key(tm, "q")
			finished(t, tm)
			if got := s.take(); len(got) != 1 || got[0].Kind != engine.CmdStop {
				t.Errorf("clicking the Stop option sent %+v", got)
			}
		})
	}
}

func TestProgramFocusOrder(t *testing.T) {
	tm, _ := program(t, 120, 30, started("M0-03"), opened("M0-03"))
	seen(t, tm, "[›Open session‹]")
	key(tm, "tab")
	seen(t, tm, "› LOG")
	key(tm, "tab")
	seen(t, tm, "› TASKS")
	key(tm, "tab")
	seen(t, tm, "[›Open session‹]")
	key(tm, "q")
	if m := finished(t, tm); m.focus != focusBar {
		t.Errorf("three tabs left the focus on %v, want the bar", m.focus)
	}
}

func TestProgramQuestionOpensDialog(t *testing.T) {
	tm, s := program(t, 100, 30, started("M0-03"), opened("M0-03"),
		asked(engine.QuestionCommit, "M0-03: Makefile"))
	seen(t, tm, "Commit")
	key(tm, "enter") // the safe default is the first option
	key(tm, "q")
	finished(t, tm)
	if got := s.take(); len(got) != 1 || got[0].Kind != engine.CmdAnswer || !got[0].Yes {
		t.Errorf("enter on the commit dialog sent %+v, want a yes answer", got)
	}
}

func TestProgramShowsTheEnd(t *testing.T) {
	tm, _ := program(t, 100, 30, started("M0-03"), engine.Event{Kind: engine.RunStopped, Detail: "completed"})
	seen(t, tm, "the run stopped: completed")
}
