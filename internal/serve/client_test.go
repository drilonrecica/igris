package serve

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// sink records what a Client hands on, in order.
type sink struct {
	mu     sync.Mutex
	snaps  []Snapshot
	events []engine.Event
	log    []string // "snapshot", "lost", "end", and each event's kind
	res    engine.Result
	err    error
	wake   chan struct{}
}

func newSink() *sink { return &sink{wake: make(chan struct{}, 1)} }

func (s *sink) note(what string) {
	s.log = append(s.log, what)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sink) Snapshot(snap Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snaps = append(s.snaps, snap)
	s.note("snapshot")
}

func (s *sink) Push(ev engine.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	s.note(string(ev.Kind))
}

func (s *sink) Lost() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("lost")
}

func (s *sink) End(res engine.Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.res, s.err = res, err
	s.note("end")
}

// waitFor waits until ok holds for the sink's record.
func (s *sink) waitFor(t *testing.T, what string, ok func(s *sink) bool) {
	t.Helper()
	deadline := time.After(patience)
	for {
		s.mu.Lock()
		done := ok(s)
		s.mu.Unlock()
		if done {
			return
		}
		select {
		case <-s.wake:
		case <-deadline:
			s.mu.Lock()
			defer s.mu.Unlock()
			t.Fatalf("waited for %s; got %v", what, s.log)
		}
	}
}

func (s *sink) count(what string) int {
	n := 0
	for _, l := range s.log {
		if l == what {
			n++
		}
	}
	return n
}

// pipes dials s over net.Pipe; kill closes the server ends so far, as a
// slow client's drop does. Once gone is set, Dial answers ErrNotServed.
type pipes struct {
	s *Server

	mu    sync.Mutex
	ends  []net.Conn
	gone  bool
	dials int
}

func (p *pipes) dial(context.Context) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dials++
	if p.gone {
		return nil, fmt.Errorf("%w (nothing answers on test)", ErrNotServed)
	}
	cs, cc := net.Pipe()
	p.ends = append(p.ends, cs)
	go p.s.ServeConn(cs)
	return cc, nil
}

func (p *pipes) kill(gone bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gone = gone
	for _, c := range p.ends {
		_ = c.Close()
	}
	p.ends = nil
}

// attachTo attaches a Client to s over pipes.
func attachTo(t *testing.T, s *Server) (*Client, *sink, *pipes) {
	t.Helper()
	p := &pipes{s: s}
	k := newSink()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	t.Cleanup(cancel)
	c, err := Attach(ctx, ClientOptions{Name: "igris 1.4.1", Dial: p.dial, Sink: k, Retry: time.Millisecond})
	if err != nil {
		t.Fatalf("Attach = %v", err)
	}
	t.Cleanup(c.Close)
	return c, k, p
}

// wireEvents are events of every shape protocol 1 carries.
func wireEvents() []engine.Event {
	at := time.Date(2026, 10, 22, 9, 41, 0, 0, time.UTC)
	waiting := plan.WaitingText("M0-02", []string{"M0-01"}, "M0-02 waits on M0-01 (ready, phase M0)")
	return []engine.Event{
		{At: at, Kind: engine.RunStarted, Run: "r1", Phase: "M0", Detail: "phase M0", Slice: []string{"M0-01", "M0-02"}},
		{At: at, Kind: engine.TaskStarted, Phase: "M0", Task: "M0-01", Title: "A", Rank: "sonnet", Model: "sonnet", Mode: "plan",
			Changes: []plan.Change{{ID: "M0-01", From: plan.Ready, To: plan.InProgress}}},
		{At: at, Kind: engine.SessionOpened, Phase: "M0", Task: "M0-01", Mode: "plan", Session: &backend.SessionRef{Backend: "tmux", PaneID: "%1"}, ClaudeSession: "u-1"},
		{At: at, Kind: engine.UsageRead, Phase: "M0", Task: "M0-01", Tokens: &engine.Tokens{In: 10, Out: 2, Models: []string{"claude-sonnet"}}},
		{At: at, Kind: engine.QuotaRead, Quota: &state.Quota{}},
		{At: at, Kind: engine.Asked, Phase: "M0", Task: "M0-01", Question: engine.QuestionPlanApproval, Detail: "approve?", Plan: "# Plan\n\n1. do it", Then: "accept"},
		{At: at, Kind: engine.HookFailed, Phase: "M0", Task: "M0-01", Detail: "before_task failed", Output: []string{"line\tone", "two"}},
		{At: at, Kind: engine.VerifyStarted, Phase: "M0", Task: "M0-01", Verify: "fast", Detail: "make test"},
		{At: at, Kind: engine.PhaseStuck, Phase: "M0", Waiting: []plan.Waiting{waiting}},
		{At: at, Kind: engine.RunStopped, Phase: "M0", Detail: "stuck"},
	}
}

// The events reach the client as the engine emitted them.
func TestClientEvents(t *testing.T) {
	s := New(Options{Version: "1.4.0", Mode: "plan"})
	_, k, _ := attachTo(t, s)
	want := wireEvents()
	for _, ev := range want {
		s.Publish(ev)
	}
	k.waitFor(t, "the end", func(s *sink) bool { return s.count("end") == 1 })
	if len(k.events) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(k.events), len(want), k.log)
	}
	for i, got := range k.events {
		w := want[i]
		if got.Kind == engine.PhaseStuck {
			if len(got.Waiting) != 1 || got.Waiting[0].String() != w.Waiting[0].String() || got.Waiting[0].Task.ID != "M0-02" {
				t.Errorf("waiting = %v, want %v", got.Waiting, w.Waiting)
			}
			got.Waiting, w.Waiting = nil, nil
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("event %d:\n got %+v\nwant %+v", i, got, w)
		}
	}
	if k.res.Outcome != engine.Stuck || k.res.Phase != "M0" || k.err != nil {
		t.Errorf("end = %+v, %v; want stuck in M0", k.res, k.err)
	}
}

// chanSender passes the run's commands on a channel.
type chanSender chan engine.Command

func (c chanSender) Send(cmd engine.Command) { c <- cmd }

// Every engine command goes over the socket and reaches the run as sent.
func TestClientSend(t *testing.T) {
	snd := make(chanSender, 16)
	s := New(Options{Version: "1.4.0", Sender: snd})
	c, _, _ := attachTo(t, s)
	cmds := []engine.Command{
		{Kind: engine.CmdPause},
		{Kind: engine.CmdStop},
		{Kind: engine.CmdDone, Text: "note"},
		{Kind: engine.CmdSkip, Text: "reason"},
		{Kind: engine.CmdRetry, Continue: true},
		{Kind: engine.CmdAnswer, Yes: true, Question: engine.QuestionConfirmReset, Task: "M0-01"},
		{Kind: engine.CmdMode, Text: engine.ModeYolo, Yes: true},
		{Kind: engine.CmdTaskMode, Task: "M0-02", Text: engine.ModeAccept},
		{Kind: engine.CmdPlan, Plan: state.PlanDeny, Text: "smaller"},
	}
	for _, cmd := range cmds {
		c.Send(cmd)
	}
	var got []engine.Command
	for range cmds {
		select {
		case cmd := <-snd:
			got = append(got, cmd)
		case <-time.After(patience):
			t.Fatalf("the run got %d of %d commands", len(got), len(cmds))
		}
	}
	if !reflect.DeepEqual(got, cmds) {
		t.Errorf("sent\n%+v\nrun got\n%+v", cmds, got)
	}
}

// A command the run refuses is told as a warning.
func TestClientRefusedCommand(t *testing.T) {
	s := New(Options{Version: "1.4.0"})
	c, k, _ := attachTo(t, s)
	c.Send(engine.Command{Kind: engine.CmdPlan, Plan: state.PlanDeny}) // no feedback
	k.waitFor(t, "a warning", func(s *sink) bool { return s.count(string(engine.Warning)) == 1 })
	if d := k.events[0].Detail; !strings.Contains(d, "the run refused a command") || !strings.Contains(d, "feedback") {
		t.Errorf("warning = %q", d)
	}
}

func TestClientTail(t *testing.T) {
	s := New(Options{Version: "1.4.0", Tail: func(_ context.Context, _ string, n int) ([]string, error) {
		return []string{fmt.Sprintf("asked %d", n), "\x1b[31mred\x1b[0m"}, nil
	}})
	c, _, _ := attachTo(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	lines, err := c.Tail(ctx, 60)
	if err != nil || !reflect.DeepEqual(lines, []string{"asked 60", "red"}) {
		t.Fatalf("Tail = %q, %v", lines, err)
	}
	var _ backend.Tailer = c
}

// A dropped connection comes back with a fresh snapshot; the run's events
// published meanwhile are in it.
func TestClientReconnects(t *testing.T) {
	s := New(Options{Version: "1.4.0", Mode: "plan"})
	_, k, p := attachTo(t, s)
	s.Publish(engine.Event{At: time.Now(), Kind: engine.RunStarted, Run: "r1", Phase: "M0"})
	k.waitFor(t, "run_started", func(s *sink) bool { return s.count(string(engine.RunStarted)) == 1 })
	p.kill(false)
	k.waitFor(t, "a second snapshot", func(s *sink) bool { return s.count("snapshot") == 2 })
	if k.count("lost") != 1 {
		t.Errorf("log = %v, want one lost before the snapshot", k.log)
	}
	k.mu.Lock()
	snap := k.snaps[1]
	k.mu.Unlock()
	if snap.Run != "r1" || snap.Phase != "M0" || snap.Server != "igris 1.4.0" {
		t.Errorf("fresh snapshot = %+v", snap)
	}
	s.Publish(engine.Event{At: time.Now(), Kind: engine.PhaseStarted, Phase: "M0"})
	k.waitFor(t, "phase_started", func(s *sink) bool { return s.count(string(engine.PhaseStarted)) == 1 })
}

// When nothing answers on the socket any more, the client ends.
func TestClientGone(t *testing.T) {
	s := New(Options{Version: "1.4.0"})
	c, k, p := attachTo(t, s)
	p.kill(true)
	k.waitFor(t, "the end", func(s *sink) bool { return s.count("end") == 1 })
	if !errors.Is(k.err, ErrGone) {
		t.Errorf("end error = %v, want ErrGone", k.err)
	}
	select {
	case <-c.Done():
	case <-time.After(patience):
		t.Fatal("the client did not stop")
	}
}

// After run_stopped the client stops without reconnecting.
func TestClientEndsWithTheRun(t *testing.T) {
	s := New(Options{Version: "1.4.0"})
	c, k, p := attachTo(t, s)
	s.Publish(engine.Event{At: time.Now(), Kind: engine.RunStopped, Detail: "completed"})
	k.waitFor(t, "the end", func(s *sink) bool { return s.count("end") == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(patience):
		t.Fatal("the client did not stop")
	}
	if k.res.Outcome != engine.Completed || k.count("lost") != 0 {
		t.Errorf("end = %+v, log %v", k.res, k.log)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dials != 1 {
		t.Errorf("%d dials, want 1", p.dials)
	}
}

// A snapshot of a run that already ended ends the client at once.
func TestClientAttachEnded(t *testing.T) {
	s := New(Options{Version: "1.4.0"})
	s.Publish(engine.Event{At: time.Now(), Kind: engine.RunStopped, Detail: "stopped"})
	_, k, _ := attachTo(t, s)
	k.waitFor(t, "the end", func(s *sink) bool { return s.count("end") == 1 })
	if k.res.Outcome != engine.Stopped {
		t.Errorf("end = %+v", k.res)
	}
}

func TestAttachNotServed(t *testing.T) {
	_, err := Attach(context.Background(), ClientOptions{Sink: newSink(), Dial: func(context.Context) (net.Conn, error) {
		return nil, fmt.Errorf("%w (nothing)", ErrNotServed)
	}})
	if !errors.Is(err, ErrNotServed) {
		t.Fatalf("Attach = %v, want ErrNotServed", err)
	}
}

// fakeServe answers a hello with the lines given, then reads until the
// client goes.
func fakeServe(t *testing.T, lines ...string) func(context.Context) (net.Conn, error) {
	t.Helper()
	return func(context.Context) (net.Conn, error) {
		cs, cc := net.Pipe()
		go func() {
			defer func() { _ = cs.Close() }()
			r := bufio.NewReader(cs)
			if _, err := r.ReadBytes('\n'); err != nil {
				return
			}
			for _, l := range lines {
				if _, err := io.WriteString(cs, l+"\n"); err != nil {
					return
				}
			}
			_, _ = io.Copy(io.Discard, r)
		}()
		return cc, nil
	}
}

// Types of a later igris are skipped; the events around them arrive.
func TestClientIgnoresUnknownTypes(t *testing.T) {
	k := newSink()
	c, err := Attach(context.Background(), ClientOptions{Sink: k, Dial: fakeServe(t,
		`{"v":1,"type":"snapshot","protocol":1,"server":"igris 1.9.0","feed":[{"v":1,"type":"warning","detail":"a"}]}`,
		`{"v":1,"type":"group_started","at":"2026-10-22T09:41:00Z","detail":"from 1.5"}`,
		`{"v":1,"type":"warning","at":"2026-10-22T09:41:00Z","detail":"b\u001b[31m","future":true}`,
		`{"v":1,"type":"run_stopped","at":"2026-10-22T09:41:00Z","detail":"completed"}`,
	)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	k.waitFor(t, "the end", func(s *sink) bool { return s.count("end") == 1 })
	if want := []string{"snapshot", "warning", "run_stopped", "end"}; !reflect.DeepEqual(k.log, want) {
		t.Errorf("log = %v, want %v", k.log, want)
	}
	if d := k.events[0].Detail; d != "b" {
		t.Errorf("detail = %q, want it cleaned", d)
	}
	if c.Server() != "igris 1.9.0" {
		t.Errorf("Server = %q", c.Server())
	}
}

// An error that ends the connection on hello is final, not retried.
func TestClientRefused(t *testing.T) {
	_, err := Attach(context.Background(), ClientOptions{Sink: newSink(), Dial: fakeServe(t,
		`{"v":1,"type":"error","error":"protocol version not supported: v 1 (this igris speaks 2)"}`)})
	var refused *refusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("Attach = %v", err)
	}
}

// Over a real socket in a project's .igris/, as igris attach dials it.
func TestClientOverSocket(t *testing.T) {
	dir := openDir(t, t.TempDir())
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Version: "1.4.0"})
	go func() { _ = s.Serve(l) }()
	defer func() { _ = s.Close() }()
	k := newSink()
	c, err := Attach(context.Background(), ClientOptions{Name: "igris 1.4.0", Sink: k, Dial: func(ctx context.Context) (net.Conn, error) {
		return Dial(ctx, dir.Root())
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s.Publish(engine.Event{At: time.Now(), Kind: engine.PauseOn})
	k.waitFor(t, "pause_on", func(s *sink) bool { return s.count(string(engine.PauseOn)) == 1 })
}
