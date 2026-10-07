package adapt

import (
	"fmt"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
)

// Review is what the owner reviews before accepting a proposal (SPEC §9.5).
// When both files have task tables they are compared table by table, so
// reordered or renamed columns don't mark every row changed; otherwise
// Prose is a line diff of the whole files.
type Review struct {
	// Tables reports that the task tables were compared by phase, task ID
	// and column name. False means Sections is empty and Prose diffs the
	// whole files.
	Tables bool
	// Sections are the changed phases: the proposal's in its order, then
	// the phases it dropped.
	Sections []Section
	// Prose is a line diff of the text outside the task tables (headings
	// of phases with a task table and the tables themselves left out).
	// Its line numbers count only those lines.
	Prose []Line
}

// Section is one phase and what changed in its task table.
type Section struct {
	Op      Op     // Add for a new phase, Del for a dropped one, Mod otherwise
	Phase   string // phase ID
	Heading string // the heading text (the proposal's, unless dropped)
	Changes []Change
}

// Change is one change in a task table.
type Change struct {
	Op   Op     // Add, Del or Mod
	What string // what changed: "M2-04", "M2-04 Deps", "column", "columns reordered"
	// Old and New are the value before and after; an Add has only New, a
	// Del only Old.
	Old, New string
}

// String is the change as one line: `M2-04 Deps: "M2-01" → "M2-01, M2-03"`,
// or `M2-05 Parse widgets` for an added or removed task.
func (c Change) String() string {
	switch c.Op {
	case Add:
		return c.What + " " + c.New
	case Del:
		return c.What + " " + c.Old
	}
	return fmt.Sprintf("%s: %s → %s", c.What, Quote(c.Old), Quote(c.New))
}

// Quote marks off a changed value, so an empty one stays visible.
func Quote(s string) string { return `"` + s + `"` }

// Summary counts the changes for the review's title, e.g.
// "4 table change(s) · +2 −1 lines outside the tables".
func (r Review) Summary() string {
	added, removed := Counts(r.Prose)
	if !r.Tables {
		return fmt.Sprintf("+%d −%d lines", added, removed)
	}
	n := 0
	for _, s := range r.Sections {
		n += len(s.Changes)
		if s.Op != Mod {
			n++
		}
	}
	return fmt.Sprintf("%d table change(s) · +%d −%d lines outside the tables", n, added, removed)
}

// LineReview is the review of two files as a plain line diff.
func LineReview(original, proposed []byte) Review {
	return Review{Prose: Diff(Lines(original), Lines(proposed))}
}

// Compare builds the review of a proposal. Both files are parsed with opts
// (the config's [columns] aliases); phases are matched by ID, tasks by ID
// and cells by column name. It falls back to LineReview when either file
// has no task table or a task without a unique ID to match it by.
func Compare(original, proposed []byte, opts plan.Options) Review {
	a := plan.Parse("original", original, opts)
	b := plan.Parse("proposal", proposed, opts)
	if !comparable(a) || !comparable(b) {
		return LineReview(original, proposed)
	}
	c := comparison{a: a, b: b, match: matchPhases(a, b)}
	r := Review{Tables: true, Prose: Diff(prose(a, original), prose(b, proposed))}
	matched := map[*plan.Phase]bool{}
	for _, pb := range b.Phases {
		s := c.phase(pb)
		if pa := c.match[pb]; pa != nil {
			matched[pa] = true
		}
		if s.Op != Mod || len(s.Changes) > 0 {
			r.Sections = append(r.Sections, s)
		}
	}
	for _, pa := range a.Phases {
		if !matched[pa] {
			r.Sections = append(r.Sections, Section{Op: Del, Phase: pa.ID, Heading: pa.Heading, Changes: c.removed(pa)})
		}
	}
	return r
}

// comparable reports whether p has task tables whose every task has an
// ID of its own.
func comparable(p *plan.Plan) bool {
	if len(p.Phases) == 0 {
		return false
	}
	for _, t := range p.Tasks {
		if t.ID == "" || p.Task(t.ID) != t {
			return false
		}
	}
	return true
}

// matchPhases pairs each proposal phase with the original phase it comes
// from: the one with the same ID, else the unpaired phase most of its
// tasks come from, if most of that phase's tasks go to it (a phase whose
// heading the proposal rewrote, e.g. "Milestone 1" to "M1 — Setup").
func matchPhases(a, b *plan.Plan) map[*plan.Phase]*plan.Phase {
	match := map[*plan.Phase]*plan.Phase{}
	used := map[*plan.Phase]bool{}
	for _, pb := range b.Phases {
		if pa := a.Phase(pb.ID); pa != nil {
			match[pb], used[pa] = pa, true
		}
	}
	for _, pb := range b.Phases {
		if match[pb] != nil {
			continue
		}
		pa := mostOf(pb.Tasks, a)
		if pa != nil && !used[pa] && mostOf(pa.Tasks, b) == pb {
			match[pb], used[pa] = pa, true
		}
	}
	return match
}

// mostOf returns the phase of other that holds most of tasks (the first
// such phase on a tie), or nil if it holds none of them.
func mostOf(tasks []*plan.Task, other *plan.Plan) *plan.Phase {
	count := map[*plan.Phase]int{}
	var best *plan.Phase
	for _, t := range tasks {
		if o := other.Task(t.ID); o != nil {
			count[o.Phase]++
			if best == nil || count[o.Phase] > count[best] {
				best = o.Phase
			}
		}
	}
	return best
}

// comparison compares the original a with the proposal b.
type comparison struct {
	a, b  *plan.Plan
	match map[*plan.Phase]*plan.Phase // proposal phase -> original phase
}

// phase compares proposal phase pb with the original phase it comes from.
func (c comparison) phase(pb *plan.Phase) Section {
	s := Section{Op: Mod, Phase: pb.ID, Heading: pb.Heading}
	pa := c.match[pb]
	if pa == nil {
		s.Op = Add
	} else {
		if pa.Heading != pb.Heading {
			s.Changes = append(s.Changes, Change{Op: Mod, What: "heading", Old: pa.Heading, New: pb.Heading})
		}
		s.Changes = append(s.Changes, columns(pa, pb)...)
		if ia, ib := order(pa, pb, c.b), order(pb, pa, c.a); !slices.Equal(ia, ib) {
			s.Changes = append(s.Changes, Change{Op: Mod, What: "task order", Old: strings.Join(ia, ", "), New: strings.Join(ib, ", ")})
		}
	}
	for _, tb := range pb.Tasks {
		ta := c.a.Task(tb.ID)
		if ta == nil {
			s.Changes = append(s.Changes, Change{Op: Add, What: tb.ID, New: tb.Title})
			continue
		}
		if ta.Phase != pa {
			s.Changes = append(s.Changes, Change{Op: Mod, What: tb.ID + " phase", Old: ta.Phase.ID, New: pb.ID})
		}
		s.Changes = append(s.Changes, cells(ta, tb)...)
	}
	if pa != nil {
		s.Changes = append(s.Changes, c.removed(pa)...)
	}
	return s
}

// removed lists the tasks of original phase pa the proposal dropped.
// Tasks it moved to another phase are shown there.
func (c comparison) removed(pa *plan.Phase) []Change {
	var out []Change
	for _, ta := range pa.Tasks {
		if c.b.Task(ta.ID) == nil {
			out = append(out, Change{Op: Del, What: ta.ID, Old: ta.Title})
		}
	}
	return out
}

// order returns the IDs of the tasks of ph that are in phase to of plan
// other too, in the order of ph.
func order(ph, to *plan.Phase, other *plan.Plan) []string {
	var ids []string
	for _, t := range ph.Tasks {
		if o := other.Task(t.ID); o != nil && o.Phase == to {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// colKey is how columns are matched: by name, case-insensitively.
func colKey(name string) string { return strings.ToLower(name) }

// columns reports columns the proposal renamed, removed, added or put in
// another order, one line each.
func columns(pa, pb *plan.Phase) []Change {
	inA, inB := map[string]int{}, map[string]int{}
	for i, c := range pa.Columns {
		inA[colKey(c)] = i
	}
	for i, c := range pb.Columns {
		inB[colKey(c)] = i
	}
	var out []Change
	var shared []string
	for i, c := range pb.Columns {
		j, ok := inA[colKey(c)]
		if !ok {
			out = append(out, Change{Op: Add, What: "column", New: pb.Header[i]})
			continue
		}
		shared = append(shared, colKey(c))
		if pa.Header[j] != pb.Header[i] {
			out = append(out, Change{Op: Mod, What: "column renamed", Old: pa.Header[j], New: pb.Header[i]})
		}
	}
	var sharedA []string
	for i, c := range pa.Columns {
		if _, ok := inB[colKey(c)]; !ok {
			out = append(out, Change{Op: Del, What: "column", Old: pa.Header[i]})
		} else {
			sharedA = append(sharedA, colKey(c))
		}
	}
	if !slices.Equal(shared, sharedA) {
		out = append(out, Change{Op: Mod, What: "columns reordered", Old: strings.Join(pa.Header, ", "), New: strings.Join(pb.Header, ", ")})
	}
	return out
}

// cells reports the cells of task tb that differ from those of ta, by
// column name in the proposal's column order, then the columns only the
// original has. A missing column counts as an empty cell.
func cells(ta, tb *plan.Task) []Change {
	type col struct{ name, a, b string }
	var cols []col
	at := map[string]int{}
	for i, name := range tb.Phase.Columns {
		at[colKey(name)] = len(cols)
		cols = append(cols, col{name: name, b: tb.Cells[i]})
	}
	for i, name := range ta.Phase.Columns {
		if j, ok := at[colKey(name)]; ok {
			cols[j].a = ta.Cells[i]
		} else {
			cols = append(cols, col{name: name, a: ta.Cells[i]})
		}
	}
	var out []Change
	for _, c := range cols {
		if c.a != c.b && c.name != plan.ColID {
			out = append(out, Change{Op: Mod, What: tb.ID + " " + c.name, Old: c.a, New: c.b})
		}
	}
	return out
}

// prose returns the lines of data outside p's task tables: everything but
// the headings of phases with a task table and the tables themselves. The
// blank lines around what is left out fold into one.
func prose(p *plan.Plan, data []byte) []string {
	skip := map[int]bool{}
	for _, ph := range p.Phases {
		skip[ph.Line] = true
		first, last := ph.TableLines()
		for n := first; n <= last; n++ {
			skip[n] = true
		}
	}
	var out []string
	for i, l := range Lines(data) {
		if skip[i+1] {
			continue
		}
		if strings.TrimSpace(l) == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue // a blank line after a blank line
		}
		out = append(out, l)
	}
	return out
}
