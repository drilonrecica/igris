package report

import (
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// statuses lists the statuses in the order the reports count them.
var statuses = []plan.Status{plan.Ready, plan.Blocked, plan.InProgress, plan.Done, plan.Skipped}

// Phase is one phase and its task counts per status.
type Phase struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// File is the file the phase is in (SPEC §3.7).
	File   string         `json:"file"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

// Parallelism is how much of a phase's remaining work could run at once
// (SPEC §14 `phases`): read-only, since igris runs one task at a time.
type Parallelism struct {
	// CriticalPath is the longest chain of unfinished tasks, in order.
	CriticalPath []string `json:"critical_path"`
	// Width is the most tasks that could run together; 0 when none is left.
	Width int `json:"width"`
	// Together lists the sets of tasks that would run together, per level
	// of the dependency graph with two or more.
	Together [][]string `json:"together"`
}

// ParallelismOf reports ph's unfinished tasks that in accepts (nil: all).
func ParallelismOf(ph *plan.Phase, in func(*plan.Task) bool) Parallelism {
	a := ph.ParallelismOf(in)
	r := Parallelism{CriticalPath: []string{}, Width: a.Width, Together: [][]string{}}
	for _, id := range a.CriticalPath {
		r.CriticalPath = append(r.CriticalPath, textsafe.Line(id))
	}
	for _, set := range a.Together {
		clean := make([]string, len(set))
		for i, id := range set {
			clean[i] = textsafe.Line(id)
		}
		r.Together = append(r.Together, clean)
	}
	return r
}

// PhaseRow is a phase of `igris phases`: its counts and its parallelism.
type PhaseRow struct {
	Phase
	Parallelism Parallelism `json:"parallelism"`
}

// PhasesReport is the result of `igris phases`.
type PhasesReport struct {
	V int `json:"v"`
	// Files are the plan's files, the main plan first (SPEC §3.7).
	Files  []string   `json:"files"`
	Phases []PhaseRow `json:"phases"`
	Plan   string     `json:"plan"`
}

// Phases lists the phases of a valid plan.
func Phases(p *plan.Plan) PhasesReport {
	r := PhasesReport{V: JSONVersion, Files: files(p), Phases: make([]PhaseRow, len(p.Phases)), Plan: textsafe.Line(p.Path)}
	for i, ph := range p.Phases {
		r.Phases[i] = PhaseRow{Phase: phaseOf(ph), Parallelism: ParallelismOf(ph, nil)}
	}
	return r
}

func phaseOf(ph *plan.Phase) Phase {
	return Phase{ID: textsafe.Line(ph.ID), Title: textsafe.Line(ph.Title), File: textsafe.Line(ph.File), Total: len(ph.Tasks), Counts: countStatuses(ph)}
}

// files lists the plan's files, the main plan first (SPEC §3.7).
func files(p *plan.Plan) []string {
	out := []string{}
	for _, f := range p.Files() {
		out = append(out, textsafe.Line(f))
	}
	return out
}

func countStatuses(ph *plan.Phase) map[string]int {
	counts := make(map[string]int, len(statuses))
	for _, s := range statuses {
		counts[CountKey(s)] = 0
	}
	for _, t := range ph.Tasks {
		counts[CountKey(t.Status)]++
	}
	return counts
}

// CountKey is a status's key in Phase.Counts: its word, with "_" for the
// space (`in_progress`), so every key is a plain JSON name.
func CountKey(s plan.Status) string {
	return strings.ReplaceAll(s.String(), " ", "_")
}

// Wait is a dependency a task still waits on.
type Wait struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
	Phase  string `json:"phase,omitempty"`
}

// Task is one task row of `igris status`.
type Task struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Rank   string `json:"rank,omitempty"` // "" for no model
	Owner  string `json:"owner"`
	Mode   string `json:"mode,omitempty"` // "" for no override
	// Verify, Timeout and Context are the optional cells (SPEC §3.2),
	// empty when not set.
	Verify  string   `json:"verify,omitempty"`
	Timeout string   `json:"timeout,omitempty"`
	Context []string `json:"context,omitempty"`
	WaitsOn []Wait   `json:"waits_on"`
}

// Optional says which optional columns some task of the plan has set:
// `status` shows only those (SPEC §14).
type Optional struct {
	Verify, Timeout, Context bool
}

// PhaseStatus is a phase with its §5.1 outcome and its tasks.
type PhaseStatus struct {
	Phase
	Outcome string `json:"outcome"`
	Next    string `json:"next,omitempty"`
	Tasks   []Task `json:"tasks"`
}

// StatusReport is the result of `igris status`.
type StatusReport struct {
	V int `json:"v"`
	// Files are the plan's files, the main plan first (SPEC §3.7).
	Files  []string      `json:"files"`
	Phases []PhaseStatus `json:"phases"`
	Plan   string        `json:"plan"`
	Run    *RunInfo      `json:"run,omitempty"` // set by the caller; nil when there is no run
	// Optional is computed over every task of the plan, not only the
	// phases shown.
	Optional Optional `json:"-"`
}

// Status reports the phases of a valid plan, or only phaseID if it isn't
// empty. An unknown phase is an error naming the phases there are.
func Status(p *plan.Plan, phaseID string) (StatusReport, error) {
	phases := p.Phases
	if phaseID != "" {
		ph := p.Phase(phaseID)
		if ph == nil {
			_, err := p.Select(phaseID) // builds the "unknown phase" message
			return StatusReport{}, err
		}
		phases = []*plan.Phase{ph}
	}
	r := StatusReport{V: JSONVersion, Files: files(p), Phases: make([]PhaseStatus, len(phases)), Plan: textsafe.Line(p.Path)}
	for _, t := range p.Tasks {
		r.Optional.Verify = r.Optional.Verify || t.Verify != ""
		r.Optional.Timeout = r.Optional.Timeout || t.TimeoutText != ""
		r.Optional.Context = r.Optional.Context || len(t.Context) > 0
	}
	for i, ph := range phases {
		sel, err := p.Select(ph.ID)
		if err != nil {
			return StatusReport{}, err
		}
		s := PhaseStatus{Phase: phaseOf(ph), Outcome: sel.Outcome.String(), Tasks: make([]Task, len(ph.Tasks))}
		if sel.Task != nil {
			s.Next = textsafe.Line(sel.Task.ID)
		}
		for j, t := range ph.Tasks {
			s.Tasks[j] = taskOf(p, t)
		}
		r.Phases[i] = s
	}
	return r, nil
}

func taskOf(p *plan.Plan, t *plan.Task) Task {
	r := Task{
		ID: textsafe.Line(t.ID), Title: textsafe.Line(t.Title), Status: t.Status.String(),
		Rank: textsafe.Line(t.Rank), Owner: textsafe.Line(string(t.Owner)), Mode: textsafe.Line(t.Mode),
		Verify: textsafe.Line(t.Verify), Timeout: textsafe.Line(t.TimeoutText),
		WaitsOn: []Wait{},
	}
	for _, c := range t.Context {
		r.Context = append(r.Context, textsafe.Line(c))
	}
	if w := p.WaitingOn(t); w != nil {
		for _, id := range w.Unmet {
			if d := p.Task(id); d != nil && t.Status != plan.Done && t.Status != plan.Skipped {
				r.WaitsOn = append(r.WaitsOn, Wait{ID: textsafe.Line(id), Status: d.Status.String(), Phase: textsafe.Line(d.Phase.ID)})
			}
		}
	}
	return r
}
