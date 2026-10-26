package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

// patience bounds every wait on the other end of a connection; tests
// never sleep.
const patience = 5 * time.Second

// sender records the commands the server passes on.
type sender struct {
	mu   sync.Mutex
	cmds []engine.Command
}

func (s *sender) Send(c engine.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds = append(s.cmds, c)
}

func (s *sender) got() []engine.Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]engine.Command(nil), s.cmds...)
}

// peer is the client end of a connection to the server under test.
type peer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	done chan struct{} // closed when ServeConn returned
}

// attach connects a client over net.Pipe; the server side runs ServeConn.
func attach(t *testing.T, s *Server) *peer {
	t.Helper()
	cs, cc := net.Pipe()
	p := &peer{t: t, conn: cc, r: bufio.NewReaderSize(cc, 64<<10), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		s.ServeConn(cs)
	}()
	t.Cleanup(func() {
		_ = cc.Close()
		<-p.done
	})
	return p
}

func (p *peer) send(line string) {
	p.t.Helper()
	_ = p.conn.SetWriteDeadline(time.Now().Add(patience))
	if _, err := io.WriteString(p.conn, line+"\n"); err != nil {
		p.t.Fatalf("send %s: %v", line, err)
	}
}

// next reads one message into a map, or fails.
func (p *peer) next() map[string]any {
	p.t.Helper()
	m, err := p.read()
	if err != nil {
		p.t.Fatalf("read: %v", err)
	}
	return m
}

func (p *peer) read() (map[string]any, error) {
	_ = p.conn.SetReadDeadline(time.Now().Add(patience))
	line, err := p.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		p.t.Fatalf("server sent %q: %v", line, err)
	}
	return m, nil
}

// hello says hello and returns the snapshot.
func (p *peer) hello() map[string]any {
	p.t.Helper()
	p.send(`{"v":1,"type":"hello","client":"igris test"}`)
	m := p.next()
	if m["type"] != TypeSnapshot {
		p.t.Fatalf("answer to hello = %v, want a snapshot", m)
	}
	return m
}

// closedByServer reads until the server ends the connection; it fails on
// a message.
func (p *peer) closedByServer() {
	p.t.Helper()
	if m, err := p.read(); err == nil {
		p.t.Fatalf("got %v, want the connection closed", m)
	}
	select {
	case <-p.done:
	case <-time.After(patience):
		p.t.Fatal("ServeConn did not return")
	}
}

var t0 = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

// The hello that starts the run (arise's) gets its snapshot even when
// the run ends and serve shuts down at once: Hello is called only once
// the snapshot is queued (V14-G: arise failed with "the connection closed
// before the snapshot" when a fast run beat it).
func TestHelloStartsTheRunAfterTheSnapshot(t *testing.T) {
	var s *Server
	s = New(Options{Version: "1.4.0", Hello: func(Request) {
		// The whole run, at once: its end, then serve's shutdown.
		s.Publish(engine.Event{At: t0, Kind: engine.RunStopped, Detail: "completed"})
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		defer cancel()
		_ = s.Shutdown(ctx)
	}})
	p := attach(t, s)
	p.hello()
	if ev := p.next(); ev["type"] != "run_stopped" || ev["detail"] != "completed" {
		t.Errorf("after the snapshot = %v, want the run's end", ev)
	}
	p.closedByServer()
}

func TestHelloSnapshotThenEvents(t *testing.T) {
	hellos := make(chan Request, 2) // Hello runs after the snapshot is queued
	s := New(Options{Version: "1.4.0", Project: "demo", Backend: "tmux", Mode: "auto", Hello: func(r Request) { hellos <- r }})
	s.Publish(engine.Event{At: t0, Kind: engine.RunStarted, Run: "20261022-090000-abcd", Phase: "M1", Detail: "phase M1"})
	p := attach(t, s)
	p.send(`{"v":1,"type":"hello","client":"igris 1.4.0\u001b[2J","yolo":true}`)
	snap := p.next()
	if snap["type"] != TypeSnapshot || snap["v"] != 1.0 || snap["protocol"] != 1.0 || snap["server"] != "igris 1.4.0" ||
		snap["project"] != "demo" || snap["backend"] != "tmux" || snap["run"] != "20261022-090000-abcd" || snap["phase"] != "M1" || snap["mode"] != "auto" {
		t.Errorf("snapshot = %v", snap)
	}
	select {
	case h := <-hellos:
		if h.Client != "igris 1.4.0" || !h.Yolo {
			t.Errorf("hello callback got %+v; want the cleaned client and yolo", h)
		}
	case <-time.After(patience):
		t.Fatal("the hello callback was not called")
	}
	s.Publish(engine.Event{At: t0.Add(time.Second), Kind: engine.TaskStarted, Phase: "M1", Task: "M1-01", Title: "Config", Rank: "sonnet", Model: "sonnet", Mode: "auto"})
	s.Publish(engine.Event{At: t0.Add(2 * time.Second), Kind: engine.VerifyPassed, Phase: "M1", Task: "M1-01", Verify: "fast", Detail: "go test"})
	ev := p.next()
	if ev["type"] != "task_started" || ev["run"] != "20261022-090000-abcd" || ev["task"] != "M1-01" || ev["at"] != "2026-10-22T09:00:01Z" {
		t.Errorf("first event = %v", ev)
	}
	if ev := p.next(); ev["type"] != "verify_passed" || ev["profile"] != "fast" {
		t.Errorf("second event = %v; want verify_passed with the run log's profile field", ev)
	}
}

func TestCommandsMirrorEngine(t *testing.T) {
	tests := []struct {
		req  string
		want engine.Command
	}{
		{`{"v":1,"type":"pause","id":"1"}`, engine.Command{Kind: engine.CmdPause}},
		{`{"v":1,"type":"stop","id":"1"}`, engine.Command{Kind: engine.CmdStop}},
		{`{"v":1,"type":"done","id":"1","text":"by hand\u001b]52;c;eA==\u0007"}`, engine.Command{Kind: engine.CmdDone, Text: "by hand"}},
		{`{"v":1,"type":"skip","id":"1","text":"not needed"}`, engine.Command{Kind: engine.CmdSkip, Text: "not needed"}},
		{`{"v":1,"type":"retry","id":"1","continue":true}`, engine.Command{Kind: engine.CmdRetry, Continue: true}},
		{`{"v":1,"type":"answer","id":"1","yes":true}`, engine.Command{Kind: engine.CmdAnswer, Yes: true}},
		{`{"v":1,"type":"answer","id":"1","question":"confirm_reset","task":"M1-03"}`, engine.Command{Kind: engine.CmdAnswer, Question: engine.QuestionConfirmReset, Task: "M1-03"}},
		{`{"v":1,"type":"mode","id":"1","text":"yolo","yes":true}`, engine.Command{Kind: engine.CmdMode, Text: "yolo", Yes: true}},
		{`{"v":1,"type":"task_mode","id":"1","task":"M1-03","text":"plan"}`, engine.Command{Kind: engine.CmdTaskMode, Task: "M1-03", Text: "plan"}},
		{`{"v":1,"type":"plan","id":"1","plan":"allow"}`, engine.Command{Kind: engine.CmdPlan, Plan: state.PlanAllow}},
		{`{"v":1,"type":"plan","id":"1","plan":"deny","text":"split it"}`, engine.Command{Kind: engine.CmdPlan, Plan: state.PlanDeny, Text: "split it"}},
		{`{"v":1,"type":"plan","id":"1","plan":"pane"}`, engine.Command{Kind: engine.CmdPlan, Plan: state.PlanPane}},
		{`{"v":1,"type":"limit","id":"1","text":"start"}`, engine.Command{Kind: engine.CmdLimitStart, Text: "start"}},
		// Fields a newer client adds are ignored.
		{`{"v":1,"type":"pause","id":"1","later":{"x":1}}`, engine.Command{Kind: engine.CmdPause}},
	}
	for _, tt := range tests {
		t.Run(tt.req, func(t *testing.T) {
			var got sender
			p := attach(t, New(Options{Sender: &got}))
			p.hello()
			p.send(tt.req)
			if m := p.next(); m["type"] != TypeAck || m["id"] != "1" {
				t.Fatalf("answer = %v, want ack 1", m)
			}
			if cmds := got.got(); len(cmds) != 1 || cmds[0] != tt.want {
				t.Errorf("engine got %+v, want %+v", cmds, tt.want)
			}
		})
	}
}

// A refused command gets an error with its id; the connection goes on.
func TestCommandsRefused(t *testing.T) {
	tests := []struct{ req, why string }{
		{`{"v":1,"type":"teleport","id":"a"}`, "unknown message type"},
		{`{"v":1,"type":"task_mode","id":"a","text":"auto"}`, "needs a task"},
		{`{"v":1,"type":"task_mode","id":"a","task":"M1 03; rm","text":"auto"}`, "not a task ID"},
		{`{"v":1,"type":"answer","id":"a","question":"launch"}`, "unknown question"},
		{`{"v":1,"type":"plan","id":"a","plan":"maybe"}`, "plan is allow"},
		{`{"v":1,"type":"plan","id":"a","plan":"deny","text":"  "}`, "needs feedback"},
		{`{"v":1,"type":"limit","id":"a","text":"later"}`, "limit takes text start"},
		{`{"v":1,"type":"skip","id":"a","text":"` + strings.Repeat("x", maxText+1) + `"}`, "over 16384 bytes"},
		{`{"v":1,"type":"hello","id":"a"}`, "already said"},
		{`{"v":1,"type":"tail","id":"a","n":0}`, "n is 1 to 200"},
		{`{"v":1,"type":"tail","id":"a","n":201}`, "n is 1 to 200"},
	}
	for _, tt := range tests {
		t.Run(tt.why, func(t *testing.T) {
			var got sender
			p := attach(t, New(Options{Sender: &got}))
			p.hello()
			p.send(tt.req)
			m := p.next()
			if m["type"] != TypeError || m["id"] != "a" || !strings.Contains(m["error"].(string), tt.why) {
				t.Fatalf("answer = %v, want an error for a saying %q", m, tt.why)
			}
			p.send(`{"v":1,"type":"pause","id":"b"}`)
			if m := p.next(); m["type"] != TypeAck || m["id"] != "b" {
				t.Fatalf("after the error: %v, want ack b", m)
			}
			if cmds := got.got(); len(cmds) != 1 || cmds[0].Kind != engine.CmdPause {
				t.Errorf("engine got %+v, want only the pause", cmds)
			}
		})
	}
}

// A message that isn't protocol 1, or anything before hello, ends the
// connection with an error that has no id.
func TestMalformedEndsConnection(t *testing.T) {
	tests := []struct {
		name  string
		hello bool
		line  string
		why   string
	}{
		{"not JSON", false, `hello`, "not a protocol message"},
		{"not an object", true, `[1,2]`, "not a protocol message"},
		{"newer protocol", false, `{"v":2,"type":"hello"}`, "protocol version"},
		{"no v", true, `{"type":"pause","id":"1"}`, "protocol version"},
		{"no type", true, `{"v":1,"id":"1"}`, "without a type"},
		{"command first", false, `{"v":1,"type":"stop","id":"1"}`, "must be hello"},
		{"no id", true, `{"v":1,"type":"stop"}`, "needs an id"},
		{"long id", true, `{"v":1,"type":"stop","id":"` + strings.Repeat("9", maxID+1) + `"}`, "printable"},
		{"control in id", true, `{"v":1,"type":"stop","id":"a\u001bb"}`, "printable"},
		{"too long", true, `{"v":1,"type":"skip","id":"1","text":"` + strings.Repeat("x", MaxLine) + `"}`, "over 1048576 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got sender
			p := attach(t, New(Options{Sender: &got}))
			if tt.hello {
				p.hello()
			}
			// The server stops reading a line that is too long, so the
			// rest of it is written on the side.
			go func() {
				_ = p.conn.SetWriteDeadline(time.Now().Add(patience))
				_, _ = io.WriteString(p.conn, tt.line+"\n")
			}()
			m := p.next()
			if _, hasID := m["id"]; m["type"] != TypeError || hasID || !strings.Contains(m["error"].(string), tt.why) {
				t.Fatalf("answer = %v, want an error without id saying %q", m, tt.why)
			}
			p.closedByServer()
			if cmds := got.got(); len(cmds) != 0 {
				t.Errorf("engine got %+v", cmds)
			}
		})
	}
}

// Every client gets every event, in order, from its own snapshot on.
func TestFanOut(t *testing.T) {
	s := New(Options{})
	a, b := attach(t, s), attach(t, s)
	a.hello()
	s.Publish(engine.Event{At: t0, Kind: engine.PauseOn})
	b.hello()
	s.Publish(engine.Event{At: t0, Kind: engine.Asked, Task: "M1-01", Question: engine.QuestionCommit, Detail: "commit M1-01?"})
	s.Publish(engine.Event{At: t0, Kind: engine.Committed, Task: "M1-01", Detail: "M1-01: config"})
	for _, want := range []string{"pause_on", "asked", "committed"} {
		if m := a.next(); m["type"] != want {
			t.Errorf("a got %v, want %s", m, want)
		}
	}
	// b attached after pause_on: its snapshot says so instead.
	for _, want := range []string{"asked", "committed"} {
		if m := b.next(); m["type"] != want {
			t.Errorf("b got %v, want %s", m, want)
		}
	}
	if s.Clients() != 2 {
		t.Errorf("Clients() = %d, want 2", s.Clients())
	}
}

// Both clients may answer a question; the engine applies the first and
// ignores the second (no question waits any more), and both clients see
// the answer as the events that follow.
func TestEveryAnswerReachesTheEngineInOrder(t *testing.T) {
	var got sender
	s := New(Options{Sender: &got})
	a, b := attach(t, s), attach(t, s)
	a.hello()
	b.hello()
	a.send(`{"v":1,"type":"answer","id":"a1","yes":true}`)
	if m := a.next(); m["type"] != TypeAck {
		t.Fatalf("a: %v", m)
	}
	b.send(`{"v":1,"type":"answer","id":"b1","yes":false}`)
	if m := b.next(); m["type"] != TypeAck {
		t.Fatalf("b: %v", m)
	}
	if cmds := got.got(); len(cmds) != 2 || !cmds[0].Yes || cmds[1].Yes {
		t.Errorf("engine got %+v, want a's yes first, then b's no", cmds)
	}
}

// A client that doesn't read is dropped once its queue is full; Publish
// never waits for it, and the other clients keep getting everything.
func TestSlowClientDropped(t *testing.T) {
	s := New(Options{QueueLen: 8})
	slow, fast := attach(t, s), attach(t, s)
	slow.hello()
	fast.hello()
	const n = 50
	published := make(chan struct{})
	go func() {
		defer close(published)
		for i := range n {
			s.Publish(engine.Event{At: t0, Kind: engine.Warning, Detail: "x"})
			// fast reads each event before the next, so its queue never
			// fills.
			if m, err := fast.read(); err != nil || m["type"] != "warning" {
				t.Errorf("fast got %v, %v at %d", m, err, i)
				return
			}
		}
	}()
	select {
	case <-published:
	case <-time.After(patience):
		t.Fatal("Publish blocked on a client that doesn't read")
	}
	select {
	case <-slow.done:
	case <-time.After(patience):
		t.Fatal("the slow client was not dropped")
	}
	if s.Clients() != 1 {
		t.Errorf("Clients() = %d, want 1", s.Clients())
	}
	// It comes back with a fresh snapshot.
	again := attach(t, s)
	if snap := again.hello(); snap["feed"] == nil {
		t.Errorf("fresh snapshot has no feed: %v", snap)
	}
}

// The byte bound drops a client as the message count does.
func TestSlowClientDroppedByBytes(t *testing.T) {
	s := New(Options{QueueBytes: 4 << 10})
	slow := attach(t, s)
	slow.hello()
	for range 10 {
		s.Publish(engine.Event{At: t0, Kind: engine.Warning, Detail: strings.Repeat("y", 1<<10)})
	}
	select {
	case <-slow.done:
	case <-time.After(patience):
		t.Fatal("the slow client was not dropped")
	}
}

func TestHelloTimesOut(t *testing.T) {
	s := New(Options{})
	s.helloTimeout = time.Millisecond
	p := attach(t, s)
	p.closedByServer()
}

func TestTail(t *testing.T) {
	var asked int
	s := New(Options{Tail: func(_ context.Context, _ string, n int) ([]string, error) {
		asked = n
		return []string{"one", "two \x1b[31mred\x1b[0m", "three"}, nil
	}})
	p := attach(t, s)
	p.hello()
	p.send(`{"v":1,"type":"tail","id":"t","n":2}`)
	m := p.next()
	lines, _ := m["lines"].([]any)
	if m["type"] != TypeTail || m["id"] != "t" || len(lines) != 2 || lines[0] != "two red" || lines[1] != "three" || asked != 2 {
		t.Errorf("tail = %v (asked %d), want the last 2 lines, cleaned", m, asked)
	}

	failing := New(Options{Tail: func(context.Context, string, int) ([]string, error) { return nil, errors.New("pane gone") }})
	p = attach(t, failing)
	p.hello()
	p.send(`{"v":1,"type":"tail","id":"t","n":6}`)
	if m := p.next(); m["type"] != TypeError || m["id"] != "t" || !strings.Contains(m["error"].(string), "pane gone") {
		t.Errorf("failing tail = %v", m)
	}

	none := New(Options{})
	p = attach(t, none)
	p.hello()
	p.send(`{"v":1,"type":"tail","id":"t","n":6}`)
	if m := p.next(); m["type"] != TypeTail || m["lines"] != nil {
		t.Errorf("tail without a session = %v", m)
	}
}

func TestCloseEndsClients(t *testing.T) {
	s := New(Options{})
	p := attach(t, s)
	p.hello()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	p.closedByServer()
	// A connection after Close is refused at once.
	late := attach(t, s)
	late.closedByServer()
}

// A test of the peer check, without a second user: the server expects
// another uid than the client's.
func TestServeRefusesAnotherUser(t *testing.T) {
	s := New(Options{})
	s.peerUID = func(*net.UnixConn) (int, error) { return 4242, nil }
	root := listenTemp(t, s)
	c, err := Dial(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(patience))
	_, _ = io.WriteString(c, `{"v":1,"type":"hello"}`+"\n")
	if b, _ := io.ReadAll(c); len(b) != 0 {
		t.Errorf("a peer of another user got %q", b)
	}
	if s.Clients() != 0 {
		t.Errorf("Clients() = %d", s.Clients())
	}
}

func TestSessionRefInEvents(t *testing.T) {
	s := New(Options{})
	p := attach(t, s)
	p.hello()
	ref := &backend.SessionRef{Backend: "tmux", TabID: "@3", PaneID: "%7", ClaudeSession: "3f2a"}
	s.Publish(engine.Event{At: t0, Kind: engine.SessionOpened, Task: "M1-01", Session: ref, ClaudeSession: "3f2a"})
	m := p.next()
	sess, _ := m["session"].(map[string]any)
	if sess["pane_id"] != "%7" || sess["tab_id"] != "@3" || m["claude_session"] != "3f2a" {
		t.Errorf("session_opened = %v", m)
	}
}

// TestCloseRightAfterAccept: a connection accepted as Close runs is
// either counted before Close waits or dropped, never counted after.
// The accept loop once counted it (wg.Add) outside the lock, so the
// count could run alongside Close's wg.Wait (the WaitGroup contract:
// the first Add happens before Wait), which go test -race reported on
// 2026-10-22 in project's TestSnapshotServed once in a few hundred runs.
// Here the peer check, which runs on the accept loop right before the
// count, holds the loop until Close is past the lock and closing the
// listener, so the count comes exactly then.
func TestCloseRightAfterAccept(t *testing.T) {
	for range 20 {
		s := New(Options{})
		closing := make(chan struct{})
		s.peerUID = func(*net.UnixConn) (int, error) {
			<-closing
			return 0, errNoPeerCred
		}
		root := t.TempDir()
		l, err := Listen(openDir(t, root))
		if err != nil {
			t.Fatal(err)
		}
		served := make(chan error, 1)
		go func() { served <- s.Serve(&closingListener{Listener: l, closing: closing}) }()
		ctx, cancel := context.WithTimeout(context.Background(), patience)
		c, err := Dial(ctx, root)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := <-served; err != nil {
			t.Error(err)
		}
		_ = c.Close()
	}
}

// closingListener closes closing when the server closes it.
type closingListener struct {
	net.Listener
	closing chan struct{}
	once    sync.Once
}

func (l *closingListener) Close() error {
	l.once.Do(func() { close(l.closing) })
	return l.Listener.Close()
}
