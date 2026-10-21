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

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
)

// ID is the session and signal ID of the adapt session.
const ID = prompt.AdaptID

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
	p := plan.Parse(o.PlanPath, original, opts)
	if inc := p.Includes(); len(inc) > 0 {
		// A split plan is more than the one file a proposal replaces
		// (SPEC §9).
		return nil, fmt.Errorf("igris adapt converts one file; %s includes %s (line %d): inline the included files, or adapt one with --plan", relTo(o.State.Root(), o.PlanPath), inc[0].Path, inc[0].Line)
	}
	issues, err := needsAdapt(p, cfg.Rules(o.State.Root()))
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

	spec, err := sessionSpec(o, issues, proposal)
	if err != nil {
		return nil, err
	}
	sig, err := RunSession(ctx, o.sessionOptions(), spec)
	if err != nil {
		return nil, err
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

// relTo returns p relative to root when it is inside root, else p.
func relTo(root, p string) string {
	if rp, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rp, "..") {
		return rp
	}
	return p
}

// sessionOptions are the session parts of o.
func (o Options) sessionOptions() SessionOptions {
	return SessionOptions{Config: o.Config, Backend: o.Backend, State: o.State, Notifier: o.Notifier, Clock: o.Clock, Out: o.Out, Opened: o.Opened}
}

// sessionSpec is the adapt session: the adapt prompt and rules, mode
// default (SPEC §9).
func sessionSpec(o Options, issues []plan.Issue, proposal string) (SessionSpec, error) {
	cfg, d := o.Config, o.State
	vars := prompt.AdaptVars{
		PlanFile:     relTo(d.Root(), o.PlanPath),
		ProposalFile: relTo(d.Root(), proposal),
		Models:       prompt.Aliases(cfg.Models),
		Verify:       cfg.Rules("").Verify,
	}
	for _, is := range issues {
		vars.Issues = append(vars.Issues, is.Error())
	}
	text, err := prompt.RenderAdapt(vars)
	if err != nil {
		return SessionSpec{}, err
	}
	return SessionSpec{
		ID:      ID,
		Command: "adapt",
		Model:   o.Model,
		Mode:    engine.ModeDefault,
		Rules:   prompt.AdaptRules(),
		Prompt:  text,
	}, nil
}
