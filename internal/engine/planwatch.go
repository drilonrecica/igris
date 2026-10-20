package engine

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// maxPlanChanges bounds how many changed cells one report lists.
const maxPlanChanges = 8

// rowSig is what igris reads from a task row. A change to it that the run
// did not write itself (a session editing later tasks, SPEC §5.4) is
// reported and holds the run.
type rowSig struct {
	status                  plan.Status
	rank, mode, owner, deps string
	verify, timeout, paths  string
	text                    string
	// file is the file the row is in, relative to the main plan's
	// directory (SPEC §3.7).
	file string
}

func sigOf(p *plan.Plan, t *plan.Task) rowSig {
	file := filepath.Base(p.Path)
	if t.Phase != nil && t.Phase.Include != "" {
		file = path.Clean(filepath.ToSlash(t.Phase.Include))
	}
	return rowSig{
		status: t.Status, rank: t.Rank, mode: t.Mode, owner: string(t.Owner), deps: t.DepsText,
		verify: t.Verify, timeout: t.TimeoutText, paths: t.ContextText, text: t.Text, file: file,
	}
}

// planWatch remembers every row as igris last saw or wrote it, in every
// file of the plan.
type planWatch struct {
	rows  map[string]rowSig
	order []string // task IDs in plan order
}

// reset takes p as the new baseline.
func (w *planWatch) reset(p *plan.Plan) {
	w.rows = make(map[string]rowSig, len(p.Tasks))
	w.order = w.order[:0]
	for _, t := range p.Tasks {
		w.rows[t.ID] = sigOf(p, t)
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

// diff describes every row of p that differs from the baseline, in plan
// order, and makes p the new baseline. Nothing is returned for a plan that
// matches. An include added or removed shows as the rows it adds or
// removes.
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
		out = append(out, rowChanges(t.ID, old, sigOf(p, t))...)
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
	// A row moved to another file is a plan edit like any other.
	cell("file", a.file, b.file)
	cell("Status", a.status.String(), b.status.String())
	cell("Model", a.rank, b.rank)
	cell("Mode", a.mode, b.mode)
	cell("Owner", a.owner, b.owner)
	cell("Deps", a.deps, b.deps)
	// A session could turn a later task's verification off, or widen its
	// Timeout, without anyone noticing.
	cell("Verify", a.verify, b.verify)
	cell("Timeout", a.timeout, b.timeout)
	cell("Context", a.paths, b.paths)
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
	held := e.hold
	e.hold = true
	if !e.pause {
		e.pause = true
		e.emit(Event{Kind: PauseOn})
	}
	if !held {
		// Run-wide: the run holds between tasks, from now until the owner
		// resumes; another edit meanwhile is the same wait.
		e.logRun(state.Event{Type: state.EventNeedsYou, Reason: state.ReasonPlanChanged, Detail: "the plan changed outside igris; paused"})
	}
	e.toast(ctx, notifyNeedsInput, "the plan changed outside igris; paused")
}
