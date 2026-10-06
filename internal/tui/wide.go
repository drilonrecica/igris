package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
)

// The wide layout (SPEC §15.1), drawn with box characters so every cell's
// position is known for the hit map:
//
//	┌ igris · sinjal ───── phase M0 · mode: plan · herdr ┐
//	│ TASKS                  │ CURRENT                   │  panes
//	├────────────────────────┴───────────────────────────┤
//	│ 09:41 M0-02 done                                   │  log
//	├────────────────────────────────────────────────────┤
//	│ [Open session] [Pause] [Done] [Quit]               │  action bar
//	└────────────────────────────────────────────────────┘

// wideMinHeight is the least height the wide layout needs: chrome, the
// pane headers plus three rows, and three log lines.
const wideMinHeight = 13

func (m *model) wideView() string {
	w, h := m.width, m.height
	inner := w - 4                                            // "│ " + text + " │"
	lw := (w - 7) / 2                                         // "│ " + left + " │ " + right + " │"
	rw := w - 7 - lw                                          // right pane
	avail := h - 5                                            // panes + log, without the 4 rules and the bar
	paneRows := min(max(m.paneNeed(lw, rw), 4), avail*55/100) // incl. the pane titles
	logRows := avail - paneRows

	var out []string
	out = append(out, m.topRule(w))
	switch body := paneRows + 1 + logRows; { // the panes, their rule and the log
	case m.dialog != nil:
		out = append(out, m.dialogBody(len(out), w, body)...)
	case m.page != nil:
		out = append(out, m.pageBody(len(out), w, body)...)
	default:
		left := m.taskPane(lw, paneRows-1)
		right := m.card(lw+5, len(out)+1, rw, paneRows-1)
		m.zones.add(rect{2, len(out) + 1, lw, paneRows - 1}, target{region: regionTasks})
		out = append(out, "│ "+pad(m.areaTitle("TASKS", focusTasks), lw)+" │ "+pad("CURRENT", rw)+" │")
		for i := range paneRows - 1 {
			out = append(out, "│ "+pad(line(left, i), lw)+" │ "+pad(line(right, i), rw)+" │")
		}
		rule := "├" + strings.Repeat("─", lw+2) + "┴" + strings.Repeat("─", rw+2) + "┤"
		if m.focus == focusLog {
			rule = overlay(rule, 2, " "+m.areaTitle("LOG", focusLog)+" ")
		}
		out = append(out, rule)
		m.zones.add(rect{2, len(out), inner, logRows}, target{region: regionLog})
		logs := m.logView(logRows)
		for i := range logRows {
			out = append(out, "│ "+pad(fit(line(logs, i), inner), inner)+" │")
		}
	}
	out = append(out, "├"+strings.Repeat("─", w-2)+"┤")
	out = append(out, "│ "+pad(m.barAt(2, len(out), inner), inner)+" │")
	out = append(out, "└"+strings.Repeat("─", w-2)+"┘")
	return strings.Join(out, "\n")
}

// overlay writes text over s from cell at on; s is box drawing, one cell
// per rune.
func overlay(s string, at int, text string) string {
	r, t := []rune(s), []rune(text)
	if at+len(t) >= len(r) {
		return s
	}
	copy(r[at:], t)
	return string(r)
}

// line returns lines[i], or "" past the end.
func line(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// topRule is the top border with the title on the left and the run's
// facts on the right.
func (m *model) topRule(w int) string {
	title := " igris · " + m.opts.Project + " "
	facts := " " + m.facts() + " "
	fill := w - 2 - textWidth(title) - textWidth(facts)
	if fill < 1 {
		facts = " " + fit(strings.TrimSpace(facts), max(w-4-textWidth(title)-1, 0)) + " "
		fill = max(w-2-textWidth(title)-textWidth(facts), 0)
	}
	return fit("┌"+title+strings.Repeat("─", fill)+facts+"┐", w)
}

// facts are the header's run facts: phase, mode, backend, pause.
func (m *model) facts() string {
	var parts []string
	if m.phase != "" {
		parts = append(parts, "phase "+m.phase)
	}
	if m.mode != "" {
		parts = append(parts, "mode: "+m.mode+badge(m.mode))
	}
	if m.mode != engine.ModeYolo && m.cur != nil && m.cur.mode == engine.ModeYolo {
		// The run mode isn't yolo but this session is (SPEC §7.3).
		parts = append(parts, "SKIP PERMISSIONS")
	}
	parts = append(parts, m.opts.Backend)
	if m.paused {
		parts = append(parts, "PAUSE AFTER TASK")
	}
	return strings.Join(parts, " · ")
}

// glyphs are the status glyphs of SPEC §15.4.
var glyphs = map[plan.Status]string{
	plan.Done:       "✓",
	plan.InProgress: "●",
	plan.Ready:      "·",
	plan.Blocked:    "⨯",
	plan.Skipped:    "–",
}

// glyph is t's status glyph; the current task shows "!" while it waits on
// the owner.
func (m *model) glyph(t *plan.Task) string {
	if m.cur != nil && m.cur.id == t.ID && m.cur.state != stateWorking && m.cur.state != stateVerifying {
		return "!"
	}
	if g, ok := glyphs[t.Status]; ok {
		return g
	}
	return "?"
}

// phaseTasks are the tasks of the phase being run.
func (m *model) phaseTasks() []*plan.Task {
	if m.plan == nil {
		return nil
	}
	if ph := m.plan.Phase(m.phase); ph != nil {
		return ph.Tasks
	}
	return nil
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

// taskLines renders the task list w cells wide, returning the lines and
// the index of the line to keep in view (-1 for none): the selected task's
// while the list has the focus, else the current task's.
func (m *model) taskLines(w int) ([]string, int) {
	tasks := m.phaseTasks()
	if len(tasks) == 0 {
		if m.plan == nil {
			return []string{"reading the plan…"}, -1
		}
		return []string{"no tasks in this phase"}, -1
	}
	idW, rankW := 0, 0
	for _, t := range tasks {
		idW, rankW = max(idW, textWidth(t.ID)), max(rankW, textWidth(rank(t)))
	}
	titleW := max(w-3-idW-2-1-rankW, 1) // "›✓ " + ID + "  " + title + " " + rank
	sel := m.listSel()
	var out []string
	follow := -1
	for i, t := range tasks {
		if (sel >= 0 && i == sel) || (sel < 0 && m.cur != nil && m.cur.id == t.ID) {
			follow = len(out)
		}
		row := rowMark(i == sel) + m.glyph(t) + " " + pad(t.ID, idW) + "  " + pad(fit(t.Title, titleW), titleW) + " " + rank(t)
		out = append(out, fit(row, w))
		if t.Status == plan.Blocked {
			if wt := m.plan.WaitingOn(t); wt != nil {
				out = append(out, fit("   waits on "+strings.Join(wt.Unmet, ", "), w))
			}
		}
	}
	return out, follow
}

// taskPane is the visible part of the task list: it follows the current
// task unless the owner scrolled.
func (m *model) taskPane(w, rows int) []string {
	lines, cur := m.taskLines(w)
	top := m.taskTop
	if top < 0 {
		top = 0
		if cur >= rows {
			top = cur - rows/2
		}
	}
	top = min(top, max(len(lines)-rows, 0))
	if m.taskTop < 0 {
		m.autoTaskTop = top
	} else {
		m.taskTop = top
	}
	return lines[top:min(top+rows, len(lines))]
}

// listSel is the selected task's index while the task list has the focus,
// else -1: the selection is only shown, and followed, then.
func (m *model) listSel() int {
	if m.focus != focusTasks || m.modal() {
		return -1
	}
	return m.selected()
}

// paneNeed is how many rows the panes want, titles included.
func (m *model) paneNeed(lw, rw int) int {
	tasks, _ := m.taskLines(lw)
	return 1 + max(len(tasks), len(m.cardLines(rw, 0, 0, false)))
}

// card renders the current-task card at (x, y), rows lines of w cells, and
// records its buttons.
func (m *model) card(x, y, w, rows int) []string {
	lines := m.cardLines(w, x, y, true)
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}

// cardLines renders the card; with record set, its buttons' zones are
// recorded for a card drawn at (x, y).
func (m *model) cardLines(w, x, y int, record bool) []string {
	if m.ended {
		// The run is over; a task it was on stays as the plan says.
		return wrap(m.endText+" — press q to quit", w)
	}
	if m.cur == nil {
		switch {
		case m.holding:
			return []string{"paused before the next task", "press p to continue"}
		}
		return []string{"no task running"}
	}
	c := m.cur
	out := []string{fit(c.id+" "+c.title, w)}
	if c.user {
		out = append(out, "user task · "+since(m.opts.Now(), c.started))
	} else {
		out = append(out, fit("rank "+c.rank+" → model "+c.model, w))
		out = append(out, fit("mode "+c.mode+badge(c.mode)+" · "+since(m.opts.Now(), c.started), w))
	}
	out = append(out, fit("state: "+m.stateText(), w))
	switch c.state {
	case stateYourTurn:
		out = append(out, wrap(strings.ReplaceAll(c.detail, "**", ""), w)...)
	case stateNeedsYou, stateLost:
		out = append(out, fit(c.detail, w))
	}
	var btns []option
	if c.session != nil {
		btns = append(btns, option{"[o] Open session", actOpen})
	}
	if m.asked != nil && m.dialog == nil {
		btns = append(btns, option{"[Answer…]", actAnswer})
	}
	for _, b := range btns {
		if record {
			m.zones.add(rect{x, y + len(out), min(textWidth(b.label), w), 1}, target{act: b.act})
		}
		out = append(out, fit(b.label, w))
	}
	return out
}

// pageBody fills rows lines below row y with the open page and records
// its zones.
func (m *model) pageBody(y, w, rows int) []string {
	inner := w - 4
	lines, z := m.page.render(inner, rows)
	m.zones.merge(z, 2, y)
	out := make([]string, 0, rows)
	for _, l := range lines {
		out = append(out, "│ "+pad(l, inner)+" │")
	}
	return out
}

// dialogBody fills rows lines below row y with the open dialog, centered,
// and records its options.
func (m *model) dialogBody(y, w, rows int) []string {
	box, z := m.dialog.render(min(w-4, 72), rows)
	top := max((rows-len(box))/2, 0)
	left := max((w-textWidth(box[0]))/2, 1)
	m.zones.merge(z, left, y+top)
	out := make([]string, 0, rows)
	for i := range rows {
		l := ""
		if i >= top && i-top < len(box) {
			l = strings.Repeat(" ", left-1) + box[i-top]
		}
		out = append(out, "│"+pad(fit(l, w-2), w-2)+"│")
	}
	return out
}

// barAt draws the action bar at (x, y), at most w cells, and records its
// buttons.
func (m *model) barAt(x, y, w int) string {
	btns := m.buttons()
	m.bar = m.bar[:0]
	for _, o := range btns {
		m.bar = append(m.bar, o.act)
	}
	mark := actNone
	if m.barFocused() {
		mark = m.focusedButton()
	}
	var b strings.Builder
	m.bar = m.bar[:0]
	for _, o := range btns {
		label := buttonLabel(o.label, o.act == mark)
		lw := textWidth(label)
		if textWidth(b.String())+lw > w {
			break
		}
		if !m.modal() {
			m.zones.add(rect{x + textWidth(b.String()), y, lw, 1}, target{act: o.act})
		}
		m.bar = append(m.bar, o.act)
		b.WriteString(label + " ")
	}
	return strings.TrimRight(b.String(), " ")
}
