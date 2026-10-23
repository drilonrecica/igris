package tui

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/serve"
)

// patience bounds a wait on the socket's other end; tests never sleep.
const patience = 5 * time.Second

// wire is igris serve between the test and the run view: the harness's
// events are published by a serve.Server and reach the model through a
// serve.Client over net.Pipe and a Feed, as in igris attach.
type wire struct {
	t      *testing.T
	srv    *serve.Server
	feed   *Feed
	client *serve.Client

	mu   sync.Mutex
	tail func(context.Context, string, int) ([]string, error)
}

// attachedHarness is newHarness with its run served: the view starts from
// serve's snapshot.
func attachedHarness(t *testing.T, w, h int) *harness {
	t.Helper()
	hs := newHarness(t, w, h)
	wr := &wire{t: t}
	wr.srv = serve.New(serve.Options{Version: "1.4.0", Project: "sinjal", Backend: "herdr", Mode: "plan", Tail: wr.readTail})
	wr.attach(hs)
	return hs
}

// attach connects a client to the wire's server and points hs's model at
// it, taking the snapshot.
func (wr *wire) attach(hs *harness) {
	wr.t.Helper()
	wr.feed = NewFeed()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := serve.Attach(ctx, serve.ClientOptions{
		Name: "igris 1.4.0", Sink: wr.feed,
		Dial: func(context.Context) (net.Conn, error) {
			cs, cc := net.Pipe()
			go wr.srv.ServeConn(cs)
			return cc, nil
		},
	})
	if err != nil {
		cancel()
		wr.t.Fatal(err)
	}
	wr.t.Cleanup(func() {
		c.Close()
		cancel()
	})
	wr.client = c
	hs.wire = wr
	hs.m.opts.Feed, hs.m.opts.Sender, hs.m.opts.Attached, hs.m.opts.Version = wr.feed, c, true, "1.4.0"
	wr.drain(hs, 0)
}

// events publishes evs and hands the model what the feed delivers until
// all of them arrived.
func (wr *wire) events(hs *harness, evs []engine.Event) {
	wr.t.Helper()
	for _, ev := range evs {
		wr.srv.Publish(ev)
	}
	wr.drain(hs, len(evs))
}

// drain applies the feed's batches until n events and any snapshot
// arrived.
func (wr *wire) drain(hs *harness, n int) {
	wr.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	got, snapped := 0, n > 0
	for got < n || !snapped {
		b, ok := wr.feed.next(ctx, false)
		if !ok {
			wr.t.Fatalf("got %d of %d events through the socket", got, n)
		}
		got += len(b.events)
		snapped = snapped || b.snap != nil
		hs.m.Update(b)
	}
}

func (wr *wire) readTail(ctx context.Context, task string, n int) ([]string, error) {
	wr.mu.Lock()
	tail := wr.tail
	wr.mu.Unlock()
	if tail == nil {
		return nil, nil
	}
	return tail(ctx, task, n)
}

// setTail makes serve read the session's tail from tail; the view reads
// it through the client, then refreshes once.
func (wr *wire) setTail(hs *harness, tail func(context.Context, string, int) ([]string, error)) {
	wr.mu.Lock()
	wr.tail = tail
	wr.mu.Unlock()
	hs.m.opts.Tail = wr.client.TailTask
}

// The run view attached to a served run draws what it draws for a run in
// this process: every layout golden, through the socket.
func TestAttachedLayoutGoldens(t *testing.T) {
	sizes := []struct {
		w, h   int
		prefix string
	}{{120, 40, "wide_"}, {50, 20, "narrow_50x20_"}, {80, 24, "narrow_80x24_"}}
	for _, sz := range sizes {
		for _, st := range layoutStates {
			t.Run(sz.prefix+st.name, func(t *testing.T) {
				hs := attachedHarness(t, sz.w, sz.h)
				hs.m.loc = time.UTC
				hs.withPlan(demoPlan)
				st.setup(hs)
				golden(t, sz.prefix+st.name, hs.m.View())
			})
		}
	}
}

// A view that attaches later starts from serve's snapshot and draws the
// same: the card, the question, the log of the last events. (A question
// one client closed with esc is open again in a new one.)
func TestAttachedSnapshotGoldens(t *testing.T) {
	sizes := []struct {
		w, h   int
		prefix string
	}{{120, 40, "wide_"}, {50, 20, "narrow_50x20_"}, {80, 24, "narrow_80x24_"}}
	for _, sz := range sizes {
		for _, st := range layoutStates {
			if st.name == "question_closed" {
				continue
			}
			t.Run(sz.prefix+st.name, func(t *testing.T) {
				hs := attachedHarness(t, sz.w, sz.h)
				hs.withPlan(demoPlan)
				st.setup(hs)

				late := newHarness(t, sz.w, sz.h)
				late.m.loc, late.now = time.UTC, hs.now
				late.m.opts.RankDurations = hs.m.opts.RankDurations
				late.withPlan(demoPlan)
				wr := &wire{t: t, srv: hs.wire.srv}
				wr.attach(late)
				if hs.m.opts.Tail != nil {
					wr.setTail(late, hs.wire.tail)
					late.refreshTail()
				}
				golden(t, sz.prefix+st.name, late.m.View())
			})
		}
	}
}

// running puts an attached harness in a run with M0-03 going.
func running(t *testing.T) *harness {
	t.Helper()
	hs := attachedHarness(t, 120, 40)
	hs.withPlan(demoPlan)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"}, started("M0-03"), opened("M0-03"))
	return hs
}

// detached points the view's commands at a recorder instead of the wire.
func detached(hs *harness) *sender {
	s := &sender{}
	hs.m.opts.Sender = s
	return s
}

// q asks Detach · Stop run · Cancel, with Detach focused; Detach leaves
// and sends nothing.
func TestAttachedQuitAsksDetach(t *testing.T) {
	hs := running(t)
	s := detached(hs)
	if msg := hs.key("q"); msg != nil {
		t.Fatalf("q = %v, want the dialog", msg)
	}
	d := hs.m.dialog
	if d == nil || d.title != "Leave the run view?" || d.options[d.selected].label != "Detach" {
		t.Fatalf("dialog = %+v", d)
	}
	var labels []string
	for _, o := range d.options {
		labels = append(labels, o.label)
	}
	if got := strings.Join(labels, " · "); got != "Detach · Stop run · Cancel" {
		t.Errorf("options = %s", got)
	}
	if _, ok := hs.key("enter").(tea.QuitMsg); !ok {
		t.Error("Detach did not quit the view")
	}
	if c := s.take(); len(c) != 0 {
		t.Errorf("Detach sent %v", c)
	}
}

func TestAttachedEscCancels(t *testing.T) {
	hs := running(t)
	hs.key("q")
	if msg := hs.key("esc"); msg != nil || hs.m.dialog != nil {
		t.Fatalf("esc = %v, dialog %+v", msg, hs.m.dialog)
	}
}

// Stop run stops the run as x does and leaves once it has stopped.
func TestAttachedStopRun(t *testing.T) {
	hs := running(t)
	s := detached(hs)
	hs.key("q")
	hs.key("down")
	if msg := hs.key("enter"); msg != nil {
		t.Fatalf("Stop run = %v, want to wait for the stop", msg)
	}
	if c := s.take(); len(c) != 1 || c[0].Kind != engine.CmdStop {
		t.Fatalf("sent %v, want a stop", c)
	}
	_, cmd := hs.m.Update(batch{events: []engine.Event{{Kind: engine.RunStopped, At: t0, Detail: "stopped"}}})
	if _, ok := run(cmd).(tea.QuitMsg); !ok {
		t.Error("the view stayed after the run stopped")
	}
}

// Pushed by home, Detach reads Home and goes back there.
func TestAttachedDetachHome(t *testing.T) {
	hs := running(t)
	type homeMsg struct{}
	hs.m.opts.Leave = func() tea.Msg { return homeMsg{} }
	hs.key("q")
	if d := hs.m.dialog; d == nil || d.options[0].label != "Home" {
		t.Fatalf("dialog = %+v", d)
	}
	if _, ok := hs.key("enter").(homeMsg); !ok {
		t.Error("Home did not go home")
	}
}

// Once the run ended, and on ctrl+c, the view closes without asking.
func TestAttachedLeavesWithoutAsking(t *testing.T) {
	hs := running(t)
	if _, ok := hs.key("ctrl+c").(tea.QuitMsg); !ok {
		t.Error("ctrl+c did not detach")
	}
	hs.events(engine.Event{Kind: engine.RunStopped, Detail: "completed"})
	if _, ok := hs.key("q").(tea.QuitMsg); !ok {
		t.Errorf("q after the end = dialog %+v", hs.m.dialog)
	}
}

// A view in this process quits on q as before: nothing to detach from.
func TestInProcessQuitDoesNotAsk(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.events(started("M0-03"))
	if _, ok := hs.key("q").(tea.QuitMsg); !ok {
		t.Errorf("q = dialog %+v", hs.m.dialog)
	}
}

// While the connection is lost the header says so, the bar offers only
// Quit and nothing reaches the run; the fresh snapshot brings the run
// back, its question included.
func TestAttachedReconnect(t *testing.T) {
	hs := running(t)
	s := detached(hs)
	hs.m.Update(batch{lost: true})
	v := hs.m.View()
	if !strings.Contains(v, "reconnecting…") {
		t.Errorf("view lacks reconnecting…:\n%s", v)
	}
	if b := hs.m.buttons(); len(b) != 1 || b[0].act != actQuit {
		t.Errorf("buttons = %+v, want only Quit", b)
	}
	hs.keys("p", "x", "d")
	if c := s.take(); len(c) != 0 || hs.m.dialog != nil {
		t.Errorf("while lost: sent %v, dialog %+v", c, hs.m.dialog)
	}
	if msg := hs.key("q"); msg == nil {
		t.Error("q while lost did not leave")
	}

	q := serve.Event{V: 1, At: t0, Type: string(engine.Asked), Task: "M0-03", Question: string(engine.QuestionCommit), Detail: "commit?"}
	snap := serve.Snapshot{
		Type: serve.TypeSnapshot, Server: "igris 1.4.0", Phase: "M0", Mode: "auto", Backend: "tmux",
		Current:  &serve.Current{Task: "M0-03", Title: "Makefile", Rank: "sonnet", Model: "sonnet", Started: t0, State: serve.StateQuestion, Since: t0},
		Question: &q,
		Feed:     []serve.Event{{V: 1, At: t0, Type: string(engine.PauseOn)}},
	}
	hs.m.Update(batch{snap: &snap})
	v = hs.m.View()
	for _, want := range []string{"mode: auto", "tmux", "Commit M0-03?"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if len(hs.m.log) != 1 || hs.m.log[0].text != "pause after task: on" {
		t.Errorf("log = %+v, want the snapshot's feed", hs.m.log)
	}
	if strings.Contains(v, "reconnecting…") || hs.m.lost {
		t.Error("still reconnecting after the snapshot")
	}
}

// The header names another serving igris, and the log says the run keeps
// it until it ends.
func TestAttachedServedByAnotherVersion(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.m.opts.Attached, hs.m.opts.Version = true, "1.4.1"
	hs.m.Update(batch{snap: &serve.Snapshot{Type: serve.TypeSnapshot, Server: "igris 1.4.0", Phase: "M0"}})
	v := hs.m.View()
	for _, want := range []string{"served by igris 1.4.0", "keeps that igris until it ends"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	hs.m.Update(batch{snap: &serve.Snapshot{Type: serve.TypeSnapshot, Server: "igris 1.4.1", Phase: "M0"}})
	if v := hs.m.View(); strings.Contains(v, "served by") {
		t.Errorf("same version named:\n%s", v)
	}
}

// Commands from the view reach serve's run through the client.
func TestAttachedCommandsReachTheRun(t *testing.T) {
	got := make(chan engine.Command, 4)
	hs := newHarness(t, 120, 40)
	wr := &wire{t: t}
	wr.srv = serve.New(serve.Options{Version: "1.4.0", Sender: chanSender(got)})
	wr.attach(hs)
	hs.withPlan(demoPlan)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0"}, started("M0-03"), opened("M0-03"))
	hs.key("p")
	select {
	case c := <-got:
		if c.Kind != engine.CmdPause {
			t.Errorf("run got %+v, want pause", c)
		}
	case <-time.After(patience):
		t.Fatal("the pause did not reach the run")
	}
}

type chanSender chan engine.Command

func (c chanSender) Send(cmd engine.Command) { c <- cmd }

// A snapshot supersedes the events the feed still holds; a loss comes
// after the events before it.
func TestFeedSnapshotAndLost(t *testing.T) {
	f := NewFeed()
	f.Push(engine.Event{Kind: engine.Warning})
	f.Snapshot(serve.Snapshot{Phase: "M1"})
	f.Push(engine.Event{Kind: engine.PauseOn})
	f.Lost()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	b, ok := f.next(ctx, false)
	if !ok || b.snap == nil || b.snap.Phase != "M1" || len(b.events) != 1 || b.events[0].Kind != engine.PauseOn || !b.lost {
		t.Fatalf("batch = %+v", b)
	}
	select {
	case <-f.Started():
	default:
		t.Error("a snapshot did not start the feed")
	}
	f.Snapshot(serve.Snapshot{})
	if b, _ := f.next(ctx, false); b.lost {
		t.Error("a snapshot after a loss still says lost")
	}
}
