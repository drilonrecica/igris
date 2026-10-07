package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/report"
)

// probe is a screen that records what reaches it.
type probe struct {
	name  string
	sizes []tea.WindowSizeMsg
	msgs  []tea.Msg
	inits int
}

func (p *probe) Init() tea.Cmd { p.inits++; return nil }

func (p *probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		p.sizes = append(p.sizes, s)
		return p, nil
	}
	p.msgs = append(p.msgs, msg)
	return p, nil
}

func (p *probe) View() string { return "probe " + p.name }

func (p *probe) got(want tea.Msg) bool {
	for _, m := range p.msgs {
		if m == want {
			return true
		}
	}
	return false
}

// testApp is an app on a fake project, unpainted, at w×h.
func testApp(svc Services, w, h int) *appModel {
	a := newApp(context.Background(), AppOptions{Services: svc, Poll: -1}, &theme{})
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return a
}

// drive runs cmd and feeds what it returns back into a, as the program
// would, until nothing is left; it reports whether the app asked to quit.
func drive(a *appModel, cmd tea.Cmd) (quit bool) {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			quit = drive(a, c) || quit
		}
	default:
		_, next := a.Update(msg)
		quit = drive(a, next)
	}
	return quit
}

// appProgram runs an app on a fake project in a real program.
func appProgram(t *testing.T, svc Services, w, h int) *teatest.TestModel {
	t.Helper()
	a := newApp(context.Background(), AppOptions{Services: svc, Poll: -1}, &theme{})
	tm := teatest.NewTestModel(t, a, teatest.WithInitialTermSize(w, h))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm
}

func finalApp(t *testing.T, tm *teatest.TestModel) *appModel {
	t.Helper()
	return tm.FinalModel(t, teatest.WithFinalTimeout(waitFor)).(*appModel)
}

func TestAppOpensHomeAndReadsTheProject(t *testing.T) {
	svc := &fakeServices{}
	tm := appProgram(t, svc, 80, 24)
	seen(t, tm, "igris · sinjal", "[›Doctor‹]", "[Quit]")
	key(tm, "q")
	a := finalApp(t, tm)
	if len(a.stack) != 1 {
		t.Errorf("stack has %d screens, want home only", len(a.stack))
	}
	if n := svc.called("Snapshot"); n != 1 {
		t.Errorf("Snapshot called %d times, want 1", n)
	}
}

func TestAppShowsWhyTheProjectCantBeRead(t *testing.T) {
	svc := &fakeServices{snapshot: func(context.Context) (*report.Snapshot, error) {
		return nil, errors.New("permission denied")
	}}
	tm := appProgram(t, svc, 80, 24)
	seen(t, tm, "could not read the project: permission denied")
	key(tm, "q")
	finalApp(t, tm)
}

func TestAppQuits(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c", "click Quit"} {
		t.Run(k, func(t *testing.T) {
			tm := appProgram(t, &fakeServices{}, 80, 24)
			seen(t, tm, "igris · sinjal")
			switch k {
			case "ctrl+c":
				tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
			case "click Quit":
				click(tm, textWidth("[›Doctor‹] [Settings] [?] ")+1, 23)
			default:
				key(tm, k)
			}
			finalApp(t, tm)
		})
	}
}

func TestAppCtrlCQuitsFromAPushedScreen(t *testing.T) {
	tm := appProgram(t, &fakeServices{}, 80, 24)
	seen(t, tm, "igris · sinjal")
	key(tm, "?")
	seen(t, tm, "Help · esc closes")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if a := finalApp(t, tm); len(a.stack) != 2 {
		t.Errorf("stack has %d screens, want home and help", len(a.stack))
	}
}

func TestAppHelp(t *testing.T) {
	tm := appProgram(t, &fakeServices{}, 80, 30)
	seen(t, tm, "igris · sinjal")
	key(tm, "?")
	seen(t, tm, "Help · esc closes", "quit igris", "Focus", "quit igris from anywhere")
	key(tm, "esc")
	key(tm, "q") // handled after esc, so it quits from home
	if a := finalApp(t, tm); len(a.stack) != 1 {
		t.Errorf("stack has %d screens after esc, want home only", len(a.stack))
	}
}

func TestAppHelpListsTheTopScreensKeys(t *testing.T) {
	a := testApp(&fakeServices{}, 80, 30)
	drive(a, func() tea.Msg { return helpMsg{} })
	v := a.View()
	for _, want := range []string{"?  Help — this page", "q  Quit — quit igris", "ctrl+c"} {
		if !strings.Contains(v, want) {
			t.Errorf("help lacks %q:\n%s", want, v)
		}
	}
	// A screen without keys of its own gets the shared ones.
	drive(a, push(&probe{name: "p"}))
	drive(a, func() tea.Msg { return helpMsg{} })
	if v := a.View(); strings.Contains(v, "quit igris\n") || !strings.Contains(v, "ctrl+c") {
		t.Errorf("help over a probe:\n%s", v)
	}
}

func TestAppTooSmall(t *testing.T) {
	tests := []struct {
		w, h  int
		small bool
	}{
		{39, 24, true},
		{80, 11, true},
		{38, 10, true},
		{40, 12, false},
		{50, 20, false},
	}
	for _, tt := range tests {
		a := testApp(&fakeServices{}, tt.w, tt.h)
		v := a.View()
		if got := strings.Contains(v, "terminal too small"); got != tt.small {
			t.Errorf("%d×%d: too small %v, want %v:\n%s", tt.w, tt.h, got, tt.small, v)
		}
	}
	a := testApp(&fakeServices{}, 60, 10)
	if got, want := a.View(), "igris — terminal too small (need 50×20, now 60×10) · q quits"; got != want {
		t.Errorf("too small line:\n got %q\nwant %q", got, want)
	}
}

func TestAppTooSmallTakesOnlyQuit(t *testing.T) {
	tm := appProgram(t, &fakeServices{}, 38, 10)
	seen(t, tm, "50×20, now 38×10")
	key(tm, "?") // nothing to act on: no help opens
	click(tm, 1, 0)
	tm.Send(tea.WindowSizeMsg{Width: 80, Height: 24})
	seen(t, tm, "igris · sinjal")
	tm.Send(tea.WindowSizeMsg{Width: 38, Height: 10})
	key(tm, "q")
	if a := finalApp(t, tm); len(a.stack) != 1 {
		t.Errorf("stack has %d screens, want home only", len(a.stack))
	}
}

func TestAppSizeReachesEveryScreen(t *testing.T) {
	a := testApp(&fakeServices{}, 80, 24)
	low, high := &probe{name: "low"}, &probe{name: "high"}
	drive(a, push(low))
	drive(a, push(high))
	for _, p := range []*probe{low, high} {
		if len(p.sizes) != 1 || p.sizes[0] != (tea.WindowSizeMsg{Width: 80, Height: 24}) || p.inits != 1 {
			t.Errorf("%s on push: sizes %v, inits %d; want one 80×24 and Init once", p.name, p.sizes, p.inits)
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	for _, p := range []*probe{low, high} {
		if n := len(p.sizes); n != 2 || p.sizes[1] != (tea.WindowSizeMsg{Width: 50, Height: 20}) {
			t.Errorf("%s after a resize: sizes %v", p.name, p.sizes)
		}
	}
	if home := a.stack[0].(*homeScreen); home.w != 50 || home.h != 20 {
		t.Errorf("home is %d×%d, want 50×20", home.w, home.h)
	}
}

func TestAppMessagesGoToTheTop(t *testing.T) {
	a := testApp(&fakeServices{}, 80, 24)
	low, high := &probe{name: "low"}, &probe{name: "high"}
	drive(a, push(low))
	drive(a, push(high))
	type ping struct{}
	a.Update(ping{})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if len(low.msgs) != 0 || len(high.msgs) != 2 {
		t.Errorf("low got %v, high got %v; want both at the top", low.msgs, high.msgs)
	}
	if a.View() != "probe high" {
		t.Errorf("view %q, want the top screen's", a.View())
	}
}

func TestAppPopReplace(t *testing.T) {
	a := testApp(&fakeServices{}, 80, 24)
	low, high, other := &probe{name: "low"}, &probe{name: "high"}, &probe{name: "other"}
	drive(a, push(low))
	drive(a, push(high))

	drive(a, replace(other))
	if len(a.stack) != 3 || a.stack[2] != other || other.inits != 1 || len(other.sizes) != 1 {
		t.Fatalf("replace: stack %v, other inits %d sizes %v", a.stack, other.inits, other.sizes)
	}

	type accepted struct{ yes bool }
	if drive(a, pop(accepted{true})) {
		t.Fatal("popping a pushed screen quit the app")
	}
	if len(a.stack) != 2 || !low.got(accepted{true}) {
		t.Errorf("pop: stack %d screens, low got %v", len(a.stack), low.msgs)
	}
	drive(a, pop(nil))
	if len(a.stack) != 1 {
		t.Fatalf("stack %d screens, want home", len(a.stack))
	}
	if !drive(a, pop(nil)) || len(a.stack) != 1 {
		t.Errorf("popping home: want quit with home kept, stack %d", len(a.stack))
	}
}

func TestAppAsyncDropsStaleResults(t *testing.T) {
	type result struct{ n int }
	for _, order := range []string{"old first", "new first"} {
		t.Run(order, func(t *testing.T) {
			a := testApp(&fakeServices{}, 80, 24)
			p := &probe{name: "p"}
			drive(a, push(p))
			_, run1 := a.Update(async(p, "k", func() tea.Msg { return result{1} })())
			_, run2 := a.Update(async(p, "k", func() tea.Msg { return result{2} })())
			done1, done2 := run1(), run2()
			if order == "old first" {
				a.Update(done1)
				a.Update(done2)
			} else {
				a.Update(done2)
				a.Update(done1)
			}
			if len(p.msgs) != 1 || p.msgs[0] != (result{2}) {
				t.Errorf("got %v, want only the newest result", p.msgs)
			}
		})
	}
}

func TestAppAsyncKeysAreSeparate(t *testing.T) {
	type result struct{ k string }
	a := testApp(&fakeServices{}, 80, 24)
	p := &probe{name: "p"}
	drive(a, push(p))
	_, runA := a.Update(async(p, "a", func() tea.Msg { return result{"a"} })())
	_, runB := a.Update(async(p, "b", func() tea.Msg { return result{"b"} })())
	a.Update(runB())
	a.Update(runA())
	if !p.got(result{"a"}) || !p.got(result{"b"}) {
		t.Errorf("got %v, want both keys' results", p.msgs)
	}
}

func TestAppAsyncReachesAScreenBelowTheTop(t *testing.T) {
	type result struct{}
	a := testApp(&fakeServices{}, 80, 24)
	low, high := &probe{name: "low"}, &probe{name: "high"}
	drive(a, push(low))
	_, run := a.Update(async(low, "k", func() tea.Msg { return result{} })())
	drive(a, push(high))
	a.Update(run())
	if !low.got(result{}) || high.got(result{}) {
		t.Errorf("low got %v, high got %v; want the result at low", low.msgs, high.msgs)
	}
}

func TestAppAsyncDropsResultsForAPoppedScreen(t *testing.T) {
	type result struct{}
	a := testApp(&fakeServices{}, 80, 24)
	p := &probe{name: "p"}
	drive(a, push(p))
	_, run := a.Update(async(p, "k", func() tea.Msg { return result{} })())
	drive(a, pop(nil))
	a.Update(run())
	if len(p.msgs) != 0 || len(a.latest) != 0 {
		t.Errorf("a popped screen got %v; requests left %v", p.msgs, a.latest)
	}
	// Pushed again, it asks afresh; the old answer still doesn't count.
	drive(a, push(p))
	a.Update(run())
	if len(p.msgs) != 0 {
		t.Errorf("an answer from before the pop got through: %v", p.msgs)
	}
}

// fakeRun is a run handle of a started run that ends when it is stopped.
func fakeRun(res engine.Result) (*runHandle, *int) {
	feed := NewFeed()
	feed.Push(engine.Event{Kind: engine.RunStarted})
	stops := 0
	return &runHandle{feed: feed, stop: func() {
		stops++
		feed.End(res, nil)
	}}, &stops
}

func TestAppQuitStopsTheRun(t *testing.T) {
	for _, k := range []string{"ctrl+c", "quit"} {
		t.Run(k, func(t *testing.T) {
			a := testApp(&fakeServices{}, 80, 24)
			h, stops := fakeRun(engine.Result{Outcome: engine.Stopped, Phase: "M2"})
			drive(a, func() tea.Msg { return runMsg{h} })
			drive(a, push(&probe{name: "run view"}))
			var quit bool
			if k == "ctrl+c" {
				_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
				quit = drive(a, func() tea.Msg { return cmd() })
			} else {
				quit = drive(a, quitApp)
			}
			if !quit || *stops != 1 {
				t.Fatalf("quit %v, stops %d; want quit and the run stopped", quit, *stops)
			}
			r := a.finish()
			if !r.Stopped || r.Res.Phase != "M2" || r.RunErr != nil {
				t.Errorf("result %+v, want the stopped run's", r)
			}
		})
	}
}

func TestAppFinish(t *testing.T) {
	t.Run("no run", func(t *testing.T) {
		if r := testApp(&fakeServices{}, 80, 24).finish(); r.Stopped {
			t.Errorf("result %+v", r)
		}
	})
	t.Run("run ended on its own", func(t *testing.T) {
		a := testApp(&fakeServices{}, 80, 24)
		h, _ := fakeRun(engine.Result{})
		h.feed.End(engine.Result{Outcome: engine.Completed, Phase: "M1"}, nil)
		a.Update(runMsg{h})
		if r := a.finish(); r.Stopped || r.Res.Outcome != engine.Completed {
			t.Errorf("result %+v, want the completed run, not stopped", r)
		}
	})
	t.Run("run gone", func(t *testing.T) {
		a := testApp(&fakeServices{}, 80, 24)
		h, _ := fakeRun(engine.Result{})
		a.Update(runMsg{h})
		a.Update(runMsg{nil})
		if r := a.finish(); r.Stopped {
			t.Errorf("result %+v, want no run", r)
		}
	})
}

func TestAppStart(t *testing.T) {
	for _, s := range []Start{nil, Home{}, Wizard{Mode: "plan", Through: "M3", ForceUnlock: true}} {
		a := newApp(context.Background(), AppOptions{Services: &fakeServices{}, Start: s, Poll: -1}, &theme{})
		if len(a.stack) != 1 {
			t.Fatalf("start %#v: %d screens", s, len(a.stack))
		}
		if _, ok := a.stack[0].(*homeScreen); !ok {
			t.Errorf("start %#v: bottom screen %T, want home", s, a.stack[0])
		}
	}
}
