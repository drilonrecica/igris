package report

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// SinceLastRun is the plan compared with the baseline the last run left
// (`igris status --since-last-run`, SPEC §5.4, §14): the Status cells
// igris wrote since, as the run log tells them, then every other edit.
type SinceLastRun struct {
	// Baseline says there is one: false when .igris/plan-cells.json is
	// missing or not igris's (Why says how); the lists are then empty.
	Baseline bool   `json:"baseline"`
	Run      string `json:"run,omitempty"`
	// At is when the baseline was written; nil without one.
	At     *time.Time   `json:"at,omitempty"`
	Igris  []StatusEdit `json:"igris"`
	Others []PlanEdit   `json:"others"`
	Why    string       `json:"-"`
}

// StatusEdit is a Status cell igris wrote: the task's first status since
// the baseline and its last.
type StatusEdit struct {
	Task string `json:"task"`
	From string `json:"from"`
	To   string `json:"to"`
}

// PlanEdit is a change igris didn't make: "Mode — → auto", "Task text
// changed", "added", "removed".
type PlanEdit struct {
	Task string `json:"task"`
	What string `json:"what"`
}

// EditedRows counts the rows others edited.
func (s SinceLastRun) EditedRows() int {
	seen := map[string]bool{}
	for _, e := range s.Others {
		seen[e.Task] = true
	}
	return len(seen)
}

// Empty says nothing changed since the baseline.
func (s SinceLastRun) Empty() bool { return len(s.Igris) == 0 && len(s.Others) == 0 }

// NewSinceLastRun compares p with base (ok false: no baseline, why says
// why), crediting igris with the Status writes the run log's events at or
// after the baseline record: task_started, task_done, task_skipped and
// task_reset. The ready/blocked cells igris recomputes itself (SPEC §5.2)
// are never reported.
func NewSinceLastRun(base state.PlanCells, ok bool, why string, events []state.Event, p *plan.Plan) SinceLastRun {
	out := SinceLastRun{Igris: []StatusEdit{}, Others: []PlanEdit{}, Why: textsafe.Line(why)}
	if !ok {
		return out
	}
	at := base.At
	out.Baseline, out.Run, out.At = true, textsafe.Line(base.Run), &at
	now := state.CellsOf(p)

	expected := maps.Clone(base.Tasks)
	writes := map[string]*StatusEdit{}
	var order []string
	for _, e := range events {
		if e.At.Before(base.At) || !plan.ValidID(e.Task) {
			continue
		}
		from, to, isWrite := impliedWrite(e, now[e.Task])
		if !isWrite {
			continue
		}
		w := writes[e.Task]
		if w == nil {
			if c, ok := expected[e.Task]; ok {
				from = c.Status
			}
			w = &StatusEdit{Task: e.Task, From: from}
			writes[e.Task] = w
			order = append(order, e.Task)
		}
		w.To = to
		if c, ok := expected[e.Task]; ok {
			c.Status = to
			expected[e.Task] = c
		}
	}
	for _, id := range order {
		if w := writes[id]; w.From != w.To {
			out.Igris = append(out.Igris, StatusEdit{Task: textsafe.Line(id), From: w.From, To: w.To})
		}
	}

	for _, t := range p.Tasks {
		old, ok := expected[t.ID]
		if !ok {
			out.Others = append(out.Others, PlanEdit{Task: textsafe.Line(t.ID), What: "added"})
			continue
		}
		for _, what := range cellEdits(old, now[t.ID]) {
			out.Others = append(out.Others, PlanEdit{Task: textsafe.Line(t.ID), What: what})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(expected)) {
		if p.Task(id) == nil {
			out.Others = append(out.Others, PlanEdit{Task: textsafe.Line(id), What: "removed"})
		}
	}
	return out
}

// impliedWrite is the Status cell event e says igris wrote: from as it was
// (when the baseline doesn't know it) and to. cur is the task's cells now:
// a reset writes ready or blocked, whichever the plan says.
func impliedWrite(e state.Event, cur state.PlanCell) (from, to string, ok bool) {
	switch e.Type {
	case state.EventTaskStarted:
		return plan.Ready.String(), plan.InProgress.String(), true
	case state.EventTaskDone:
		return plan.InProgress.String(), plan.Done.String(), true
	case state.EventTaskSkipped:
		return plan.InProgress.String(), plan.Skipped.String(), true
	case state.EventTaskReset:
		from = "—"
		if s, suffix, ok := plan.ParseStatus(e.Detail); ok && suffix == "" {
			from = s.String()
		}
		to = plan.Ready.String()
		if cur.Status == plan.Blocked.String() {
			to = cur.Status
		}
		return from, to, true
	}
	return "", "", false
}

// cellEdits lists the cells of a row that differ between a and b, as
// planwatch writes them (SPEC §5.4).
func cellEdits(a, b state.PlanCell) []string {
	var out []string
	cell := func(name, from, to string) {
		if from != to {
			out = append(out, fmt.Sprintf("%s %s → %s", name, dash(from), dash(to)))
		}
	}
	if !recomputed(a.Status, b.Status) {
		cell("Status", a.Status, b.Status)
	}
	cell("Model", a.Rank, b.Rank)
	cell("Mode", a.Mode, b.Mode)
	cell("Owner", a.Owner, b.Owner)
	cell("Deps", a.Deps, b.Deps)
	cell("Verify", a.Verify, b.Verify)
	if a.TextSHA256 != b.TextSHA256 {
		out = append(out, "Task text changed")
	}
	return out
}

// recomputed says a change between a and b is one igris makes itself:
// ready and blocked follow the dependencies (SPEC §5.2).
func recomputed(a, b string) bool {
	rb := func(s string) bool { return s == plan.Ready.String() || s == plan.Blocked.String() }
	return rb(a) && rb(b)
}

func dash(s string) string {
	if s = textsafe.Line(strings.TrimSpace(s)); s == "" {
		return "—"
	}
	return s
}
