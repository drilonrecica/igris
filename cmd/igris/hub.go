package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/hub"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Seams for tests: where the hub token lives, the live runs, the
// checklist's stdin and whether it is on a terminal, and the context the
// hub runs in until a signal ends it.
var (
	hubTokenEnv           = state.SystemHubTokenEnv
	hubLive               = func(ctx context.Context) []state.RunEntry { r, _ := state.LiveRuns(ctx); return r }
	hubStdin    io.Reader = os.Stdin
	hubContext            = func() (context.Context, context.CancelFunc) {
		ctx, cancel, _ := signalContext(os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		return ctx, cancel
	}
)

// hubTokenArg is the word that makes `igris hub token` (SPEC §14).
const hubTokenArg = "token"

// hubTLSFlags name the hub's TLS setting in its errors.
const hubTLSFlags = "--tls-cert and --tls-key"

// hubFlags registers hub's flags and checks them.
func hubFlags(fs *flag.FlagSet) func([]string) error {
	all := fs.Bool("all", false, "every live run and every run started later, without the checklist")
	httpAddr := fs.String("http", "", "listen on HTTP at ADDR:PORT (required): a loopback or Tailscale address, or another with --listen-public")
	fs.Bool("listen-public", false, "let --http be an address beyond loopback and Tailscale (needs TLS)")
	cert := fs.String("tls-cert", "", "serve TLS with this PEM certificate (with --tls-key)")
	key := fs.String("tls-key", "", "the PEM key of --tls-cert")
	fs.Bool("rotate", false, "hub token only: replace the token, unpairing every device")
	return func(args []string) error {
		if len(args) > 0 && args[0] == hubTokenArg {
			var other []string
			fs.Visit(func(f *flag.Flag) {
				if f.Name != "rotate" {
					other = append(other, "--"+f.Name)
				}
			})
			if len(other) > 0 {
				return fmt.Errorf("hub token takes only --rotate, not %s", strings.Join(other, ", "))
			}
			return atMost(args, 1)
		}
		switch {
		case flagSet(fs, "rotate"):
			return errors.New("--rotate is for `igris hub token`")
		case *all && len(args) > 0:
			return errors.New("--all takes every live run; leave it out to pin projects by PATH")
		case *httpAddr == "":
			return errors.New("the hub needs --http ADDR:PORT, e.g. 127.0.0.1:7777 for ssh -L, or this machine's Tailscale address")
		case (*cert == "") != (*key == ""):
			return errors.New("--tls-cert and --tls-key go together")
		}
		if _, err := config.ParseHTTPAddr(*httpAddr); err != nil {
			return fmt.Errorf("invalid --http %q: %v", *httpAddr, err)
		}
		return nil
	}
}

// hubConfig is what `igris hub` was asked for.
type hubConfig struct {
	all       bool
	paths     []string
	addr      string
	public    bool
	cert, key string
}

// execHub is `igris hub` (SPEC §14): the chosen live runs behind one HTTP
// listener and one pairing, until a signal ends the hub (never a run).
func execHub(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == hubTokenArg {
		return execHubToken(fs.Lookup("rotate").Value.String() == "true", stdout, stderr)
	}
	c := hubConfig{
		all: fs.Lookup("all").Value.String() == "true", paths: args,
		addr: fs.Lookup("http").Value.String(), public: fs.Lookup("listen-public").Value.String() == "true",
		cert: fs.Lookup("tls-cert").Value.String(), key: fs.Lookup("tls-key").Value.String(),
	}
	ctx, cancel := hubContext()
	defer cancel()
	return runHub(ctx, c, stdout, stderr)
}

// runHub runs the hub until ctx ends.
func runHub(ctx context.Context, c hubConfig, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris hub: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	o := newOut(stdout)
	// Which runs: the pinned projects', the checklist's, or all.
	live := hubLive(ctx)
	all, roots := c.all, []string(nil)
	switch {
	case len(c.paths) > 0:
		var missing []string
		for _, p := range c.paths {
			root, ok := pinnedRoot(p, live)
			if !ok {
				missing = append(missing, root)
				continue
			}
			roots = append(roots, root)
		}
		if len(missing) > 0 {
			for _, m := range missing {
				fmt.Fprintf(stderr, "igris hub: no run is live in %s; start one there with `igris arise` (or `igris serve`)\n", m)
			}
			return exitFail
		}
	case all:
	case !interactiveTerm():
		fmt.Fprintln(stderr, "igris hub: the checklist needs a terminal; pass --all, or the PATH of each project to put in the hub")
		return exitUsage
	case len(live) == 0:
		o.println("no run is live yet; runs join the hub as they start")
		all = true
	default:
		var ok bool
		all, roots, ok = chooseRuns(hubStdin, stdout, live)
		if !ok {
			o.println("nothing chosen; the hub didn't start")
			return exitOK
		}
	}

	tok, path, created, err := state.EnsureHubToken(hubTokenEnv())
	if err != nil {
		return fail("%v", err)
	}
	if c.cert != "" {
		if c.cert, err = filepath.Abs(c.cert); err == nil {
			c.key, err = filepath.Abs(c.key)
		}
		if err != nil {
			return fail("%v", err)
		}
	}
	l, err := listenHub(c)
	if err != nil {
		return fail("%v", err)
	}
	if l.Exposure == serve.Public {
		o.warnf("the hub on %s is reachable beyond loopback and Tailscale; only the token and TLS guard it", l.Addr())
	}
	if created {
		o.printf("the hub's token is new (%s): every device pairs once", path)
	}

	h := hub.New(hub.Options{All: all, Roots: roots, Name: "igris " + igrisVersion(), Live: hubLive, Out: stdout})
	hs := serve.HTTPServer(h.Handler(hub.HandlerOptions{TLS: l.TLS, Token: func() (string, error) { return state.ReadHubToken(path) }}))
	served := make(chan error, 1)
	go func() { served <- hs.Serve(l) }()

	base := l.URL()
	o.printf("hub on %s/ for paired devices; Ctrl-C ends the hub, never a run", base)
	url := base + serve.PathPair + "?token=" + tok
	o.println("Open this URL on the device to pair it; anyone with it controls every run in the hub (short of skip-permissions mode):")
	o.printf("  %s", url)
	if ap, err := netip.ParseAddrPort(l.Addr().String()); err == nil && ap.Addr().IsUnspecified() {
		o.printf("the hub takes every address (%s): put this machine's own address in place of %s", ap.Addr(), ap.Addr())
	} else {
		printQR(stdout, url)
	}

	hubCtx, stopHub := context.WithCancel(ctx)
	ran := make(chan struct{})
	go func() {
		defer close(ran)
		h.Run(hubCtx)
	}()
	code := exitOK
	select {
	case <-ctx.Done():
	case err := <-served:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "igris hub: the HTTP listener stopped: %v\n", err)
			code = exitFail
		}
	}
	sctx, done := context.WithTimeout(context.Background(), shutdownWait)
	defer done()
	_ = hs.Shutdown(sctx)
	stopHub()
	<-ran // the mirrors are detached: every run goes on
	fmt.Fprintf(stdout, "%s the hub ended; the runs go on\n", look.ClockSeconds(time.Now()))
	return code
}

// listenHub binds the hub's listener with serve's bind rules.
func listenHub(c hubConfig) (*serve.HTTPListener, error) {
	return serve.ListenHTTP(serve.HTTPConfig{Addr: c.addr, Public: c.public, CertFile: c.cert, KeyFile: c.key, TLSSetting: hubTLSFlags})
}

// pinnedRoot is the project root of path, and whether a run is live there.
// Outside a project the root is path itself, which has no run.
func pinnedRoot(path string, live []state.RunEntry) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path, false
	}
	root := abs
	if r, err := state.FindRoot(abs); err == nil {
		root = r
	}
	real, _ := filepath.EvalSymlinks(root)
	for _, e := range live {
		if e.Root == root {
			return root, true
		}
		if er, err := filepath.EvalSymlinks(e.Root); err == nil && real != "" && er == real {
			return e.Root, true
		}
	}
	return root, false
}

// chooseRuns is the hub's checklist (SPEC §14): the live runs with All (and
// new runs) checked; Enter keeps All, numbers choose runs. ok is false when
// stdin ended without a choice.
func chooseRuns(in io.Reader, out io.Writer, live []state.RunEntry) (all bool, roots []string, ok bool) {
	fmt.Fprintf(out, "Put which runs in the hub? %s live:\n", look.Count(len(live), "run", "runs"))
	fmt.Fprintln(out, "  [x] 0  All (and new runs)")
	for i, e := range live {
		fmt.Fprintf(out, "  [ ] %d  %s  %s · since %s\n", i+1, textsafe.Line(e.Project), textsafe.Line(e.Root), look.Date(e.Started.Local(), time.Now()))
	}
	sc := bufio.NewScanner(in)
	for {
		fmt.Fprint(out, "Enter keeps All; or the numbers of the runs to put in (e.g. 1 3): ")
		if !sc.Scan() {
			fmt.Fprintln(out)
			return false, nil, false
		}
		picked, err := parseChoice(sc.Text(), len(live))
		if err != nil {
			fmt.Fprintf(out, "%v\n", err)
			continue
		}
		if picked == nil {
			return true, nil, true
		}
		for _, i := range picked {
			roots = append(roots, live[i].Root)
		}
		return false, roots, true
	}
}

// parseChoice reads the checklist's answer: nil for All (empty, 0, a or
// all), else the chosen runs' indexes, each once.
func parseChoice(s string, n int) ([]int, error) {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
	var picked []int
	seen := map[int]bool{}
	for _, f := range fields {
		if f == "a" || f == "all" {
			return nil, nil
		}
		i, err := strconv.Atoi(f)
		if err != nil || i < 0 || i > n {
			return nil, fmt.Errorf("%q is not on the list: type numbers from 0 to %d", f, n)
		}
		if i == 0 {
			return nil, nil
		}
		if !seen[i] {
			seen[i] = true
			picked = append(picked, i-1)
		}
	}
	return picked, nil
}

// execHubToken is `igris hub token [--rotate]` (SPEC §14): it prints the
// hub's pairing URL, making the token first when there is none, or a new
// one with rotate. The hub's address is given at each start (--http), so
// the URL leaves it for the owner to fill in.
func execHubToken(rotate bool, stdout, stderr io.Writer) int {
	var tok string
	var err error
	if rotate {
		tok, _, err = state.RotateHubToken(hubTokenEnv())
	} else {
		tok, _, _, err = state.EnsureHubToken(hubTokenEnv())
	}
	if err != nil {
		fmt.Fprintf(stderr, "igris hub token: %v\n", err)
		return exitFail
	}
	o := newOut(stdout)
	if rotate {
		o.println("the token was replaced: every device paired with the hub must pair again")
	}
	o.println("Open this URL on the device to pair it; anyone with it controls every run in the hub (short of skip-permissions mode):")
	o.printf("  http://ADDR:PORT%s?token=%s", serve.PathPair, tok)
	o.println("put the hub's --http address in place of ADDR:PORT (https with --tls-cert); `igris hub` prints the whole URL and its QR code when it starts")
	o.next("igris hub --http ADDR:PORT")
	return exitOK
}
