package report

import (
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// statuses lists the statuses in the order the reports count them.
var statuses = []plan.Status{plan.Ready, plan.Blocked, plan.InProgress, plan.Done, plan.Skipped}

// Phase is one phase and its task counts per status.
type Phase struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

// PhasesReport is the result of `igris phases`.
type PhasesReport struct {
	Phases []Phase `json:"phases"`
	Plan   string  `json:"plan"`
}

// Phases lists the phases of a valid plan.
func Phases(p *plan.Plan) PhasesReport {
	r := PhasesReport{Phases: make([]Phase, len(p.Phases)), Plan: textsafe.Line(p.Path)}
	for i, ph := range p.Phases {
		r.Phases[i] = phaseOf(ph)
	}
	return r
}

func phaseOf(ph *plan.Phase) Phase {
	return Phase{ID: textsafe.Line(ph.ID), Title: textsafe.Line(ph.Title), Total: len(ph.Tasks), Counts: countStatuses(ph)}
}

func countStatuses(ph *plan.Phase) map[string]int {
	counts := make(map[string]int, len(statuses))
	for _, s := range statuses {
		counts[s.String()] = 0
	}
	for _, t := range ph.Tasks {
		counts[t.Status.String()]++
	}
	return counts
}

// Wait is a dependency a task still waits on.
type Wait struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
	Phase  string `json:"phase,omitempty"`
}

// Task is one task row of `igris status`.
type Task struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Rank    string `json:"rank"`
	Owner   string `json:"owner"`
	Mode    string `json:"mode"`
	WaitsOn []Wait `json:"waits_on"`
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
	Phases []PhaseStatus `json:"phases"`
	Plan   string        `json:"plan"`
	Run    *RunInfo      `json:"run,omitempty"` // set by the caller; nil when there is no run
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
	r := StatusReport{Phases: make([]PhaseStatus, len(phases)), Plan: textsafe.Line(p.Path)}
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
		Rank: orDash(textsafe.Line(t.Rank)), Owner: textsafe.Line(string(t.Owner)), Mode: orDash(textsafe.Line(t.Mode)),
		WaitsOn: []Wait{},
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

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
