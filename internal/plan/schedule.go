package plan

import (
	"fmt"
	"strings"
)

// Change is a status change of one task.
type Change struct {
	ID       string
	From, To Status
}

func (c Change) String() string { return fmt.Sprintf("%s: %s → %s", c.ID, c.From, c.To) }

// unmet returns the deps of t that are not satisfied, unknown IDs included.
func (p *Plan) unmet(t *Task) []string {
	var out []string
	for _, id := range t.Deps {
		if d := p.Task(id); d == nil || !d.Status.Satisfied() {
			out = append(out, id)
		}
	}
	return out
}

// Readiness returns, in file order, every ready/blocked task whose status
// differs from what its dependencies say (SPEC §5.2): ready if all deps are
// satisfied, blocked otherwise. Other statuses are never changed. Before a
// write these differences are drift, which `check` reports as warnings.
func (p *Plan) Readiness() []Change {
	var out []Change
	for _, t := range p.Tasks {
		if t.Status != Ready && t.Status != Blocked {
			continue
		}
		want := Ready
		if len(p.unmet(t)) > 0 {
			want = Blocked
		}
		if t.Status != want {
			out = append(out, Change{ID: t.ID, From: t.Status, To: want})
		}
	}
	return out
}

// WaitingOn returns t with its unmet dependencies, or nil if every
// dependency is satisfied.
func (p *Plan) WaitingOn(t *Task) *Waiting {
	unmet := p.unmet(t)
	if len(unmet) == 0 {
		return nil
	}
	return &Waiting{Task: t, Unmet: unmet, plan: p}
}

// Apply sets the statuses in memory. It fails without changing anything if
// a task does not exist.
func (p *Plan) Apply(changes []Change) error {
	for _, c := range changes {
		if p.Task(c.ID) == nil {
			return fmt.Errorf("task %s is not in the plan %s", c.ID, p.Path)
		}
	}
	for _, c := range changes {
		t := p.Task(c.ID)
		t.Status, t.StatusText, t.Suffix = c.To, c.To.String(), ""
	}
	return nil
}

// Sync sets task id to status in memory, then recomputes readiness for the
// whole plan. It returns all resulting changes (the target first), already
// applied — the set of Status cells to write.
func (p *Plan) Sync(id string, to Status) ([]Change, error) {
	t := p.Task(id)
	if t == nil {
		return nil, fmt.Errorf("task %s is not in the plan %s", id, p.Path)
	}
	var changes []Change
	if t.Status != to {
		changes = append(changes, Change{ID: id, From: t.Status, To: to})
		if err := p.Apply(changes); err != nil {
			return nil, err
		}
	}
	rest := p.Readiness()
	if err := p.Apply(rest); err != nil {
		return nil, err
	}
	return append(changes, rest...), nil
}

// Outcome is the result of selecting in a phase.
type Outcome int

// Selection outcomes (SPEC §5.1).
const (
	Next     Outcome = iota // Selection.Task is the task to run
	Complete                // every task in the phase is satisfied
	Stuck                   // unfinished tasks remain, none can start
)

func (o Outcome) String() string {
	return [...]string{"next", "complete", "stuck"}[o]
}

// Waiting is an unfinished task and the IDs of the dependencies it waits on.
type Waiting struct {
	Task  *Task
	Unmet []string
	plan  *Plan
}

// String explains the wait, e.g. "M0-13 waits on P0-05 (ready, phase P0)".
func (w Waiting) String() string {
	parts := make([]string, len(w.Unmet))
	for i, id := range w.Unmet {
		if d := w.plan.Task(id); d != nil {
			parts[i] = fmt.Sprintf("%s (%s, phase %s)", id, d.Status, d.Phase.ID)
		} else {
			parts[i] = id + " (unknown task)"
		}
	}
	return fmt.Sprintf("%s waits on %s", w.Task.ID, strings.Join(parts, ", "))
}

// Selection is the result of Select.
type Selection struct {
	Outcome Outcome
	Task    *Task     // the task to run (Next)
	Waiting []Waiting // every unfinished task with its unmet deps (Stuck)
}

// Select picks the next task of a phase (SPEC §5.1): the first task in
// progress (resume), else the first unsatisfied task whose dependencies are
// all satisfied. Otherwise the phase is complete or stuck.
func (p *Plan) Select(phaseID string) (Selection, error) { return p.SelectIn(phaseID, nil) }

// SelectIn is Select among the tasks of the phase that in accepts, a run's
// slice (SPEC §5.5); a nil in accepts every task. Complete then means every
// accepted task is satisfied, and Stuck that accepted tasks are left and
// none can start.
func (p *Plan) SelectIn(phaseID string, in func(*Task) bool) (Selection, error) {
	ph := p.Phase(phaseID)
	if ph == nil {
		return Selection{}, p.unknownPhase(phaseID)
	}
	tasks := ph.Tasks
	if in != nil {
		tasks = nil
		for _, t := range ph.Tasks {
			if in(t) {
				tasks = append(tasks, t)
			}
		}
	}
	for _, t := range tasks {
		if t.Status == InProgress {
			return Selection{Outcome: Next, Task: t}, nil
		}
	}
	var waiting []Waiting
	for _, t := range tasks {
		if t.Status.Satisfied() {
			continue
		}
		unmet := p.unmet(t)
		if len(unmet) == 0 {
			return Selection{Outcome: Next, Task: t}, nil
		}
		waiting = append(waiting, Waiting{Task: t, Unmet: unmet, plan: p})
	}
	if len(waiting) == 0 {
		return Selection{Outcome: Complete}, nil
	}
	return Selection{Outcome: Stuck, Waiting: waiting}, nil
}

// PhasesThrough returns the phases from `from` through `through` inclusive,
// in file order (`arise <phase> --through <phase>`, SPEC §5.3). An empty
// through means just `from`. IDs match case-insensitively.
func (p *Plan) PhasesThrough(from, through string) ([]*Phase, error) {
	start := p.phaseIndex(from)
	if start < 0 {
		return nil, p.unknownPhase(from)
	}
	if through == "" {
		return p.Phases[start : start+1], nil
	}
	end := p.phaseIndex(through)
	if end < 0 {
		return nil, p.unknownPhase(through)
	}
	if end < start {
		return nil, fmt.Errorf("--through %s comes before %s in the plan; give a later phase", p.Phases[end].ID, p.Phases[start].ID)
	}
	return p.Phases[start : end+1], nil
}

func (p *Plan) phaseIndex(id string) int {
	for i, ph := range p.Phases {
		if strings.EqualFold(ph.ID, id) {
			return i
		}
	}
	return -1
}

func (p *Plan) unknownPhase(id string) error {
	ids := make([]string, len(p.Phases))
	for i, ph := range p.Phases {
		ids[i] = ph.ID
	}
	return fmt.Errorf("unknown phase %q in %s; phases are: %s", id, p.Path, strings.Join(ids, ", "))
}

// ForceError says a reset needs --force: the task is done or skipped
// (SPEC §14 `reset`).
type ForceError struct {
	ID     string
	Status Status
}

func (e *ForceError) Error() string {
	return fmt.Sprintf("%s is %s; pass --force to reset it", e.ID, e.Status)
}

// Resettable says whether a reset changes t (SPEC §14 `reset`): a task in
// progress is reset, a done or skipped one only with force (else a
// *ForceError), and a ready or blocked one has nothing to reset.
func Resettable(t *Task, force bool) (bool, error) {
	switch {
	case t.Status == InProgress:
		return true, nil
	case t.Status.Satisfied() && !force:
		return false, &ForceError{ID: t.ID, Status: t.Status}
	case t.Status.Satisfied():
		return true, nil
	}
	return false, nil
}

// Reset puts task id back to ready, or blocked if a dependency is unmet
// (SPEC §5.2), and syncs readiness. It returns the changes like Sync, the
// target first, or none when the task has nothing to reset. Tasks in
// progress elsewhere are never changed.
func (p *Plan) Reset(id string, force bool) ([]Change, error) {
	t := p.Task(id)
	if t == nil {
		return nil, fmt.Errorf("task %s is not in the plan %s", id, p.Path)
	}
	if ok, err := Resettable(t, force); !ok || err != nil {
		return nil, err
	}
	to := Ready
	if len(p.unmet(t)) > 0 {
		to = Blocked
	}
	return p.Sync(id, to)
}

// Dependents returns the tasks that list id in their Deps, in file order.
func (p *Plan) Dependents(id string) []*Task {
	var out []*Task
	for _, t := range p.Tasks {
		for _, d := range t.Deps {
			if d == id {
				out = append(out, t)
				break
			}
		}
	}
	return out
}
