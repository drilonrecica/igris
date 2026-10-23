package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/qr"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

// Seams for tests: serve's stdin, whether it is a terminal the owner can
// type the skip-permissions phrase into, how long stop waits, the answers
// of the igris that started this serve and how long serve waits for it.
var (
	serveStdin      io.Reader = os.Stdin
	serveTerminal             = func() bool { return typable(os.Stdin) }
	stopTimeout               = 10 * time.Minute
	lockReleaseWait           = 5 * time.Second
	serveStarter              = starterAnswers
	starterWait               = 30 * time.Second
)

// starterAnswers reads the start-up answers of the igris (arise, home)
// that started this serve in the background, from its environment
// (project.ServeAnswersVar), and removes them from it, so nothing serve
// starts inherits them. ok is false for a serve the owner started.
func starterAnswers() (project.ServeAnswers, bool) {
	v, ok := os.LookupEnv(project.ServeAnswersVar)
	if !ok {
		return project.ServeAnswers{}, false
	}
	_ = os.Unsetenv(project.ServeAnswersVar)
	return project.ParseServeAnswers(v), true
}

// serveSignals stop a served run: Ctrl-C, kill, and a closed terminal.
var serveSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// shutdownWait is how long serve gives its clients to read the run's last
// events before it closes their connections.
const shutdownWait = 2 * time.Second

// serveTokenArg is the word that makes `igris serve token` (SPEC §14).
const serveTokenArg = "token"

// serveFlags registers serve's flags: arise's run flags, the HTTP
// listener's, and --rotate for `serve token`.
func serveFlags(fs *flag.FlagSet) func([]string) error {
	runValidate := runFlags(fs)
	httpAddr := fs.String("http", "", "also listen on HTTP at ADDR:PORT for the web UI and ntfy answers (in place of [serve] http)")
	fs.Bool("listen-public", false, "let --http or [serve] http be an address beyond loopback and Tailscale (needs TLS)")
	fs.Bool("rotate", false, "serve token only: replace the token, unpairing every device")
	return func(args []string) error {
		if len(args) > 0 && args[0] == serveTokenArg {
			var other []string
			fs.Visit(func(f *flag.Flag) {
				if f.Name != "rotate" {
					other = append(other, "--"+f.Name)
				}
			})
			if len(other) > 0 {
				return fmt.Errorf("serve token takes only --rotate, not %s", strings.Join(other, ", "))
			}
			return atMost(args, 1)
		}
		if flagSet(fs, "rotate") {
			return errors.New("--rotate is for `igris serve token`")
		}
		if flagSet(fs, "http") {
			if _, err := config.ParseHTTPAddr(*httpAddr); err != nil {
				return fmt.Errorf("invalid --http %q: %v", *httpAddr, err)
			}
		}
		return runValidate(args)
	}
}

// execServe is `igris serve` (SPEC §14): arise's run, headless, hosted on
// the project's socket. Start-up answers come from stdin; the owner's
// commands come over the socket from attached clients and `igris stop`.
func execServe(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == serveTokenArg {
		return execServeToken(fs.Lookup("rotate").Value.String() == "true", stdout, stderr)
	}
	f := runFlagValues(fs, args)
	// Started by arise or home, serve takes the start-up answers from
	// that igris: the questions from its environment, the typed
	// skip-permissions phrase from its hello (SPEC §14).
	answers, started := serveStarter()
	failed := "" // why serve ends before its run started, for the clients
	fail := func(format string, a ...any) int {
		failed = fmt.Sprintf(format, a...)
		fmt.Fprintf(stderr, "igris serve: %s\n", failed)
		return exitFail
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail("%v", err)
	}
	env := projectEnv()
	env.Serve = true // outside herdr and tmux, sessions go to a detached tmux session
	// A serve never starts a serve. Its depth mark (SpawnServe) is not for
	// the sessions: a nested igris in one starts its own serve.
	env.Spawn = project.SpawnRefused("igris serve does not start another igris serve")
	_ = os.Unsetenv(project.SpawnDepthVar)
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
		if c.Confirm && !answers.Start && !confirm(out, ask, "Start the run anyway?") {
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
	// The HTTP listener's rules are checked before the socket exists, so a
	// refusal leaves nothing behind (SPEC §14).
	web, err := listenWeb(l.State, proj.Cfg.Serve, fs.Lookup("http").Value.String(), fs.Lookup("listen-public").Value.String() == "true", out)
	if err != nil {
		return fail("the HTTP listener can't start: %v", err)
	}
	if web != nil {
		defer func() { _ = web.Close() }() // also when serve fails before it serves
	}
	ln, err := serve.Listen(l.State)
	if err != nil {
		return fail("the run's socket can't be created: %v", err)
	}
	host := &serveHost{stopEarly: stopEarly}
	// The first hello is the starter's; a later client's yolo changes
	// nothing (it confirms with the mode command instead).
	firstHello := make(chan serve.Request, 1)
	var helloOnce sync.Once
	var onHello func(serve.Request)
	if started {
		onHello = func(r serve.Request) { helloOnce.Do(func() { firstHello <- r }) }
	}
	srv := serve.New(serve.Options{
		Hello:   onHello,
		Version: igrisVersion(), Project: proj.Name(), Backend: l.Backend.Name(), Mode: f.mode,
		Sender: host, Tail: host.tail,
		Plan: func() (*plan.Plan, error) { return plan.Load(proj.PlanPath, proj.Cfg.PlanOptions(proj.Root)) },
		Logf: func(format string, a ...any) {
			fmt.Fprintf(out, "%s %s\n", look.ClockSeconds(time.Now()), fmt.Sprintf(format, a...))
		},
	})
	go func() {
		if err := srv.Serve(ln); err != nil {
			fmt.Fprintf(out, "warning: %v\n", err)
		}
	}()
	// ntfy's answer buttons go to the listener as the phone reaches it.
	var buttons notify.Actions
	if web != nil {
		buttons = serveButtons(srv, web, proj.Cfg.Notify.Ntfy, out)
		root := l.State.Root()
		h := srv.Remote(serve.RemoteOptions{TLS: web.TLS, UI: srv.UI(serve.UIOptions{}), Token: func() (string, error) { return state.ReadServeToken(root) }})
		go func() {
			if err := srv.ServeRemote(web, h); err != nil {
				fmt.Fprintf(out, "warning: the HTTP listener stopped: %v\n", err)
			}
		}()
	}
	defer func() {
		if !host.ended() {
			// The run never started: tell waiting clients it is over.
			detail := "error"
			if host.stopped() {
				detail = engine.Stopped.String()
			} else if failed != "" {
				srv.Publish(engine.Event{Kind: engine.RunFailed, At: time.Now(), Detail: failed})
			}
			srv.Publish(engine.Event{Kind: engine.RunStopped, At: time.Now(), Detail: detail})
		}
		sctx, done := context.WithTimeout(context.Background(), shutdownWait)
		defer done()
		_ = srv.Shutdown(sctx) // also removes the socket
		// Serve's goroutine may not have taken ln yet (a refusal right
		// after Listen); the socket goes now, not when it runs.
		_ = ln.Close()
	}()
	fmt.Fprintf(out, "serving the run on %s; `igris attach` shows it, `igris stop` stops it\n", ln.Path())
	if web != nil {
		fmt.Fprintf(out, "web UI on %s/ for paired devices; `igris serve token` pairs one\n", web.URL())
	}
	if tb, ok := l.Backend.(*tmux.Backend); ok && tb.Headless() != "" {
		fmt.Fprintf(out, "sessions open in the tmux session %s: `tmux attach -t %s` shows them\n", tb.Headless(), tb.Headless())
	}

	conf := report.Confirmations{ForceUnlock: f.forceUnlock, Drift: answers.Drift, Config: answers.Config}
	if started {
		select {
		case r := <-firstHello:
			conf.Yolo = r.Yolo
		case <-time.After(starterWait):
			fmt.Fprintln(out, "no client attached in time; the run starts without one")
		case <-ctx.Done():
			fmt.Fprintln(out, "stopped before the run started; nothing was started")
			return exitOK
		}
	}
	for {
		start := newRunStart(proj, f)
		opts := l.Options(conf, func(ev engine.Event) {
			logLines(out, ev, formatServed(ev), start)
			srv.Publish(ev)
			host.saw(ev)
		})
		opts.LockHeld = true
		opts.Actions = buttons
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
			return fail("%s: skip-permissions mode needs the typed phrase %q, and stdin is not a terminal to type it on; nothing was started (run `igris serve` in a terminal, or start the run with `igris arise`)", what, engine.YoloPhrase)
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

// formatServed renders ev for serve's plain log: --no-tui's lines, minus
// their advice to type a command, which serve doesn't read from stdin
// (SPEC §14): the owner answers in an attached client instead.
func formatServed(ev engine.Event) []string {
	const attach = "answer in `igris attach`"
	switch ev.Kind {
	case engine.Asked:
		return []string{"? " + ev.Detail, "  " + attach}
	case engine.YourTurn:
		return []string{
			look.GlyphNeedsYou + " " + ev.Task + " your turn: " + ev.Detail,
			"  do it outside igris (no session), then run `igris done " + ev.Task + "` anywhere in the project (or " + attach + ")",
		}
	case engine.PauseOn:
		return []string{"pause after task: on"}
	case engine.Paused:
		return []string{"paused before " + ev.Task + "; Pause in `igris attach` continues"}
	}
	return formatEvent(ev)
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

// tail reads the last lines of task's session ("" the current task's)
// for a client.
func (h *serveHost) tail(ctx context.Context, task string, n int) ([]string, error) {
	h.mu.Lock()
	eng := h.eng
	h.mu.Unlock()
	if eng == nil {
		return nil, nil
	}
	return eng.TailTask(ctx, task, n)
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
	waitLockFree(ctx, proj.Root)
	fmt.Fprintln(stdout, "the run stopped; its sessions stay open")
	newOut(stdout).next("igris status")
	return exitOK
}

// waitLockFree gives the stopped serve a moment to release the run lock,
// which it does just after the run's end reaches its clients, so that
// `igris stop && igris arise` finds the project free (and so does arise
// after a run that ended while a view was attached). A lock whose process
// is gone won't be released: it returns at once.
func waitLockFree(ctx context.Context, root string) {
	ctx, cancel := context.WithTimeout(ctx, lockReleaseWait)
	defer cancel()
	for {
		if l, err := state.PeekLock(root); err != nil || !l.Held || !l.Alive {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// listenWeb binds serve's HTTP listener when an address is set (flag, then
// [serve] http), or returns nil. It makes the owner token when there is
// none, without printing it: serve's output goes to serve.log.
func listenWeb(d *state.Dir, cfg config.Serve, flagAddr string, public bool, out io.Writer) (*serve.HTTPListener, error) {
	addr := cfg.HTTP
	if flagAddr != "" {
		addr = flagAddr
	}
	if addr == "" {
		if public {
			return nil, errors.New("--listen-public needs an address: pass --http ADDR:PORT or set [serve] http")
		}
		return nil, nil
	}
	hc := serve.HTTPConfig{Addr: addr, Public: public}
	if cfg.TLS() {
		hc.CertFile, hc.KeyFile = rootPath(d.Root(), cfg.TLSCert), rootPath(d.Root(), cfg.TLSKey)
	}
	l, err := serve.ListenHTTP(hc)
	if err != nil {
		return nil, err
	}
	_, created, err := d.EnsureServeToken()
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	if l.Exposure == serve.Public {
		fmt.Fprintf(out, "warning: the HTTP listener on %s is reachable beyond loopback and Tailscale; only the token and TLS guard it\n", l.Addr())
	}
	if created {
		fmt.Fprintln(out, "no device is paired yet; `igris serve token` pairs one")
	}
	return l, nil
}

// serveButtons is what makes ntfy's answer buttons for a run served on
// web (SPEC §10), or nil when they are off. A listener on every address
// has no URL a phone could use, so it gets none, with a warning.
func serveButtons(srv *serve.Server, web *serve.HTTPListener, ntfy config.Ntfy, out io.Writer) notify.Actions {
	if !ntfy.Actions || ntfy.Topic == "" {
		return nil
	}
	if ap, err := netip.ParseAddrPort(web.Addr().String()); err == nil && ap.Addr().IsUnspecified() {
		fmt.Fprintf(out, "warning: ntfy's answer buttons are left out: the HTTP listener takes every address (%s), so no URL in a button would reach it; set [serve] http to one address\n", ap.Addr())
		return nil
	}
	fmt.Fprintf(out, "ntfy's answer buttons go to %s\n", web.URL())
	return srv.Buttons(web.URL())
}

// execServeToken is `igris serve token [--rotate]` (SPEC §14): it prints
// the pairing URL of the project's HTTP listener, making the token first
// when there is none, or a new one with rotate.
func execServeToken(rotate bool, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris serve token: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	proj, err := loadProject("")
	if err != nil {
		return fail("%v", err)
	}
	d, err := state.Open(proj.Root, state.Options{})
	if err != nil {
		return fail("%v", err)
	}
	var tok string
	if rotate {
		tok, err = d.RotateServeToken()
	} else {
		tok, _, err = d.EnsureServeToken()
	}
	if err != nil {
		return fail("%v", err)
	}
	base, note := pairingBase(proj.Cfg.Serve)
	if rotate {
		fmt.Fprintln(stdout, "the token was replaced: every paired device must pair again")
	}
	fmt.Fprintln(stdout, "Open this URL on the device to pair it; anyone with it controls the run (short of skip-permissions mode):")
	url := fmt.Sprintf("%s%s?token=%s", base, serve.PathPair, tok)
	fmt.Fprintf(stdout, "  %s\n", url)
	o := newOut(stdout)
	if note != "" {
		o.println(note)
		o.next("igris serve token")
		return exitOK // the URL needs editing first: a code of it would pair nothing
	}
	printQR(stdout, url)
	o.next("igris arise")
	return exitOK
}

// printQR draws url as a QR code in half blocks, black on white whatever
// the terminal's colours, so a phone camera reads it. A URL too long for
// version 10 is left as text.
func printQR(w io.Writer, url string) {
	c, err := qr.Encode([]byte(url))
	if err != nil {
		return
	}
	fmt.Fprintln(w, "or scan it:")
	for _, l := range c.HalfBlocks() {
		fmt.Fprintf(w, "\x1b[30;107m%s\x1b[0m\n", l)
	}
}

// pairingBase is the listener's base URL as [serve] says it, and a note
// when the owner has to fill in or change the address.
func pairingBase(cfg config.Serve) (base, note string) {
	scheme := "http"
	if cfg.TLS() {
		scheme = "https"
	}
	if cfg.HTTP == "" {
		return scheme + "://ADDR:PORT", `set [serve] http in igris.toml (e.g. http = "127.0.0.1:7777" for ssh -L, or this machine's Tailscale address) and put it in place of ADDR:PORT`
	}
	ap, err := config.ParseHTTPAddr(cfg.HTTP)
	if err != nil { // the config loaded, so it parses
		return scheme + "://" + cfg.HTTP, ""
	}
	base = scheme + "://" + ap.String()
	if ap.Addr().IsUnspecified() {
		note = fmt.Sprintf("the listener takes every address (%s): put this machine's own address in place of %s", ap.Addr(), ap.Addr())
	}
	return base, note
}
