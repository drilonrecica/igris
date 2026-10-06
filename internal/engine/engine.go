package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// ErrUnsupported marks a situation the run loop doesn't handle yet: user
// tasks and resuming an interrupted run. The run stops without touching
// anything.
var ErrUnsupported = errors.New("not supported yet")

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

	Phase   string // phase to run
	Through string // last phase to run; "" means only Phase (SPEC §5.3)
	Mode    string // run mode chosen for this run (--mode); "" means none

	ForceUnlock bool // clear a stale run lock (--force-unlock)
	// ConfirmedDrift says the owner accepted that the first write also fixes
	// the plan's drifted ready/blocked cells (SPEC §5.2).
	ConfirmedDrift bool
	// ConfirmedYolo says the owner confirmed skip-permissions mode for this
	// run (SPEC §7.3). Set it only from that explicit, per-run confirmation.
	ConfirmedYolo bool

	// Events receives every event, on the goroutine that called Run. It may
	// call Send.
	Events func(Event)

	beforeMark func() // test hook: runs just before a task is marked in progress
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
}

// Engine runs the tasks of one or more phases, one session at a time
// (SPEC §5, §6). Create it with New, call Run once, and steer it with Send.
type Engine struct {
	opts     Options
	cfg      *config.Config
	be       backend.Backend
	dir      *state.Dir
	clock    Clock
	planPath string
	planOpts plan.Options
	writer   *plan.Writer
	conf     configWatch

	mu    sync.Mutex
	queue []Command     // guarded by mu
	wake  chan struct{} // poked by Send

	// Only touched by the goroutine in Run.
	run   *state.Run // state.json
	phase string     // current phase
	task  *launch    // current task; nil between tasks
	pause bool       // pause-after-task is on
	stop  bool       // the owner asked to stop
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
	case opts.Phase == "":
		return nil, errors.New("engine: no phase; name the phase to run")
	case opts.Mode != "" && !ValidMode(opts.Mode):
		return nil, fmt.Errorf("engine: unknown run mode %q (want default|accept|auto|plan|yolo)", opts.Mode)
	}
	if err := opts.Config.Validate(); err != nil {
		return nil, fmt.Errorf("engine: invalid config: %w", err)
	}
	if opts.Clock == nil {
		opts.Clock = systemClock{}
	}
	root := opts.State.Root()
	e := &Engine{
		opts:     opts,
		cfg:      opts.Config,
		be:       opts.Backend,
		dir:      opts.State,
		clock:    opts.Clock,
		planPath: inRoot(root, opts.Config.Plan),
		planOpts: plan.Options{Columns: opts.Config.Columns},
		conf:     newConfigWatch(filepath.Join(root, state.ConfigFile), opts.Config),
		wake:     make(chan struct{}, 1),
	}
	e.writer = plan.NewWriter(e.planPath, e.planOpts, e.cfg.Models)
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
	scope := "phase " + strings.Join(phases, ", ")
	e.log(state.Event{Type: state.EventRunStarted, Detail: scope})
	e.emit(Event{Kind: RunStarted, Detail: scope})

	res, err := e.runPhases(ctx, phases)
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
// in state.json. It returns the IDs of the phases to run.
func (e *Engine) prepare() ([]string, error) {
	p, err := e.loadPlan()
	if err != nil {
		return nil, err
	}
	phases, err := p.PhasesThrough(e.opts.Phase, e.opts.Through)
	if err != nil {
		return nil, err
	}
	if drift := p.Readiness(); len(drift) > 0 && !e.opts.ConfirmedDrift {
		return nil, &DriftError{Changes: drift}
	}
	ids := make([]string, len(phases))
	for i, ph := range phases {
		ids[i] = ph.ID
		// Refuse an unconfirmed skip-permissions task now rather than hours
		// into the run. runTask checks again: the plan can change.
		for _, t := range ph.Tasks {
			if t.Status.Satisfied() || !t.Owner.IsAgent() {
				continue
			}
			if _, err := e.modeFor(t); err != nil {
				return nil, err
			}
		}
	}

	switch prev, err := e.dir.LoadRun(); {
	case errors.Is(err, state.ErrNoRun):
	case err != nil:
		return nil, err
	case prev.Current != nil:
		// Its session may still be alive; a new run would overwrite the
		// only record of it.
		return nil, fmt.Errorf("an earlier run stopped while working on %s; resuming a run is %w", prev.Current.TaskID, ErrUnsupported)
	}
	e.run = &state.Run{StartedAt: e.clock.Now(), Phases: ids, Through: e.opts.Through, ConfigHash: e.cfg.Hash()}
	return ids, e.dir.SaveRun(e.run)
}

// loadPlan re-reads the plan; igris never works from a stale or invalid one.
func (e *Engine) loadPlan() (*plan.Plan, error) {
	p, err := plan.Load(e.planPath, e.planOpts)
	if err != nil {
		return nil, err
	}
	if err := p.Check(e.cfg.Models); err != nil {
		return nil, err
	}
	return p, nil
}

// modeFor resolves the run mode of t (SPEC §7.2) and refuses
// skip-permissions mode the owner didn't confirm.
func (e *Engine) modeFor(t *plan.Task) (string, error) {
	mode, err := ResolveMode("", t.Mode, e.opts.Mode, e.cfg.DefaultMode)
	if err != nil {
		return "", fmt.Errorf("task %s: %w", t.ID, err)
	}
	if mode == ModeYolo && !e.opts.ConfirmedYolo {
		return "", fmt.Errorf("task %s would run in yolo mode: %w", t.ID, ErrYoloUnconfirmed)
	}
	return mode, nil
}

func (e *Engine) runPhases(ctx context.Context, phases []string) (Result, error) {
	var res Result
	for _, id := range phases {
		e.phase = id
		e.emit(Event{Kind: PhaseStarted})
		var err error
		if res, err = e.runPhase(ctx, id); err != nil || res.Outcome != Completed {
			return res, err
		}
	}
	return res, nil
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
		p, err := e.loadPlan()
		if err != nil {
			return res, err
		}
		sel, err := p.Select(id)
		if err != nil {
			return res, err
		}
		switch sel.Outcome {
		case plan.Complete:
			res.Outcome = Completed
			e.emit(Event{Kind: PhaseDone})
			e.toast(ctx, notifyPhaseDone, "complete")
			return res, nil
		case plan.Stuck:
			res.Outcome, res.Waiting = Stuck, sel.Waiting
			waits := make([]string, len(sel.Waiting))
			for i, w := range sel.Waiting {
				waits[i] = w.String()
			}
			e.emit(Event{Kind: PhaseStuck, Waiting: sel.Waiting, Detail: strings.Join(waits, "; ")})
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
