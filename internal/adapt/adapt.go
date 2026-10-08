// Package adapt implements `igris adapt` (SPEC §9): one Claude Code session
// converts a plan that fails `igris check` into a canonical proposal in
// .igris/adapt/, which igris validates for the owner's review.
package adapt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
)

// ID is the session and signal ID of the adapt session.
const ID = prompt.AdaptID

// settleMax bounds the wait for the agent's last message before the
// session is closed.
const settleMax = 30 * time.Second

// ErrAlreadyValid means the plan passes `igris check`: there is nothing to
// adapt.
var ErrAlreadyValid = errors.New("the plan already passes `igris check`; nothing to adapt")

// Options configure Run.
type Options struct {
	Config   *config.Config
	PlanPath string // absolute path of the plan to adapt
	Model    string // "sonnet" or "opus"; resolved through [models]
	Backend  backend.Backend
	State    *state.Dir
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

// Result is a finished adapt session: the original, the proposal and the
// proposal's validation problems (none if it passes `igris check`).
type Result struct {
	PlanPath     string
	ProposalPath string
	Original     []byte
	Proposed     []byte
	Issues       []plan.Issue
	Note         string // the session's `igris done ADAPT --note`
}

// ProposalPath returns .igris/adapt/<plan-name>.proposed.md for planPath.
func ProposalPath(d *state.Dir, planPath string) string {
	return filepath.Join(d.AdaptDir(), planName(planPath)+".proposed.md")
}

// planName is the plan's file name without .md: "tasks" for tasks.md.
func planName(planPath string) string {
	base := filepath.Base(planPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Run validates the plan, runs the adapt session until it signals
// `igris done ADAPT`, and validates the proposal it wrote. It never touches
// the plan itself. If ctx ends first, the session is left open.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Clock == nil {
		o.Clock = engine.SystemClock()
	}
	cfg := o.Config
	opts := plan.Options{Columns: cfg.Columns}

	original, err := os.ReadFile(o.PlanPath)
	if err != nil {
		return nil, fmt.Errorf("read plan %s: %w; set plan in igris.toml or pass --plan", o.PlanPath, err)
	}
	issues, err := needsAdapt(plan.Parse(o.PlanPath, original, opts), cfg.Rules(o.State.Root()))
	if err != nil {
		return nil, err
	}

	lock, err := o.State.Lock(false)
	if err != nil {
		return nil, lockError(err)
	}
	defer func() { _ = lock.Release() }()

	proposal := ProposalPath(o.State, o.PlanPath)
	if err := os.Remove(proposal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove the earlier proposal %s: %w", proposal, err)
	}
	if err := o.State.RemoveSignal(ID); err != nil {
		return nil, err
	}

	r := &runner{o: o, started: o.Clock.Now()}
	sess, err := r.open(ctx, issues, proposal)
	if err != nil {
		return nil, err
	}
	sig, err := r.wait(ctx, sess)
	if err != nil {
		return nil, err
	}
	r.settle(ctx, sess)
	if err := sess.Close(ctx); err != nil {
		r.say("warning: close the adapt session: %v", err)
	}

	proposed, err := os.ReadFile(proposal) //nolint:gosec // igris's own state directory
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the adapt session finished without writing %s; run `igris adapt` again", proposal)
	}
	if err != nil {
		return nil, fmt.Errorf("read proposal: %w", err)
	}
	return &Result{
		PlanPath:     o.PlanPath,
		ProposalPath: proposal,
		Original:     original,
		Proposed:     proposed,
		Issues:       plan.Parse(proposal, proposed, opts).Validate(cfg.Rules(o.State.Root())),
		Note:         sig.Note,
	}, nil
}

// needsAdapt returns the plan's problems, or ErrAlreadyValid.
func needsAdapt(p *plan.Plan, rules plan.Rules) ([]plan.Issue, error) {
	if issues := p.Validate(rules); len(issues) > 0 {
		return issues, nil
	}
	return nil, ErrAlreadyValid
}

// lockError rewords the run-lock errors for adapt, which has no
// --force-unlock of its own.
func lockError(err error) error {
	var stale *state.StaleLockError
	if errors.As(err, &stale) {
		return fmt.Errorf("%v (or run `igris arise --force-unlock` once)", err)
	}
	return fmt.Errorf("%w; adapt can't run while igris arise runs in this project", err)
}

// runner is one adapt session.
type runner struct {
	o       Options
	started time.Time
}

func (r *runner) say(format string, a ...any) {
	if r.o.Out != nil {
		fmt.Fprintf(r.o.Out, "%s %s\n", r.o.Clock.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
	}
}

// open starts the adapt session and sends it the adapt prompt.
func (r *runner) open(ctx context.Context, issues []plan.Issue, proposal string) (backend.Session, error) {
	cfg, d := r.o.Config, r.o.State
	model := cfg.Models[r.o.Model]
	if model == "" {
		model = r.o.Model // sonnet and opus are Claude Code aliases themselves
	}
	rel := func(p string) string {
		if rp, err := filepath.Rel(d.Root(), p); err == nil && !strings.HasPrefix(rp, "..") {
			return rp
		}
		return p
	}
	vars := prompt.AdaptVars{
		PlanFile:     rel(r.o.PlanPath),
		ProposalFile: rel(proposal),
		Models:       prompt.Aliases(cfg.Models),
		Verify:       cfg.Rules("").Verify,
	}
	for _, is := range issues {
		vars.Issues = append(vars.Issues, is.Error())
	}
	text, err := prompt.RenderAdapt(vars)
	if err != nil {
		return nil, err
	}
	rules, err := d.WriteRules(ID, prompt.AdaptRules())
	if err != nil {
		return nil, err
	}
	sid, err := engine.NewSessionID()
	if err != nil {
		return nil, err
	}
	hooks, err := engine.WriteHooks(d, ID, "")
	if err != nil {
		return nil, err
	}
	args, err := engine.ClaudeArgs(engine.ClaudeParams{Model: model, SessionID: sid, Mode: engine.ModeDefault, RulesFile: rules, SettingsFile: hooks})
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
		TaskID: ID,
		Dir:    d.Root(),
		Label:  ID + " · " + r.o.Model,
		Args:   args,
		// Keys the session's hook state (SPEC §6.3).
		ClaudeSession: sid,
	})
	if err != nil {
		return nil, fmt.Errorf("open the adapt session: %w", err)
	}
	if r.o.Opened != nil {
		r.o.Opened(sess.Ref())
	}
	if err := sess.Prompt(ctx, text); err != nil {
		return nil, fmt.Errorf("send the adapt prompt: %w", err)
	}
	which := r.o.Model
	if model != r.o.Model {
		which += " → " + model
	}
	r.say("adapt session started (%s); answer its questions in the %s pane", which, ID)
	return sess, nil
}

// wait polls for `igris done ADAPT` and the session's state until the
// session signals done, ends, or ctx ends.
func (r *runner) wait(ctx context.Context, sess backend.Session) (*state.Signal, error) {
	var idleSince time.Time
	asked := false
	for {
		sig, err := r.o.State.ReadSignal(ID)
		if err != nil {
			return nil, err
		}
		if sig != nil && !sig.At.Before(r.started) {
			if err := r.o.State.RemoveSignal(ID); err != nil {
				return nil, err
			}
			if sig.Action != state.ActionDone {
				return nil, fmt.Errorf("the adapt session was skipped (%s); nothing changed", sig.Note)
			}
			r.say("adapt session done: %s", sig.Note)
			return sig, nil
		}

		st, err := sess.State(ctx)
		switch {
		case ctx.Err() != nil:
			return nil, fmt.Errorf("adapt stopped; the %s session stays open: %w", ID, ctx.Err())
		case errors.Is(err, backend.ErrSessionGone) || (err == nil && st == backend.Exited):
			r.notify(ctx, notify.SessionLost, "adapt session lost")
			return nil, fmt.Errorf("the adapt session ended without `igris done %s`; run `igris adapt` again", ID)
		case err != nil:
			r.say("warning: read the session state: %v", err)
		case st == backend.Working:
			if asked {
				r.say("working again")
			}
			idleSince, asked = time.Time{}, false
		case st.Settled():
			now := r.o.Clock.Now()
			if idleSince.IsZero() {
				idleSince = now
			}
			if after := r.o.Config.NeedsInputAfter.Std(); !asked && now.Sub(idleSince) >= after {
				asked = true
				r.say("needs you: the agent is %s and has not run `igris done %s`; answer in the %s pane", st, ID, ID)
				r.notify(ctx, notify.NeedsInput, "needs you")
			}
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("adapt stopped; the %s session stays open: %w", ID, ctx.Err())
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
		if ctx.Err() != nil || (err == nil && (st.Settled() || st == backend.Exited)) {
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
	m := notify.Message{Event: ev, Project: filepath.Base(r.o.State.Root()), TaskID: ID, Title: "igris adapt", What: what}
	for _, res := range r.o.Notifier.Notify(ctx, m) {
		if res.Err != nil {
			r.say("warning: notification %s not delivered to %s: %v", ev, res.Channel, res.Err)
		}
	}
}
