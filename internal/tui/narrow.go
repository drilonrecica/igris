package tui

import (
	"strings"
)

// The narrow layout (SPEC §15.2) for terminals under 100 columns, e.g.
// Termius on a phone: one column with the header, the current-task card, a
// compact task list, the last log lines and the action bar. It must stay
// usable at 50×20.

// narrowLog is how many log lines the narrow layout shows.
const narrowLog = 3

// barRows is how many rows the action bar may take before it folds
// actions into More….
const barRows = 2

func (m *model) narrowView() string {
	w, h := max(m.width, 10), max(m.height, 3)
	switch {
	case m.dialog != nil:
		return m.fullScreenDialog(w, h)
	case m.page != nil:
		lines, z := m.page.render(m.th, w, h)
		m.zones.merge(z, 0, 0)
		return strings.Join(lines, "\n")
	}
	bar := m.fitBar(w)

	// Header, three section rules and the bar are fixed; the card, the
	// task list and the log share the rest.
	rest := max(h-1-3-len(bar.rows), 0)
	logRows := min(narrowLog, rest)
	card := m.cardLines(w, 0, 0, false)
	cardRows := min(len(card), max(rest-logRows-1, 0)) // leave a task row
	taskRows := max(rest-logRows-cardRows, 0)

	var out []string
	out = append(out, fit(m.th.paint(lookAccentBold, "igris")+" · "+m.opts.Project+" · "+m.facts(), w))
	out = append(out, m.rule(m.th.paint(lookTitle, "CURRENT"), w))
	out = append(out, m.card(0, len(out), w, cardRows)...)
	out = append(out, m.rule(m.areaTitle("TASKS", focusTasks), w))
	m.zones.add(rect{0, len(out), w, taskRows}, target{region: regionTasks})
	out = append(out, m.compactTasks(len(out), w, taskRows)...)
	for len(out) < 1+1+cardRows+1+taskRows {
		out = append(out, "")
	}
	out = append(out, m.rule(m.areaTitle("LOG", focusLog), w))
	m.zones.add(rect{0, len(out), w, logRows}, target{region: regionLog})
	logs := m.logView(logRows)
	for i := range logRows {
		out = append(out, fit(line(logs, i), w))
	}
	for i, row := range bar.rows {
		y := len(out)
		for _, b := range row {
			m.zones.add(rect{b.x, y, textWidth(b.text), 1}, target{act: b.act})
		}
		out = append(out, bar.text[i])
	}
	m.folded = bar.folded
	if len(out) > h {
		out = out[:h]
	}
	return strings.Join(out, "\n")
}

// rule is a section rule with its title: "── TASKS ─────".
func (m *model) rule(title string, w int) string {
	fill := max(w-3-textWidth(title)-1, 0) // "── " + title + " "
	return fit(m.th.paint(lookFrame, "──")+" "+title+" "+m.th.paint(lookFrame, strings.Repeat("─", fill)), w)
}

// compactTasks lays the phase's tasks out as "glyph ID rank" cells in as
// many columns as fit, row by row, following the current task, drawn from
// row y on. Each cell is recorded as a click target.
func (m *model) compactTasks(y, w, rows int) []string {
	if rows <= 0 {
		return nil
	}
	tasks := m.phaseTasks()
	if len(tasks) == 0 {
		lines, _, _ := m.taskLines(w)
		return lines[:min(rows, len(lines))]
	}
	lines, cols, cellW, cur := m.rows().compact(w, m.listSel())
	curLine := -1
	if cur >= 0 {
		curLine = cur / cols
	}
	top := m.taskTop
	if top < 0 {
		top = 0
		if curLine >= rows {
			top = curLine - rows/2
		}
	}
	top = min(top, max(len(lines)-rows, 0))
	if m.taskTop < 0 {
		m.autoTaskTop = top
	} else {
		m.taskTop = top
	}
	end := min(top+rows, len(lines))
	for l := top; l < end; l++ {
		for c := range cols {
			if i := l*cols + c; i < len(tasks) {
				m.zones.add(rect{c * (cellW + 1), y + l - top, min(cellW, w), 1}, target{act: actTaskRow, option: i})
			}
		}
	}
	return lines[top:end]
}

// fullScreenDialog draws the open dialog over the whole screen.
func (m *model) fullScreenDialog(w, h int) string {
	box, z := m.dialog.render(m.th, w, h)
	m.zones.merge(z, 0, 0)
	if len(box) > h {
		box = box[:h]
	}
	for i := range box {
		box[i] = fit(box[i], w)
	}
	return strings.Join(box, "\n")
}

// placed is a button on a bar row.
type placed struct {
	text string
	x    int
	act  action
}

// barLayout is the action bar broken into rows.
type barLayout struct {
	rows   [][]placed
	text   []string
	folded []option // the actions behind More…
}

// foldOrder lists the actions the bar folds into More… first when it
// doesn't fit; actions not listed are never folded.
var foldOrder = []action{actHelp, actTaskMode, actStopAsk, actRetry, actMode, actPause, actQuit, actSkip}

// fitBar lays out the model's bar w cells wide, records its buttons and
// marks the focused one. The focus moves to the first button if its
// button was folded away or is gone.
func (m *model) fitBar(w int) barLayout {
	btns := m.buttons()
	mark := actNone
	if m.barFocused() {
		mark = m.barFocus
	}
	l := fitBar(m.th, btns, w, barRows, mark)
	if m.barFocused() && !l.has(mark) {
		mark = l.rows[0][0].act
		l = fitBar(m.th, btns, w, barRows, mark)
	}
	m.bar = m.bar[:0]
	for _, row := range l.rows {
		for _, p := range row {
			m.bar = append(m.bar, p.act)
		}
	}
	return l
}

// has reports whether a is on the bar.
func (l barLayout) has(a action) bool {
	for _, row := range l.rows {
		for _, p := range row {
			if p.act == a {
				return true
			}
		}
	}
	return false
}

// fitBar lays the buttons out in at most maxRows rows of w cells, marking
// the focused one. When they don't fit, the less common actions move
// behind a More… button.
func fitBar(th *theme, btns []option, w, maxRows int, focused action) barLayout {
	keep := append([]option{}, btns...)
	var folded []option
	for _, a := range foldOrder {
		if l := layoutBar(th, keep, w, focused); len(l.rows) <= maxRows {
			l.folded = folded
			return l
		}
		for i, b := range keep {
			if b.act == a {
				folded = append(folded, b)
				keep = append(keep[:i:i], keep[i+1:]...)
				if len(folded) == 1 {
					keep = append(keep, option{"More…", actMore})
				}
				break
			}
		}
	}
	l := layoutBar(th, keep, w, focused)
	if len(l.rows) > maxRows { // can't fit even folded: cut the bar
		l.rows, l.text = l.rows[:maxRows], l.text[:maxRows]
	}
	l.folded = folded
	return l
}

// layoutBar wraps the buttons into rows of w cells.
func layoutBar(th *theme, btns []option, w int, focused action) barLayout {
	var l barLayout
	var row []placed
	x := 0
	flush := func() {
		var b strings.Builder
		for _, p := range row {
			b.WriteString(pad("", p.x-textWidth(b.String())) + p.text)
		}
		l.rows = append(l.rows, row)
		l.text = append(l.text, b.String())
		row, x = nil, 0
	}
	for _, o := range btns {
		label := fit(th.button(o.label, barState(o.act, focused, false)), w)
		lw := textWidth(label)
		if x > 0 && x+1+lw > w {
			flush()
		}
		if x > 0 {
			x++
		}
		row = append(row, placed{label, x, o.act})
		x += lw
	}
	if len(row) > 0 {
		flush()
	}
	return l
}

// moreDialog lists the actions the bar folded away.
func moreDialog(folded []option) *dialog {
	return &dialog{title: "More actions", options: folded, cancel: actClose}
}
