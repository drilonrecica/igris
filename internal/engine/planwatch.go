package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
)

// maxPlanChanges bounds how many changed cells one report lists.
const maxPlanChanges = 8

// rowSig is what igris reads from a task row. A change to it that the run
// did not write itself (a session editing later tasks, SPEC §5.4) is
// reported and holds the run.
type rowSig struct {
	status                  plan.Status
	rank, mode, owner, deps string
	text                    string
}

func sigOf(t *plan.Task) rowSig {
	return rowSig{status: t.Status, rank: t.Rank, mode: t.Mode, owner: string(t.Owner), deps: t.DepsText, text: t.Text}
}

// planWatch remembers every row as igris last saw or wrote it.
type planWatch struct {
	rows  map[string]rowSig
	order []string // task IDs in file order
}

// reset takes p as the new baseline.
func (w *planWatch) reset(p *plan.Plan) {
	w.rows = make(map[string]rowSig, len(p.Tasks))
	w.order = w.order[:0]
	for _, t := range p.Tasks {
		w.rows[t.ID] = sigOf(t)
		w.order = append(w.order, t.ID)
	}
}

// wrote records the Status cells the run changed itself.
func (w *planWatch) wrote(changes []plan.Change) {
	for _, c := range changes {
		if r, ok := w.rows[c.ID]; ok {
			r.status = c.To
			w.rows[c.ID] = r
		}
	}
}

// diff describes every row of p that differs from the baseline, in file
// order, and makes p the new baseline. Nothing is returned for a plan that
// matches.
func (w *planWatch) diff(p *plan.Plan) []string {
	if w.rows == nil {
		w.reset(p)
		return nil
	}
	var out []string
	seen := make(map[string]bool, len(p.Tasks))
	for _, t := range p.Tasks {
		seen[t.ID] = true
		old, ok := w.rows[t.ID]
		if !ok {
			out = append(out, t.ID+" added")
			continue
		}
		out = append(out, rowChanges(t.ID, old, sigOf(t))...)
	}
	for _, id := range w.order {
		if !seen[id] {
			out = append(out, id+" removed")
		}
	}
	if len(out) > 0 {
		w.reset(p)
	}
	return out
}

// rowChanges lists the cells of task id that differ between a and b.
func rowChanges(id string, a, b rowSig) []string {
	var out []string
	cell := func(name, from, to string) {
		if from != to {
			out = append(out, fmt.Sprintf("%s %s %s → %s", id, name, orDash(from), orDash(to)))
		}
	}
	cell("Status", a.status.String(), b.status.String())
	cell("Model", a.rank, b.rank)
	cell("Mode", a.mode, b.mode)
	cell("Owner", a.owner, b.owner)
	cell("Deps", a.deps, b.deps)
	if a.text != b.text {
		out = append(out, id+" Task text changed")
	}
	return out
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// checkPlan compares p with the rows igris last saw. A change the run did
// not write is reported once and holds the run: pause goes on and nothing
// is selected until the owner, who alone can tell their edit from a
// session's, looks and resumes (SPEC §5.4).
func (e *Engine) checkPlan(ctx context.Context, p *plan.Plan) {
	changes := e.plans.diff(p)
	if len(changes) == 0 {
		return
	}
	list := changes
	more := ""
	if len(list) > maxPlanChanges {
		list, more = list[:maxPlanChanges], fmt.Sprintf(" and %d more", len(changes)-maxPlanChanges)
	}
	e.emit(Event{Kind: PlanChanged, Detail: "the plan changed outside igris: " + strings.Join(list, "; ") + more + " — igris paused; check the plan, then resume"})
	e.hold = true
	if !e.pause {
		e.pause = true
		e.emit(Event{Kind: PauseOn})
	}
	e.toast(ctx, notifyNeedsInput, "the plan changed outside igris; paused")
}
