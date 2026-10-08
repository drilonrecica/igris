package tui

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
)

// pollApp is an app on a fake project, started and settled, with the poll
// off: the tests send its ticks.
func pollApp(t *testing.T, svc *fakeServices) (*appModel, *homeScreen) {
	t.Helper()
	a := newApp(context.Background(), AppOptions{Services: svc, Poll: -1}, &theme{})
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	home := a.stack[0].(*homeScreen)
	// Init's commands; the poll is off here, because driving it would
	// wait 2 s for every tick, forever. The tests send the ticks.
	for _, c := range []tea.Cmd{home.refresh(), home.checkBackend(), home.runDoctor()} {
		drive(a, c)
	}
	return a, home
}

func stampOf(size int64) report.Stamp {
	return report.Stamp{Plan: report.FileStamp{Exists: true, Size: size, ModTime: homeNow}}
}

func TestHomePollRereadsOnlyWhatChanged(t *testing.T) {
	svc := &fakeServices{stamp: stampOf(1)}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) {
		s := fakeSnapshot("sinjal")
		s.Stamp = svc.stamp
		return s, nil
	}
	a, home := pollApp(t, svc)
	if n := svc.called("Snapshot"); n != 1 {
		t.Fatalf("Snapshot called %d times after opening, want 1", n)
	}

	// A tick stats the files and, nothing changed, reads nothing.
	drive(a, func() tea.Msg { return ownedMsg{home, pollMsg{}} })
	if n := svc.called("Snapshot"); n != 1 {
		t.Errorf("an unchanged project was read again: %d reads", n)
	}
	if n := svc.called("Stamp"); n != 1 {
		t.Errorf("Stamp called %d times, want 1", n)
	}

	// A watched file changed: the next tick reads the project.
	svc.stamp = stampOf(2)
	drive(a, func() tea.Msg { return ownedMsg{home, pollMsg{}} })
	if n := svc.called("Snapshot"); n != 2 {
		t.Errorf("a changed file: %d reads, want 2", n)
	}
	if home.stamp != svc.stamp {
		t.Errorf("home keeps stamp %v, want %v", home.stamp, svc.stamp)
	}
	// The slow checks never run on the poll.
	if svc.called("BackendAvailable") != 1 || svc.called("Doctor") != 1 {
		t.Errorf("the poll ran herdr %d times and doctor %d times, want once each", svc.called("BackendAvailable"), svc.called("Doctor"))
	}
}

func TestHomePollSkipsWhileReading(t *testing.T) {
	svc := &fakeServices{stamp: stampOf(1)}
	a, home := pollApp(t, svc)
	home.reading = true
	a.Update(stampMsg{stampOf(5)})
	if home.reading && svc.called("Snapshot") != 1 {
		t.Errorf("a second read started while one was going")
	}
}

func TestHomePollSchedulesItself(t *testing.T) {
	a := newApp(context.Background(), AppOptions{Services: &fakeServices{}, Poll: time.Millisecond}, &theme{})
	home := a.stack[0].(*homeScreen)
	msg := home.nextPoll()()
	o, ok := msg.(ownedMsg)
	if !ok || o.owner != home {
		t.Fatalf("tick gave %#v, want a message owned by home", msg)
	}
	_, cmd := a.Update(msg)
	if cmd == nil {
		t.Error("the tick scheduled neither the stat nor the next tick")
	}
	off := newApp(context.Background(), AppOptions{Services: &fakeServices{}, Poll: -1}, &theme{})
	if off.stack[0].(*homeScreen).nextPoll() != nil {
		t.Error("a negative Poll still polls")
	}
	if def := newApp(context.Background(), AppOptions{Services: &fakeServices{}}, &theme{}).stack[0].(*homeScreen); def.poll != 2*time.Second {
		t.Errorf("default poll %v, want 2s", def.poll)
	}
}

func TestAppTicksReachHomeUnderAPage(t *testing.T) {
	svc := &fakeServices{stamp: stampOf(1)}
	a, home := pollApp(t, svc)
	top := &probe{name: "page"}
	drive(a, push(top))
	svc.stamp = stampOf(2)
	drive(a, func() tea.Msg { return ownedMsg{home, pollMsg{}} })
	if n := svc.called("Snapshot"); n != 2 {
		t.Errorf("a tick under a page: %d reads, want 2", n)
	}
	if len(top.msgs) != 0 {
		t.Errorf("the page got home's tick: %v", top.msgs)
	}
	// A tick for a screen that has left is dropped.
	gone := &probe{name: "gone"}
	if _, cmd := a.Update(ownedMsg{gone, pollMsg{}}); cmd != nil {
		t.Error("a tick for a screen off the stack did something")
	}
}

func TestAppRefreshesOnFocusAndReturn(t *testing.T) {
	svc := &fakeServices{}
	a, home := pollApp(t, svc)
	reads := func() int { return svc.called("Snapshot") }
	base := reads()

	drive(a, func() tea.Msg { return tea.FocusMsg{} })
	if reads() != base+1 {
		t.Errorf("focus: %d reads, want %d", reads(), base+1)
	}
	if home.reading {
		t.Error("the read never finished")
	}

	page := &probe{name: "page"}
	drive(a, push(page))
	drive(a, pop(nil))
	if reads() != base+2 {
		t.Errorf("return from a page: %d reads, want %d", reads(), base+2)
	}
	if svc.called("BackendAvailable") != 1 {
		t.Errorf("a refresh asked herdr again: %d", svc.called("BackendAvailable"))
	}
}

func TestAppAppliesTheConfigLive(t *testing.T) {
	th := newTheme(lipgloss.NewRenderer(io.Discard), true, nil)
	a := newApp(context.Background(), AppOptions{Services: &fakeServices{}, Poll: -1, Mouse: true, Theme: "dark"}, th)
	dark := th.looks[lookAccent].color

	same := config.TUI{Mouse: true, Theme: "dark", RankColors: nil}
	if _, cmd := a.Update(configMsg{same}); cmd != nil {
		t.Error("an unchanged config did something")
	}
	if th.looks[lookAccent].color != dark {
		t.Error("an unchanged config repainted")
	}

	_, cmd := a.Update(configMsg{config.TUI{Mouse: false, Theme: "light", RankColors: map[string]string{"opus": "#112233"}}})
	if cmd == nil {
		t.Error("mouse turned off: no command")
	}
	if th.looks[lookAccent].color == dark || th.ranks["opus"] != "#112233" {
		t.Errorf("theme not reapplied: accent %q, opus %q", th.looks[lookAccent].color, th.ranks["opus"])
	}
	if a.o.Mouse || a.o.Theme != "light" {
		t.Errorf("options not updated: %+v", a.o)
	}
	if _, cmd := a.Update(configMsg{config.TUI{Mouse: true, Theme: "light", RankColors: map[string]string{"opus": "#112233"}}}); cmd == nil {
		t.Error("mouse turned on: no command")
	}
}

// A read of a good igris.toml hands its [tui] section to the app; a
// broken one doesn't, so a typo can't reset the theme.
func TestHomeSendsTheConfigOnlyWhenValid(t *testing.T) {
	_, home := pollApp(t, &fakeServices{})
	good := homeSnap("ready")
	if _, cmd := home.Update(snapshotMsg{s: good}); cmd == nil {
		t.Fatal("a valid config isn't sent")
	} else if _, ok := cmd().(configMsg); !ok {
		t.Errorf("got %#v, want a configMsg", cmd())
	}
	bad := homeSnap("ready")
	bad.ConfigProblems = []string{"igris.toml:3: bad"}
	if _, cmd := home.Update(snapshotMsg{s: bad}); cmd != nil {
		t.Error("a broken config was sent")
	}
}

// largeHome is home at 120×40 on the §17 large fixture (150+ tasks).
func largeHome(tb testing.TB) *homeScreen {
	tb.Helper()
	raw, err := os.ReadFile("../plan/testdata/large.md")
	if err != nil {
		tb.Fatal(err)
	}
	cfg := config.Default()
	s := &report.Snapshot{Root: "/src/large", Project: "large", Found: true, Config: cfg, ConfigPath: "/src/large/igris.toml", PlanPath: "/src/large/tasks.md", Recent: homeRecent()}
	s.Plan = plan.Parse("tasks.md", raw, plan.Options{})
	if s.Issues = report.Issues(s.Plan.Validate(cfg.Rules())); len(s.Issues) > 0 {
		tb.Fatalf("large fixture invalid: %v", s.Issues)
	}
	st, err := report.Status(s.Plan, "")
	if err != nil {
		tb.Fatal(err)
	}
	s.Status = &st
	if len(s.Plan.Tasks) < 150 {
		tb.Fatalf("large fixture has %d tasks, want 150+", len(s.Plan.Tasks))
	}
	return homeAt(newTheme(lipgloss.NewRenderer(io.Discard), true, nil), 120, 40, homeFixture{snap: s, doctor: homeDoctor, doctorDone: true})
}

func BenchmarkHomeView(b *testing.B) {
	m := largeHome(b)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// View must stay under 2 ms at 120×40 on the large plan (SPEC §15.6).
func TestHomeViewIsFast(t *testing.T) {
	if raceBuild || testing.Short() {
		t.Skip("frame-time budget is measured without -race and -short")
	}
	m := largeHome(t)
	m.View() // warm up
	const n = 100
	start := time.Now()
	for range n {
		_ = m.View()
	}
	if per := time.Since(start) / n; per > 2*time.Millisecond {
		t.Errorf("View takes %v at 120×40 on the large plan, want < 2ms", per)
	}
}
