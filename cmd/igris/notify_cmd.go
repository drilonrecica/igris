package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/drilonrecica/igris/internal/notify"
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
	root, cfg, err := loadProject()
	if err != nil {
		return fail("%v", err)
	}
	secrets, err := cfg.Resolve(ariseGetenv)
	if err != nil {
		return fail("%v", err)
	}
	// The toast is part of the check when herdr is there; outside herdr the
	// remote channels are still worth testing.
	be, err := ariseBackend(cfg)
	if err == nil {
		err = be.Available(context.Background())
	}
	if err != nil {
		fmt.Fprintf(stdout, "backend: skipped (%v)\n", err)
		be = nil
	}
	router := notify.FromConfig(cfg.Notify, secrets, be)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if !sendTestMessages(ctx, router, filepath.Base(root), only, stdout) {
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
func sendTestMessages(ctx context.Context, r *notify.Router, project string, only notify.Event, out io.Writer) bool {
	ok, sent := true, 0
	for _, ev := range notify.AllEvents {
		if (only != "" && ev != only) || !r.Enabled(ev) {
			continue
		}
		m := notify.Message{
			Event: ev, Project: project, Phase: "TEST", TaskID: "TEST-1", Title: "Notification check",
			What: "test message from `igris notify test`",
		}
		for _, res := range r.Notify(ctx, m) {
			sent++
			if res.Err != nil {
				ok = false
				fmt.Fprintf(out, "%-20s %-8s FAILED: %v\n", ev, res.Channel, res.Err)
				continue
			}
			fmt.Fprintf(out, "%-20s %-8s ok\n", ev, res.Channel)
		}
	}
	if sent == 0 {
		fmt.Fprintln(out, "no channel is set up for that: set [notify.ntfy] topic or [notify.discord] webhook_url in igris.toml (see the README), or enable the herdr toast")
		return false
	}
	return ok
}
