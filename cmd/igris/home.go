package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// Seams for tests: whether the terminal can host the app, and the app.
var (
	// interactiveTerm says stdin and stdout are a terminal igris can draw on.
	interactiveTerm = func() bool { return interactive(os.Stdin, os.Stdout, os.Getenv("TERM")) }
	appUI           = tui.App
)

// interactive reports whether stdin and stdout are both a terminal (a
// character device, with stdin not /dev/null) and term is not "dumb"
// (SPEC §15.6). It needs no isatty dependency.
func interactive(stdin, stdout *os.File, term string) bool {
	if term == "dumb" || !isCharDevice(stdin) || !isCharDevice(stdout) {
		return false
	}
	// /dev/null is a character device too, but nobody types into it.
	si, err := stdin.Stat()
	if err != nil {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(si, null) {
		return false
	}
	return true
}

func isCharDevice(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// wantWizard says `igris arise` with these flags should open the start-run
// wizard instead of starting at once: on a terminal, no phase named, not
// --no-tui or --dry-run, and no run to resume (SPEC §15.6).
func wantWizard(f ariseFlags, root string) bool {
	if f.phase != "" || !f.sel.Empty() || f.noTUI || f.dryRun || !interactiveTerm() {
		return false
	}
	r, err := state.PeekRun(root)
	switch {
	case errors.Is(err, state.ErrNoRun):
		return true
	case err != nil:
		return false // unreadable state: arise says so, as before
	}
	return len(r.Phases) == 0
}

// runApp opens the app on start and returns the process exit code: 0, or 1
// if the TUI failed or a run it held ended badly.
func runApp(start tui.Start, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris: %v\n", err)
		return exitFail
	}
	env := projectEnv()
	opts := tui.AppOptions{Services: project.NewServices(cwd, env), Start: start}
	if p, err := project.Open(cwd, env); err == nil {
		opts.Mouse, opts.Theme, opts.RankColors = p.Cfg.TUI.Mouse, p.Cfg.TUI.Theme, p.Cfg.TUI.RankColors
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	res, err := appUI(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "igris: the TUI failed: %v; `igris arise --no-tui` runs without it\n", err)
		return exitFail
	}
	if res.Stopped || res.RunErr != nil || res.Res.Phase != "" {
		if res.Stopped {
			res.Res.Outcome = engine.Stopped
		}
		return tuiExit(res.Res, res.RunErr, stdout, stderr)
	}
	return exitOK
}
