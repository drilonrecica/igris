package serve

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Sender takes the clients' commands; *engine.Engine is one.
type Sender interface {
	Send(engine.Command)
}

// Options configures a Server.
type Options struct {
	// Version is the serving igris's version, e.g. "1.4.0".
	Version string
	Project string // shown in the clients' header
	Backend string // the backend's name
	Mode    string // the run mode chosen for the run; "" for none

	// Sender gets every valid command. Nil drops them, still answered
	// with an ack (a run that hasn't started yet).
	Sender Sender
	// Tail reads the last lines of active task's session, "" for the
	// current task's (engine.Engine.TailTask); nil answers every tail
	// request with no lines.
	Tail func(ctx context.Context, task string, n int) ([]string, error)
	// Plan loads the plan for a snapshot's phases; nil sends none.
	Plan func() (*plan.Plan, error)
	// Hello is called with every client's hello, once its snapshot is
	// queued, so a run it starts can't end before that client has its
	// snapshot; the host takes a launching client's confirmations from it
	// (SPEC §14). It must not block.
	Hello func(Request)
	// Logf reports what happened to clients (a dropped slow client, a
	// refused peer); nil discards it. Its text never holds a message's
	// content.
	Logf func(format string, args ...any)

	// QueueBytes and QueueLen bound what may wait for one client before
	// it is dropped as too slow; zero means 4 MiB and 4096 messages.
	QueueBytes int64
	QueueLen   int
}

// Default per-client bounds (SPEC §13).
const (
	defaultQueueBytes = 4 << 20
	defaultQueueLen   = 4096
)

// Timeouts of a connection.
const (
	helloTimeout = 10 * time.Second // to say hello after connecting
	writeTimeout = 10 * time.Second // to take one message
	tailTimeout  = 10 * time.Second // for the backend to read a tail
)

// Server hosts one run for any number of clients. Publish fans the
// engine's events out to them; their commands go to Options.Sender.
type Server struct {
	opts Options
	// uid is the user a peer must run as; peerUID reads a peer's (Linux).
	uid     int
	peerUID func(*net.UnixConn) (int, error)
	// helloTimeout is how long a new connection may take to say hello.
	helloTimeout time.Duration

	mu        sync.Mutex
	track     *tracker
	clients   map[*client]struct{}
	listeners []net.Listener
	// https are the HTTP listeners' servers (ServeRemote).
	https  []*http.Server
	closed bool

	wg sync.WaitGroup
}

// New returns a Server for opts.
func New(opts Options) *Server {
	if opts.QueueBytes <= 0 {
		opts.QueueBytes = defaultQueueBytes
	}
	if opts.QueueLen <= 0 {
		opts.QueueLen = defaultQueueLen
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return &Server{
		opts: opts, uid: os.Getuid(), peerUID: peerUID, helloTimeout: helloTimeout,
		track: newTracker(opts.Mode), clients: map[*client]struct{}{},
	}
}

// Publish hands ev to every client and folds it into the snapshot. It
// never blocks: a client whose queue is full is dropped. Call it from the
// engine's Events callback.
func (s *Server) Publish(ev engine.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.track.run
	if ev.Kind == engine.RunStarted && ev.Run != "" {
		run = ev.Run
	}
	wire := fromEngine(ev, run)
	s.track.apply(ev, wire)
	b, err := encodeEvent(wire)
	if err != nil {
		s.opts.Logf("event %s not sent: %v", wire.Type, err)
		return
	}
	for c := range s.clients {
		if c.ready {
			c.enqueue(b)
		}
	}
}

// Clients returns how many clients are attached.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Serve accepts clients on l until l is closed or Close is called, and
// then returns nil. On Linux a peer of another user is refused (SPEC
// §16). l is closed when Serve returns, also when Shutdown or Close came
// first: a serve that ends before this goroutine runs must not leave its
// socket behind.
func (s *Server) Serve(l net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = l.Close()
		return nil
	}
	s.listeners = append(s.listeners, l)
	s.mu.Unlock()
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return fmt.Errorf("accept on the run's socket: %w", err)
		}
		if !s.allowed(conn) {
			_ = conn.Close()
			continue
		}
		// Count the connection under the lock: Close marks the server
		// closed there before it waits, so a connection accepted as
		// Close runs is counted before the wait or dropped, never
		// counted after it (the WaitGroup contract).
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			s.ServeConn(conn)
		}()
	}
}

// allowed checks a unix peer's uid where the system tells it.
func (s *Server) allowed(conn net.Conn) bool {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return true
	}
	uid, err := s.peerUID(uc)
	switch {
	case errors.Is(err, errNoPeerCred):
		return true // the socket's mode and directory are the guard
	case err != nil:
		s.opts.Logf("client refused: its user can't be checked: %v", err)
		return false
	case uid != s.uid:
		s.opts.Logf("client refused: it runs as uid %d, not %d", uid, s.uid)
		return false
	}
	return true
}

// Close ends every connection and listener and waits for them.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	ls := s.listeners
	s.listeners = nil
	hs := s.https
	s.https = nil
	for c := range s.clients {
		c.close()
	}
	s.mu.Unlock()
	var first error
	for _, l := range ls {
		if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) && first == nil {
			first = err
		}
	}
	for _, h := range hs {
		if err := h.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.wg.Wait()
	return first
}

// Shutdown stops accepting clients and ends every connection once what is
// queued for it is written, so the run's last events reach the clients.
// When ctx ends first, it closes what is left as Close does.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	ls := s.listeners
	s.listeners = nil
	hs := s.https
	s.https = nil
	for c := range s.clients {
		c.end()
	}
	s.mu.Unlock()
	var first error
	for _, l := range ls {
		if err := l.Close(); err != nil && !errors.Is(err, net.ErrClosed) && first == nil {
			first = err
		}
	}
	done := make(chan struct{})
	go func() {
		// The event streams end with their clients (above); the HTTP
		// servers then have only idle connections left.
		for _, h := range hs {
			if err := h.Shutdown(ctx); err != nil {
				_ = h.Close()
			}
		}
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		for _, h := range hs {
			_ = h.Close()
		}
		if err := s.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// client is one attached connection. Everything sent to it goes through
// its queue, written by its own goroutine, so a slow reader never holds
// up the engine or the other clients.
type client struct {
	srv   *Server
	conn  net.Conn
	out   chan []byte
	bytes atomic.Int64 // queued, not yet written
	done  chan struct{}
	once  sync.Once
	// ready is set (under srv.mu) once the snapshot is queued: events
	// follow it.
	ready bool
	// tailing is set while a tail request runs; one at a time.
	tailing atomic.Bool
	// ending says the queue ends with nil: the writer closes the
	// connection once what comes before is written.
	ending atomic.Bool
}

// enqueue queues b, or drops the client when its queue is full. It never
// blocks.
func (c *client) enqueue(b []byte) bool {
	if c.bytes.Add(int64(len(b))) > c.srv.opts.QueueBytes {
		c.drop()
		return false
	}
	select {
	case c.out <- b:
		return true
	default:
		c.drop()
		return false
	}
}

func (c *client) drop() {
	c.srv.opts.Logf("client dropped: it read too slowly; it gets a fresh snapshot when it attaches again")
	c.close()
}

// end closes the connection once the queue before it is written; a full
// queue closes it at once.
func (c *client) end() {
	c.ending.Store(true)
	select {
	case c.out <- nil:
	default:
		c.close()
	}
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// writer writes the queue until the client closes.
func (c *client) writer() {
	for {
		select {
		case <-c.done:
			return
		case b := <-c.out:
			if b == nil {
				c.close()
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := c.conn.Write(b); err != nil {
				c.close()
				return
			}
			c.bytes.Add(-int64(len(b)))
		}
	}
}

// ServeConn talks protocol 1 with one client until it goes away or the
// server closes. It is what Serve runs for each connection.
func (s *Server) ServeConn(conn net.Conn) {
	s.serveClient(conn, s.read)
}

// serveClient attaches conn as a client, runs body for it and detaches it
// when body returns.
func (s *Server) serveClient(conn net.Conn, body func(*client)) {
	c := &client{srv: s, conn: conn, out: make(chan []byte, s.opts.QueueLen), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return
	}
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.writer()
	}()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		if !c.ending.Load() {
			c.close()
		}
		wg.Wait() // after an error, until the writer has sent it
	}()
	body(c)
}

// read handles the client's messages. A malformed one ends the
// connection with an error; an unknown or refused command gets an error
// and the connection goes on.
func (s *Server) read(c *client) {
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 0, 64<<10), MaxLine+1)
	_ = c.conn.SetReadDeadline(time.Now().Add(s.helloTimeout))
	hello := false
	for sc.Scan() {
		r, err := decodeRequest(sc.Bytes())
		if err != nil {
			s.fatal(c, err)
			return
		}
		if !hello {
			if r.Type != TypeHello {
				s.fatal(c, errNoHello)
				return
			}
			hello = true
			_ = c.conn.SetReadDeadline(time.Time{})
			s.hello(c, r)
			continue
		}
		if !s.handle(c, r) {
			return
		}
	}
	if c.ending.Load() {
		return
	}
	if errors.Is(sc.Err(), bufio.ErrTooLong) {
		s.fatal(c, fmt.Errorf("a message over %d bytes", MaxLine))
	}
}

// hello answers a client's hello with the snapshot; the events follow it.
func (s *Server) hello(c *client, r Request) {
	r.Client = cut(textsafe.Line(r.Client), maxClient)
	s.snapshot(c)
	if s.opts.Hello != nil {
		s.opts.Hello(r)
	}
}

// Snapshot returns the run as a client starting now would see it (the
// web UI's pages, SPEC §15.8).
func (s *Server) Snapshot() Snapshot {
	phases, planErr := s.phases()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(phases, planErr)
}

// phases reads the plan for a snapshot, outside the lock: the plan is a
// file, events must not wait.
func (s *Server) phases() (phases []Phase, planErr string) {
	if s.opts.Plan == nil {
		return nil, ""
	}
	p, err := s.opts.Plan()
	if err != nil {
		return nil, cut(textsafe.Line(err.Error()), 4<<10)
	}
	if p != nil {
		phases = phasesOf(p)
	}
	return phases, ""
}

// snapshotLocked is the snapshot with phases; s.mu is held.
func (s *Server) snapshotLocked(phases []Phase, planErr string) Snapshot {
	snap := s.track.snapshot(phases)
	snap.Server, snap.Project, snap.Backend = "igris "+s.opts.Version, textsafe.Line(s.opts.Project), s.opts.Backend
	snap.Phases, snap.PlanError = phases, planErr
	return snap
}

// snapshot queues the run's snapshot for c; the events follow it.
func (s *Server) snapshot(c *client) {
	phases, planErr := s.phases()
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := encodeSnapshot(s.snapshotLocked(phases, planErr))
	if err != nil {
		s.opts.Logf("snapshot not sent: %v", err)
		c.close()
		return
	}
	if c.enqueue(b) {
		c.ready = true
	}
}

// handle answers a command or a tail request. It reports false when the
// message ends the connection.
func (s *Server) handle(c *client, r Request) bool {
	if err := checkID(r.ID); err != nil {
		s.fatal(c, err)
		return false
	}
	switch r.Type {
	case TypeHello:
		c.reply(Reply{Type: TypeError, ID: r.ID, Error: "hello was already said"})
	case TypeTail:
		s.tail(c, r)
	default:
		cmd, err := r.command()
		if err != nil {
			c.reply(Reply{Type: TypeError, ID: r.ID, Error: err.Error()})
			return true
		}
		if s.opts.Sender != nil {
			s.opts.Sender.Send(cmd)
		}
		c.reply(Reply{Type: TypeAck, ID: r.ID})
	}
	return true
}

// tail reads the session's last lines off the reading goroutine, so
// commands keep flowing while the backend answers.
func (s *Server) tail(c *client, r Request) {
	if rep, bad := checkTail(r); bad {
		c.reply(rep)
		return
	}
	if s.opts.Tail == nil {
		c.reply(Reply{Type: TypeTail, ID: r.ID})
		return
	}
	if !c.tailing.CompareAndSwap(false, true) {
		c.reply(Reply{Type: TypeError, ID: r.ID, Error: "a tail is already being read"})
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer c.tailing.Store(false)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-c.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		c.reply(s.readTail(ctx, r))
	}()
}

// checkTail checks a tail request's n and task; bad reports a refusal.
func checkTail(r Request) (rep Reply, bad bool) {
	if r.N < 1 || r.N > maxTail {
		return Reply{Type: TypeError, ID: r.ID, Error: fmt.Sprintf("n is 1 to %d", maxTail)}, true
	}
	if r.Task != "" && (len(r.Task) > maxID || !plan.ValidID(r.Task)) {
		return Reply{Type: TypeError, ID: r.ID, Error: fmt.Sprintf("%q is not a task ID", textsafe.Line(cut(r.Task, maxID)))}, true
	}
	return Reply{}, false
}

// readTail reads a checked tail request's lines, within tailTimeout.
func (s *Server) readTail(ctx context.Context, r Request) Reply {
	if s.opts.Tail == nil {
		return Reply{Type: TypeTail, ID: r.ID}
	}
	ctx, cancel := context.WithTimeout(ctx, tailTimeout)
	defer cancel()
	lines, err := s.opts.Tail(ctx, r.Task, r.N)
	if err != nil {
		return Reply{Type: TypeError, ID: r.ID, Error: "tail: " + cut(textsafe.Line(err.Error()), 4<<10)}
	}
	out := make([]string, 0, len(lines))
	for _, l := range tail(lines, r.N) {
		out = append(out, cut(textsafe.Clean(l), 4<<10))
	}
	return Reply{Type: TypeTail, ID: r.ID, Lines: out}
}

// reply queues r for the client.
func (c *client) reply(r Reply) {
	r.V = Protocol
	b, err := encode(r)
	if err != nil {
		c.srv.opts.Logf("reply not sent: %v", err)
		return
	}
	c.enqueue(b)
}

// fatal tells the client why its connection ends; the writer closes it
// once that is written.
func (s *Server) fatal(c *client, err error) {
	c.reply(Reply{Type: TypeError, Error: err.Error()})
	select {
	case c.out <- nil: // the last message
		c.ending.Store(true)
	default:
		c.close()
	}
}
