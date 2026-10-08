package engine

import (
	"fmt"
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Range is the phases a run walks and, with a selection, the slice of their
// tasks it runs (SPEC §5.3, §5.5).
type Range struct {
	Phases []*plan.Phase
	// Through is the last phase as state.json records it: --through as
	// given, or the last phase a selection's range was derived to; "" for a
	// single phase.
	Through   string
	Selection state.Selection
	// Slice holds the IDs of the tasks the run may select; nil runs every
	// task of Phases.
	Slice map[string]bool
}

// In says whether t belongs to the run's slice.
func (r Range) In(t *plan.Task) bool { return r.Slice == nil || r.Slice[t.ID] }

// Scope names the range as run_started does: "phase A, B", plus the
// selection ("phase M1; only M1-03, M1-05").
func (r Range) Scope() string {
	ids := make([]string, len(r.Phases))
	for i, ph := range r.Phases {
		ids[i] = ph.ID
	}
	scope := "phase " + strings.Join(ids, ", ")
	if !r.Selection.Empty() {
		scope += "; " + r.Selection.String()
	}
	return scope
}

// ResolveRange works out what a run of phase (through `through`) limited to
// sel covers (SPEC §5.5). With no phase and a selection, the range runs from
// the phase of the earliest named task through `through`, else through the
// phase of the latest. It fails, saying what to change, when a named ID is
// not in the plan or outside the range, or --from comes after --until.
func ResolveRange(p *plan.Plan, phase, through string, sel state.Selection) (Range, error) {
	return resolveRange(p, phase, through, sel, "")
}

// resolveRange is ResolveRange with origin before each flag in the errors:
// "" for the command line, "the last run's " for a selection resumed from
// state.json. The IDs are cleaned for the terminal either way.
func resolveRange(p *plan.Plan, phase, through string, sel state.Selection, origin string) (Range, error) {
	type named struct{ flag, id string }
	var names []named
	for _, id := range sel.Only {
		names = append(names, named{origin + "--only", id})
	}
	if sel.From != "" {
		names = append(names, named{origin + "--from", sel.From})
	}
	if sel.Until != "" {
		names = append(names, named{origin + "--until", sel.Until})
	}
	index := make(map[string]int, len(p.Tasks))
	for i, t := range p.Tasks {
		if _, dup := index[t.ID]; !dup {
			index[t.ID] = i
		}
	}
	first, last := -1, -1
	for _, n := range names {
		i, ok := index[n.id]
		if !ok {
			return Range{}, fmt.Errorf("%s %s is not a task in %s; check the ID with `igris status`", n.flag, textsafe.Line(n.id), p.Path)
		}
		if first < 0 || i < first {
			first = i
		}
		if i > last {
			last = i
		}
	}

	if phase == "" {
		if len(names) == 0 {
			return Range{}, fmt.Errorf("no phase to run; name one, e.g. `igris arise %s`", firstPhase(p))
		}
		phase = p.Tasks[first].Phase.ID
		if through == "" && p.Tasks[last].Phase.ID != phase {
			through = p.Tasks[last].Phase.ID
		}
	}
	phases, err := p.PhasesThrough(phase, through)
	if err != nil {
		return Range{}, err
	}
	r := Range{Phases: phases, Through: through, Selection: sel}
	if sel.Empty() {
		return r, nil
	}

	inRange := map[*plan.Phase]bool{}
	for _, ph := range phases {
		inRange[ph] = true
	}
	for _, n := range names {
		t := p.Tasks[index[n.id]]
		if inRange[t.Phase] {
			continue
		}
		hint := "widen --through or drop it"
		if phaseIndex(p, t.Phase) < phaseIndex(p, phases[0]) {
			hint = "start at an earlier phase or drop it"
		}
		return Range{}, fmt.Errorf("%s %s is in phase %s, outside the run's %s; %s", n.flag, textsafe.Line(n.id), t.Phase.ID, phaseSpan(phases), hint)
	}
	if sel.From != "" && sel.Until != "" && index[sel.From] > index[sel.Until] {
		return Range{}, fmt.Errorf("%s--from %s comes after --until %s in the plan; swap them", origin, textsafe.Line(sel.From), textsafe.Line(sel.Until))
	}

	r.Slice = map[string]bool{}
	if len(sel.Only) > 0 {
		for _, id := range sel.Only {
			r.Slice[id] = true
		}
		return r, nil
	}
	in := sel.From == ""
	for _, ph := range phases {
		for _, t := range ph.Tasks {
			if t.ID == sel.From {
				in = true
			}
			if in {
				r.Slice[t.ID] = true
			}
			if t.ID == sel.Until {
				return r, nil
			}
		}
	}
	return r, nil
}

// phaseSpan names a run's phases for an error: "phase M1", "phases M1…M3".
func phaseSpan(phases []*plan.Phase) string {
	if len(phases) == 1 {
		return "phase " + phases[0].ID
	}
	return "phases " + phases[0].ID + "…" + phases[len(phases)-1].ID
}

func phaseIndex(p *plan.Plan, ph *plan.Phase) int {
	for i, q := range p.Phases {
		if q == ph {
			return i
		}
	}
	return -1
}

func firstPhase(p *plan.Plan) string {
	if len(p.Phases) == 0 {
		return "M0"
	}
	return p.Phases[0].ID
}
