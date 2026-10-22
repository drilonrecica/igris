package plan

import (
	"strings"
)

// depsLikeColumns are header names (lower-case) that other plans use for
// dependencies. igris reads dependencies only from the Deps column (or a
// [columns] alias), so such a column would silently mean "no dependencies".
var depsLikeColumns = map[string]bool{
	"depends": true, "depends on": true, "dependencies": true, "dependency": true,
	"dep": true, "requires": true, "prereqs": true, "prerequisites": true,
	"blocked by": true, "needs": true, "after": true,
}

// Hints returns warnings about a valid plan that igris reads differently
// from how it was probably meant, sorted by line: a phase without a Deps
// column whose table has a column that looks like dependencies, a user
// task with a cell that only a session uses, Group cells in a phase that
// isn't parallel, and a parallel phase's task naming an earlier one it
// doesn't depend on (SPEC §3.8). They never make the plan invalid.
func (p *Plan) Hints() []Issue {
	var out []Issue
	for _, ph := range p.Phases {
		if contains(ph.Columns, ColDeps) {
			continue
		}
		for _, c := range ph.Columns {
			if depsLikeColumns[strings.ToLower(c)] {
				out = append(out, Issue{File: ph.File, Line: ph.tableLine, Msg: "column \"" + c + "\" looks like dependencies, but igris reads them only from a Deps column, so every task in phase " + ph.ID + " runs as if it had none; rename the column to Deps or add \"" + c + "\" = \"Deps\" under [columns] in igris.toml"})
				break
			}
		}
	}
	for _, t := range p.Tasks {
		if t.Owner != OwnerUser || t.Status.Satisfied() {
			continue
		}
		for _, c := range []struct{ name, value string }{{ColVerify, t.Verify}, {ColTimeout, t.TimeoutText}, {ColContext, t.ContextText}} {
			if c.value != "" {
				out = append(out, Issue{File: t.Phase.File, Line: t.Line, Msg: t.ID + ": user tasks have no session, so its " + c.name + " is ignored; clear the cell"})
			}
		}
	}
	out = append(out, p.groupHints()...)
	out = append(out, p.impliedDeps()...)
	p.sortIssues(out)
	return out
}

// groupHints warns once per phase without the parallel marker whose
// unfinished tasks have a Group, which only a parallel phase reads.
func (p *Plan) groupHints() []Issue {
	var out []Issue
	for _, ph := range p.Phases {
		if ph.Parallel {
			continue
		}
		for _, t := range ph.Tasks {
			if t.Group != "" && !t.Status.Satisfied() {
				out = append(out, Issue{File: ph.File, Line: ph.tableLine, Msg: "phase " + ph.ID + " is not parallel, so its Group cells change nothing; add <!-- igris:parallel --> under its heading, or clear them"})
				break
			}
		}
	}
	return out
}

// impliedDeps warns about each unfinished task of a parallel phase whose
// Task cell names an earlier unfinished task of the phase when neither
// depends on the other: the file order a sequential run relied on means
// nothing there (SPEC §3.8).
func (p *Plan) impliedDeps() []Issue {
	var out []Issue
	reach := map[*Task]map[string]bool{}
	reachOf := func(t *Task) map[string]bool {
		if reach[t] == nil {
			reach[t] = p.reach(t)
		}
		return reach[t]
	}
	for _, ph := range p.Phases {
		if !ph.Parallel {
			continue
		}
		for i, t := range ph.Tasks {
			if t.Status.Satisfied() || t.ID == "" {
				continue
			}
			for _, e := range ph.Tasks[:i] {
				if e.Status.Satisfied() || e.ID == "" || e.ID == t.ID || !namesID(t.Text, e.ID) || reachOf(t)[e.ID] || reachOf(e)[t.ID] {
					continue
				}
				out = append(out, Issue{File: t.Phase.File, Line: t.Line, Msg: t.ID + " names " + e.ID + ", which comes before it in parallel phase " + ph.ID + ", but neither depends on the other, so they may run at the same time; add " + e.ID + " to its Deps if it must finish first"})
			}
		}
	}
	return out
}

// namesID reports whether text holds id as a whole word: not inside a
// longer ID (M1-021, x.M1-02), though a sentence's full stop may follow.
func namesID(text, id string) bool {
	for at := 0; ; {
		i := strings.Index(text[at:], id)
		if i < 0 {
			return false
		}
		i += at
		j := i + len(id)
		before := i == 0 || !isIDByte(text[i-1])
		after := j == len(text) || text[j] != '.' && !isIDByte(text[j]) ||
			text[j] == '.' && (j+1 == len(text) || !isIDByte(text[j+1]) || text[j+1] == '.')
		if before && after {
			return true
		}
		at = i + 1
	}
}

// isIDByte reports whether b may be part of a task ID (SPEC §3.2).
func isIDByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-'
}
