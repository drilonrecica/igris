package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/project"
)

// notifyArgs checks `igris notify test`.
func notifyArgs(fs *flag.FlagSet) func([]string) error {
	event := fs.String("event", "", "send only this event (default: every event a channel is set up for)")
	return func(args []string) error {
		if len(args) == 0 || args[0] != "test" {
			return fmt.Errorf("missing subcommand; use `igris notify test`")
		}
		if *event != "" && !isEvent(notify.Event(*event)) {
			return fmt.Errorf("unknown event %q; use any of: %v", *event, notify.AllEvents)
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
	proj, err := loadProject("")
	if err != nil {
		return fail("%v", err)
	}
	ctx, cancel, _ := interruptContext() // a second Ctrl-C exits at once
	defer cancel()
	router, skipped, err := proj.NotifyRouter(ctx)
	if err != nil {
		return fail("%v", err)
	}
	o := newOut(stdout)
	if skipped != nil {
		o.println(o.mark(look.Plain, look.GlyphSkipped, fmt.Sprintf("backend toast skipped (%v)", skipped)))
	}
	if !sendTestMessages(ctx, router, proj.Name(), only, o) {
		return exitFail
	}
	o.next("igris arise")
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
func sendTestMessages(ctx context.Context, r *notify.Router, name string, only notify.Event, o textOut) bool {
	ok := true
	tb := &look.Table{Header: []string{"EVENT", "CHANNEL", "RESULT"}}
	for _, m := range project.TestMessages(r, name, only) {
		for _, res := range r.Notify(ctx, m) {
			result := look.Cell{Text: look.GlyphDone + " ok", Sev: look.Accent}
			if res.Err != nil {
				ok = false
				result = look.Cell{Text: fmt.Sprintf("%s FAILED: %v", look.GlyphFail, res.Err), Sev: look.Fail, Span: len(look.GlyphFail + " FAILED")}
			}
			tb.Row(look.TextCell(string(m.Event)), look.TextCell(res.Channel), result)
		}
	}
	if tb.Len() == 0 {
		if only != "" && r.HasChannels() {
			o.println(project.NoChannelSends(only))
		} else {
			o.println(project.NoChannels)
		}
		return false
	}
	o.table(tb)
	return ok
}
