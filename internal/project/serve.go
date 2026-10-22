package project

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/tui"
)

// Since 1.4 a run started by `igris arise` with the TUI or by home's Arise
// is hosted by an `igris serve` started in the background, which this
// igris attaches to (SPEC §13, §14): the owner's start-up answers are
// asked here first, then handed to serve, the typed skip-permissions
// phrase in the hello and never as a flag (§7.3).

// ServeAnswersVar is the environment variable through which arise and home
// hand the serve they start the start-up questions the owner answered yes
// (ServeAnswers). Its value is not frozen.
const ServeAnswersVar = "IGRIS_SERVE_ANSWERS"

// ServeAnswers are the start-up questions answered yes in the igris that
// started a serve. The skip-permissions phrase is never one of them: it
// travels in the starter's hello.
type ServeAnswers struct {
	Start  bool // the start-up warnings that need a yes (Prelaunch)
	Drift  bool // readiness drift: let igris fix it (SPEC §5.2)
	Config bool // use the igris.toml changed during the last run (§13)
}

// String is a's value for ServeAnswersVar: "start,drift", say.
func (a ServeAnswers) String() string {
	var out []string
	for _, w := range []struct {
		on   bool
		word string
	}{{a.Start, "start"}, {a.Drift, "drift"}, {a.Config, "config"}} {
		if w.on {
			out = append(out, w.word)
		}
	}
	return strings.Join(out, ",")
}

// ParseServeAnswers reads ServeAnswersVar's value; words it doesn't know
// are no answer.
func ParseServeAnswers(v string) ServeAnswers {
	var a ServeAnswers
	for _, w := range strings.Split(v, ",") {
		switch strings.TrimSpace(w) {
		case "start":
			a.Start = true
		case "drift":
			a.Drift = true
		case "config":
			a.Config = true
		}
	}
	return a
}

// Spawn starts `igris serve` with args in the background in root, in a
// session of its own, with stdout and stderr going to log and env added to
// igris's environment; exited is closed once that process has ended.
type Spawn func(root string, args, env []string, log *os.File) (exited <-chan struct{}, err error)

// Timeouts of starting a serve.
var (
	// serveUp is how long a started serve may take to answer on its
	// socket: its start-up checks run version commands first.
	serveUp = 60 * time.Second
	// serveStart is how long it may then take to start the run.
	serveStart = 60 * time.Second
	// servePoll is how often the socket is tried meanwhile.
	servePoll = 25 * time.Millisecond
)

// serveArgs are `igris serve`'s arguments for a run of req with the
// owner's answers c: the run flags, then the phase.
func serveArgs(req report.RunRequest, c report.Confirmations) []string {
	var args []string
	flag := func(name, value string) {
		if value != "" {
			args = append(args, "--"+name, value)
		}
	}
	flag("through", req.Through)
	flag("mode", req.Mode)
	flag("only", strings.Join(req.Selection.Only, ","))
	flag("from", req.Selection.From)
	flag("until", req.Selection.Until)
	if req.Worktree {
		args = append(args, "--worktree")
	}
	if c.ForceUnlock {
		args = append(args, "--force-unlock")
	}
	args = append(args, "--")
	if req.Phase != "" {
		args = append(args, req.Phase)
	}
	return args
}

// Serve starts this launch's run with the owner's answers c in a
// background igris serve (stdout and stderr to .igris/serve.log) and
// attaches to it; sink gets the run from its start on. It first runs the
// run's start-up checks in this process (engine.Preflight) and returns the
// error the run would refuse to start with (drift, a changed igris.toml,
// skip-permissions, a lock), so the caller asks the owner and calls again:
// nothing is started then. The start-up warnings (Prelaunch) must have
// been asked already. When ctx ends before the run started, serve is
// asked to stop.
func (l *Launch) Serve(ctx context.Context, c report.Confirmations, sink serve.Sink) (*Served, error) {
	eng, err := engine.New(l.Options(c, nil))
	if err != nil {
		return nil, err
	}
	if err := eng.Preflight(ctx); err != nil {
		return nil, err
	}
	log, err := l.State.ServeLog()
	if err != nil {
		return nil, err
	}
	answers := ServeAnswers{Start: true, Drift: c.Drift, Config: c.Config}
	exited, err := l.p.env.spawn()(l.p.Root, serveArgs(l.req, c), []string{ServeAnswersVar + "=" + answers.String()}, log)
	_ = log.Close() // serve has its own
	if err != nil {
		return nil, fmt.Errorf("start igris serve: %w", err)
	}
	return l.p.attachStarting(ctx, exited, c.Yolo, sink)
}

// attachStarting waits for the serve just started to answer, attaches to
// it (with the typed phrase as yolo in the hello) and waits for its run to
// start, which sink then gets.
func (p *Project) attachStarting(ctx context.Context, exited <-chan struct{}, yolo bool, sink serve.Sink) (*Served, error) {
	ended := func() error {
		why := "it ended before the run started"
		if lines, _ := state.ServeLogTail(p.Root, 3); len(lines) > 0 {
			for i, l := range lines {
				lines[i] = textsafe.Line(l)
			}
			why += ": " + strings.Join(lines, " · ")
		}
		return fmt.Errorf("igris serve did not start the run: %s (.igris/serve.log has its output)", why)
	}
	up := time.NewTimer(serveUp)
	defer up.Stop()
	for !serve.Answers(ctx, p.Root) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-exited:
			return nil, ended()
		case <-up.C:
			return nil, errors.New("igris serve did not answer on the run's socket in time; .igris/serve.log may say why")
		case <-time.After(servePoll):
		}
	}
	gate := newStartGate(sink)
	// The client outlives ctx: once the run started, leaving is Close.
	c, err := serve.Attach(context.WithoutCancel(ctx), serve.ClientOptions{
		Name: "igris " + p.env.Version,
		Yolo: yolo,
		Dial: func(ctx context.Context) (net.Conn, error) { return serve.Dial(ctx, p.Root) },
		Sink: gate,
	})
	if err != nil {
		return nil, err
	}
	start := time.NewTimer(serveStart)
	defer start.Stop()
	select {
	case <-gate.started:
		return &Served{client: c, version: p.env.Version}, nil
	case <-gate.ended:
		c.Close()
		return nil, gate.endErr()
	case <-ctx.Done():
		// Before the run started this ends serve; nothing was started.
		c.Send(engine.Command{Kind: engine.CmdStop})
		c.Close()
		return nil, ctx.Err()
	case <-start.C:
		c.Close()
		return nil, errors.New("igris serve did not start the run in time; `igris attach` shows it if it starts, .igris/serve.log may say why")
	}
}

// Attach attaches to the run igris serve hosts in this project (home's
// Attach); sink gets it from its snapshot on. The error wraps
// serve.ErrNotServed when no run answers.
func (p *Project) Attach(ctx context.Context, sink serve.Sink) (*Served, error) {
	c, err := serve.Attach(context.WithoutCancel(ctx), serve.ClientOptions{
		Name: "igris " + p.env.Version,
		Dial: func(ctx context.Context) (net.Conn, error) { return serve.Dial(ctx, p.Root) },
		Sink: sink,
	})
	if err != nil {
		return nil, err
	}
	return &Served{client: c, version: p.env.Version}, nil
}

// Served is a run igris serve hosts, attached to through its socket: the
// run view's engine (tui.AttachedRunner). The run goes on when it is left.
type Served struct {
	client  *serve.Client
	version string
}

var _ tui.AttachedRunner = (*Served)(nil)

// Run waits until the run is over for this client (its sink has been told
// how) or ctx ends, which detaches: then it returns tui.ErrDetached and
// the run goes on.
func (s *Served) Run(ctx context.Context) (engine.Result, error) {
	select {
	case <-s.client.Done():
		return engine.Result{}, nil
	case <-ctx.Done():
		s.client.Close()
		return engine.Result{}, tui.ErrDetached
	}
}

// Send passes the owner's command to the run.
func (s *Served) Send(c engine.Command) { s.client.Send(c) }

// Tail reads the current session's last lines through the run.
func (s *Served) Tail(ctx context.Context, n int) ([]string, error) { return s.client.Tail(ctx, n) }

// ClientVersion is this igris's version, so the view can name another
// serving igris.
func (s *Served) ClientVersion() string { return s.version }

// Close detaches.
func (s *Served) Close() { s.client.Close() }

// startGate holds a run being started back from its sink until it started
// (run_started, or a snapshot of a started run): an error before that is
// the start's, told by Serve, not a run the view shows.
type startGate struct {
	sink    serve.Sink
	started chan struct{}
	ended   chan struct{}

	mu     sync.Mutex
	open   bool
	snap   *serve.Snapshot
	events []engine.Event
	err    error
}

func newStartGate(sink serve.Sink) *startGate {
	return &startGate{sink: sink, started: make(chan struct{}), ended: make(chan struct{})}
}

func (g *startGate) Snapshot(s serve.Snapshot) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open {
		g.sink.Snapshot(s)
		return
	}
	g.snap, g.events = &s, nil
	if s.Run != "" {
		g.openLocked()
	}
}

func (g *startGate) Push(ev engine.Event) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open {
		g.sink.Push(ev)
		return
	}
	g.events = append(g.events, ev)
	if ev.Kind == engine.RunStarted {
		g.openLocked()
	}
}

func (g *startGate) Lost() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open {
		g.sink.Lost()
	}
}

func (g *startGate) End(res engine.Result, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open {
		g.sink.End(res, err)
		return
	}
	select {
	case <-g.ended:
		return
	default:
	}
	if err == nil {
		err = fmt.Errorf("the run ended before it started (%s)", res.Outcome)
	}
	g.err = err
	close(g.ended)
}

// openLocked hands the sink what was held, in order, and lets the rest
// through.
func (g *startGate) openLocked() {
	g.open = true
	if g.snap != nil {
		g.sink.Snapshot(*g.snap)
	}
	for _, ev := range g.events {
		g.sink.Push(ev)
	}
	g.snap, g.events = nil, nil
	close(g.started)
}

func (g *startGate) endErr() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.err
}
