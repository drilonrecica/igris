package tui

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// Every screen the app can show says what is under the pointer.
var (
	_ hoverModel = (*appModel)(nil)
	_ hoverModel = (*model)(nil)
	_ hoverModel = (*review)(nil)
	_ hoverer    = (*homeScreen)(nil)
	_ hoverer    = (*pageScreen)(nil)
	_ hoverer    = (*previewScreen)(nil)
	_ hoverer    = (*settingsScreen)(nil)
	_ hoverer    = (*phaseScreen)(nil)
	_ hoverer    = (*taskScreen)(nil)
	_ hoverer    = (*checkScreen)(nil)
	_ hoverer    = (*doctorScreen)(nil)
	_ hoverer    = (*historyScreen)(nil)
	_ hoverer    = (*notifyScreen)(nil)
	_ hoverer    = (*adaptScreen)(nil)
	_ hoverer    = (*initScreen)(nil)
)

const ul, noUL = sgrUnderline, sgrNoUnderline

func TestUnderlineSpan(t *testing.T) {
	cases := []struct {
		name     string
		s        string
		from, to int
		want     string
	}{
		{"button", "[Mode] [Stop]", 0, 6, ul + "[Mode]" + noUL + " [Stop]"},
		{"second button", "[Mode] [Stop]", 7, 13, "[Mode] " + ul + "[Stop]" + noUL},
		{"row trims blanks", "  › M0-01  title   ", 0, 19, "  " + ul + "› M0-01  title" + noUL + "   "},
		{"dialog option trims the box", "│ 1. Commit   │", 0, 15, "│ " + ul + "1. Commit" + noUL + "   │"},
		{"reset inside turns it on again", "\x1b[7m[›Go‹]\x1b[0m x", 0, 6,
			"\x1b[7m" + ul + "[›Go‹]\x1b[0m" + ul + noUL + " x"},
		{"wide grapheme", "⚠️ ok", 0, 5, ul + "⚠️ ok" + noUL},
		{"span past the end", "ab", 0, 10, ul + "ab" + noUL},
		{"blank span", "ab    cd", 2, 6, "ab    cd"},
		{"outside the line", "ab", 5, 9, "ab"},
	}
	for _, c := range cases {
		if got := underlineSpan(c.s, c.from, c.to); got != c.want {
			t.Errorf("%s: underlineSpan(%q, %d, %d) = %q, want %q", c.name, c.s, c.from, c.to, got, c.want)
		}
	}
}

func TestUnderlineRect(t *testing.T) {
	got := underlineRect("one\n[Go] x\nthree", rect{0, 1, 4, 1})
	if want := "one\n" + ul + "[Go]" + noUL + " x\nthree"; got != want {
		t.Errorf("underlineRect = %q, want %q", got, want)
	}
	if got := underlineRect("one", rect{0, 3, 4, 1}); got != "one" {
		t.Errorf("a rect below the frame changed it: %q", got)
	}
}

// fakeHover is a screen with one button, "[Go]", at (0..3, 0).
type fakeHover struct {
	views int
	got   []tea.Msg
}

func (f *fakeHover) Init() tea.Cmd { return nil }
func (f *fakeHover) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	f.got = append(f.got, msg)
	return f, nil
}
func (f *fakeHover) View() string { f.views++; return "[Go] text" }
func (f *fakeHover) hoverAt(x, y int) (rect, bool) {
	r := rect{0, 0, 4, 1}
	return r, r.contains(x, y)
}

func motion(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionMotion}
}

func TestHoverWrapper(t *testing.T) {
	f := &fakeHover{}
	h := newHover(f)
	under := ul + "[Go]" + noUL + " text"
	steps := []struct {
		name  string
		msg   tea.Msg
		want  string
		views int // the screen's View calls so far
	}{
		{"first frame", nil, "[Go] text", 1},
		{"onto the button", motion(1, 0), under, 2},
		{"along the button: the frame is reused", motion(3, 0), under, 2},
		{"off it", motion(6, 0), "[Go] text", 3},
		{"off it again: reused", motion(8, 0), "[Go] text", 3},
		{"back on", motion(0, 0), under, 4},
		{"a key redraws", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}, under, 5},
		{"another window takes the pointer", tea.BlurMsg{}, "[Go] text", 6},
		{"a click redraws and hovers", tea.MouseMsg{X: 2, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}, under, 7},
	}
	for _, s := range steps {
		if s.msg != nil {
			h.Update(s.msg)
		}
		if got := h.View(); got != s.want || f.views != s.views {
			t.Errorf("%s: view %q (%d draws), want %q (%d)", s.name, got, f.views, s.want, s.views)
		}
	}
	// Motion stops at the wrapper; everything else reaches the screen.
	for _, msg := range f.got {
		if m, ok := msg.(tea.MouseMsg); ok && m.Action == tea.MouseActionMotion {
			t.Errorf("the screen got a motion event: %+v", m)
		}
	}
	if len(f.got) != 3 {
		t.Errorf("the screen got %d messages, want the key, the blur and the click: %v", len(f.got), f.got)
	}
	if unwrap(h) != f || unwrap(f) != f {
		t.Error("unwrap doesn't return the wrapped model")
	}
}

// zoneRect is the rectangle of the first zone in z whose target want
// accepts.
func zoneRect(t *testing.T, z zones, want func(target) bool) rect {
	t.Helper()
	for _, zn := range z.list {
		if want(zn.t) {
			return zn.r
		}
	}
	t.Fatalf("no such zone in %+v", z.list)
	return rect{}
}

// The run view hovers what a click acts on: the bar, until a dialog opens;
// then only the dialog's options.
func TestRunViewHover(t *testing.T) {
	hs := newHarness(t, 80, 24)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.m.View()
	stop := zoneRect(t, hs.m.zones, isAct(actStopAsk))
	if r, ok := hs.m.hoverAt(stop.x, stop.y); !ok || r != stop {
		t.Fatalf("hover over Stop = %+v %v, want %+v", r, ok, stop)
	}
	tasks := zoneRect(t, hs.m.zones, func(t target) bool { return t.region == regionTasks })
	if _, ok := hs.m.hoverAt(tasks.x+tasks.w-1, tasks.y+tasks.h-1); ok {
		t.Error("an empty part of the task list hovers")
	}
	hs.key("x") // Stop asks first
	hs.m.View()
	if hs.m.dialog == nil {
		t.Fatal("x opened no dialog")
	}
	if _, ok := hs.m.hoverAt(stop.x, stop.y); ok {
		t.Error("the bar under the dialog hovers")
	}
	opt := zoneRect(t, hs.m.zones, isOption(1))
	if r, ok := hs.m.hoverAt(opt.x+2, opt.y); !ok || r != opt {
		t.Errorf("hover over option 2 = %+v %v, want %+v", r, ok, opt)
	}
}

// With [tui] mouse off the app hovers nothing, also after the setting is
// changed live.
func TestAppHoverFollowsTheMouseSetting(t *testing.T) {
	a := testApp(&fakeServices{}, 80, 24)
	a.View()
	home := a.stack[0].(*homeScreen)
	q := zoneRect(t, home.zones, isAct(actQuit))
	if _, ok := a.hoverAt(q.x, q.y); ok {
		t.Error("the app hovers with the mouse off")
	}
	a.o.Mouse = true
	if r, ok := a.hoverAt(q.x, q.y); !ok || r != q {
		t.Errorf("hover over Quit = %+v %v, want %+v", r, ok, q)
	}
	a.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	if _, ok := a.hoverAt(q.x, q.y); ok {
		t.Error("the app hovers while it only says it is too small")
	}
}

func TestPageRowHover(t *testing.T) {
	p := &page{rows: 4, top: 1}
	owner := []int{-1, -1, -1, 0, 1, -1, 2}
	cases := []struct {
		x, y int
		ok   bool
	}{
		{3, 2, false}, // body line 1: no task
		{3, 4, true},  // body line 3: task 0
		{0, 5, true},  // task 1
		{3, 6, false},
		{3, 7, false}, // past the rows drawn, though line 6 has a task
		{3, 1, false}, // the rule
		{40, 4, false},
	}
	for _, c := range cases {
		r, ok := rowAt(p, owner, 40, c.x, c.y)
		if ok != c.ok || ok && r != (rect{0, c.y, 40, 1}) {
			t.Errorf("rowAt(%d, %d) = %+v %v, want %v", c.x, c.y, r, ok, c.ok)
		}
	}
}

// In a real program, moving the pointer onto a home button underlines it.
func TestHoverInProgram(t *testing.T) {
	a := newApp(context.Background(), AppOptions{Services: &fakeServices{}, Poll: -1, Mouse: true}, &theme{})
	tm := teatest.NewTestModel(t, newHover(a), teatest.WithInitialTermSize(80, 24))
	t.Cleanup(func() { _ = tm.Quit() })
	// Where Quit is, from an app drawn the same way (the program's own is
	// not ours to read while it runs).
	same := testApp(&fakeServices{}, 80, 24)
	same.View()
	q := zoneRect(t, same.stack[0].(*homeScreen).zones, isAct(actQuit))
	seen(t, tm, "[Quit]")
	tm.Send(motion(q.x+1, q.y))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte(ul+"[Quit]"+noUL))
	}, teatest.WithDuration(waitFor))
	key(tm, "q")
	if _, ok := unwrap(tm.FinalModel(t, teatest.WithFinalTimeout(waitFor))).(*appModel); !ok {
		t.Error("the final model isn't the app")
	}
}

func TestProgramOptionsReportAllMotion(t *testing.T) {
	want := reflect.ValueOf(tea.WithMouseAllMotion()).Pointer()
	for _, o := range programOptions(context.Background(), true) {
		if reflect.ValueOf(o).Pointer() == want {
			return
		}
	}
	t.Error("with the mouse on, programOptions doesn't report all motion: hover needs it")
}
