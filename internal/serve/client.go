package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The client side of protocol 1 (SPEC §13, §14 igris attach): Client
// stands in for the engine of a run served elsewhere. It takes the
// owner's commands (Send) and tail requests (Tail) as the engine does, and
// hands what the run says to a Sink as the engine hands its events to the
// run view's feed, so the run view renders an in-process engine or a
// served one alike.

// Sink receives a served run: tui.Feed is one.
type Sink interface {
	// Snapshot starts the run over from s: on attaching, and after each
	// reconnect. The events that follow continue it.
	Snapshot(s Snapshot)
	// Push is one event of the run.
	Push(ev engine.Event)
	// Lost says the connection is gone; a Snapshot follows once the
	// client has reconnected.
	Lost()
	// End says the run is over for this client, once: it ended (err nil),
	// or nothing answers on its socket any more.
	End(res engine.Result, err error)
}

// ClientOptions configures a Client.
type ClientOptions struct {
	// Name is this igris in the hello, e.g. "igris 1.4.0".
	Name string
	// Yolo goes in the hello: the owner typed the skip-permissions phrase
	// for the run this client launched (SPEC §14).
	Yolo bool
	// Dial connects to the run's socket; it returns an error wrapping
	// ErrNotServed when nothing answers there.
	Dial func(ctx context.Context) (net.Conn, error)
	Sink Sink
	// Retry is the wait between reconnect attempts; zero means 1 s.
	Retry time.Duration
}

// ErrGone is what a Client ends with when its connection is lost and
// nothing answers on the run's socket any more.
var ErrGone = errors.New("the connection to the run was lost and nothing answers on its socket: the igris serving it has ended")

// Timeouts of the client.
const (
	snapshotTimeout = 10 * time.Second // from hello to the snapshot
	clientWrite     = 10 * time.Second // to write one request
	retryEvery      = time.Second
)

// Client is attached to one served run. Create it with Attach.
type Client struct {
	opts ClientOptions
	ctx  context.Context
	stop context.CancelFunc
	done chan struct{}

	mu      sync.Mutex
	conn    net.Conn // nil while reconnecting
	server  string   // the snapshot's Server, e.g. "igris 1.4.0"
	seq     int
	waiting map[string]chan Reply // replies by request id
	ended   bool
}

// Attach connects to a served run and hands its snapshot to opts.Sink
// before it returns; the events follow from a goroutine of the client's,
// which reconnects with a fresh snapshot when the connection drops. The
// error wraps ErrNotServed when no run answers. Close ends it.
func Attach(ctx context.Context, opts ClientOptions) (*Client, error) {
	if opts.Retry <= 0 {
		opts.Retry = retryEvery
	}
	ctx, stop := context.WithCancel(ctx)
	c := &Client{opts: opts, ctx: ctx, stop: stop, done: make(chan struct{}), waiting: map[string]chan Reply{}}
	conn, sc, err := c.connect()
	if err != nil {
		stop()
		return nil, err
	}
	go c.loop(conn, sc)
	return c, nil
}

// Server names the serving igris as its last snapshot did, e.g.
// "igris 1.4.0".
func (c *Client) Server() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.server
}

// Close detaches: it ends the connection and waits for the client's
// goroutine.
func (c *Client) Close() {
	c.stop()
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.mu.Unlock()
	<-c.done
}

// Done is closed once the client has stopped: closed, or the run is over.
func (c *Client) Done() <-chan struct{} { return c.done }

// connect dials, says hello and hands the snapshot to the sink.
func (c *Client) connect() (net.Conn, *bufio.Scanner, error) {
	conn, err := c.opts.Dial(c.ctx)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (net.Conn, *bufio.Scanner, error) {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := write(conn, Request{Type: TypeHello, Client: c.opts.Name, Yolo: c.opts.Yolo}); err != nil {
		return fail(fmt.Errorf("say hello to the run: %w", err))
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64<<10), MaxLine+1)
	_ = conn.SetReadDeadline(time.Now().Add(snapshotTimeout))
	stop := context.AfterFunc(c.ctx, func() { _ = conn.Close() })
	defer stop()
	if !sc.Scan() {
		err := sc.Err()
		if err == nil {
			err = errors.New("the connection closed before the snapshot")
		}
		return fail(fmt.Errorf("read the run's snapshot: %w", err))
	}
	_ = conn.SetReadDeadline(time.Time{})
	var snap Snapshot
	if err := json.Unmarshal(sc.Bytes(), &snap); err != nil {
		return fail(fmt.Errorf("the run sent something that is not a protocol message: %w", err))
	}
	if snap.Type != TypeSnapshot {
		if snap.Type == TypeError {
			var r Reply
			_ = json.Unmarshal(sc.Bytes(), &r)
			return fail(&refusedError{textsafe.Line(cut(r.Error, 4<<10))})
		}
		return fail(fmt.Errorf("the run answered hello with %q, not a snapshot", textsafe.Line(cut(snap.Type, maxID))))
	}
	c.mu.Lock()
	c.conn, c.server = conn, textsafe.Line(cut(snap.Server, maxClient))
	c.mu.Unlock()
	c.opts.Sink.Snapshot(snap)
	if snap.Ended {
		c.end(engine.Result{Outcome: outcomeOf(snap.End), Phase: snap.Phase}, endError(snap.End, lastFailure(snap.Feed)))
	}
	return conn, sc, nil
}

// refusedError is the serve's own error ending the connection: retrying
// won't help (another protocol version, a refused hello).
type refusedError struct{ msg string }

func (e *refusedError) Error() string { return "the run refused this client: " + e.msg }

// loop reads the run until it ends, reconnecting when the connection
// drops.
func (c *Client) loop(conn net.Conn, sc *bufio.Scanner) {
	defer close(c.done)
	for {
		err := c.read(sc)
		_ = conn.Close()
		c.lost()
		if c.isEnded() || c.ctx.Err() != nil {
			return
		}
		var refused *refusedError
		if errors.As(err, &refused) {
			c.end(engine.Result{}, err)
			return
		}
		c.opts.Sink.Lost()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-time.After(c.opts.Retry):
			}
			conn, sc, err = c.connect()
			if err == nil {
				break
			}
			if errors.Is(err, ErrNotServed) || errors.As(err, &refused) {
				if errors.Is(err, ErrNotServed) {
					err = ErrGone
				}
				c.end(engine.Result{}, err)
				return
			}
			if c.ctx.Err() != nil {
				return
			}
		}
		if c.isEnded() {
			_ = conn.Close()
			return
		}
	}
}

// lost forgets the connection and fails the requests waiting on it.
func (c *Client) lost() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn = nil
	for id, ch := range c.waiting {
		ch <- Reply{Type: TypeError, ID: id, Error: "the connection to the run was lost"}
		delete(c.waiting, id)
	}
}

// read handles the run's messages until the connection ends; an error
// from serve that ends the connection comes back as a refusedError.
func (c *Client) read(sc *bufio.Scanner) error {
	failure := "" // the run's error, told before its run_stopped
	for sc.Scan() {
		line := sc.Bytes()
		var head struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			return fmt.Errorf("the run sent something that is not a protocol message: %w", err)
		}
		switch head.Type {
		case TypeAck, TypeError, TypeTail:
			var r Reply
			if err := json.Unmarshal(line, &r); err != nil {
				return fmt.Errorf("a malformed %s: %w", head.Type, err)
			}
			if r.Type == TypeError && r.ID == "" {
				return &refusedError{textsafe.Line(cut(r.Error, 4<<10))}
			}
			c.reply(r)
		case TypeSnapshot:
			// Only after hello; a later one is not part of protocol 1.
		default:
			var ev Event
			if err := json.Unmarshal(line, &ev); err != nil {
				return fmt.Errorf("a malformed %s event: %w", textsafe.Line(cut(head.Type, maxID)), err)
			}
			e, ok := ev.Engine()
			if !ok {
				continue // a type of a later igris: clients ignore what they don't know
			}
			c.opts.Sink.Push(e)
			switch e.Kind {
			case engine.RunFailed:
				failure = e.Detail
			case engine.RunStopped:
				c.end(engine.Result{Outcome: outcomeOf(e.Detail), Phase: e.Phase}, endError(e.Detail, failure))
			}
		}
	}
	return sc.Err()
}

// reply hands r to the request waiting for it. A refused command has
// nobody waiting: it is told in the run's log as a warning.
func (c *Client) reply(r Reply) {
	c.mu.Lock()
	ch, ok := c.waiting[r.ID]
	delete(c.waiting, r.ID)
	c.mu.Unlock()
	switch {
	case ok:
		ch <- r
	case r.Type == TypeError:
		c.opts.Sink.Push(engine.Event{At: time.Now(), Kind: engine.Warning, Detail: "the run refused a command: " + textsafe.Line(cut(r.Error, 4<<10))})
	}
}

func (c *Client) isEnded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended
}

// end tells the sink the run is over for this client, once.
func (c *Client) end(res engine.Result, err error) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	c.mu.Unlock()
	c.opts.Sink.End(res, err)
}

// RunError is how a Client ends when the run ended with an error
// (run_stopped "error"): Detail is the error the run told, cleaned.
type RunError struct{ Detail string }

func (e *RunError) Error() string {
	if e.Detail == "" {
		return "the run ended with an error; .igris/serve.log may say more"
	}
	return e.Detail
}

// endError is the error a run_stopped with detail ends the client with:
// a RunError for "error", carrying failure, the run's error.
func endError(detail, failure string) error {
	if detail != "error" {
		return nil
	}
	return &RunError{Detail: failure}
}

// lastFailure is the last run_error in a snapshot's feed.
func lastFailure(feed []Event) string {
	for i := len(feed) - 1; i >= 0; i-- {
		if ev, ok := feed[i].Engine(); ok && ev.Kind == engine.RunFailed {
			return ev.Detail
		}
	}
	return ""
}

// outcomeOf reads run_stopped's detail.
func outcomeOf(detail string) engine.Outcome {
	for _, o := range []engine.Outcome{engine.Completed, engine.Stuck, engine.Stopped} {
		if detail == o.String() {
			return o
		}
	}
	return 0
}

// send writes r with a fresh id; wait, when set, gets its reply.
func (c *Client) send(r Request, wait chan Reply) error {
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return errors.New("the connection to the run is lost; reconnecting")
	}
	c.seq++
	r.ID = strconv.Itoa(c.seq)
	if wait != nil {
		c.waiting[r.ID] = wait
	}
	// Under c.mu, so requests go out whole and in order.
	err := write(conn, r)
	if err != nil && wait != nil {
		delete(c.waiting, r.ID)
	}
	c.mu.Unlock()
	if err != nil {
		_ = conn.Close() // the reader sees it and reconnects
	}
	return err
}

// write sends one request.
func write(conn net.Conn, r Request) error {
	r.V = Protocol
	b, err := encode(r)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(clientWrite))
	_, err = conn.Write(b)
	return err
}

// Send passes the owner's command to the run, as engine.Engine.Send does;
// it never blocks for the answer. A command that can't be sent is told in
// the run's log as a warning.
func (c *Client) Send(cmd engine.Command) {
	r, ok := requestOf(cmd)
	if !ok {
		return
	}
	if err := c.send(r, nil); err != nil {
		c.opts.Sink.Push(engine.Event{At: time.Now(), Kind: engine.Warning, Detail: "not sent: " + err.Error()})
	}
}

// Tail reads the current session's last n lines through the run, as
// engine.Engine.Tail does (backend.Tailer).
func (c *Client) Tail(ctx context.Context, n int) ([]string, error) {
	ch := make(chan Reply, 1)
	if err := c.send(Request{Type: TypeTail, N: min(n, maxTail)}, ch); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		for id, w := range c.waiting {
			if w == ch {
				delete(c.waiting, id)
			}
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	case r := <-ch:
		if r.Type == TypeError {
			return nil, errors.New(textsafe.Line(cut(r.Error, 4<<10)))
		}
		return r.Lines, nil
	}
}

// commandTypes are the commands' message types by engine.CommandKind.
var commandTypes = func() map[engine.CommandKind]string {
	m := make(map[engine.CommandKind]string, len(commandKinds))
	for t, k := range commandKinds {
		m[k] = t
	}
	return m
}()

// requestOf is cmd as a request, without its id.
func requestOf(cmd engine.Command) (Request, bool) {
	t, ok := commandTypes[cmd.Kind]
	if !ok {
		return Request{}, false
	}
	return Request{
		Type: t, Text: cmd.Text, Continue: cmd.Continue, Yes: cmd.Yes, Task: cmd.Task,
		Question: string(cmd.Question), Plan: cmd.Plan,
	}, true
}

// knownKinds are the event types this igris shows: every engine kind,
// the live usage under its socket name.
var knownKinds = func() map[string]engine.EventKind {
	m := map[string]engine.EventKind{EventTypeTokens: engine.UsageRead}
	for _, k := range []engine.EventKind{
		engine.RunStarted, engine.WorktreeReady, engine.WorktreeLeft, engine.PhaseStarted, engine.TaskStarted,
		engine.TaskResumed, engine.SessionOpened, engine.TaskDone, engine.PhaseDone, engine.PhaseStuck, engine.NotRun,
		engine.PauseOn, engine.PauseOff, engine.Paused, engine.ModeChanged, engine.TaskModeChanged,
		engine.ConfigChanged, engine.ConfigRestored, engine.PlanChanged, engine.StaleSignal, engine.StraySignal,
		engine.NeedsYou, engine.NeedsYouClear, engine.AgentWaiting, engine.AgentWaitingOver, engine.Nudged,
		engine.SessionLost, engine.TaskOverdue, engine.RankMismatch, engine.QuotaRead, engine.Asked,
		engine.PlanClosed, engine.Retrying, engine.TaskSkipped, engine.TaskReset, engine.ResetDropped,
		engine.YourTurn, engine.VerifyStarted, engine.VerifyPassed, engine.VerifyFailed, engine.VerifyLimit,
		engine.Committed, engine.NotCommitted, engine.Integrated, engine.IntegrationFailed, engine.Overlap,
		engine.HookStarted, engine.HookFailed, engine.Warning, engine.RunFailed, engine.RunStopped,
	} {
		m[string(k)] = k
	}
	return m
}()

// Engine is ev as the engine's event, the inverse of what Publish sends;
// ok is false for a type this igris doesn't know. Its text is cleaned for
// the terminal: it comes over the socket.
func (ev Event) Engine() (engine.Event, bool) {
	kind, ok := knownKinds[ev.Type]
	if !ok {
		return engine.Event{}, false
	}
	out := engine.Event{
		At: ev.At, Kind: kind, Phase: textsafe.Line(ev.Phase), Task: textsafe.Line(ev.Task),
		Title: textsafe.Clean(ev.Title), Rank: textsafe.Line(ev.Rank), Model: textsafe.Line(ev.Model),
		Mode: textsafe.Line(ev.Mode), Verify: textsafe.Line(ev.Profile), Detail: textsafe.Clean(ev.Detail),
		Session: ev.Session, ClaudeSession: textsafe.Line(ev.ClaudeSession),
		Question: engine.Question(ev.Question), Plan: textsafe.Clean(ev.Plan), Then: textsafe.Line(ev.Then),
		Quota: ev.Quota,
	}
	if kind == engine.RunStarted {
		out.Run = textsafe.Line(ev.Run) // the envelope stamps every event; the engine this one
	}
	for _, ch := range ev.Changes {
		from, _, _ := plan.ParseStatus(ch.From)
		to, _, _ := plan.ParseStatus(ch.To)
		out.Changes = append(out.Changes, plan.Change{ID: textsafe.Line(ch.ID), From: from, To: to})
	}
	for _, w := range ev.Waiting {
		id, unmet := textsafe.Line(w.Task), make([]string, len(w.Unmet))
		for i, u := range w.Unmet {
			unmet[i] = textsafe.Line(u)
		}
		text := textsafe.Clean(w.Text)
		if text == "" {
			text = id + " waits on " + strings.Join(unmet, ", ")
		}
		out.Waiting = append(out.Waiting, plan.WaitingText(id, unmet, text))
	}
	for _, id := range ev.Slice {
		out.Slice = append(out.Slice, textsafe.Line(id))
	}
	for _, l := range ev.Output {
		out.Output = append(out.Output, textsafe.Clean(l))
	}
	if t := ev.Tokens; t != nil {
		out.Tokens = &engine.Tokens{In: t.In, Out: t.Out, Models: t.Models, Unavailable: t.Unavailable}
	}
	return out, true
}
