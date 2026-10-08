package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// ErrYoloUnconfirmed is returned when a task would launch in skip-permissions
// mode without the owner's confirmation for this run (SPEC §7.3).
var ErrYoloUnconfirmed = errors.New("skip-permissions mode needs the owner's confirmation for this run; confirm it when starting `igris arise`")

// DriftError is returned when the plan's ready/blocked cells don't match its
// dependencies and the owner hasn't confirmed the fix (SPEC §5.2). Igris's
// first write would rewrite those cells.
type DriftError struct {
	Changes []plan.Change // what the first write would change
}

func (e *DriftError) Error() string {
	parts := make([]string, len(e.Changes))
	for i, c := range e.Changes {
		parts[i] = c.String()
	}
	return fmt.Sprintf("%d ready/blocked cell(s) in the plan don't match its dependencies (%s); confirm the fix before igris writes the plan", len(e.Changes), strings.Join(parts, ", "))
}

// Options configures one run.
type Options struct {
	// Config is the snapshot used for the whole run (SPEC §13): exactly what
	// was loaded from igris.toml in the project root (or config.Default if
	// there is none). The file is watched, never re-applied.
	Config  *config.Config
	Backend backend.Backend
	State   *state.Dir // the project's .igris/; its root is the project root
	Clock   Clock      // nil means the system clock
	// Runner runs the verify command and git; nil means real processes.
	Runner runner.Runner
	// Secrets are the resolved env: references of the notify settings
	// (Config.Resolve); the zero value sets up no ntfy token or Discord.
	Secrets config.Secrets
	// Notifier delivers notifications (SPEC §10); nil means a router built
	// from Config.Notify, Secrets and Backend.
	Notifier Notifier

	Phase   string // phase to run; "" resumes the previous run's phases
	Through string // last phase to run; "" means only Phase (SPEC §5.3)
	Mode    string // run mode chosen for this run (--mode); "" means none
	// Selection limits the run to part of its phases (SPEC §5.5: --only,
	// --from, --until). With no Phase it also sets the phase range; with
	// neither, the previous run's selection is resumed.
	Selection state.Selection

	ForceUnlock bool // clear a stale run lock (--force-unlock)
	// ConfirmedDrift says the owner accepted that the first write also fixes
	// the plan's drifted ready/blocked cells (SPEC §5.2).
	ConfirmedDrift bool
	// ConfirmedYolo says the owner confirmed skip-permissions mode for this
	// run (SPEC §7.3). Set it only from that explicit, per-run confirmation.
	ConfirmedYolo bool

	// IgrisPath is the igris binary the session hooks run (SPEC §6.3); ""
	// means the running one.
	IgrisPath string

	// Events receives every event, on the goroutine that called Run. It may
	// call Send.
	Events func(Event)

	beforeMark func() // test hook: runs just before a task is marked in progress
	noVerify   bool   // a dry run: verify profiles are shown, never run
	noHooks    bool   // a dry run: task hooks are announced, never run
}

// Notifier delivers one notification to the owner's channels and says how
// each delivery went. *notify.Router is the real one.
type Notifier interface {
	Notify(ctx context.Context, m notify.Message) []notify.Result
}

// Outcome says how a run ended.
type Outcome int

// Run outcomes. A failed run has no outcome; Run returns an error instead.
const (
	Completed Outcome = iota + 1 // every phase of the run is complete
	Stuck                        // a phase has unfinished tasks and none can start
	Stopped                      // the owner stopped the run, or its context ended
)

func (o Outcome) String() string {
	switch o {
	case Completed:
		return "completed"
	case Stuck:
		return "stuck"
	case Stopped:
		return "stopped"
	}
	return "unknown"
}

// Result is how a run ended.
type Result struct {
	Outcome Outcome
	Phase   string         // the phase the run ended in
	Waiting []plan.Waiting // Stuck: every unfinished task with its unmet deps
	// NotRun are the slice's tasks that were left because their deps are
	// unmet (SPEC §5.5), in the order they were reported.
	NotRun []plan.Waiting
}

// Engine runs the tasks of one or more phases, one session at a time
// (SPEC §5, §6). Create it with New, call Run once, and steer it with Send.
type Engine struct {
	notifier  Notifier
	opts      Options
	cfg       *config.Config
	be        backend.Backend
	dir       *state.Dir
	clock     Clock
	runner    runner.Runner
	planPath  string
	planOpts  plan.Options
	writer    *plan.Writer
	conf      configWatch
	plans     planWatch
	commitMsg *template.Template

	mu    sync.Mutex
	queue []Command     // guarded by mu
	wake  chan struct{} // poked by Send

	// Only touched by the goroutine in Run.
	run      *state.Run      // state.json
	rng      Range           // the run's phases and slice
	phase    string          // current phase
	task     *launch         // current task; nil between tasks
	pause    bool            // pause-after-task is on
	hold     bool            // the plan changed outside igris; nothing is selected until pause goes off
	stop     bool            // the owner asked to stop
	pending  []Command       // task-scoped commands not handled yet
	reported map[string]bool // keys of the signals and problems reported once
	// configMoved: igris.toml differs from the one of the run being resumed.
	configMoved bool
	runMode     string            // the run mode chosen for this run; see CmdMode
	overrides   map[string]string // per-task modes by task ID; see CmdTaskMode
	yoloOK      bool              // skip-permissions mode was confirmed through CmdMode or CmdTaskMode
}

// New checks opts and returns an engine. It does no I/O.
func New(opts Options) (*Engine, error) {
	opts.Phase = strings.TrimSpace(opts.Phase)
	opts.Mode = strings.ToLower(strings.TrimSpace(opts.Mode))
	switch {
	case opts.Config == nil:
		return nil, errors.New("engine: no config")
	case opts.Backend == nil:
		return nil, errors.New("engine: no backend")
	case opts.State == nil:
		return nil, errors.New("engine: no state directory")
	case opts.Mode != "" && !ValidMode(opts.Mode):
		return nil, fmt.Errorf("engine: unknown run mode %q (want default|accept|auto|plan|yolo)", opts.Mode)
	}
	if err := opts.Config.Validate(); err != nil {
		return nil, fmt.Errorf("engine: invalid config: %w", err)
	}
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	if opts.Runner == nil {
		opts.Runner = runner.Exec{}
	}
	commitMsg, err := parseCommitMessage(opts.Config.Run.CommitMessage)
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	if opts.Notifier == nil {
		opts.Notifier = notify.FromConfig(opts.Config.Notify, opts.Secrets, opts.Backend)
	}
	root := opts.State.Root()
	e := &Engine{
		notifier:  opts.Notifier,
		opts:      opts,
		cfg:       opts.Config,
		be:        opts.Backend,
		dir:       opts.State,
		clock:     opts.Clock,
		runner:    opts.Runner,
		planPath:  inRoot(root, opts.Config.Plan),
		planOpts:  plan.Options{Columns: opts.Config.Columns},
		conf:      newConfigWatch(filepath.Join(root, state.ConfigFile), opts.Config),
		wake:      make(chan struct{}, 1),
		reported:  map[string]bool{},
		commitMsg: commitMsg,
		runMode:   opts.Mode,
		overrides: map[string]string{},
	}
	e.writer = plan.NewWriter(e.planPath, e.planOpts, e.cfg.Rules(e.dir.Root()))
	return e, nil
}

// inRoot resolves a config path relative to the project root.
func inRoot(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}

// Run runs the phases until they are complete, a phase is stuck, the owner
// stops the run or something fails. It holds the project's run lock while it
// runs. Stopping (also by cancelling ctx) is not an error: the current
// session stays open and the state is kept.
//
// Run refuses to start, before writing anything, if the backend is
// unavailable, another run holds the lock, the plan is invalid, its
// readiness drifted without ConfirmedDrift (*DriftError), or a task would
// run in skip-permissions mode without ConfirmedYolo (ErrYoloUnconfirmed).
func (e *Engine) Run(ctx context.Context) (Result, error) {
	if err := e.be.Available(ctx); err != nil {
		return Result{}, fmt.Errorf("backend %s is not available: %w", e.be.Name(), err)
	}
	lock, err := e.dir.Lock(e.opts.ForceUnlock)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if err := lock.Release(); err != nil {
			e.warn(err.Error())
		}
	}()

	phases, err := e.prepare()
	if err != nil {
		return Result{}, err
	}
	scope := e.rng.Scope()
	e.log(state.Event{Type: state.EventRunStarted, Detail: scope})
	e.emit(Event{Kind: RunStarted, Detail: scope})

	if e.configMoved {
		e.warn("igris.toml changed since the interrupted run; this run uses the file as it is now")
	}
	res, err := e.resumeAndRun(ctx, phases)
	if err != nil && ctx.Err() != nil {
		// A call was cut short by the cancellation: that is a stop.
		res, err = Result{Outcome: Stopped, Phase: e.phase}, nil
	}
	detail := res.Outcome.String()
	if err != nil {
		res, detail = Result{Phase: e.phase}, "error"
		e.log(state.Event{Type: state.EventError, Detail: err.Error()})
		e.emit(Event{Kind: RunFailed, Detail: err.Error()})
		e.toast(ctx, notifyRunError, "the run stopped with an error")
	}
	e.log(state.Event{Type: state.EventRunStopped, Detail: detail})
	e.emit(Event{Kind: RunStopped, Detail: detail})
	return res, err
}

// prepare validates the plan and the run's gates, then records the new run
// in state.json. It returns the IDs of the phases to run. A task the
// previous run was working on is carried over for resume (SPEC §13); with
// no phase named, the previous run's phases are run again.
func (e *Engine) prepare() ([]string, error) {
	prev, err := e.dir.LoadRun()
	switch {
	case errors.Is(err, state.ErrNoRun):
		prev = nil
	case err != nil:
		return nil, err
	}
	from, through, sel := e.opts.Phase, e.opts.Through, e.opts.Selection
	resumed := false
	if from == "" && sel.Empty() {
		if prev == nil || len(prev.Phases) == 0 {
			return nil, errors.New("there is no earlier run to resume; name the phase to run, e.g. `igris arise M0`")
		}
		from, through, resumed = prev.Phases[0], prev.Through, true
		if prev.Selection != nil {
			sel = *prev.Selection
		}
	}

	p, err := e.loadPlan()
	if err != nil {
		return nil, err
	}
	rng, err := ResolveRange(p, from, through, sel)
	if err != nil {
		if resumed && !sel.Empty() {
			return nil, fmt.Errorf("resume the last run's %s: %w; or name a phase to start a new run", sel, err)
		}
		return nil, err
	}
	e.rng = rng
	phases := rng.Phases
	if drift := p.Readiness(); len(drift) > 0 && !e.opts.ConfirmedDrift {
		return nil, &DriftError{Changes: drift}
	}
	e.plans.reset(p)
	var cur *state.Current
	if prev != nil {
		cur = prev.Current
	}
	// A session left running in skip-permissions mode is only reattached
	// with this run's confirmation too (SPEC §7.3); new sessions are gated
	// by modeFor.
	if cur != nil && cur.Mode == ModeYolo && !e.yoloConfirmed() {
		return nil, fmt.Errorf("the interrupted task %s runs in yolo mode: %w", cur.TaskID, ErrYoloUnconfirmed)
	}
	ids := make([]string, 0, len(phases))
	for _, ph := range phases {
		// A resumed run starts in the phase of the task it picks up again;
		// the phases before it have nothing left to do for this run. A
		// slice keeps them: its earlier tasks may still be left.
		if cur != nil && rng.Slice == nil && len(ids) > 0 && hasTask(ph, cur.TaskID) {
			ids = ids[:0]
		}
		ids = append(ids, ph.ID)
		// Refuse an unconfirmed skip-permissions task now rather than hours
		// into the run. runTask checks again: the plan can change.
		for _, t := range ph.Tasks {
			if t.Status.Satisfied() || !t.Owner.IsAgent() || !rng.In(t) {
				continue
			}
			if _, err := e.modeFor(t); err != nil {
				return nil, err
			}
		}
	}
	e.run = &state.Run{StartedAt: e.clock.Now(), Phases: ids, Through: rng.Through, ConfigHash: e.cfg.Hash(), Current: cur}
	if !sel.Empty() {
		e.run.Selection = &sel
	}
	if prev != nil && cur != nil && prev.ConfigHash != e.run.ConfigHash {
		e.configMoved = true
	}
	return ids, e.dir.SaveRun(e.run)
}

func hasTask(ph *plan.Phase, id string) bool {
	for _, t := range ph.Tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

// PlanLoadError adds the next step to a plan that can't be read.
func PlanLoadError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w; set plan in igris.toml to your plan file, or write one there (format: README \"Your plan\"; `igris adapt` converts another format)", err)
	}
	return err
}

// loadPlan re-reads the plan; igris never works from a stale or invalid one.
func (e *Engine) loadPlan() (*plan.Plan, error) {
	p, err := plan.Load(e.planPath, e.planOpts)
	if err != nil {
		return nil, PlanLoadError(err)
	}
	if err := p.Check(e.cfg.Rules(e.dir.Root())); err != nil {
		return nil, err
	}
	return p, nil
}

// IsUserTask reports whether id is a task in the plan that igris launches
// no session for (it is false when the plan can't be read or has no such task).
func (e *Engine) IsUserTask(id string) bool {
	p, err := plan.Load(e.planPath, e.planOpts)
	if err != nil {
		return false
	}
	t := p.Task(id)
	return t != nil && !t.Owner.IsAgent()
}

// modeFor resolves the run mode of t (SPEC §7.2) and refuses
// skip-permissions mode the owner didn't confirm.
func (e *Engine) modeFor(t *plan.Task) (string, error) {
	mode, err := ResolveMode(e.overrides[t.ID], t.Mode, e.runMode, e.cfg.DefaultMode)
	if err != nil {
		return "", fmt.Errorf("task %s: %w", t.ID, err)
	}
	if mode == ModeYolo && !e.yoloConfirmed() {
		return "", fmt.Errorf("task %s would run in yolo mode: %w", t.ID, ErrYoloUnconfirmed)
	}
	return mode, nil
}

// resumeAndRun picks up the task an earlier run was working on, then runs
// the phases.
func (e *Engine) resumeAndRun(ctx context.Context, phases []string) (Result, error) {
	if e.run.Current != nil {
		stopped, err := e.resume(ctx)
		if err != nil {
			return Result{Phase: e.phase}, err
		}
		if stopped {
			return Result{Outcome: Stopped, Phase: e.phase}, nil
		}
	}
	return e.runPhases(ctx, phases)
}

// yoloConfirmed reports whether the owner confirmed skip-permissions mode
// for this run.
func (e *Engine) yoloConfirmed() bool { return e.opts.ConfirmedYolo || e.yoloOK }

func (e *Engine) runPhases(ctx context.Context, phases []string) (Result, error) {
	var res Result
	var notRun []plan.Waiting
	for _, id := range phases {
		if e.rng.Slice != nil {
			// A phase without a slice task has nothing to run.
			p, err := e.loadPlan()
			if err != nil {
				return Result{Phase: e.phase, NotRun: notRun}, err
			}
			if !sliceHas(p, id, e.rng) {
				continue
			}
		}
		e.phase = id
		e.emit(Event{Kind: PhaseStarted})
		var err error
		res, err = e.runPhase(ctx, id)
		notRun = append(notRun, res.NotRun...)
		res.NotRun = notRun
		if err != nil || res.Outcome != Completed {
			return res, err
		}
	}
	if res.Phase == "" {
		// No phase had a slice task left: only an interrupted task ran.
		res = Result{Outcome: Completed, Phase: e.phase, NotRun: notRun}
	}
	return res, nil
}

// sliceHas says whether the phase id has a task of the run's slice.
func sliceHas(p *plan.Plan, id string, rng Range) bool {
	ph := p.Phase(id)
	if ph == nil {
		return false
	}
	for _, t := range ph.Tasks {
		if rng.In(t) {
			return true
		}
	}
	return false
}

// runPhase runs the tasks of one phase in §5.1 order until the phase is
// complete or stuck, or the run stops.
func (e *Engine) runPhase(ctx context.Context, id string) (Result, error) {
	res := Result{Phase: id}
	held := false // the Paused event for the current hold was sent
	for {
		e.checkConfig(ctx)
		if e.stopping(ctx) {
			res.Outcome = Stopped
			return res, nil
		}
		for _, c := range e.takeCommands() {
			e.reject(c, "no task is running")
		}
		p, err := e.loadPlan()
		if err != nil {
			return res, err
		}
		e.checkPlan(ctx, p)
		if e.hold {
			e.wait(ctx, e.cfg.PollInterval.Std())
			continue
		}
		sel, err := p.SelectIn(id, e.rng.In)
		if err != nil {
			return res, err
		}
		switch sel.Outcome {
		case plan.Complete:
			res.Outcome = Completed
			// The slice may be done in a phase that is not (SPEC §5.5).
			if whole, err := p.Select(id); err == nil && whole.Outcome == plan.Complete {
				e.emit(Event{Kind: PhaseDone})
				e.toast(ctx, notifyPhaseDone, "complete")
			}
			return res, nil
		case plan.Stuck:
			if e.rng.Slice != nil {
				// Not a stuck phase: the slice's tasks left here wait on
				// tasks the run doesn't touch. The run goes on.
				res.Outcome, res.NotRun = Completed, sel.Waiting
				e.emit(Event{Kind: NotRun, Waiting: sel.Waiting, Detail: waitText(sel.Waiting)})
				return res, nil
			}
			res.Outcome, res.Waiting = Stuck, sel.Waiting
			e.emit(Event{Kind: PhaseStuck, Waiting: sel.Waiting, Detail: waitText(sel.Waiting)})
			e.toast(ctx, notifyPhaseStuck, fmt.Sprintf("stuck: %d unfinished task(s), none can start", len(sel.Waiting)))
			return res, nil
		}

		if e.pause {
			// Pause-after-task: the run stays alive but launches nothing.
			if !held {
				held = true
				e.emit(Event{Kind: Paused, Task: sel.Task.ID, Title: sel.Task.Title})
			}
			e.wait(ctx, e.cfg.PollInterval.Std())
			continue
		}
		held = false
		stopped, err := e.runTask(ctx, sel.Task)
		if err != nil {
			return res, err
		}
		if stopped {
			res.Outcome = Stopped
			return res, nil
		}
	}
}

// waitText joins the waits for an event's Detail.
func waitText(ws []plan.Waiting) string {
	waits := make([]string, len(ws))
	for i, w := range ws {
		waits[i] = w.String()
	}
	return strings.Join(waits, "; ")
}
