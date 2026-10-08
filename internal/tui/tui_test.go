package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
)

// sender records the commands the TUI sends.
type sender struct {
	mu   sync.Mutex
	cmds []engine.Command
}

func (s *sender) Send(c engine.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds = append(s.cmds, c)
}

func (s *sender) take() []engine.Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cmds
	s.cmds = nil
	return c
}

var t0 = time.Date(2026, 10, 6, 9, 41, 0, 0, time.UTC)

// harness is a model driven by hand: no program, no terminal.
type harness struct {
	t     *testing.T
	m     *model
	s     *sender
	now   time.Time
	focus []backend.SessionRef
}

func newHarness(t *testing.T, w, h int) *harness {
	t.Helper()
	hs := &harness{t: t, s: &sender{}, now: t0}
	hs.m = newModel(context.Background(), Options{
		Project: "sinjal",
		Backend: "herdr",
		Mode:    "plan",
		Feed:    NewFeed(),
		Sender:  hs.s,
		Now:     func() time.Time { return hs.now },
		Focus: func(_ context.Context, ref backend.SessionRef) error {
			hs.focus = append(hs.focus, ref)
			return nil
		},
	})
	hs.m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return hs
}

// events applies evs as one feed batch, stamping times from t0.
func (hs *harness) events(evs ...engine.Event) {
	for i := range evs {
		if evs[i].At.IsZero() {
			evs[i].At = hs.now
		}
	}
	hs.m.Update(batch{events: evs})
}

// keys presses each key in turn.
func (hs *harness) keys(ks ...string) {
	for _, k := range ks {
		hs.key(k)
	}
}

// key presses k and runs the command it returns, like the program would.
func (hs *harness) key(k string) tea.Msg {
	_, cmd := hs.m.Update(keyMsg(k))
	return run(cmd)
}

// keyMsg is the key message for the key named k.
func keyMsg(k string) tea.KeyMsg {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+c":
		msg = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "pgup":
		msg = tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		msg = tea.KeyMsg{Type: tea.KeyPgDown}
	case "tab":
		msg = tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		msg = tea.KeyMsg{Type: tea.KeyShiftTab}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "backspace":
		msg = tea.KeyMsg{Type: tea.KeyBackspace}
	case "end":
		msg = tea.KeyMsg{Type: tea.KeyEnd}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	return msg
}

// click renders a frame and presses the left button on the zone matching
// want.
func (hs *harness) click(want func(target) bool) tea.Msg {
	hs.t.Helper()
	hs.m.View()
	for _, z := range hs.m.zones.list {
		if want(z.t) {
			_, cmd := hs.m.Update(tea.MouseMsg{X: z.r.x, Y: z.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			return run(cmd)
		}
	}
	hs.t.Fatalf("no zone to click; zones: %+v\n%s", hs.m.zones.list, hs.m.View())
	return nil
}

func isAct(a action) func(target) bool { return func(t target) bool { return t.act == a } }

func isOption(i int) func(target) bool {
	return func(t target) bool { return t.act == actOption && t.option == i }
}

func run(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func started(id string) engine.Event {
	return engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: id, Title: "Makefile", Rank: "sonnet", Model: "sonnet", Mode: "plan"}
}

func opened(id string) engine.Event {
	return engine.Event{Kind: engine.SessionOpened, Task: id, Mode: "plan", Session: &backend.SessionRef{Backend: "herdr", PaneID: "p1"}}
}

func TestEventsDriveTheCurrentTask(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"}, started("M0-03"), opened("M0-03"))
	c := hs.m.cur
	if c == nil || c.id != "M0-03" || c.session == nil || c.state != stateWorking || hs.m.phase != "M0" {
		t.Fatalf("current = %+v, phase %q", c, hs.m.phase)
	}
	hs.now = t0.Add(4*time.Minute + 12*time.Second)
	hs.events(engine.Event{Kind: engine.NeedsYou, Task: "M0-03", Detail: "idle", At: t0.Add(3*time.Minute + 27*time.Second)})
	view := hs.m.View()
	for _, want := range []string{"igris · sinjal · phase M0 · mode: plan · herdr", "M0-03 Makefile", "4m12s", "NEEDS YOU (idle 45s)", "M0-03 needs you: idle", "[›Open session‹]"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	hs.events(engine.Event{Kind: engine.TaskDone, Task: "M0-03", Detail: "ok"})
	if hs.m.cur != nil {
		t.Errorf("current task kept after done: %+v", hs.m.cur)
	}
	if v := hs.m.View(); !strings.Contains(v, "no task running") || strings.Contains(v, "[Done]") || strings.Contains(v, "[Open session]") {
		t.Errorf("bar or card shows actions that do nothing:\n%s", v)
	}
}

func TestShortcutKeysSendCommands(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.key("d") // no task: nothing to mark done
	hs.key("p")
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdPause {
		t.Fatalf("sent %+v, want one pause", got)
	}
	hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.PauseOn})
	if !strings.Contains(hs.m.View(), "[Resume]") {
		t.Errorf("pause button doesn't show the state:\n%s", hs.m.View())
	}
	hs.key("d")
	if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdDone {
		t.Fatalf("sent %+v, want done", got)
	}
	hs.key("o")
	if len(hs.focus) != 1 || hs.focus[0].PaneID != "p1" {
		t.Errorf("focused %+v", hs.focus)
	}
	for _, k := range []string{"q", "ctrl+c"} {
		if _, ok := hs.key(k).(tea.QuitMsg); !ok {
			t.Errorf("%s does not quit", k)
		}
	}
	if got := hs.s.take(); len(got) != 0 {
		t.Errorf("quitting sent %+v; it must leave the run alone", got)
	}
}

func TestClickingBarButtons(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.click(isAct(actPause))
	hs.click(isAct(actDone))
	got := hs.s.take()
	if len(got) != 2 || got[0].Kind != engine.CmdPause || got[1].Kind != engine.CmdDone {
		t.Fatalf("sent %+v", got)
	}
	hs.click(isAct(actOpen))
	if len(hs.focus) != 1 {
		t.Errorf("open session not focused")
	}
	if _, ok := hs.click(isAct(actQuit)).(tea.QuitMsg); !ok {
		t.Error("quit button does not quit")
	}
}

func asked(q engine.Question, detail string) engine.Event {
	return engine.Event{Kind: engine.Asked, Task: "M0-03", Question: q, Detail: detail}
}

func TestQuestionDialogs(t *testing.T) {
	tests := []struct {
		name    string
		q       engine.Question
		keys    []string // pressed in order
		clickOp int      // option to click instead, when keys is empty
		want    engine.Command
		open    bool // the dialog stays open, nothing sent
		closed  bool // the dialog closes, the question stays pending
	}{
		{name: "commit default", q: engine.QuestionCommit, keys: []string{"enter"}, want: engine.Command{Kind: engine.CmdAnswer, Yes: true}},
		{name: "commit by number", q: engine.QuestionCommit, keys: []string{"2"}, want: engine.Command{Kind: engine.CmdAnswer}},
		{name: "commit esc closes, stays pending", q: engine.QuestionCommit, keys: []string{"esc"}, closed: true},
		{name: "skip default keeps working", q: engine.QuestionConfirmSkip, keys: []string{" "}, want: engine.Command{Kind: engine.CmdAnswer}},
		{name: "skip esc keeps working", q: engine.QuestionConfirmSkip, keys: []string{"esc"}, want: engine.Command{Kind: engine.CmdAnswer}},
		{name: "skip by arrows", q: engine.QuestionConfirmSkip, keys: []string{"down", "enter"}, want: engine.Command{Kind: engine.CmdAnswer, Yes: true}},
		{name: "lost default fresh", q: engine.QuestionSessionLost, keys: []string{"enter"}, want: engine.Command{Kind: engine.CmdRetry}},
		{name: "lost wraps up to stop", q: engine.QuestionSessionLost, keys: []string{"up", "enter"}, want: engine.Command{Kind: engine.CmdStop}},
		{name: "lost click continue", q: engine.QuestionSessionLost, clickOp: 1, want: engine.Command{Kind: engine.CmdRetry, Continue: true}},
		{name: "lost click done", q: engine.QuestionSessionLost, clickOp: 2, want: engine.Command{Kind: engine.CmdDone}},
		{name: "lost esc closes, stays pending", q: engine.QuestionSessionLost, keys: []string{"esc"}, closed: true},
		{name: "hook failed default retry", q: engine.QuestionHookFailed, keys: []string{"enter"}, want: engine.Command{Kind: engine.CmdRetry}},
		{name: "hook failed click done", q: engine.QuestionHookFailed, clickOp: 1, want: engine.Command{Kind: engine.CmdDone}},
		{name: "hook failed wraps up to stop", q: engine.QuestionHookFailed, keys: []string{"up", "enter"}, want: engine.Command{Kind: engine.CmdStop}},
		{name: "shortcuts are off in a dialog", q: engine.QuestionCommit, keys: []string{"p", "d", "9"}, open: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hs := newHarness(t, 80, 24)
			hs.events(started("M0-03"), asked(tt.q, "what now?"))
			if hs.m.dialog == nil || hs.m.dialog.selected != 0 {
				t.Fatalf("no dialog on the default: %+v", hs.m.dialog)
			}
			if len(tt.keys) == 0 {
				hs.click(isOption(tt.clickOp))
			}
			for _, k := range tt.keys {
				hs.key(k)
			}
			got := hs.s.take()
			if tt.closed {
				if len(got) != 0 || hs.m.dialog != nil || hs.m.asked == nil {
					t.Fatalf("sent %+v, dialog %v, asked %v; want it closed, nothing sent, still pending", got, hs.m.dialog, hs.m.asked)
				}
				return
			}
			if tt.open {
				if len(got) != 0 || hs.m.dialog == nil {
					t.Fatalf("sent %+v, dialog %v; want it open and nothing sent", got, hs.m.dialog)
				}
				return
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("sent %+v, want %+v", got, tt.want)
			}
			if hs.m.dialog != nil || hs.m.asked != nil {
				t.Errorf("dialog still open after the answer")
			}
		})
	}
}

func TestDialogIsModalAndSettledByEvents(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"), opened("M0-03"), asked(engine.QuestionCommit, "commit?"))
	view := hs.m.View()
	for _, want := range []string{"Commit M0-03?", "› 1. Commit", "  2. Leave uncommitted"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	// A click outside the dialog does nothing while it is open.
	hs.m.View()
	for _, z := range hs.m.zones.list {
		if z.t.act == actPause {
			t.Fatalf("bar button clickable under a dialog")
		}
	}
	// The owner answered elsewhere (e.g. --no-tui never; but a later event).
	hs.events(engine.Event{Kind: engine.Committed, Task: "M0-03", Detail: "M0-03: Makefile"})
	if hs.m.dialog != nil {
		t.Error("dialog not closed by the event that settled it")
	}
}

func TestWheelScrollsTheLog(t *testing.T) {
	hs := newHarness(t, 60, 8)
	for i := 0; i < 20; i++ {
		hs.events(engine.Event{Kind: engine.Warning, Detail: "line " + string(rune('a'+i))})
	}
	if v := hs.m.View(); !strings.Contains(v, "line t") || strings.Contains(v, "line a") {
		t.Fatalf("log doesn't end at the newest line:\n%s", v)
	}
	var logZone rect
	for _, z := range hs.m.zones.list {
		if z.t.region == regionLog {
			logZone = z.r
		}
	}
	for i := 0; i < 10; i++ {
		hs.m.Update(tea.MouseMsg{X: logZone.x, Y: logZone.y, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	}
	if v := hs.m.View(); !strings.Contains(v, "line a") || strings.Contains(v, "line t") {
		t.Fatalf("wheel did not scroll back:\n%s", v)
	}
	hs.m.Update(tea.MouseMsg{X: logZone.x, Y: logZone.y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	hs.key("pgup")
	if hs.m.scroll != len(hs.m.log)-1 {
		t.Errorf("scroll = %d, want clamped to %d", hs.m.scroll, len(hs.m.log)-1)
	}
}

func TestViewFitsTheScreen(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {50, 20}, {20, 5}} {
		hs := newHarness(t, size[0], size[1])
		hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.Warning, Detail: strings.Repeat("long ", 40)},
			asked(engine.QuestionSessionLost, strings.Repeat("the pane is gone ", 6)))
		lines := strings.Split(hs.m.View(), "\n")
		if len(lines) > size[1] {
			t.Errorf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if textWidth(l) > size[0] {
				t.Errorf("%v: line too wide (%d): %q", size, textWidth(l), l)
			}
		}
	}
}

func TestRunEnd(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"), engine.Event{Kind: engine.RunStopped, Detail: "completed"})
	hs.m.Update(batch{ended: true})
	v := hs.m.View()
	if !strings.Contains(v, "the run stopped: completed — press q to quit") || strings.Contains(v, "[Pause]") || strings.Contains(v, "[Done]") {
		t.Errorf("end not shown:\n%s", v)
	}
}

func TestProgramOptionsMouse(t *testing.T) {
	ctx := context.Background()
	if on, off := len(programOptions(ctx, true)), len(programOptions(ctx, false)); on != off+1 {
		t.Errorf("mouse on adds %d options, want exactly the mouse one", on-off)
	}
}

// wheel is a wheel event over r.
func wheel(r rect, up bool) tea.MouseMsg {
	b := tea.MouseButtonWheelDown
	if up {
		b = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{X: r.x, Y: r.y, Button: b, Action: tea.MouseActionPress}
}
