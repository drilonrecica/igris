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
	if m.dialog != nil {
		return m.fullScreenDialog(w, h)
	}
	bar := fitBar(m.buttons(), w, barRows)

	// Header, three section rules and the bar are fixed; the card, the
	// task list and the log share the rest.
	rest := max(h-1-3-len(bar.rows), 0)
	logRows := min(narrowLog, rest)
	card := m.cardLines(w, 0, 0, false)
	cardRows := min(len(card), max(rest-logRows-1, 0)) // leave a task row
	taskRows := max(rest-logRows-cardRows, 0)

	var out []string
	out = append(out, fit("igris · "+m.opts.Project+" · "+m.facts(), w))
	out = append(out, rule("CURRENT", w))
	out = append(out, m.card(0, len(out), w, cardRows)...)
	out = append(out, rule("TASKS", w))
	m.zones.add(rect{0, len(out), w, taskRows}, target{region: regionTasks})
	out = append(out, m.compactTasks(w, taskRows)...)
	for len(out) < 1+1+cardRows+1+taskRows {
		out = append(out, "")
	}
	out = append(out, rule("LOG", w))
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

// rule is a section rule: "── TASKS ─────".
func rule(name string, w int) string {
	s := "── " + name + " "
	return fit(s+strings.Repeat("─", max(w-textWidth(s), 0)), w)
}

// compactTasks lays the phase's tasks out as "glyph ID rank" cells in as
// many columns as fit, row by row, following the current task.
func (m *model) compactTasks(w, rows int) []string {
	if rows <= 0 {
		return nil
	}
	tasks := m.phaseTasks()
	if len(tasks) == 0 {
		lines, _ := m.taskLines(w)
		return lines[:min(rows, len(lines))]
	}
	cells := make([]string, len(tasks))
	cellW, cur := 0, -1
	for i, t := range tasks {
		cells[i] = m.glyph(t) + " " + t.ID + " " + rank(t)
		cellW = max(cellW, textWidth(cells[i]))
		if m.cur != nil && m.cur.id == t.ID {
			cur = i
		}
	}
	cols := max((w+2)/(cellW+2), 1)
	var lines []string
	for i := 0; i < len(cells); i += cols {
		var b strings.Builder
		for j := i; j < min(i+cols, len(cells)); j++ {
			if j > i {
				b.WriteString("  ")
			}
			b.WriteString(pad(cells[j], cellW))
		}
		lines = append(lines, fit(strings.TrimRight(b.String(), " "), w))
	}
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
	return lines[top:min(top+rows, len(lines))]
}

// fullScreenDialog draws the open dialog over the whole screen.
func (m *model) fullScreenDialog(w, h int) string {
	box, z := m.dialog.render(w, h)
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
var foldOrder = []action{actPause, actQuit}

// fitBar lays the buttons out in at most maxRows rows of w cells. When they
// don't fit, the less common actions move behind a More… button.
func fitBar(btns []option, w, maxRows int) barLayout {
	keep := append([]option{}, btns...)
	var folded []option
	for _, a := range foldOrder {
		if l := layoutBar(keep, w); len(l.rows) <= maxRows {
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
	l := layoutBar(keep, w)
	if len(l.rows) > maxRows { // can't fit even folded: cut the bar
		l.rows, l.text = l.rows[:maxRows], l.text[:maxRows]
	}
	l.folded = folded
	return l
}

// layoutBar wraps the buttons into rows of w cells.
func layoutBar(btns []option, w int) barLayout {
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
		label := fit("["+o.label+"]", w)
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
