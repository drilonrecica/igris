package project

import (
	"context"
	"time"

	"github.com/drilonrecica/igris/internal/notify"
)

// NotifyRouter is the router `igris notify test` sends through: every
// channel of igris.toml, plus the herdr toast when herdr is reachable.
// skipped says why the toast is left out (nil when it is in); err is a
// secret that can't be resolved.
func (p *Project) NotifyRouter(ctx context.Context) (r *notify.Router, skipped, err error) {
	secrets, err := p.Cfg.Resolve(p.env.getenv())
	if err != nil {
		return nil, nil, err
	}
	// The toast is part of the check when herdr is there; outside herdr the
	// remote channels are still worth testing.
	be, err := p.env.backend(p.Cfg, p.Root)
	if err == nil {
		err = be.Available(ctx)
	}
	if err != nil {
		skipped, be = err, nil
	}
	return notify.FromConfig(p.Cfg.Notify, secrets, be), skipped, nil
}

// testWhat is what a real notification of each event says (internal/engine),
// so a test shows the owner what to expect and each sample is told apart.
var testWhat = map[notify.Event]string{
	notify.NeedsInput:        "needs you",
	notify.SessionLost:       "session lost",
	notify.TaskOverdue:       "needs you (running longer than its Timeout 45m)",
	notify.VerifyFailedLimit: "verification keeps failing; needs you",
	notify.TaskDone:          "done",
	notify.PhaseDone:         "complete",
	notify.PhaseStuck:        "stuck: 2 unfinished task(s), none can start",
	notify.RunError:          "the run stopped with an error",
}

// TestMessages are the samples `igris notify test` sends for the project
// named name: one per event some channel of r wants, or only that event.
// None means no channel is set up for it.
func TestMessages(r *notify.Router, name string, only notify.Event) []notify.Message {
	var out []notify.Message
	for _, ev := range notify.AllEvents {
		if (only != "" && ev != only) || !r.Enabled(ev) {
			continue
		}
		m := notify.Message{
			Event: ev, Project: name, Phase: "TEST", TaskID: "TEST-1", Title: "Notification check",
			What: "test of " + string(ev) + ": " + testWhat[ev],
			At:   time.Now(),
		}
		if ev == notify.PhaseDone || ev == notify.PhaseStuck || ev == notify.RunError {
			m.TaskID, m.Title = "", "" // these belong to no task in a real run
		}
		out = append(out, m)
	}
	return out
}

// NoChannels is what notify test says when no channel is set up.
const NoChannels = "no channel is set up for that: set up [notify.ntfy], [notify.discord], [notify.webhook], [notify.slack] or [notify.gotify] in igris.toml (see the README), or enable the herdr toast"
