package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// The run loop (SPEC §5.1). Every poll, runPhase re-reads the plan, starts
// what may start (one task in a phase without the parallel marker, up to
// max in a parallel phase, §3.8), then steps every active task once:
// its commands, its signals and its session's state. A task's launch stays
// in e.active from its start (or its reattach on resume, §13) until it is
// done, skipped or reset.

// addLaunch records l as active, last, as state.json does (Run.Activate).
func (e *Engine) addLaunch(l *launch) {
	for i, old := range e.active {
		if old.t.ID == l.t.ID {
			e.active[i] = l
			return
		}
	}
	e.active = append(e.active, l)
}

// dropLaunch forgets the active task id, and the focus with it when it was
// the focused one.
func (e *Engine) dropLaunch(id string) {
	if l := e.launchOf(id); l != nil {
		e.dropCheck(l)
		e.unqueueIntegration(l)
	}
	e.active = slices.DeleteFunc(e.active, func(l *launch) bool { return l.t.ID == id })
	if e.task != nil && e.task.t.ID == id {
		e.task = nil
	}
}

// launchOf returns the active task id, or nil.
func (e *Engine) launchOf(id string) *launch {
	for _, l := range e.active {
		if l.t.ID == id {
			return l
		}
	}
	return nil
}

// first is the first active task, state.json's current one (SPEC §13), or
// nil.
func (e *Engine) first() *launch {
	if len(e.active) == 0 {
		return nil
	}
	return e.active[0]
}

// focus makes l the task igris handles now: events, log lines, waits and
// notifications without a task of their own are its (emit, log, toast).
func (e *Engine) focus(l *launch) { e.task = l }

// unfocus ends a focus: the first active task is handled again.
func (e *Engine) unfocus() { e.task = e.first() }

// activeIDs are the IDs of the active tasks, in order.
func (e *Engine) activeIDs() []string {
	ids := make([]string, len(e.active))
	for i, l := range e.active {
		ids[i] = l.t.ID
	}
	return ids
}

// schedule is what one poll of a phase decided (SPEC §5.1): the tasks to
// start, the tasks in progress to pick up (none of this run's), and the
// outcome when nothing starts.
type schedule struct {
	outcome plan.Outcome
	start   []*plan.Task
	adopt   []*plan.Task
	waiting []plan.Waiting
	// parallel says the phase is scheduled as a parallel one: its tasks
	// get slots (SPEC §6.11).
	parallel bool
}

// schedule applies SPEC §5.1 to the phase id of plan p: the parallel rule
// in a phase marked parallel that may run that way (parallelOn), else the
// sequential rule, one task at a time. An active task of another phase
// (the interrupted task of an earlier run, picked up first, §13) is
// finished before anything starts.
func (e *Engine) schedule(ctx context.Context, p *plan.Plan, id string) (schedule, error) {
	ph := p.Phase(id)
	if ph == nil {
		_, err := p.Select(id)
		return schedule{}, err
	}
	for _, l := range e.active {
		if !strings.EqualFold(phaseOf(l.t), id) {
			return schedule{outcome: plan.Busy}, nil
		}
	}
	if e.parallelOn(ctx, ph) {
		rules := plan.ParallelRules{Max: e.parallelMax(), RankCaps: e.cfg.Parallel.RankCaps(), ExclusiveOwner: e.cfg.Parallel.ExclusiveOwnerTasksOn()}
		sel, err := p.SelectParallel(id, e.rng.In, e.activeIDs(), rules)
		if err != nil {
			return schedule{}, err
		}
		return schedule{outcome: sel.Outcome, start: sel.Start, adopt: sel.Adopt, waiting: sel.Waiting, parallel: true}, nil
	}
	if len(e.active) > 0 {
		return schedule{outcome: plan.Busy}, nil
	}
	sel, err := p.SelectIn(id, e.rng.In)
	if err != nil {
		return schedule{}, err
	}
	s := schedule{outcome: sel.Outcome, waiting: sel.Waiting}
	if sel.Outcome == plan.Next {
		if sel.Task.Status == plan.InProgress {
			s.adopt = []*plan.Task{sel.Task}
		} else {
			s.start = []*plan.Task{sel.Task}
		}
	}
	return s, nil
}

// wantsPlan says whether a poll of phase id re-reads the plan: when a task
// could start, so with nothing active, or in a phase known to run its tasks
// at once while fewer than max are active. A phase is known that way once
// its first poll decided it (parallelOn).
func (e *Engine) wantsPlan(id string) bool {
	return len(e.active) == 0 || (e.parallel[id] && len(e.active) < e.parallelMax())
}

// parallelMax is how many tasks a parallel phase runs at once: --parallel,
// else [parallel] max (SPEC §5.1).
func (e *Engine) parallelMax() int { return cmp.Or(e.opts.Parallel, e.cfg.Parallel.MaxOrDefault()) }

// parallelOn says whether ph runs its tasks at the same time: it is marked
// parallel and the project has a git repository with a commit and commit =
// ask or auto (SPEC §6.11). Otherwise a marked phase runs one task at a
// time, with a warning once per phase. Decided once per phase and run.
func (e *Engine) parallelOn(ctx context.Context, ph *plan.Phase) bool {
	if !ph.Parallel {
		return false
	}
	if on, decided := e.parallel[ph.ID]; decided {
		return on
	}
	why := ""
	switch commit := cmp.Or(e.opts.scheduleCommit, e.cfg.Run.Commit); {
	case commit == CommitNever:
		why = fmt.Sprintf("commit = %q", commit)
	case !e.opts.noGit && !e.hasCommit(ctx):
		why = "there is no git repository with a commit here"
	}
	if e.parallel == nil {
		e.parallel = map[string]bool{}
	}
	e.parallel[ph.ID] = why == ""
	if why != "" {
		e.warn(fmt.Sprintf("phase %s is marked parallel, but %s; it runs one task at a time", ph.ID, why))
	}
	return why == ""
}

// hasCommit reports whether the project root is in a git repository with
// at least one commit.
func (e *Engine) hasCommit(ctx context.Context) bool {
	_, err := e.gitIn(ctx, e.dir.Root(), "rev-parse", "--verify", "HEAD")
	return err == nil
}

// freeSlot is the lowest slot from 0 to state.MaxSlots-1 no active task
// holds (SPEC §6.11), or nil when every one is held (more tasks in
// progress than slots: max was lowered).
func (e *Engine) freeSlot() *int {
	held := map[int]bool{}
	for _, l := range e.active {
		if l.cur != nil && l.cur.Slot != nil {
			held[*l.cur.Slot] = true
		}
	}
	for n := range state.MaxSlots {
		if !held[n] {
			return &n
		}
	}
	return nil
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
		e.routeCommands()
		if _, err := e.resets(ctx, true); err != nil {
			return res, err
		}
		// The plan is re-read to select (SPEC §5.1, §5.4): when a task
		// could start. While tasks run, a plan that can't be read holds
		// the starts, never the run.
		var p *plan.Plan
		if e.wantsPlan(id) {
			var err error
			if p, err = e.loadPlan(); err != nil {
				if len(e.active) == 0 {
					return res, err
				}
				e.reportOnce("plan|"+err.Error(), Warning, fmt.Sprintf("nothing new starts until the plan is valid: %v", err))
			}
		}
		if p != nil {
			e.checkPlan(ctx, p)
		}
		if p != nil && !e.hold {
			s, err := e.schedule(ctx, p, id)
			if err != nil {
				return res, err
			}
			switch s.outcome {
			case plan.Complete:
				res.Outcome = Completed
				// The slice may be done in a phase that is not (SPEC §5.5).
				if whole, err := p.Select(id); err == nil && whole.Outcome == plan.Complete {
					e.emit(Event{Kind: PhaseDone})
					e.flushTasks(ctx)
					e.toast(ctx, notifyPhaseDone, "complete")
				}
				return res, nil
			case plan.Stuck:
				if e.rng.Slice != nil {
					// Not a stuck phase: the slice's tasks left here wait on
					// tasks the run doesn't touch. The run goes on.
					res.Outcome, res.NotRun = Completed, s.waiting
					e.emit(Event{Kind: NotRun, Waiting: s.waiting, Detail: waitText(s.waiting)})
					return res, nil
				}
				res.Outcome, res.Waiting = Stuck, s.waiting
				e.emit(Event{Kind: PhaseStuck, Waiting: s.waiting, Detail: waitText(s.waiting)})
				e.flushTasks(ctx)
				e.toast(ctx, notifyPhaseStuck, fmt.Sprintf("stuck: %d unfinished task(s), none can start", len(s.waiting)))
				return res, nil
			case plan.Next:
				switch {
				case len(s.adopt) > 0:
					// A task in progress is picked up whatever pause says:
					// it is already running (SPEC §5.1 rule 1).
					if err := e.startAll(ctx, s); err != nil {
						return res, err
					}
				case e.pause:
					// Pause-after-task: the run stays alive but launches nothing.
					if !held {
						held = true
						e.emit(Event{Kind: Paused, Task: s.start[0].ID, Title: s.start[0].Title})
					}
				default:
					held = false
					if err := e.startAll(ctx, s); err != nil {
						return res, err
					}
				}
			}
		}
		progressed := false
		for _, l := range slices.Clone(e.active) {
			if e.launchOf(l.t.ID) != l {
				continue // settled meanwhile, by a reset
			}
			e.focus(l)
			ended, stopped, err := e.step(ctx, l)
			e.unfocus()
			if err != nil {
				return res, err
			}
			if stopped {
				res.Outcome = Stopped
				return res, nil
			}
			progressed = progressed || ended
		}
		if progressed {
			continue // a task ended: select again at once
		}
		e.watchOverlap(ctx)
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}

// startAll picks up s's tasks in progress and starts its tasks, in order,
// as one scheduling round. A task whose row changed since the selection
// ends the round: the loop selects again from the fresh plan.
func (e *Engine) startAll(ctx context.Context, s schedule) error {
	e.round++
	for _, t := range s.adopt {
		if err := e.adoptTask(ctx, t, s.parallel); err != nil {
			return err
		}
	}
	for _, t := range s.start {
		started, err := e.startTask(ctx, t, s.parallel)
		if err != nil || !started {
			return err
		}
	}
	return nil
}

// routeCommands rejects the task-scoped commands no active task can take:
// one naming a task that isn't active, or any when nothing runs. The rest
// wait for the step of their task (takeCommands).
func (e *Engine) routeCommands() {
	kept := e.pending[:0]
	for _, c := range e.pending {
		switch {
		case len(e.active) == 0:
			e.reject(c, "no task is running")
		case c.Task != "" && e.launchOf(c.Task) == nil:
			e.reject(c, c.Task+" is not an active task")
		default:
			kept = append(kept, c)
		}
	}
	e.pending = kept
}

// step handles one poll of the active task l: its commands, its signals
// and, for an agent task, its session (SPEC §6.3). ended reports that the
// task is no longer active (done, skipped or reset); stopped that the run
// stops.
func (e *Engine) step(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	e.checkConfig(ctx) // before the signal, so a config edit is seen before the task is accepted
	if l.integ != nil {
		return e.stepIntegrate(ctx, l)
	}
	if l.check != nil {
		return e.stepCheck(ctx, l)
	}
	var v verdict
	var decided bool
	if l.t.Owner.IsAgent() {
		v, decided, err = e.watchStep(ctx, l)
	} else {
		v, decided = e.ownerStep(ctx, l)
	}
	if err != nil || !decided {
		return false, false, err
	}
	return e.conclude(ctx, l, v)
}

// conclude acts on the verdict v of l's task: a done signal is verified
// (SPEC §6.4) and committed (§6.5) before the task is marked done; the
// owner's word is final. A failed verify leaves the task active; so does
// a verify in a parallel phase until its check ends (stepCheck).
func (e *Engine) conclude(ctx context.Context, l *launch, v verdict) (ended, stopped bool, err error) {
	switch v.kind {
	case verdictDone:
		if l.t.Owner.IsAgent() && v.sig != nil && e.verifies(l.t) {
			if l.isolated() {
				// Beside the other tasks' sessions (SPEC §6.11).
				e.queueVerify(ctx, l, v)
				return false, false, nil
			}
			passed, err := e.verify(ctx, l)
			if err != nil || !passed {
				return false, false, err // the task stays in progress
			}
		}
		return e.accept(ctx, l, v)
	case verdictSkip:
		if reset, stopped, err := e.settleResets(ctx, l.t.ID); err != nil || stopped || reset {
			return reset, stopped, err
		}
		return true, false, e.finish(ctx, l, plan.Skipped, v.note)
	case verdictReset:
		return true, false, nil
	}
	return false, true, nil
}

// accept accepts the done v of l's task once verified, or the owner's
// done: it is committed (SPEC §6.5) before it is marked done. A task in a
// worktree of its own is integrated first (SPEC §6.11), beside the other
// tasks.
func (e *Engine) accept(ctx context.Context, l *launch, v verdict) (ended, stopped bool, err error) {
	if l.isolated() && l.t.Owner.IsAgent() {
		// A user task has no worktree and is never committed (SPEC §6.5).
		return e.beginIntegration(ctx, l, v)
	}
	// A reset requested while the task was verified is answered and
	// applied before it is accepted, never lost (SPEC §6.2).
	if reset, stopped, err := e.settleResets(ctx, l.t.ID); err != nil || stopped || reset {
		return reset, stopped, err
	}
	if l.t.Owner.IsAgent() {
		if stopped, reset, err := e.commit(ctx, l, v.note); err != nil || stopped || reset {
			return reset, stopped, err
		}
	}
	return true, false, e.finish(ctx, l, plan.Done, v.note)
}
