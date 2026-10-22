package plan

import "strings"

// ParallelRules are the limits SPEC §5.1 applies when it starts tasks in a
// parallel phase (§3.8).
type ParallelRules struct {
	// Max is how many tasks may be active at once ([parallel] max, or
	// --parallel). Tasks already in progress count even past it.
	Max int
	// RankCaps holds, per rank, how many active tasks of it there may be
	// ([parallel.ranks]); a rank not listed is held by Max alone. Ranks
	// compare case-sensitively, as everywhere.
	RankCaps map[string]int
	// ExclusiveOwner: a user or agent + user task starts only when no task
	// is active, and nothing starts while one is active ([parallel]
	// exclusive_owner_tasks).
	ExclusiveOwner bool
}

// ParallelSelection is the result of SelectParallel.
type ParallelSelection struct {
	// Outcome is Next when Start holds tasks, Busy when tasks are active
	// and none may start now, Complete or Stuck.
	Outcome Outcome
	Start   []*Task // the tasks to start now, in file order
	// Adopt are the tasks the plan says are in progress that the caller did
	// not name as active: every one of them is active (§5.1 rule 1), so the
	// caller picks them up.
	Adopt   []*Task
	Waiting []Waiting // Stuck: every unfinished task with its unmet deps
}

// SelectParallel applies the parallel rule of SPEC §5.1 to the phase: with
// active the IDs of the tasks the run drives, it returns the tasks to start
// now, in file order, as many as fit under r.Max, the rank caps, the Groups
// (§3.8) and the owner tasks' exclusivity. in limits the selection to a
// slice (§5.5); nil accepts every task. The phase is Complete when every
// accepted task is satisfied and none is active, Stuck when none is active,
// none may start and some are not satisfied, and Busy when tasks are active
// and nothing more starts now.
func (p *Plan) SelectParallel(phaseID string, in func(*Task) bool, active []string, r ParallelRules) (ParallelSelection, error) {
	ph := p.Phase(phaseID)
	if ph == nil {
		return ParallelSelection{}, p.unknownPhase(phaseID)
	}
	var sel ParallelSelection
	// The active tasks of this phase: the ones named, plus every task in
	// progress (rule 1: resume), which the caller adopts.
	running := map[string]bool{}
	for _, id := range active {
		if t := p.Task(id); t != nil && t.Phase == ph {
			running[id] = true
		}
	}
	var act []*Task
	for _, t := range ph.Tasks {
		if t.Status == InProgress && !running[t.ID] {
			running[t.ID] = true
			sel.Adopt = append(sel.Adopt, t)
		}
		if running[t.ID] {
			act = append(act, t)
		}
	}
	accepted := func(t *Task) bool { return in == nil || in(t) }
	ownerActive := func() bool {
		for _, t := range act {
			if isOwnerTask(t) {
				return true
			}
		}
		return false
	}
	// Rule 2: the candidates in file order, each held by the caps, the
	// Groups and (rule 3) the owner tasks.
	for _, t := range ph.Tasks {
		if len(act) >= r.Max || (r.ExclusiveOwner && ownerActive()) {
			break
		}
		if !accepted(t) || t.Status.Satisfied() || running[t.ID] {
			continue
		}
		if len(p.unmet(t)) > 0 {
			continue
		}
		if r.ExclusiveOwner && isOwnerTask(t) {
			if len(act) > 0 {
				break // it waits for them, and holds the rest back (rule 3)
			}
		} else if !mayStart(t, act, r) {
			continue
		}
		act = append(act, t)
		running[t.ID] = true
		sel.Start = append(sel.Start, t)
	}
	switch {
	case len(sel.Start) > 0:
		sel.Outcome = Next
	case len(act) > 0:
		sel.Outcome = Busy
	default:
		// Nothing is active and nothing started: every unsatisfied accepted
		// task waits on a dependency.
		for _, t := range ph.Tasks {
			if !accepted(t) || t.Status.Satisfied() {
				continue
			}
			sel.Waiting = append(sel.Waiting, Waiting{Task: t, Unmet: p.unmet(t), plan: p})
		}
		sel.Outcome = Complete
		if len(sel.Waiting) > 0 {
			sel.Outcome = Stuck
		}
	}
	return sel, nil
}

// mayStart says whether candidate t may start beside the active tasks
// under the rank caps and the Groups.
func mayStart(t *Task, active []*Task, r ParallelRules) bool {
	cap, capped := r.RankCaps[t.Rank]
	sameRank := 0
	for _, a := range active {
		if t.Group != "" && strings.EqualFold(a.Group, t.Group) {
			return false
		}
		if a.Rank == t.Rank {
			sameRank++
		}
	}
	return !capped || sameRank < cap
}

// isOwnerTask reports whether t needs the owner: a user or agent + user
// task (gates included), which §5.1 runs alone by default.
func isOwnerTask(t *Task) bool { return t.Owner != OwnerAgent }
