// Package project opens an igris project and does what the CLI and the home
// screen both need on it: build a run's engine exactly as `igris arise`
// does, set the project up (`init`), test notifications, and read the state
// the home screen shows. It prints nothing: results are data (see
// internal/report), and the callers render them.
package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// Env is the outside world a project talks to. Every field may be nil for
// the real thing; tests set fakes.
type Env struct {
	// Getenv reads the environment: secrets, ANTHROPIC_API_KEY, herdr's
	// pane variables, the editor. nil means os.Getenv.
	Getenv func(string) string
	// Runner runs every process igris starts itself (git, herdr, version
	// checks, verify). nil means real processes.
	Runner runner.Runner
	// Backend builds the backend named in the config. nil means
	// NewBackend with this Env and the project root.
	Backend func(*config.Config) (backend.Backend, error)
	// Versions checks the Claude Code and herdr versions (SPEC §11.4).
	// nil means checks.ToolVersions.
	Versions func(context.Context, runner.Runner) []checks.Result
	// Clock paces adapt's polling and stamps its backups. nil means the
	// system clock.
	Clock engine.Clock
	// LookPath finds a program on PATH: vi, offered when no editor is
	// set. nil means exec.LookPath.
	LookPath func(string) (string, error)
	// Format renders an event as the lines the dry run shows for it; the
	// CLI's plain log format. nil shows the event's kind and detail.
	Format func(engine.Event) []string
}

func (e Env) getenv() func(string) string {
	if e.Getenv != nil {
		return e.Getenv
	}
	return os.Getenv
}

func (e Env) lookPath() func(string) (string, error) {
	if e.LookPath != nil {
		return e.LookPath
	}
	return exec.LookPath
}

// runner is the runner for igris's own checks: never nil.
func (e Env) runner() runner.Runner {
	if e.Runner != nil {
		return e.Runner
	}
	return runner.Exec{}
}

func (e Env) versions() func(context.Context, runner.Runner) []checks.Result {
	if e.Versions != nil {
		return e.Versions
	}
	return checks.ToolVersions
}

func (e Env) clock() engine.Clock {
	if e.Clock != nil {
		return e.Clock
	}
	return engine.SystemClock()
}

func (e Env) format() func(engine.Event) []string {
	if e.Format != nil {
		return e.Format
	}
	return func(ev engine.Event) []string {
		return []string{strings.TrimSpace(string(ev.Kind) + " " + ev.Task + " " + ev.Detail)}
	}
}

func (e Env) backend(cfg *config.Config, root string) (backend.Backend, error) {
	if e.Backend != nil {
		return e.Backend(cfg)
	}
	return NewBackend(cfg, root, e)
}

// Project is an opened project directory.
type Project struct {
	// Root is the project root: the nearest directory, from the working
	// directory up, that holds igris.toml or .igris/ (Found), else the
	// working directory itself.
	Root  string
	Found bool
	// Cfg is the config; the defaults when igris.toml is missing
	// (NoConfig) or invalid (CfgErr says why). Never nil.
	Cfg      *config.Config
	CfgErr   error
	NoConfig bool
	// CfgKeys are the keys igris.toml sets; nil unless it is valid.
	CfgKeys config.Keys
	// PlanPath is the plan's absolute path.
	PlanPath string

	env Env
}

// Open opens the project that cwd belongs to. It is tolerant: a missing or
// invalid igris.toml is data in the result, not an error, and nothing is
// created. It fails only if cwd can't be made absolute.
func Open(cwd string, env Env) (*Project, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	p := &Project{Root: cwd, env: env}
	if root, err := state.FindRoot(cwd); err == nil {
		p.Root, p.Found = root, true
	}
	cfg, keys, err := config.LoadKeys(p.ConfigPath())
	switch {
	case err == nil:
		p.Cfg, p.CfgKeys = cfg, keys
	case errors.Is(err, config.ErrNotFound):
		p.Cfg, p.NoConfig = config.Default(), true
	default:
		p.Cfg, p.CfgErr = config.Default(), err
	}
	p.PlanPath = InRoot(p.Root, p.Cfg.Plan)
	return p, nil
}

// Load opens the project for a command that works in it (`arise`, `adapt`,
// `notify test`; SPEC §14) and fails where Open is tolerant: on an invalid
// igris.toml, and outside a project, unless cwd holds the plan (explicitPlan,
// else the default tasks.md), so .igris/ is never created in an unrelated
// directory.
func Load(cwd, explicitPlan string, env Env) (*Project, error) {
	p, err := Open(cwd, env)
	if err != nil {
		return nil, err
	}
	if !p.Found {
		planPath := explicitPlan
		if planPath == "" {
			planPath = config.Default().Plan
		}
		if _, serr := os.Stat(InRoot(p.Root, planPath)); serr != nil {
			return nil, fmt.Errorf("no igris project in %s: no %s or %s here or in a parent, and no %s; run `igris init` in your project (or cd into it)", p.Root, state.ConfigFile, state.DirName, planPath)
		}
	}
	if p.CfgErr != nil {
		return nil, p.CfgErr
	}
	return p, nil
}

// InRoot resolves a config path relative to the project root.
func InRoot(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

// ConfigPath is igris.toml's absolute path.
func (p *Project) ConfigPath() string { return filepath.Join(p.Root, state.ConfigFile) }

// Backend builds the project's backend.
func (p *Project) Backend() (backend.Backend, error) { return p.env.backend(p.Cfg, p.Root) }

// Name is the root's base name, shown in headers and notifications.
func (p *Project) Name() string { return filepath.Base(p.Root) }

// Focus brings a session's pane to the front through the backend.
func Focus(be backend.Backend) func(context.Context, backend.SessionRef) error {
	return func(ctx context.Context, ref backend.SessionRef) error {
		s, err := be.Attach(ctx, ref)
		if err != nil {
			return err
		}
		return s.Focus(ctx)
	}
}
