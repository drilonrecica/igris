package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/tui"
)

// attachUI shows the attached run view; a seam for tests.
var attachUI = tui.Run

// attachSignals detach: Ctrl-C, kill, and a closed terminal or a dropped
// ssh connection. The run goes on in serve.
var attachSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// execAttach is `igris attach` (SPEC §14): the run view of this project's
// live run, served by igris serve, through its socket. Leaving the view
// detaches; the run goes on.
func execAttach(_ *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(stderr, "igris attach: %s\n", fmt.Sprintf(format, a...))
		return code
	}
	if !interactiveTerm() {
		return fail(exitUsage, "needs a terminal to show the run view; `igris status` shows the run without one")
	}
	proj, err := loadProject("")
	if err != nil {
		return fail(exitFail, "%v", err)
	}
	ctx, cancel, _ := signalContext(attachSignals...)
	defer cancel()
	feed := tui.NewFeed()
	client, err := serve.Attach(ctx, serve.ClientOptions{
		Name: "igris " + igrisVersion(),
		Dial: func(ctx context.Context) (net.Conn, error) { return serve.Dial(ctx, proj.Root) },
		Sink: feed,
	})
	if err != nil {
		if errors.Is(err, serve.ErrNotServed) {
			return fail(exitFail, "%v; `igris arise` starts one, `igris status` shows the last", err)
		}
		return fail(exitFail, "%v", err)
	}
	uiErr := attachUI(ctx, proj.AttachOptions(feed, client, igrisVersion()))
	client.Close()
	if uiErr != nil {
		return fail(exitFail, "the TUI failed: %v; the run goes on (`igris stop` stops it)", uiErr)
	}
	select {
	case <-feed.Ended():
		res, err := feed.Result()
		if err != nil {
			fmt.Fprintln(stdout, err)
			return exitOK
		}
		if res.Outcome != 0 {
			fmt.Fprintln(stdout, tui.ExitText(res, nil))
			return exitOK
		}
	default:
	}
	fmt.Fprintln(stdout, "detached; the run goes on: `igris attach` shows it again, `igris stop` stops it")
	return exitOK
}
