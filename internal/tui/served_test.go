package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/report"
)

// Home with a run igris serve hosts (SPEC §15.6, since 1.4): Attach
// first on the NOW card, the attached view's Home detaches, and quitting
// the app with the view up detaches too.

// fakeAttached is an AttachedRunner: the run igris serve hosts feeds the
// feed itself, and Run ends with ErrDetached when its ctx ends first (the
// run goes on); a stop command ends it as stopped.
type fakeAttached struct {
	feed *Feed
	mu   sync.Mutex
	sent []engine.Command
	stop chan struct{}
	once sync.Once
}

func newFakeAttached(feed *Feed) *fakeAttached {
	return &fakeAttached{feed: feed, stop: make(chan struct{})}
}

func (r *fakeAttached) Run(ctx context.Context) (engine.Result, error) {
	r.feed.Push(engine.Event{Kind: engine.RunStarted, Detail: "phase M2", At: t0})
	r.feed.Push(engine.Event{Kind: engine.PhaseStarted, Phase: "M2", At: t0})
	select {
	case <-ctx.Done():
		return engine.Result{}, ErrDetached
	case <-r.stop:
		r.feed.Push(engine.Event{Kind: engine.RunStopped, Detail: "stopped", At: t0})
		return engine.Result{Outcome: engine.Stopped, Phase: "M2"}, nil
	}
}

func (r *fakeAttached) Send(c engine.Command) {
	r.mu.Lock()
	r.sent = append(r.sent, c)
	r.mu.Unlock()
	if c.Kind == engine.CmdStop {
		r.once.Do(func() { close(r.stop) })
	}
}

func (r *fakeAttached) ClientVersion() string { return "1.4.0" }

func (r *fakeAttached) commands() []engine.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]engine.Command(nil), r.sent...)
}

// attachedRuns hands out fakeAttached runners and remembers the last.
type attachedRuns struct {
	mu   sync.Mutex
	last *fakeAttached
}

func (a *attachedRuns) attach(feed *Feed) (AttachedRunner, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = newFakeAttached(feed)
	return a.last, nil
}

func (a *attachedRuns) get() *fakeAttached {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last
}

// fixture is the home state named in homeStates.
func fixture(t *testing.T, name string) homeFixture {
	t.Helper()
	for _, st := range homeStates {
		if st.name == name {
			return st.fix()
		}
	}
	t.Fatalf("no home state %q", name)
	return homeFixture{}
}

func TestHomeServedOffersAttachFirst(t *testing.T) {
	m := homeAt(&theme{}, 120, 40, fixture(t, "served"))
	if b := m.buttons(); len(b) == 0 || b[0].label != "Attach" || b[0].act != actAttach {
		t.Errorf("buttons = %v, want Attach first", b)
	}
	for _, want := range []string{"Running elsewhere · in igris serve", "Attach (t) shows its run view"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("home lacks %q:\n%s", want, m.View())
		}
	}
	// A run another igris holds without a socket is only watched.
	m = homeAt(&theme{}, 120, 40, fixture(t, "running_elsewhere"))
	for _, b := range m.buttons() {
		if b.act == actAttach {
			t.Error("Attach offered for a run no serve hosts")
		}
	}
	if !strings.Contains(m.View(), "Running elsewhere · in another igris") {
		t.Errorf("home:\n%s", m.View())
	}
}

// servedProgram runs the app on the served project, attaching through ar.
func servedProgram(t *testing.T, ar *attachedRuns) (*teatest.TestModel, *fakeServices) {
	t.Helper()
	svc := &fakeServices{attach: ar.attach}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return fixture(t, "served").snap, nil }
	tm := appProgram(t, svc, 120, 40)
	seen(t, tm, "Running elsewhere · in igris serve")
	return tm, svc
}

func TestHomeAttachHomeDetaches(t *testing.T) {
	ar := &attachedRuns{}
	tm, svc := servedProgram(t, ar)
	tm.Type("t")
	seen(t, tm, "[q Home]", "phase M2")
	tm.Type("q")
	seen(t, tm, "Leave the run view?")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Home, focused
	seen(t, tm, "detached; the run goes on in igris serve")
	tm.Type("q")
	a := finalApp(t, tm)
	if len(a.stack) != 1 || a.run != nil {
		t.Errorf("stack %d, run %v; want home alone and no run", len(a.stack), a.run)
	}
	if r := a.finish(); r.Detached || r.Stopped {
		t.Errorf("finish %+v, want nothing to report: the run was left earlier", r)
	}
	if got := ar.get().commands(); len(got) != 0 {
		t.Errorf("detaching sent %v", got)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.calls["Attach"] != 1 {
		t.Errorf("Attach called %d times", svc.calls["Attach"])
	}
}

func TestHomeAttachStopRun(t *testing.T) {
	ar := &attachedRuns{}
	tm, _ := servedProgram(t, ar)
	tm.Type("t")
	seen(t, tm, "[q Home]")
	tm.Type("q")
	seen(t, tm, "Leave the run view?")
	tm.Type("2") // Stop run
	seen(t, tm, "igris stopped; a running session keeps running")
	tm.Type("q")
	if a := finalApp(t, tm); len(a.stack) != 1 || a.run != nil {
		t.Errorf("stack %d, run %v", len(a.stack), a.run)
	}
	if got := ar.get().commands(); len(got) != 1 || got[0].Kind != engine.CmdStop {
		t.Errorf("sent %v, want one stop", got)
	}
}

func TestHomeAttachQuitDetaches(t *testing.T) {
	ar := &attachedRuns{}
	tm, _ := servedProgram(t, ar)
	tm.Type("t")
	seen(t, tm, "[q Home]")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	a := finalApp(t, tm)
	if r := a.finish(); !r.Detached || r.Stopped {
		t.Errorf("finish %+v, want detached", r)
	}
	if got := ar.get().commands(); len(got) != 0 {
		t.Errorf("quitting sent %v", got)
	}
}

func TestHomeAttachFails(t *testing.T) {
	svc := &fakeServices{attach: func(*Feed) (AttachedRunner, error) { return nil, errors.New("no run answers on the socket") }}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return fixture(t, "served").snap, nil }
	tm := appProgram(t, svc, 120, 40)
	seen(t, tm, "Running elsewhere · in igris serve")
	tm.Type("t")
	seen(t, tm, "attach: no run answers on the socket")
	tm.Type("q")
	if a := finalApp(t, tm); len(a.stack) != 1 || a.run != nil {
		t.Errorf("stack %d, run %v", len(a.stack), a.run)
	}
}

// A run home's Arise starts is served too: its view asks Home · Stop run
// · Cancel, and quitting the app with it up detaches.
func TestHomeAriseServedQuitDetaches(t *testing.T) {
	ar := &attachedRuns{}
	svc := &fakeServices{startFeed: func(_ report.RunRequest, _ report.Confirmations, feed *Feed) (Runner, error) { return ar.attach(feed) }}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return homeSnap("ready"), nil }
	tm := appProgram(t, svc, 120, 40)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	seen(t, tm, "[q Home]", "phase M2")
	tm.Type("q")
	seen(t, tm, "Leave the run view?")
	tm.Type("3") // Cancel: stay
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	a := finalApp(t, tm)
	if r := a.finish(); !r.Detached || r.Stopped {
		t.Errorf("finish %+v, want detached", r)
	}
	if got := ar.get().commands(); len(got) != 0 {
		t.Errorf("sent %v", got)
	}
}
