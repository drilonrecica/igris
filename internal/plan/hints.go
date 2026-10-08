package plan

import (
	"sort"
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
// column whose table has a column that looks like dependencies, and a user
// task with a cell that only a session uses. They never make the plan
// invalid.
func (p *Plan) Hints() []Issue {
	var out []Issue
	for _, ph := range p.Phases {
		if contains(ph.Columns, ColDeps) {
			continue
		}
		for _, c := range ph.Columns {
			if depsLikeColumns[strings.ToLower(c)] {
				out = append(out, Issue{File: p.Path, Line: ph.tableLine, Msg: "column \"" + c + "\" looks like dependencies, but igris reads them only from a Deps column, so every task in phase " + ph.ID + " runs as if it had none; rename the column to Deps or add \"" + c + "\" = \"Deps\" under [columns] in igris.toml"})
				break
			}
		}
	}
	for _, t := range p.Tasks {
		if t.Owner != OwnerUser || t.Status.Satisfied() {
			continue
		}
		for _, c := range []struct{ name, value string }{{ColVerify, t.Verify}} {
			if c.value != "" {
				out = append(out, Issue{File: p.Path, Line: t.Line, Msg: t.ID + ": user tasks have no session, so its " + c.name + " is ignored; clear the cell"})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}
