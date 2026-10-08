package plan

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Lint hint names (SPEC §14 `check --strict`).
const (
	LintTitle     = "title"
	LintLong      = "long"
	LintOwnerStep = "owner-step"
	LintGate      = "gate"
	LintYolo      = "yolo"
	LintFable     = "fable"
)

// lintLongRunes is the longest Task cell that is not a `long` hint.
const lintLongRunes = 400

// ownerWords are the words (lower-case prefixes) that say what the owner
// does in an `agent + user` row.
var ownerWords = []string{"owner", "approv", "decid", "decision"}

// Lint is a lint hint: a valid plan that works but could be clearer.
type Lint struct {
	Name string // one of the Lint* names
	Issue
}

// Lint returns the lint hints of a valid plan (SPEC §14 `check --strict`),
// sorted by line. Done and skipped tasks are not linted. Only `check
// --strict` reports them.
func (p *Plan) Lint() []Lint {
	var out []Lint
	add := func(t *Task, name, format string, a ...any) {
		out = append(out, Lint{Name: name, Issue: Issue{File: p.Path, Line: t.Line, Msg: fmt.Sprintf(format, a...)}})
	}
	for _, t := range p.Tasks {
		if t.Status.Satisfied() {
			continue
		}
		if msg := titleHint(t); msg != "" {
			add(t, LintTitle, "%s: %s; start the cell with **Title**", t.ID, msg)
		}
		if n := utf8.RuneCountInString(t.Text); n > lintLongRunes {
			add(t, LintLong, "%s: the Task cell is %d characters (over %d); keep the row short and point to a spec for the details", t.ID, n, lintLongRunes)
		}
		if t.Owner == OwnerAgentUser && !mentionsOwner(t) {
			add(t, LintOwnerStep, `%s: agent + user task, but its row never says what the owner does (no "owner", "approve" or "decide"); say what needs the owner's sign-off`, t.ID)
		}
		if strings.HasSuffix(strings.ToUpper(t.ID), "-G") && t.Phase != nil {
			if missing, first, last := p.gateMissing(t); len(missing) > 0 {
				add(t, LintGate, "%s: the gate does not depend on %s of phase %s; add them to Deps (e.g. %s…%s)", t.ID, strings.Join(missing, ", "), t.Phase.ID, first, last)
			}
		}
		if t.Mode == "yolo" {
			add(t, LintYolo, "%s: Mode yolo runs this task with --dangerously-skip-permissions; prefer auto unless it must run unattended", t.ID)
		}
		if strings.EqualFold(t.Rank, "fable") && t.Phase != nil && onlyHeavy(t) {
			add(t, LintFable, "%s: the only heavy-rank task of phase %s is fable; check that this task needs fable", t.ID, t.Phase.ID)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// titleHint says what igris shows as t's title when its Task cell has no
// **bold** title, "" when it has one or its table has no Task column.
func titleHint(t *Task) string {
	switch {
	case hasBold(t.Text) || !hasCol(t.Phase.Columns, ColTask):
		return ""
	case strings.TrimSpace(t.Text) == "":
		return "the Task cell is empty, so the task has no title"
	case utf8.RuneCountInString(strings.TrimSpace(t.Text)) <= titleMaxRunes:
		return "the Task cell has no **bold** title, so igris shows the whole cell as its title"
	}
	return fmt.Sprintf("the Task cell has no **bold** title, so igris shows its first %d characters", titleMaxRunes)
}

// hasBold reports whether text has a non-empty **bold** span, the title.
func hasBold(text string) bool {
	_, rest, ok := strings.Cut(text, "**")
	if !ok {
		return false
	}
	bold, _, ok := strings.Cut(rest, "**")
	return ok && strings.TrimSpace(bold) != ""
}

// mentionsOwner reports whether any cell of t's row says what the owner
// does.
func mentionsOwner(t *Task) bool {
	row := strings.ToLower(strings.Join(t.Cells, " | "))
	for _, w := range ownerWords {
		if strings.Contains(row, w) {
			return true
		}
	}
	return false
}

// gateMissing returns the other tasks of the gate's phase it doesn't
// depend on, directly or through other deps, in file order, and the first
// and last other task of the phase (for the suggested range). Tasks that
// depend on the gate themselves (a release after it) are not counted.
func (p *Plan) gateMissing(gate *Task) (missing []string, first, last string) {
	reach := p.reach(gate)
	for _, t := range gate.Phase.Tasks {
		if t == gate || p.reach(t)[gate.ID] {
			continue
		}
		if first == "" {
			first = t.ID
		}
		last = t.ID
		if !reach[t.ID] {
			missing = append(missing, t.ID)
		}
	}
	return missing, first, last
}

// reach is every task ID t depends on, directly or through other deps.
func (p *Plan) reach(t *Task) map[string]bool {
	reach := map[string]bool{}
	var walk func(t *Task)
	walk = func(t *Task) {
		for _, id := range t.Deps {
			if reach[id] {
				continue
			}
			reach[id] = true
			if d := p.Task(id); d != nil {
				walk(d)
			}
		}
	}
	walk(t)
	return reach
}

// onlyHeavy reports whether no other task of t's phase has rank opus or
// fable, as written.
func onlyHeavy(t *Task) bool {
	for _, o := range t.Phase.Tasks {
		if o != t && (strings.EqualFold(o.Rank, "opus") || strings.EqualFold(o.Rank, "fable")) {
			return false
		}
	}
	return true
}
