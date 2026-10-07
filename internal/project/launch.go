package project

import (
	"context"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/tui"
)

// Launch is a run being set up: everything its engine needs that doesn't
// depend on the owner's answers. `igris arise` and the home screen build
// their engines through it, so both start identical runs.
type Launch struct {
	p       *Project
	req     report.RunRequest
	secrets config.Secrets
	// Backend hosts the sessions; State is the project's .igris/.
	Backend backend.Backend
	State   *state.Dir
}

// Launch resolves the config's secrets, builds the backend and opens
// .igris/ (creating it) for a run of req. It fails on an invalid igris.toml.
func (p *Project) Launch(req report.RunRequest) (*Launch, error) {
	if p.CfgErr != nil {
		return nil, p.CfgErr
	}
	secrets, err := p.Cfg.Resolve(p.env.getenv())
	if err != nil {
		return nil, err
	}
	be, err := p.env.backend(p.Cfg, p.Root)
	if err != nil {
		return nil, err
	}
	dir, err := state.Open(p.Root, state.Options{})
	if err != nil {
		return nil, err
	}
	return &Launch{p: p, req: req, secrets: secrets, Backend: be, State: dir}, nil
}

// Prelaunch runs the checks `igris arise` reports before a run starts
// (SPEC §14). It changes nothing.
func (l *Launch) Prelaunch(ctx context.Context) report.Prelaunch {
	return l.p.Prelaunch(ctx, l.Backend)
}

// Prelaunch is Launch.Prelaunch without a launch, so nothing is created:
// be answers the herdr integration check, which is left out when be is nil.
func (p *Project) Prelaunch(ctx context.Context, be backend.Backend) report.Prelaunch {
	o := checks.Options{
		IDs:  []string{checks.IDHerdrIntegration, checks.IDClaude, checks.IDHerdr, checks.IDConfig, checks.IDPlanHints, checks.IDAPIKey, checks.IDGit},
		Root: p.Root, Runner: p.env.runner(), Getenv: p.env.getenv(), Versions: p.env.versions(),
		Config: p.Cfg,
	}
	if integration, ok := be.(checks.Integration); ok {
		o.Integration = integration
	}
	cs := checks.Run(ctx, o)
	var r report.Prelaunch
	// Information first, then what may need an answer.
	r.Warnings = append(r.Warnings, checks.Problems(checks.Pick(cs, checks.IDHerdrIntegration, checks.IDClaude, checks.IDHerdr))...)
	r.Warnings = append(r.Warnings, checks.Problems(checks.Pick(cs, checks.IDConfig, checks.IDPlanHints, checks.IDAPIKey, checks.IDGit))...)
	if prev, err := state.PeekRun(p.Root); err == nil && prev.Current != nil {
		r.Interrupted = textsafe.Line(prev.Current.TaskID)
	}
	return r
}

// Options are the engine options of the run with the owner's answers c.
// events receives the run's events.
func (l *Launch) Options(c report.Confirmations, events func(engine.Event)) engine.Options {
	return engine.Options{
		Config:         l.p.Cfg,
		Backend:        l.Backend,
		State:          l.State,
		Runner:         l.p.env.Runner,
		Secrets:        l.secrets,
		Phase:          l.req.Phase,
		Through:        l.req.Through,
		Mode:           l.req.Mode,
		ForceUnlock:    c.ForceUnlock,
		ConfirmedDrift: c.Drift,
		ConfirmedYolo:  c.Yolo,
		Events:         events,
	}
}

// Engine builds the run's engine with the owner's answers c. Nothing
// happens until its Run is called.
func (l *Launch) Engine(c report.Confirmations, events func(engine.Event)) (*engine.Engine, error) {
	return engine.New(l.Options(c, events))
}

// TUIOptions are the run view's options for this run, fed by feed and
// sending the owner's commands to sender.
func (l *Launch) TUIOptions(feed *tui.Feed, sender tui.Sender) tui.Options {
	cfg := l.p.Cfg
	return tui.Options{
		Project:    l.p.Name(),
		Backend:    l.Backend.Name(),
		Mode:       l.req.Mode,
		Mouse:      cfg.TUI.Mouse,
		Theme:      cfg.TUI.Theme,
		RankColors: cfg.TUI.RankColors,
		// The task list reads the plan the engine writes.
		PlanPath:    l.p.PlanPath,
		PlanOptions: plan.Options{Columns: cfg.Columns},
		Feed:        feed,
		Sender:      sender,
		Focus:       Focus(l.Backend),
	}
}

// DryRun is `igris arise --dry-run` for req (SPEC §14): it walks the phases
// on a copy of the plan and writes nothing in the project.
func (p *Project) DryRun(ctx context.Context, req report.RunRequest) (report.DryRun, error) {
	return engine.DryRun(ctx, engine.DryRunOptions{
		Root: p.Root, Config: p.Cfg, Phase: req.Phase, Through: req.Through, Mode: req.Mode,
		Runner: p.env.runner(), Getenv: p.env.getenv(), Versions: p.env.versions(), Format: p.env.format(),
	})
}
