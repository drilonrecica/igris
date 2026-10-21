package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/serve"
)

// Seams for tests: serve's stdin, whether it is a terminal the owner can
// type the skip-permissions phrase into, and how long stop waits.
var (
	serveStdin    io.Reader = os.Stdin
	serveTerminal           = func() bool { return typable(os.Stdin) }
	stopTimeout             = 10 * time.Minute
)

// serveSignals stop a served run: Ctrl-C, kill, and a closed terminal.
var serveSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// shutdownWait is how long serve gives its clients to read the run's last
// events before it closes their connections.
const shutdownWait = 2 * time.Second

// execServe is `igris serve` (SPEC §14): arise's run, headless, hosted on
// the project's socket. Start-up answers come from stdin; the owner's
// commands come over the socket from attached clients and `igris stop`.
func execServe(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	f := runFlagValues(fs, args)
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris serve: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail("%v", err)
	}
	env := projectEnv()
	env.Serve = true // outside herdr and tmux, sessions go to a detached tmux session
	proj, err := project.Load(cwd, "", env)
	if err != nil {
		return fail("%v", err)
	}
	if err := proj.CheckSelection(f.request()); err != nil {
		return fail("%v", err)
	}
	l, err := proj.Launch(f.request())
	if err != nil {
		return fail("%v", err)
	}

	// SIGHUP too: a closed terminal stops the run, the sessions stay open.
	sigCtx, cancel, _ := signalContext(serveSignals...)
	defer cancel()
	ctx, stopEarly := context.WithCancel(sigCtx)
	defer stopEarly()
	out := &lockedWriter{w: stdout}
	r := bufio.NewReader(serveStdin)
	ask := func() (string, bool) { return readLine(ctx, r) }
	for _, c := range l.Prelaunch(ctx).Warnings {
		fmt.Fprintf(out, "warning: %s\n", c)
		if c.Confirm && !confirm(out, ask, "Start the run anyway?") {
			return fail("not confirmed; nothing was started")
		}
	}

	// The lock first: the socket is created by the run that holds it.
	lock, err := l.State.Lock(f.forceUnlock)
	if err != nil {
		return fail("%v", err)
	}
	defer func() {
		if err := lock.Release(); err != nil {
			fmt.Fprintf(stderr, "igris serve: %v\n", err)
		}
	}()
	ln, err := serve.Listen(l.State)
	if err != nil {
		return fail("the run's socket can't be created: %v", err)
	}
	host := &serveHost{stopEarly: stopEarly}
	srv := serve.New(serve.Options{
		Version: igrisVersion(), Project: proj.Name(), Backend: l.Backend.Name(), Mode: f.mode,
		Sender: host, Tail: host.tail,
		Plan: func() (*plan.Plan, error) { return plan.Load(proj.PlanPath, proj.Cfg.PlanOptions(proj.Root)) },
		Logf: func(format string, a ...any) {
			fmt.Fprintf(out, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
		},
	})
	go func() {
		if err := srv.Serve(ln); err != nil {
			fmt.Fprintf(out, "warning: %v\n", err)
		}
	}()
	defer func() {
		if !host.ended() {
			// The run never started: tell waiting clients it is over.
			detail := "error"
			if host.stopped() {
				detail = engine.Stopped.String()
			}
			srv.Publish(engine.Event{Kind: engine.RunStopped, At: time.Now(), Detail: detail})
		}
		sctx, done := context.WithTimeout(context.Background(), shutdownWait)
		defer done()
		_ = srv.Shutdown(sctx) // also removes the socket
	}()
	fmt.Fprintf(out, "serving the run on %s; `igris attach` shows it, `igris stop` stops it\n", ln.Path())
	if tb, ok := l.Backend.(*tmux.Backend); ok && tb.Headless() != "" {
		fmt.Fprintf(out, "sessions open in the tmux session %s: `tmux attach -t %s` shows them\n", tb.Headless(), tb.Headless())
	}

	conf := report.Confirmations{ForceUnlock: f.forceUnlock}
	for {
		opts := l.Options(conf, func(ev engine.Event) {
			printEvent(out, ev)
			srv.Publish(ev)
			host.saw(ev)
		})
		opts.LockHeld = true
		// Clients show plans for approval, as the TUI does (SPEC §6.3).
		opts.PlanApproval = true
		res, err := host.run(ctx, opts)
		if host.stopped() && !host.ended() {
			fmt.Fprintln(out, "stopped over the socket before the run started; nothing was started")
			return exitOK
		}
		if errors.Is(err, engine.ErrYoloUnconfirmed) && !conf.Yolo && !serveTerminal() {
			// SPEC §7.3: the phrase is typed, and only a terminal can.
			// The engine's error names the task; its advice is arise's.
			what := strings.TrimSuffix(err.Error(), ": "+engine.ErrYoloUnconfirmed.Error())
			return fail("%s: skip-permissions mode needs the typed phrase %q, and stdin is not a terminal to type it on; nothing was started (run igris serve in a terminal, or start the run with igris arise)", what, engine.YoloPhrase)
		}
		if asked, refusal := askStartUp(err, &conf, out, ask, !f.worktree && !proj.Cfg.Run.WorktreeOn()); asked {
			if refusal != "" {
				if host.stopped() {
					fmt.Fprintln(out, "stopped over the socket before the run started; nothing was started")
					return exitOK
				}
				return fail("%s", refusal)
			}
			continue
		}
		switch {
		case err != nil:
			return fail("%v", err)
		case res.Outcome == engine.Stuck:
			return exitFail
		default:
			printNotRun(out, res)
			return exitOK
		}
	}
}

// serveHost passes the clients' commands to the engine of the moment.
// Before a run started (while serve asks its start-up questions), a stop
// ends serve; other commands have nothing to go to.
type serveHost struct {
	stopEarly func()

	mu                 sync.Mutex
	eng                *engine.Engine
	stop, started, end bool
}

// Send implements serve.Sender. The engine is called outside h.mu: its
// events take h.mu (saw).
func (h *serveHost) Send(c engine.Command) {
	h.mu.Lock()
	eng := h.eng
	early := c.Kind == engine.CmdStop && !h.started
	if early {
		h.stop = true
	}
	h.mu.Unlock()
	switch {
	case early:
		// Ends the start-up questions, or the engine's start-up as a stop.
		h.stopEarly()
	case eng != nil:
		eng.Send(c)
	}
}

// run runs one engine with opts, taking commands while it runs.
func (h *serveHost) run(ctx context.Context, opts engine.Options) (engine.Result, error) {
	eng, err := engine.New(opts)
	if err != nil {
		return engine.Result{}, err
	}
	h.mu.Lock()
	if h.stop {
		h.mu.Unlock()
		return engine.Result{}, context.Canceled
	}
	h.eng = eng
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.eng = nil
		h.mu.Unlock()
	}()
	return eng.Run(ctx)
}

// tail reads the current session's last lines for a client.
func (h *serveHost) tail(ctx context.Context, n int) ([]string, error) {
	h.mu.Lock()
	eng := h.eng
	h.mu.Unlock()
	if eng == nil {
		return nil, nil
	}
	return eng.Tail(ctx, n)
}

// saw notes the run's start and end.
func (h *serveHost) saw(ev engine.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch ev.Kind {
	case engine.RunStarted:
		h.started = true
	case engine.RunStopped:
		h.end = true
	}
}

func (h *serveHost) stopped() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stop
}

func (h *serveHost) ended() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.end
}

// execStop is `igris stop` (SPEC §14): it asks this project's live run to
// stop over its socket and waits until it has.
func execStop(_ *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris stop: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	proj, err := loadProject("")
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel, _ := interruptContext()
	defer cancel()
	ctx, done := context.WithTimeout(ctx, stopTimeout)
	defer done()
	conn, err := serve.Dial(ctx, proj.Root)
	if err != nil {
		return fail("%v", err)
	}
	if err := serve.Stop(ctx, conn, "igris "+igrisVersion()); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintln(stdout, "the run stopped; its sessions stay open")
	return exitOK
}
