package tui

// The narrow layout (SPEC §15.2) for terminals under 100 columns, e.g.
// Termius on a phone: one column with the header, the current-task card, a
// compact task list, the last log lines and the action bar. It must stay
// usable at 50×20.

// narrowLog is how many log lines the narrow layout keeps at least.
const narrowLog = 3

// barRows is how many rows the action bar may take on a screen too
// narrow even for its keys on one row (fitBar).
const barRows = 2

// narrowScreen draws the narrow layout's h lines of w cells and records
// its zones (SPEC §15.2): the header and its rule, the switcher with
// several active tasks (SPEC §15.1), the selected card, TASKS, the log
// and the bar; a dialog takes the whole screen (View). The card gets what
// it needs while the log keeps narrowLog lines and TASKS one row; TASKS is
// only as tall as its rows, and the log gets the rest. A log with fewer
// lines than its rows lends the difference to the card's live tail.
func (m *model) narrowScreen(w, h int) []string {
	m.lend = 0
	bar := m.fitBar(w, barRows)
	switchRows := 0
	if m.multi() && !m.ended {
		switchRows = 1
	}
	// The header, its rule, the switcher, the two section rules and the
	// bar are fixed; the card, the task list and the log share the rest.
	rest := max(h-2-switchRows-2-len(bar.rows), 0)
	logMin := min(narrowLog, rest)
	need := m.cardNeed(w)
	cardRows := min(need, max(rest-logMin-1, 0)) // leave a task row
	taskRows := min(m.compactNeed(w), max(rest-logMin-cardRows, 0))
	logRows := max(rest-cardRows-taskRows, 0)
	if spare := logRows - len(m.log); spare > 0 && cardRows == need {
		m.lend = spare
		grow := min(m.cardNeed(w)-cardRows, spare)
		cardRows, logRows = cardRows+grow, logRows-grow
	}

	out := []string{m.header(w), m.th.rule(w, "")}
	if switchRows > 0 {
		out = append(out, m.switcher(len(out), w))
	}
	out = append(out, m.cardPane(0, len(out), w, cardRows)...)
	for len(out) < 2+switchRows+cardRows {
		out = append(out, "")
	}
	out = append(out, m.th.rule(w, m.areaTitle("TASKS", focusTasks)))
	m.zones.add(rect{0, len(out), w, taskRows}, target{region: regionTasks})
	out = append(out, m.compactTasks(len(out), w, taskRows)...)
	for len(out) < 2+switchRows+cardRows+1+taskRows {
		out = append(out, "")
	}
	out = append(out, m.th.rule(w, m.areaTitle("LOG", focusLog)))
	m.zones.add(rect{0, len(out), w, logRows}, target{region: regionLog})
	logs := m.logView(logRows)
	for i := range logRows {
		out = append(out, fit(line(logs, i), w))
	}
	bar.barZones(&m.zones, len(out), m.modal())
	out = append(out, bar.text...)
	if len(out) > h {
		out = out[:h]
	}
	return out
}

// compactNeed is how many rows the narrow TASKS area wants w cells wide.
func (m *model) compactNeed(w int) int {
	if len(m.phaseTasks()) == 0 {
		lines, _, _ := m.taskLines(w)
		return len(lines)
	}
	lines, _, _, _ := m.rows().compact(w, m.listSel())
	return len(lines)
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
