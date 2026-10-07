package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/project"
)

// notifyArgs checks `igris notify test`.
func notifyArgs(fs *flag.FlagSet) func([]string) error {
	fs.String("event", "", "send only this event (default: every event a channel is set up for)")
	return func(args []string) error {
		if len(args) == 0 || args[0] != "test" {
			return fmt.Errorf("missing subcommand; use `igris notify test`")
		}
		return atMost(args, 1)
	}
}

func execNotify(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris notify test: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	only := notify.Event(fs.Lookup("event").Value.String())
	if only != "" && !isEvent(only) {
		return fail("unknown event %q; use any of: %v", only, notify.AllEvents)
	}
	proj, err := loadProject("")
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	router, skipped, err := proj.NotifyRouter(ctx)
	if err != nil {
		return fail("%v", err)
	}
	if skipped != nil {
		fmt.Fprintf(stdout, "backend: skipped (%v)\n", skipped)
	}
	if !sendTestMessages(ctx, router, proj.Name(), only, stdout) {
		return exitFail
	}
	return exitOK
}

func isEvent(e notify.Event) bool {
	for _, a := range notify.AllEvents {
		if a == e {
			return true
		}
	}
	return false
}

// sendTestMessages sends one sample message per event that some channel
// wants (or just only) and prints how each delivery went. It reports whether
// something was sent and nothing failed.
func sendTestMessages(ctx context.Context, r *notify.Router, name string, only notify.Event, out io.Writer) bool {
	ok, sent := true, 0
	for _, m := range project.TestMessages(r, name, only) {
		for _, res := range r.Notify(ctx, m) {
			sent++
			if res.Err != nil {
				ok = false
				fmt.Fprintf(out, "%-20s %-8s FAILED: %v\n", m.Event, res.Channel, res.Err)
				continue
			}
			fmt.Fprintf(out, "%-20s %-8s ok\n", m.Event, res.Channel)
		}
	}
	if sent == 0 {
		fmt.Fprintln(out, project.NoChannels)
		return false
	}
	return ok
}
