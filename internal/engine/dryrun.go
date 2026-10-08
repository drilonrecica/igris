package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// DryRunOptions are the inputs of DryRun.
type DryRunOptions struct {
	Root    string
	Config  *config.Config
	Phase   string // empty: the phases of the last run
	Through string
	Mode    string
	// Runner and Getenv serve the start-up checks (git, ANTHROPIC_API_KEY).
	Runner runner.Runner
	Getenv func(string) string
	// Versions checks the Claude Code and backend versions (usually
	// checks.ToolVersions); nil checks none. BackendName is the backend the
	// config chooses ("" when none can be chosen): only its version counts.
	Versions    func(context.Context, runner.Runner) []checks.Result
	BackendName string
	// Format renders an event as the lines the walk shows for it.
	Format func(Event) []string
}

// DryRun walks the phases on a copy of the plan with the fake backend and
// reports which task would launch with which model and mode (SPEC §14). It
// writes nothing in the project: the plan copy, the state and the lock live in
// a temporary directory, and no command is run. On an error the report holds
// what was found before it.
func DryRun(ctx context.Context, o DryRunOptions) (report.DryRun, error) {
	var r report.DryRun
	f := o
	switch prev, err := state.PeekRun(o.Root); {
	case errors.Is(err, state.ErrNoRun):
		if f.Phase == "" {
			return r, errors.New("there is no earlier run to resume; name the phase to run, e.g. `igris arise M0 --dry-run`")
		}
	case err != nil:
		return r, err
	default:
		if prev.Current != nil {
			r.Interrupted = textsafe.Line(prev.Current.TaskID)
		}
		if f.Phase == "" && len(prev.Phases) > 0 {
			f.Phase, f.Through = prev.Phases[0], prev.Through
		}
	}

	warn := func(s string) { r.Warnings = append(r.Warnings, textsafe.Line(s)) }
	cs := checks.Run(ctx, checks.Options{
		IDs:  []string{checks.IDClaude, checks.IDHerdr, checks.IDTmux, checks.IDConfig, checks.IDPlanHints, checks.IDAPIKey, checks.IDGit, checks.IDDrift},
		Root: f.Root, Runner: f.Runner, Getenv: f.Getenv, Versions: f.Versions, BackendName: f.BackendName, Config: f.Config,
	})
	for _, c := range checks.Problems(checks.Pick(cs, checks.IDClaude, checks.IDHerdr, checks.IDTmux, checks.IDConfig, checks.IDPlanHints)) {
		warn(c.String())
	}
	for _, c := range checks.Problems(checks.Pick(cs, checks.IDAPIKey, checks.IDGit)) {
		asks := ""
		if c.Confirm {
			asks = " (a real run asks you to confirm)"
		}
		warn(c.Message + asks)
	}
	p, err := plan.Load(inRoot(f.Root, f.Config.Plan), plan.Options{Columns: f.Config.Columns})
	if err != nil {
		return r, PlanLoadError(err)
	}
	if err := p.Check(f.Config.Rules(f.Root)); err != nil {
		return r, err
	}
	// Check the range on the owner's plan, so errors name it, not the copy.
	if _, err := p.PhasesThrough(f.Phase, f.Through); err != nil {
		return r, err
	}
	for _, c := range checks.Problems(checks.Pick(cs, checks.IDDrift)) {
		warn(fmt.Sprintf("drift: %s (a real run asks you before fixing it)", checks.DriftLine(c)))
	}

	tmp, err := os.MkdirTemp("", "igris-dry-run-*")
	if err != nil {
		return r, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	cfg, err := dryRunProject(tmp, f.Root, f.Config)
	if err != nil {
		return r, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	clock := NewFakeClock(time.Now())
	// A walk that never ends would be a bug; fake time runs out first.
	clock.At(365*24*time.Hour, cancel)
	dir, err := state.Open(tmp, state.Options{Now: clock.Now})
	if err != nil {
		return r, err
	}
	w := &dryWalk{r: &r, format: f.Format, resumed: map[string]bool{}}
	var eng *Engine
	eng, err = New(Options{
		Config:         cfg,
		Backend:        newDryBackend(dir),
		State:          dir,
		Clock:          clock,
		Runner:         &runner.Fake{}, // verify and commits are off; nothing may run
		noVerify:       true,
		noHooks:        true,
		Phase:          f.Phase,
		Through:        f.Through,
		Mode:           f.Mode,
		ConfirmedDrift: true,
		ConfirmedYolo:  true,
		Events:         func(ev Event) { w.event(eng, dir, ev) },
	})
	if err != nil {
		return r, err
	}
	if len(cfg.Hooks.BeforeTask) > 0 {
		r.Hooks = append(r.Hooks, hookBefore)
	}
	if len(cfg.Hooks.AfterTask) > 0 {
		r.Hooks = append(r.Hooks, hookAfter)
	}
	r.Scope = textsafe.Line(f.Phase)
	if f.Through != "" {
		r.Scope += " through " + textsafe.Line(f.Through)
	}
	_, err = eng.Run(ctx)
	return r, err
}

// dryRunProject sets up a scratch project in tmp: the plan (and prompt
// template) copied from root, and a config that never commits or toasts.
// It returns that config. Its verify profiles stay, so the plan validates
// and the walk shows each task's profile, and so do its task hooks, so the
// walk can name them; the engine runs none of either.
func dryRunProject(tmp, root string, orig *config.Config) (*config.Config, error) {
	cfg := *orig
	cfg.Run.Commit = CommitNever
	cfg.Notify.Backend.Enabled = false
	cfg.Notify.Ntfy.Topic = "" // no Discord webhook either: dry runs resolve no secrets
	copyIn := func(rel string) (string, error) {
		name := rel
		if filepath.IsAbs(rel) || !filepath.IsLocal(rel) {
			name = filepath.Base(rel)
		}
		data, err := os.ReadFile(inRoot(root, rel)) //nolint:gosec // the owner's own plan/template
		if err != nil {
			return "", err
		}
		dst := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		return name, os.WriteFile(dst, data, 0o600) //nolint:gosec // dst is a local name inside our own temp dir
	}
	var err error
	if cfg.Plan, err = copyIn(orig.Plan); err != nil {
		return nil, err
	}
	if orig.Run.PromptTemplate != "" {
		if cfg.Run.PromptTemplate, err = copyIn(orig.Run.PromptTemplate); err != nil {
			return nil, fmt.Errorf("read prompt_template: %w", err)
		}
	}
	// The engine watches igris.toml against its snapshot; give it this one.
	if err := config.Write(filepath.Join(tmp, state.ConfigFile), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// dryWalk records the walk and plays the owner and the sessions.
type dryWalk struct {
	r       *report.DryRun
	format  func(Event) []string
	resumed map[string]bool
}

func (w *dryWalk) event(eng *Engine, dir *state.Dir, ev Event) {
	r := w.r
	switch ev.Kind {
	case SessionOpened:
		r.Sessions++
		r.Steps = append(r.Steps, report.DryStep{
			Kind: report.StepSession, N: r.Sessions + r.Users, Task: textsafe.Line(ev.Task),
			Rank: textsafe.Line(ev.Rank), Model: textsafe.Line(ev.Model), Mode: textsafe.Line(ev.Mode), Verify: textsafe.Line(ev.Verify),
			Title: textsafe.Line(ev.Title), Resumed: w.resumed[ev.Task],
		})
	case YourTurn:
		r.Users++
		r.Steps = append(r.Steps, report.DryStep{
			Kind: report.StepUser, N: r.Sessions + r.Users, Task: textsafe.Line(ev.Task), Title: textsafe.Line(ev.Title),
		})
		if err := dir.WriteSignal(state.Signal{ID: ev.Task, Action: state.ActionDone, Note: "dry run"}); err != nil {
			r.Steps = append(r.Steps, report.DryStep{Kind: report.StepWarning, Lines: []string{textsafe.Line(err.Error())}})
		}
	case TaskResumed:
		w.resumed[ev.Task] = true
	case Asked:
		// Only a task the plan says is in progress gets here: start fresh.
		eng.Send(Command{Kind: CmdRetry})
	case PhaseDone:
		r.Steps = append(r.Steps, report.DryStep{Kind: report.StepPhaseDone, Phase: textsafe.Line(ev.Phase)})
	case PhaseStuck, ModeChanged, TaskModeChanged, ConfigChanged, Warning, RunFailed:
		step := report.DryStep{Kind: report.StepNote}
		for _, line := range w.format(ev) {
			step.Lines = append(step.Lines, textsafe.Line(line))
		}
		r.Steps = append(r.Steps, step)
	}
}

// newDryBackend is a fake backend whose sessions report done right away.
func newDryBackend(dir *state.Dir) backend.Backend {
	be := fake.New()
	be.SetAutoSignal(func(_ context.Context, id string) error {
		return dir.WriteSignal(state.Signal{ID: id, Action: state.ActionDone, Note: "dry run"})
	})
	return be
}
