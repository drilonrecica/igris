package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
)

// The home layouts (SPEC §15.6). Wide (≥ 100×13) puts PHASES left and the
// NOW card, HEALTH and RECENT right:
//
//	igris · sinjal · tasks.md · herdr ✓ · mode default              idle
//	─ PHASES ──────────────────────┬─ NOW ───────────────────────────────
//	  ✓ M0  Foundations  ██████████ 3/3 done │ READY · phase M2 …
//	                                         ├─ HEALTH ────────────────────
//	                                         ├─ RECENT ────────────────────
//	───────────────────────────────┴─────────────────────────────────────
//	✓ status line                                                   09:41
//	[›Arise…‹] [Preview] …
//
// Narrow is one column: NOW, PHASES, HEALTH, RECENT, the status line and
// the bar, which folds into More… when it needs more than two rows.

// homeWideH is the least height the wide layout needs.
const homeWideH = 13

// homeBarWidth is the progress bar's width; below homeBarsFrom columns the
// bars are dropped and the numbers stay.
const (
	homeBarWidth = 10
	homeBarsFrom = 60
)

// View draws the screen, exactly h lines, and records its zones.
func (m *homeScreen) View() string {
	m.zones.reset()
	w, h := max(m.w, 10), max(m.h, 3)
	var out []string
	if w >= wideWidth && h >= homeWideH {
		out = m.wideHome(w, h)
	} else {
		out = m.narrowHome(w, h)
	}
	for len(out) < h {
		out = append(out, "")
	}
	return strings.Join(out[:h], "\n")
}

// modal reports whether the More… dialog holds the focus.
func (m *homeScreen) modal() bool { return m.dialog != nil }

// headerLine is the header: the facts on the left, the run state on the
// right.
func (m *homeScreen) headerLine(w int, wide bool) string {
	word, l := m.stateWord()
	left := m.header(wide)
	room := w - textWidth(word) - 2
	if room < 8 {
		return fit(left, w)
	}
	left = fit(left, room)
	return left + strings.Repeat(" ", w-textWidth(left)-textWidth(word)) + m.th.paint(l, word)
}

// regionTitle is a region's title, marked while it has the focus.
func (m *homeScreen) regionTitle(name string, focused bool) string {
	if focused && !m.modal() {
		return m.th.paint(lookAccentBold, "› "+name)
	}
	return m.th.paint(lookTitle, name)
}

// rule is a section rule with its title: "── NOW ─────" (narrow) or
// "├─ HEALTH ───" (wide, right column) with the given lead.
func (m *homeScreen) rule(lead, title string, w int) string {
	fill := max(w-textWidth(lead)-1-textWidth(title)-1, 0)
	return fit(m.th.paint(lookFrame, lead)+" "+title+" "+m.th.paint(lookFrame, strings.Repeat("─", fill)), w)
}

// phaseLines are the PHASES rows w cells wide, the selected one marked
// (and a focus bar while the list has the focus). With bars the progress
// bar is drawn before the count.
func (m *homeScreen) phaseLines(w int, bars bool) []string {
	rows := m.phaseRowsData()
	if len(rows) == 0 {
		return []string{m.th.paint(lookDim, fit(m.noPhasesText(), w))}
	}
	idW, countW := 0, 0
	for _, r := range rows {
		idW, countW = max(idW, textWidth(r.id)), max(countW, textWidth(r.count))
	}
	barW := 0
	if bars {
		barW = homeBarWidth + 1
	}
	titleW := max(w-4-idW-2-barW-1-countW-1-5, 4) // "› ✓ " ID "  " title " " bar " " count " " word
	sel := m.phaseIndex()
	out := make([]string, 0, len(rows))
	for i, r := range rows {
		bar := ""
		if bars {
			bar = progressBar(r.done, r.total, homeBarWidth) + " "
		}
		count := strings.Repeat(" ", countW-textWidth(r.count)) + r.count
		title := pad(fit(r.title, titleW), titleW)
		if i == sel && m.focus == homePhases && !m.modal() {
			row := rowMark(true) + " " + r.glyph + " " + pad(r.id, idW) + "  " + title + " " + bar + count + " " + r.word
			out = append(out, m.th.focusLine(pad(fit(row, w), w), w))
			continue
		}
		row := rowMark(i == sel) + " " + m.th.paint(r.glyphLook, r.glyph) + " " + m.th.paint(r.textLook, pad(r.id, idW)) + "  " +
			m.th.paint(r.textLook, title) + " " + m.th.paint(lookAccent, bar) + m.th.paint(r.textLook, count+" "+r.word)
		out = append(out, fit(row, w))
	}
	return out
}

// noPhasesText says why there are no phase rows.
func (m *homeScreen) noPhasesText() string {
	switch m.kind() {
	case nowLoading:
		return "reading the plan…"
	case nowUnreadable:
		return "the project can't be read"
	case nowPlanMissing:
		return m.planName() + " not found"
	case nowGetStarted:
		if m.planFile() {
			return "Check shows the plan's problems"
		}
		return "no plan yet"
	}
	if m.snap != nil && m.snap.Plan == nil {
		return m.planName() + " can't be read"
	}
	if m.snap != nil && m.snap.Status == nil {
		return "plan invalid — Check shows the problems"
	}
	return "no phases in the plan"
}

// phaseWindow is the first phase row drawn, for rows rows: the owner's
// scroll position, else one that keeps the selection in view.
func (m *homeScreen) phaseWindow(rows int) int {
	n := len(m.phases())
	top := m.phaseTop
	if top < 0 {
		top = 0
		if sel := m.phaseIndex(); sel >= rows {
			top = sel - rows/2
		}
	}
	top = min(top, max(n-rows, 0))
	if m.phaseTop < 0 {
		m.autoTop = top
	} else {
		m.phaseTop = top
	}
	return top
}

// phaseRows is how many phase rows the last frame showed.
func (m *homeScreen) phaseRows() int { return max(m.rowsShown, 1) }

// phasePane draws rows phase rows at (x, y), w cells, recording each as a
// click target and the pane as a wheel region.
func (m *homeScreen) phasePane(x, y, w, rows int, bars bool) []string {
	if rows <= 0 {
		return nil
	}
	m.rowsShown = rows
	lines := m.phaseLines(w, bars)
	n := len(m.phases())
	if n == 0 {
		return lines[:min(rows, len(lines))]
	}
	top := m.phaseWindow(rows)
	end := min(top+rows, n)
	if !m.modal() {
		m.zones.add(rect{x, y, w, rows}, target{region: regionPhases})
		for i := top; i < end; i++ {
			m.zones.add(rect{x, y + i - top, w, 1}, target{act: actPhaseRow, option: i})
		}
	}
	return lines[top:end]
}

// lineRows draws the HEALTH/RECENT lines from first on at (x, y), w cells,
// recording each as a click target; the selected one is a focus bar while
// the lines have the focus.
func (m *homeScreen) lineRows(first int, lines []homeLine, x, y, w int) []string {
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		idx := first + i
		if !m.modal() && l.act != actNone {
			m.zones.add(rect{x, y + i, w, 1}, target{act: actLine, option: idx})
		}
		switch {
		case m.focus == homeLines && idx == m.lineSel && !m.modal():
			out = append(out, m.th.focusLine(pad(fit(l.text, w), w), w))
		case l.act == actNone:
			out = append(out, m.th.paint(lookDim, fit(l.text, w)))
		default:
			out = append(out, fit(m.paintLine(l.text), w))
		}
	}
	return out
}

// paintLine paints a HEALTH/RECENT line by its glyph: what fails stands
// out, what is fine recedes.
func (m *homeScreen) paintLine(text string) string {
	switch {
	case strings.HasPrefix(text, glyphs[plan.Blocked]+" "):
		return m.th.paint(lookAlert, text)
	case strings.HasPrefix(text, "! "):
		return m.th.paint(lookTitle, text)
	case strings.HasPrefix(text, glyphs[plan.Done]+" "):
		return m.th.paint(lookDim, text)
	}
	return text
}

// statusLine is the last action's result with the time it happened.
func (m *homeScreen) statusLine(w int) string {
	if m.status == "" {
		return ""
	}
	at := m.statusAt.In(m.loc).Format("15:04")
	text := fit(m.status, max(w-textWidth(at)-2, 0))
	return text + strings.Repeat(" ", max(w-textWidth(text)-textWidth(at), 1)) + m.th.paint(lookDim, at)
}

// barLayout lays the bar out in at most rows rows, w cells, records which
// buttons are on it and marks the focused one. The state's own action
// (the first button) is never folded into More….
func (m *homeScreen) barLayout(w, rows int) barLayout {
	btns := m.buttons()
	order := make([]action, 0, len(homeFold))
	for _, a := range homeFold {
		if a != btns[0].act {
			order = append(order, a)
		}
	}
	mark := actNone
	if m.focus == homeBar && !m.modal() {
		mark = btns[0].act // the state's own action
		if m.barMoved {
			mark = m.focusedButton()
		}
	}
	l := fitBarOrder(m.th, btns, w, rows, mark, order)
	if mark != actNone && !l.has(mark) {
		mark = l.rows[0][0].act
		l = fitBarOrder(m.th, btns, w, rows, mark, order)
	}
	m.bar = m.bar[:0]
	for _, row := range l.rows {
		for _, p := range row {
			m.bar = append(m.bar, p.act)
		}
	}
	if m.focus == homeBar {
		m.barFocus = mark
	}
	m.folded = l.folded
	if m.modal() {
		// Under the dialog the buttons are drawn inactive and take no clicks.
		labels := map[action]string{}
		for _, b := range btns {
			labels[b.act] = b.label
		}
		labels[actMore] = "More…"
		for i, row := range l.rows {
			var b strings.Builder
			for _, p := range row {
				b.WriteString(pad("", p.x-textWidth(b.String())))
				b.WriteString(m.th.button(labels[p.act], btnInactive))
			}
			l.text[i] = b.String()
		}
	}
	return l
}

// barRowsAt draws the laid-out bar from row y on, recording its buttons.
func (m *homeScreen) barRowsAt(l barLayout, y int) []string {
	out := make([]string, 0, len(l.rows))
	for i, row := range l.rows {
		if !m.modal() {
			for _, b := range row {
				m.zones.add(rect{b.x, y + i, textWidth(b.text), 1}, target{act: b.act})
			}
		}
		out = append(out, l.text[i])
	}
	return out
}

func (m *homeScreen) wideHome(w, h int) []string {
	lw := (w - 1) / 2 // left text, then "│", then the right column
	rw := w - 1 - lw
	body := h - 5 // header, two rules, the status line and the bar
	bar := m.barLayout(w, 1)
	v := m.th.paint(lookFrame, "│")

	out := []string{m.headerLine(w, true)}
	if m.modal() {
		out = append(out, m.th.paint(lookFrame, strings.Repeat("─", w)))
		out = append(out, m.dialogRows(0, len(out), w, body)...)
	} else {
		// The right column: the card, HEALTH and RECENT, each as long as
		// the budget allows; the card comes first.
		health, recent := m.healthLines(), m.recentLines()
		m.lines = append(append([]homeLine{}, health...), recent...)
		card := m.cardLines(rw - 1)
		cardRows := min(len(card), max(body-2-len(health)-1, 1))
		recentRows := min(len(recent), 3, max(body-cardRows-2-len(health), 0))
		healthRows := min(len(health), max(body-cardRows-2-recentRows, 0))

		phasesFocused := m.focus == homePhases
		out = append(out, m.rule("─", m.regionTitle("PHASES", phasesFocused), lw)+m.th.paint(lookFrame, "┬")+m.rule("─", m.th.paint(lookTitle, "NOW"), rw))
		left := m.phasePane(0, len(out), lw-1, body, w >= homeBarsFrom)
		var right []string
		for _, l := range card[:cardRows] {
			right = append(right, v+" "+pad(l, rw-1))
		}
		linesFocused := m.focus == homeLines
		y := len(out) + len(right) + 1
		right = append(right, m.rule("├─", m.regionTitle("HEALTH", linesFocused && m.lineSel < len(health)), rw+1))
		for _, l := range m.lineRows(0, health[:healthRows], lw+2, y, rw-1) {
			right = append(right, v+" "+pad(l, rw-1))
		}
		y = len(out) + len(right) + 1
		right = append(right, m.rule("├─", m.regionTitle("RECENT", linesFocused && m.lineSel >= len(health)), rw+1))
		for _, l := range m.lineRows(len(health), recent[:recentRows], lw+2, y, rw-1) {
			right = append(right, v+" "+pad(l, rw-1))
		}
		for i := range body {
			r := line(right, i)
			if r == "" {
				r = v
			}
			out = append(out, pad(line(left, i), lw)+r)
		}
	}
	join := "┴"
	if m.modal() {
		join = "─" // no columns under the dialog
	}
	out = append(out, m.th.paint(lookFrame, strings.Repeat("─", lw)+join+strings.Repeat("─", rw)))
	out = append(out, m.statusLine(w))
	return append(out, m.barRowsAt(bar, len(out))...)
}

// dialogRows fills rows lines below row y with the More… dialog, centered,
// and records its options.
func (m *homeScreen) dialogRows(x, y, w, rows int) []string {
	box, z := m.dialog.render(m.th, min(w-2, 72), rows)
	top := max((rows-len(box))/2, 0)
	left := max((w-textWidth(box[0]))/2, 0)
	m.zones.merge(z, x+left, y+top)
	out := make([]string, 0, rows)
	for i := range rows {
		l := ""
		if i >= top && i-top < len(box) {
			l = strings.Repeat(" ", left) + box[i-top]
		}
		out = append(out, fit(l, w))
	}
	return out
}

func (m *homeScreen) narrowHome(w, h int) []string {
	if m.modal() {
		box, z := m.dialog.render(m.th, w, h)
		m.zones.merge(z, 0, 0)
		for i := range box {
			box[i] = fit(box[i], w)
		}
		return box[:min(len(box), h)]
	}
	bar := m.barLayout(w, barRows)
	// The header, four rules, the status line and the bar are fixed; the
	// card, the phases and the lines share the rest.
	rest := max(h-1-4-1-len(bar.rows), 0)
	health, recent := m.healthLines(), m.recentLines()
	m.lines = append(append([]homeLine{}, health...), recent...)
	small := rest < 14
	healthRows, recentRows := len(health), min(len(recent), 2)
	if small {
		healthRows, recentRows = 1, min(len(recent), 1)
	}
	card := m.cardLines(w)
	cardRows := min(len(card), max(rest-healthRows-recentRows-3, 2))
	phaseRows := max(rest-cardRows-healthRows-recentRows, 0)

	out := []string{m.headerLine(w, false)}
	out = append(out, m.rule("──", m.th.paint(lookTitle, "NOW"), w))
	out = append(out, card[:cardRows]...)
	out = append(out, m.rule("──", m.regionTitle("PHASES", m.focus == homePhases), w))
	phases := m.phasePane(0, len(out), w, phaseRows, w >= homeBarsFrom)
	out = append(out, phases...)
	for range phaseRows - len(phases) {
		out = append(out, "")
	}
	linesFocused := m.focus == homeLines
	out = append(out, m.rule("──", m.regionTitle("HEALTH", linesFocused && m.lineSel < len(health)), w))
	if small {
		out = append(out, m.joinedLines(0, health, len(out), w))
	} else {
		out = append(out, m.lineRows(0, health, 0, len(out), w)...)
	}
	out = append(out, m.rule("──", m.regionTitle("RECENT", linesFocused && m.lineSel >= len(health)), w))
	out = append(out, m.lineRows(len(health), recent[:recentRows], 0, len(out), w)...)
	for len(out) < h-1-len(bar.rows) {
		out = append(out, "")
	}
	out = append(out, m.statusLine(w))
	return append(out, m.barRowsAt(bar, len(out))...)
}

// joinedLines draws lines side by side on one row at y, w cells, each its
// own click target; the selected one is marked.
func (m *homeScreen) joinedLines(first int, lines []homeLine, y, w int) string {
	var b strings.Builder
	x := 0
	for i, l := range lines {
		idx := first + i
		short := l.short
		if short == "" {
			short = l.text
		}
		text := fit(short, max(w-x, 0))
		if i > 0 {
			if x+2+textWidth(text) > w {
				break
			}
			b.WriteString("  ")
			x += 2
		}
		if l.act != actNone {
			m.zones.add(rect{x, y, textWidth(text), 1}, target{act: actLine, option: idx})
		}
		switch {
		case m.focus == homeLines && idx == m.lineSel:
			b.WriteString(m.th.paint(lookFocus, text))
		case l.act == actNone:
			b.WriteString(m.th.paint(lookDim, text))
		default:
			b.WriteString(m.paintLine(text))
		}
		x += textWidth(text)
	}
	return fit(b.String(), w)
}
