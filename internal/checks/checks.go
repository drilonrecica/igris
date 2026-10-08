// Package checks is the one ordered list of checks shared by `igris check`,
// `igris arise` (and its dry run), `igris doctor` and the home screen: tool
// versions, environment, herdr, git, igris.toml and the plan's hints and
// drift. It returns results only and never prints; each caller picks the
// checks it shows and renders them.
package checks

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Level says how serious a result is.
type Level string

const (
	OK   Level = "ok"
	Warn Level = "warn"
	Fail Level = "fail"
)

// Check IDs, in the order Run returns them. A check can give several
// results (one per ignored setting, plan hint or drifted task) or none
// (plan checks on a plan that can't be read or isn't valid).
const (
	IDClaude           = "claude"            // Claude Code found, version
	IDHerdr            = "herdr"             // herdr found, version (when herdr is the backend)
	IDTmux             = "tmux"              // tmux found, version (when tmux is the backend)
	IDAPIKey           = "api-key"           // ANTHROPIC_API_KEY set
	IDBackend          = "backend"           // the backend can host sessions (herdr pane, tmux session)
	IDHerdrIntegration = "herdr-integration" // herdr's Claude Code integration installed
	IDGit              = "git"               // git repository, clean working tree
	IDConfig           = "config"            // igris.toml settings that have no effect
	IDProject          = "project"           // no igris.toml here, or one in a parent directory
	IDPlanHints        = "plan-hints"        // a valid plan igris reads differently than meant
	IDDrift            = "drift"             // ready/blocked cells that don't match the dependencies
)

// order is the one order of the checks.
var order = []string{
	IDClaude, IDHerdr, IDTmux, IDAPIKey, IDBackend, IDHerdrIntegration, IDGit,
	IDConfig, IDProject, IDPlanHints, IDDrift,
}

// Result is the outcome of one check. Text in it is safe to draw: untrusted
// parts (plan cells, command output) went through textsafe.
type Result struct {
	ID      string `json:"id"`
	Level   Level  `json:"level"`
	Message string `json:"message"`
	// Next is the command that fixes a problem, if there is one.
	Next string `json:"next,omitempty"`
	// Confirm says a run starts only after the owner confirms.
	Confirm bool `json:"confirm,omitempty"`
	// File and Line locate a config or plan result; Task, From and To
	// describe a drifted task.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Task string `json:"task,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// String is the result as a warning line: `file:line: message`, without
// the parts it doesn't have.
func (r Result) String() string {
	switch {
	case r.File == "":
		return r.Message
	case r.Line == 0:
		return fmt.Sprintf("%s: %s", r.File, r.Message)
	}
	return fmt.Sprintf("%s:%d: %s", r.File, r.Line, r.Message)
}

// Availability is a backend that can say whether it can host sessions.
type Availability interface {
	Name() string
	Available(ctx context.Context) error
}

// Integration is a backend that can advise on its Claude Code integration
// ("" when there is nothing to say).
type Integration interface {
	IntegrationHint(ctx context.Context) string
}

// Options are the inputs of Run. A check whose input is missing is left
// out.
type Options struct {
	// IDs are the checks to run; nil runs all of them. Results come in
	// the list's order whatever the order here.
	IDs []string
	// Root is the project directory: git runs there, the plan is found
	// relative to it.
	Root string
	// Runner runs git.
	Runner runner.Runner
	// Getenv reads ANTHROPIC_API_KEY.
	Getenv func(string) string
	// Versions checks the tool versions; usually ToolVersions. A seam for
	// tests, which must not run the real claude and herdr.
	Versions func(context.Context, runner.Runner) []Result
	// BackendName is the backend chosen for the project ("herdr", "tmux";
	// "" when none can be chosen): only its tool version is checked.
	BackendName string
	// Backend answers the availability check; Integration herdr's
	// integration check (the herdr backend implements both).
	Backend     Availability
	Integration Integration
	// Config is the project's config (the defaults without igris.toml).
	Config *config.Config
	// NoConfig: there is no igris.toml in the directory read. ParentConfig
	// is the igris.toml of a parent directory, if any (see ParentConfig).
	NoConfig     bool
	ParentConfig string
	// Plan is the loaded plan; nil loads it from Root and Config.
	Plan *plan.Plan
}

// Run runs the checks of o.IDs whose inputs are set, in the list's order.
// It never fails: a check that can't tell gives no result or a warning.
func Run(ctx context.Context, o Options) []Result {
	var out []Result
	add := func(rs ...Result) { out = append(out, rs...) }
	planLoaded := false
	var p *plan.Plan
	validPlan := func() *plan.Plan {
		if !planLoaded {
			planLoaded = true
			p = loadValidPlan(o)
		}
		return p
	}
	var versions []Result
	for _, id := range order {
		if o.IDs != nil && !slices.Contains(o.IDs, id) {
			continue
		}
		switch id {
		case IDClaude, IDHerdr, IDTmux:
			if o.Versions == nil || (id != IDClaude && id != o.BackendName) {
				continue
			}
			if versions == nil {
				versions = o.Versions(ctx, o.Runner)
			}
			add(Pick(versions, id)...)
		case IDAPIKey:
			if o.Getenv != nil {
				add(apiKey(o.Getenv))
			}
		case IDBackend:
			if o.Backend != nil {
				add(backendAvailable(ctx, o.Backend))
			}
		case IDHerdrIntegration:
			if o.Integration != nil {
				add(herdrIntegration(ctx, o.Integration))
			}
		case IDGit:
			if o.Runner != nil {
				add(git(ctx, o.Runner, o.Root))
			}
		case IDConfig:
			if o.Config != nil {
				add(configWarnings(o.Config)...)
			}
		case IDProject:
			add(project(o))
		case IDPlanHints:
			if p := validPlan(); p != nil {
				add(planHints(p, o.Config)...)
			}
		case IDDrift:
			if p := validPlan(); p != nil {
				add(drift(p)...)
			}
		}
	}
	for i := range out {
		r := &out[i]
		r.Message, r.Next, r.File, r.Task = textsafe.Line(r.Message), textsafe.Line(r.Next), textsafe.Line(r.File), textsafe.Line(r.Task)
	}
	return out
}

// Pick returns the results of the given checks, in the order of ids.
func Pick(rs []Result, ids ...string) []Result {
	var out []Result
	for _, id := range ids {
		for _, r := range rs {
			if r.ID == id {
				out = append(out, r)
			}
		}
	}
	return out
}

// Problems returns the results that aren't OK.
func Problems(rs []Result) []Result {
	var out []Result
	for _, r := range rs {
		if r.Level != OK {
			out = append(out, r)
		}
	}
	return out
}

func configWarnings(cfg *config.Config) []Result {
	ws := cfg.Warnings()
	if len(ws) == 0 {
		return []Result{{ID: IDConfig, Level: OK, Message: "no ignored settings", File: state.ConfigFile}}
	}
	out := make([]Result, len(ws))
	for i, w := range ws {
		out[i] = Result{ID: IDConfig, Level: Warn, Message: w, File: state.ConfigFile}
	}
	return out
}

// project notes a directory without igris.toml. With one in a parent
// directory that is a warning: the owner probably meant to run there.
func project(o Options) Result {
	switch {
	case o.ParentConfig != "":
		return Result{ID: IDProject, Level: Warn, Message: ParentHint(o.ParentConfig), Next: "cd " + filepath.Dir(o.ParentConfig)}
	case o.NoConfig:
		// Not a warning: a directory with just a plan is a valid project.
		return Result{ID: IDProject, Level: OK, Message: fmt.Sprintf("no %s here; using the defaults (`igris init` creates one)", state.ConfigFile), Next: "igris init"}
	}
	return Result{ID: IDProject, Level: OK, Message: state.ConfigFile + " found"}
}

// ParentConfig returns the igris.toml of the nearest parent directory of
// dir that has one, or "".
func ParentConfig(dir string) string {
	root, err := state.FindRoot(dir)
	if err != nil || root == dir {
		return ""
	}
	path := filepath.Join(root, state.ConfigFile)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// ParentHint says that the igris.toml at path, in a parent directory, is
// not read.
func ParentHint(path string) string {
	return fmt.Sprintf("%s found in %s; run igris from there (this directory uses the defaults)", state.ConfigFile, filepath.Dir(path))
}

// loadValidPlan returns o.Plan, or the plan loaded from o.Root, if it is
// valid; nil otherwise: hints and drift need a valid plan (unknown or
// cyclic dependencies make readiness undefined), and an invalid one is
// reported by whoever loads it.
func loadValidPlan(o Options) *plan.Plan {
	if o.Config == nil {
		return nil
	}
	p := o.Plan
	if p == nil {
		path := o.Config.Plan
		if !filepath.IsAbs(path) {
			path = filepath.Join(o.Root, path)
		}
		var err error
		if p, err = plan.Load(path, plan.Options{Columns: o.Config.Columns}); err != nil {
			return nil
		}
	}
	if len(p.Validate(o.Config.Rules())) > 0 {
		return nil
	}
	return p
}

// planHints lists the plan's hints and each [phases.<id>] of the config
// that names no phase of the plan (the config is validated without it).
func planHints(p *plan.Plan, cfg *config.Config) []Result {
	var out []Result
	for _, h := range p.Hints() {
		out = append(out, Result{ID: IDPlanHints, Level: Warn, Message: h.Msg, File: h.File, Line: h.Line})
	}
	for _, id := range slices.Sorted(maps.Keys(cfg.Phases)) {
		if p.Phase(id) == nil {
			out = append(out, Result{ID: IDPlanHints, Level: Warn, File: state.ConfigFile,
				Message: fmt.Sprintf("[phases.%s] names no phase of %s; fix the phase ID or remove the table", id, p.Path)})
		}
	}
	if len(out) == 0 {
		return []Result{{ID: IDPlanHints, Level: OK, Message: "no columns that look like dependencies outside Deps", File: p.Path}}
	}
	return out
}

// drift lists the tasks the readiness sync would flip (SPEC §5.2). A run
// asks before its first write fixes them.
func drift(p *plan.Plan) []Result {
	changes := p.Readiness()
	if len(changes) == 0 {
		return []Result{{ID: IDDrift, Level: OK, Message: "ready/blocked statuses match the dependencies", File: p.Path}}
	}
	out := make([]Result, len(changes))
	for i, c := range changes {
		t := p.Task(c.ID)
		out[i] = Result{
			ID: IDDrift, Level: Warn, Confirm: true, File: p.Path, Line: t.Line,
			Task: c.ID, From: c.From.String(), To: c.To.String(),
			Message: fmt.Sprintf("%s is %s but %s; igris will set it to %s", c.ID, c.From, DriftReason(p, t, c.To), c.To),
		}
	}
	return out
}

// DriftReason says why the readiness sync would flip a task's status.
func DriftReason(p *plan.Plan, t *plan.Task, to plan.Status) string {
	if to == plan.Ready {
		return "all its dependencies are satisfied"
	}
	return "waits on " + strings.Join(p.WaitingOn(t).Unmet, ", ")
}

// DriftLine is a drift result in the short form `ID: from → to`.
func DriftLine(r Result) string {
	return fmt.Sprintf("%s: %s → %s", r.Task, r.From, r.To)
}
