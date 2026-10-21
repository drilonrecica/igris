package adapt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/state"
)

// SessionOptions are what a plan-writing session needs from its caller:
// the adapt session (SPEC §9) and the draft session (SPEC §9.1) share them.
type SessionOptions struct {
	Config  *config.Config
	Backend backend.Backend
	State   *state.Dir
	// Notifier delivers needs_input and session_lost (SPEC §10).
	Notifier engine.Notifier
	// Clock paces the polling; nil means the real clock.
	Clock engine.Clock
	// Out receives plain progress lines.
	Out io.Writer
	// Opened, if set, gets the session's ref as soon as it is open, so a
	// caller can bring its pane to the front.
	Opened func(backend.SessionRef)
}

// SessionSpec is one plan-writing session: who it is and what it is told.
type SessionSpec struct {
	// ID is the session's task and signal ID: it finishes with
	// `igris done <ID>`.
	ID string
	// Command names the session in messages and notifications: "adapt"
	// is "adapt session started", "igris adapt".
	Command string
	// Rerun is the command the owner runs to try again; "" is
	// "igris <Command>".
	Rerun  string
	Model  string // a rank, resolved through [models]
	Mode   string // an engine mode
	Rules  string // the rules file's content, instead of the task rules
	Prompt string // the first message
}

func (s SessionSpec) rerun() string {
	if s.Rerun != "" {
		return s.Rerun
	}
	return "igris " + s.Command
}

// RunSession opens the session, sends its prompt, waits for
// `igris done <ID>` and closes it. A skip, a lost session and the end of
// ctx are errors; when ctx ends the session is left open. The caller holds
// the run lock and has removed an earlier signal of the same ID.
func RunSession(ctx context.Context, o SessionOptions, s SessionSpec) (*state.Signal, error) {
	if o.Clock == nil {
		o.Clock = engine.SystemClock()
	}
	r := &runner{o: o, s: s, started: o.Clock.Now()}
	sess, err := r.open(ctx)
	if err != nil {
		return nil, err
	}
	sig, err := r.wait(ctx, sess)
	if err != nil {
		return nil, err
	}
	r.settle(ctx, sess)
	if err := sess.Close(ctx); err != nil {
		r.say("warning: close the %s session: %v", s.Command, err)
	}
	return sig, nil
}

// settleMax bounds the wait for the agent's last message before the
// session is closed.
const settleMax = 30 * time.Second

// runner is one plan-writing session.
type runner struct {
	o       SessionOptions
	s       SessionSpec
	started time.Time
}

func (r *runner) say(format string, a ...any) {
	if r.o.Out != nil {
		fmt.Fprintf(r.o.Out, "%s %s\n", r.o.Clock.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
	}
}

// open starts the session and sends it its prompt.
func (r *runner) open(ctx context.Context) (backend.Session, error) {
	cfg, d, id := r.o.Config, r.o.State, r.s.ID
	model := cfg.Models[r.s.Model]
	if model == "" {
		model = r.s.Model // sonnet and opus are Claude Code aliases themselves
	}
	rules, err := d.WriteRules(id, r.s.Rules)
	if err != nil {
		return nil, err
	}
	sid, err := engine.NewSessionID()
	if err != nil {
		return nil, err
	}
	hooks, err := engine.WriteHooks(d, id, "", nil, nil, false)
	if err != nil {
		return nil, err
	}
	args, err := engine.ClaudeArgs(engine.ClaudeParams{Model: model, SessionID: sid, Mode: r.s.Mode, RulesFile: rules, SettingsFile: hooks})
	if err != nil {
		return nil, err
	}
	if err := r.o.Backend.Available(ctx); err != nil {
		return nil, err
	}
	if err := state.ClearAgentState(d.Root(), sid); err != nil {
		return nil, err
	}
	sess, err := r.o.Backend.OpenSession(ctx, backend.SessionSpec{
		TaskID: id,
		Dir:    d.Root(),
		Label:  id + " · " + r.s.Model,
		Args:   args,
		// Keys the session's hook state (SPEC §6.3).
		ClaudeSession: sid,
	})
	if err != nil {
		return nil, fmt.Errorf("open the %s session: %w", r.s.Command, err)
	}
	if r.o.Opened != nil {
		r.o.Opened(sess.Ref())
	}
	if err := sess.Prompt(ctx, r.s.Prompt); err != nil {
		return nil, fmt.Errorf("send the %s prompt: %w", r.s.Command, err)
	}
	which := r.s.Model
	if model != r.s.Model {
		which += " → " + model
	}
	r.say("%s session started (%s); answer its questions in the %s pane", r.s.Command, which, id)
	return sess, nil
}

// wait polls for `igris done <ID>` and the session's state until the
// session signals done, ends, or ctx ends.
func (r *runner) wait(ctx context.Context, sess backend.Session) (*state.Signal, error) {
	id, name := r.s.ID, r.s.Command
	var idleSince, waitSince time.Time // idle, or waiting on background work (SPEC §6.3)
	asked := false
	for {
		sig, err := r.o.State.ReadSignal(id)
		if err != nil {
			return nil, err
		}
		if sig != nil && !sig.At.Before(r.started) {
			if err := r.o.State.RemoveSignal(id); err != nil {
				return nil, err
			}
			if sig.Action != state.ActionDone {
				return nil, fmt.Errorf("the %s session was skipped (%s); nothing changed", name, sig.Note)
			}
			r.say("%s session done: %s", name, sig.Note)
			return sig, nil
		}

		st, err := sess.State(ctx)
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("%s stopped; the %s session stays open: %w", name, id, ctx.Err())
		case errors.Is(err, backend.ErrSessionGone) || (err == nil && st == backend.Exited):
			r.notify(ctx, notify.SessionLost, name+" session lost")
			return nil, fmt.Errorf("the %s session ended without `igris done %s`; run `%s` again", name, id, r.s.rerun())
		case err != nil:
			r.say("warning: read the session state: %v", err)
		case st == backend.Working:
			if asked {
				r.say("working again")
			}
			idleSince, waitSince, asked = time.Time{}, time.Time{}, false
		case st == backend.Waiting:
			// Claude Code resumes the session by itself: not idle, until
			// waiting_limit (no reminder here; that is for task sessions).
			now := r.o.Clock.Now()
			if waitSince.IsZero() {
				waitSince, idleSince, asked = now, time.Time{}, false
			}
			if limit := r.o.Config.WaitingLimitOrDefault(); !asked && now.Sub(waitSince) >= limit {
				asked = true
				r.say("needs you: the agent has waited on background work longer than waiting_limit; look at the %s pane", id)
				r.notify(ctx, notify.NeedsInput, "needs you")
			}
		case st.Settled():
			now := r.o.Clock.Now()
			if !waitSince.IsZero() {
				waitSince, asked = time.Time{}, false
			}
			if idleSince.IsZero() {
				idleSince = now
			}
			if after := r.o.Config.NeedsInputAfter.Std(); !asked && now.Sub(idleSince) >= after {
				asked = true
				r.say("needs you: the agent is %s and has not run `igris done %s`; answer in the %s pane", st, id, id)
				r.notify(ctx, notify.NeedsInput, "needs you")
			}
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%s stopped; the %s session stays open: %w", name, id, ctx.Err())
		case <-r.o.Clock.After(r.o.Config.PollInterval.Std()):
		}
	}
}

// settle gives the agent a moment to finish its last message after
// `igris done`, as the engine does before closing a task's session.
func (r *runner) settle(ctx context.Context, sess backend.Session) {
	deadline := r.o.Clock.Now().Add(settleMax)
	for r.o.Clock.Now().Before(deadline) {
		st, err := sess.State(ctx)
		if ctx.Err() != nil || (err == nil && (st.Settled() || st == backend.Waiting || st == backend.Exited)) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-r.o.Clock.After(r.o.Config.PollInterval.Std()):
		}
	}
}

// notify sends a SPEC §10 notification; delivery problems are only shown.
func (r *runner) notify(ctx context.Context, ev notify.Event, what string) {
	if r.o.Notifier == nil {
		return
	}
	m := notify.Message{Event: ev, Project: filepath.Base(r.o.State.Root()), TaskID: r.s.ID, Title: "igris " + r.s.Command, What: what}
	for _, res := range r.o.Notifier.Notify(ctx, m) {
		if res.Err != nil {
			r.say("warning: notification %s not delivered to %s: %v", ev, res.Channel, res.Err)
		}
	}
}
