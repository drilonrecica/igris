package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
)

// glyphs are the status glyphs of SPEC §15.4.
var glyphs = map[plan.Status]string{
	plan.Done:       "✓",
	plan.InProgress: "●",
	plan.Ready:      "·",
	plan.Blocked:    "⨯",
	plan.Skipped:    "–",
}

// rows is what task rows, task details and the phase summary are drawn
// from: the plan, the phase shown, the current task, the owner's per-task
// modes and the theme. The run view and the home screen's phase pages share
// them, so neither needs the run model.
type rows struct {
	plan  *plan.Plan // nil until loaded
	phase string
	// curID is the task the run is on ("" for none); the list follows it.
	curID string
	// waiting is set while the current task waits on the owner: its glyph
	// is "!" then.
	waiting bool
	// mode is the run mode; overrides are the per-task modes the owner chose.
	mode      string
	overrides map[string]string
	th        *theme
}

// rows is the model's view of the plan for the shared task builders.
func (m *model) rows() rows {
	r := rows{plan: m.plan, phase: m.phase, mode: m.mode, overrides: m.overrides, th: m.th}
	if m.cur != nil {
		r.curID = m.cur.id
		r.waiting = m.cur.state != stateWorking && m.cur.state != stateVerifying
	}
	return r
}

// tasks are the tasks of the phase.
func (r rows) tasks() []*plan.Task {
	if r.plan == nil {
		return nil
	}
	if ph := r.plan.Phase(r.phase); ph != nil {
		return ph.Tasks
	}
	return nil
}

// needsOwner reports whether t is the current task and waits on the owner.
func (r rows) needsOwner(t *plan.Task) bool {
	return r.curID == t.ID && r.waiting
}

// glyph is t's status glyph; the current task shows "!" while it waits on
// the owner.
func (r rows) glyph(t *plan.Task) string {
	if r.needsOwner(t) {
		return "!"
	}
	if g, ok := glyphs[t.Status]; ok {
		return g
	}
	return "?"
}

// looks are the looks of t's glyph and of its text: what runs or waits on
// the owner stands out, what is over or can't start recedes.
func (r rows) looks(t *plan.Task) (glyph, text look) {
	if r.needsOwner(t) {
		return lookAlert, lookTitle
	}
	switch t.Status {
	case plan.InProgress:
		return lookAccentBold, lookTitle
	case plan.Done:
		return lookAccent, lookDim
	case plan.Blocked, plan.Skipped:
		return lookDim, lookDim
	}
	return lookPlain, lookPlain
}

// paintedGlyph is t's glyph in its look.
func (r rows) paintedGlyph(t *plan.Task) string {
	l, _ := r.looks(t)
	return r.th.paint(l, r.glyph(t))
}

// rank is the rank shown for t: its rank name, or "user" for user tasks.
func rank(t *plan.Task) string {
	switch {
	case !t.Owner.IsAgent():
		return "user"
	case t.Rank == "":
		return "—"
	}
	return t.Rank
}

// lines renders the task list w cells wide, returning the lines, the index
// of the line to keep in view (-1 for none) — the selected task's when sel
// is a task index, else the current task's — and for each line the index of
// the task it shows (-1 for a "waits on" line). sel < 0 means no selection
// is shown.
func (r rows) lines(w, sel int) (lines []string, follow int, owner []int) {
	tasks := r.tasks()
	if len(tasks) == 0 {
		if r.plan == nil {
			return []string{"reading the plan…"}, -1, []int{-1}
		}
		return []string{"no tasks in this phase"}, -1, []int{-1}
	}
	idW, rankW := 0, 0
	for _, t := range tasks {
		idW, rankW = max(idW, textWidth(t.ID)), max(rankW, textWidth(rank(t)))
	}
	titleW := max(w-3-idW-2-1-rankW, 1) // "›✓ " + ID + "  " + title + " " + rank
	var out []string
	follow = -1
	for i, t := range tasks {
		if (sel >= 0 && i == sel) || (sel < 0 && r.curID == t.ID) {
			follow = len(out)
		}
		id, title := pad(t.ID, idW), pad(fit(t.Title, titleW), titleW)
		if i == sel {
			// The selected row is one focus bar; its parts keep no looks.
			row := rowMark(true) + r.glyph(t) + " " + id + "  " + title + " " + rank(t)
			out = append(out, r.th.focusLine(fit(row, w), w))
		} else {
			_, tl := r.looks(t)
			row := rowMark(false) + r.paintedGlyph(t) + " " + r.th.paint(tl, id) + "  " + r.th.paint(tl, title) + " " + r.th.rank(rank(t), tl == lookDim)
			out = append(out, fit(row, w))
		}
		owner = append(owner, i)
		if t.Status == plan.Blocked {
			if wt := r.plan.WaitingOn(t); wt != nil {
				out = append(out, r.th.paint(lookDim, fit("   waits on "+strings.Join(wt.Unmet, ", "), w)))
				owner = append(owner, -1)
			}
		}
	}
	return out, follow, owner
}

// compact lays the phase's tasks out as "glyph ID rank" cells in as many
// columns as fit w, row by row, returning the lines, the column count, the
// cell width and the index of the task to keep in view (-1 for none): the
// selected one, else the current one. sel < 0 means no selection is shown.
func (r rows) compact(w, sel int) (lines []string, cols, cellW, cur int) {
	tasks := r.tasks()
	cells := make([]string, len(tasks))
	cur = sel
	for i, t := range tasks {
		cells[i] = rowMark(i == sel) + r.glyph(t) + " " + t.ID + " " + rank(t)
		cellW = max(cellW, textWidth(cells[i]))
		if sel < 0 && r.curID == t.ID {
			cur = i
		}
	}
	for i, t := range tasks {
		if i == sel {
			cells[i] = r.th.focusLine(cells[i], cellW)
			continue
		}
		_, tl := r.looks(t)
		cells[i] = rowMark(false) + r.paintedGlyph(t) + " " + r.th.paint(tl, t.ID) + " " + r.th.rank(rank(t), tl == lookDim)
	}
	cols = max((w+1)/(cellW+1), 1)
	for i := 0; i < len(cells); i += cols {
		var b strings.Builder
		for j := i; j < min(i+cols, len(cells)); j++ {
			if j > i {
				b.WriteString(" ")
			}
			b.WriteString(pad(cells[j], cellW))
		}
		lines = append(lines, fit(strings.TrimRight(b.String(), " "), w))
	}
	return lines, cols, cellW, cur
}

// detail is the body of task id's detail page w cells wide (SPEC §15.3):
// its text, status, rank, owner, mode, dependencies with their states and
// the extra columns.
func (r rows) detail(id string, w int) []string {
	t := r.task(id)
	if t == nil {
		return wrap(id+" is no longer in the plan.", w)
	}
	var out []string
	for _, l := range wrap(t.ID+" — "+t.Title, w) {
		out = append(out, r.th.paint(lookTitle, l))
	}
	out = append(out, "")
	out = append(out, wrap(strings.ReplaceAll(t.Text, "**", ""), w)...)
	out = append(out, "")
	return append(out, fields(r.th, r.facts(t), w)...)
}

// task is task id in the plan as last loaded, or nil.
func (r rows) task(id string) *plan.Task {
	if r.plan == nil {
		return nil
	}
	return r.plan.Task(id)
}

// about is what t is about: its text without the bold title it starts with
// (the card shows the title already), and without markdown emphasis.
func about(t *plan.Task) string { return aboutText(t.Text, t.Title) }

// aboutText is text without the bold title it starts with, and without
// markdown emphasis.
func aboutText(text, title string) string {
	text = strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(text, "**"+title+"**"); ok {
		text = strings.TrimLeft(strings.TrimSpace(rest), "—–:·- ")
	} else if text == title {
		text = ""
	}
	return strings.TrimSpace(strings.ReplaceAll(text, "**", ""))
}

// fact is a labelled line of the details; more lines follow under it.
type fact struct {
	name  string
	lines []string
}

// facts are the details of t below its text.
func (r rows) facts(t *plan.Task) []fact {
	owner := t.OwnerText
	if owner == "" {
		owner = "—"
	}
	facts := []fact{
		{"Status", []string{r.paintedGlyph(t) + " " + statusWord(t)}},
		{"Rank", []string{r.th.rank(rank(t), false)}},
		{"Owner", []string{owner}},
		{"Mode", []string{r.modeText(t)}},
	}
	if t.Phase != nil {
		facts = append(facts, fact{"Phase", []string{strings.TrimSpace(t.Phase.ID + " " + t.Phase.Title)}})
	}
	deps := fact{name: "Deps"}
	for _, d := range t.Deps {
		dt := r.plan.Task(d)
		if dt == nil {
			deps.lines = append(deps.lines, "? "+d+" (not in the plan)")
			continue
		}
		deps.lines = append(deps.lines, r.paintedGlyph(dt)+" "+d+" "+statusWord(dt)+" · "+dt.Title)
	}
	if len(deps.lines) == 0 {
		deps.lines = []string{"none"}
	}
	facts = append(facts, deps)
	if wt := r.plan.WaitingOn(t); wt != nil && t.Status != plan.Done && t.Status != plan.Skipped {
		facts = append(facts, fact{"Waits on", []string{strings.Join(wt.Unmet, ", ")}})
	}
	if t.Phase != nil {
		for _, col := range t.Phase.Columns {
			if v, ok := t.Extra[col]; ok {
				facts = append(facts, fact{col, []string{v}})
			}
		}
	}
	return facts
}

// statusWord is t's Status cell as written, or its status by name.
func statusWord(t *plan.Task) string {
	if s := strings.TrimSpace(t.StatusText); s != "" {
		return s
	}
	return t.Status.String()
}

// taskMode is the mode t's next session would run in, as far as the TUI
// knows: the owner's override, its Mode column, else the run mode.
func (r rows) taskMode(t *plan.Task) string {
	switch {
	case r.overrides[t.ID] != "":
		return r.overrides[t.ID]
	case t.Mode != "":
		return t.Mode
	}
	return r.mode
}

// modeText says which mode t's next session runs in, and why (SPEC §7.2).
func (r rows) modeText(t *plan.Task) string {
	mode, why := r.taskMode(t), "run mode"
	switch {
	case r.overrides[t.ID] != "":
		why = "your override"
	case t.Mode != "":
		why = "Mode column"
	case mode == "":
		mode = "default"
	}
	return mode + badge(mode) + " (" + why + ")"
}
