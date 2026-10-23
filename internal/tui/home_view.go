package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/plan"
)

// The home layouts (SPEC §15.6), open screens (chrome.go). Wide
// (≥ 100×13) puts PHASES left and the NOW card, HEALTH and RECENT right:
//
//	igris · sinjal · tasks.md · herdr ✓ · mode auto                          Idle
//	──────────────────────────────────────────────────────────────────────────────
//	PHASES                                  │ READY · phase M2  Config & state
//	 ✓ M0  Foundations  ██████████ 3/3 done │ next  · M2-02  Lock file       opus
//	                                        │ ── HEALTH ─────────────────────────
//	                                        │ ── RECENT ─────────────────────────
//	──────────────────────────────────────────────────────────────────────────────
//	✓ status line                                                           09:41
//	[›a Arise…‹] [v Preview] … [?] [q Quit]
//
// Narrow is one column: the card, PHASES, HEALTH, RECENT, the status line
// and the bar, which folds into More… when it needs more than two rows.

// homeWideH is the least height the wide layout needs.
const homeWideH = 13

// homeBarWidth is the progress bar's width; below homeBarsFrom columns the
// bars are dropped and the numbers stay.
const (
	homeBarWidth = 10
	homeBarsFrom = 60
)

// View draws the screen, exactly h lines, and records its zones; a dialog
// floats over the dimmed screen when wide and takes the whole screen when
// narrow (SPEC §15.5).
func (m *homeScreen) View() string {
	m.zones.reset()
	w, h := max(m.w, 10), max(m.h, 3)
	var out []string
	wide := w >= wideWidth && h >= homeWideH
	if wide {
		out = m.wideHome(w, h)
	} else {
		out = m.narrowHome(w, h)
	}
	for len(out) < h {
		out = append(out, "")
	}
	out = out[:h]
	if m.dialog != nil {
		m.zones.reset() // what is under the dialog takes no clicks
		var z zones
		if wide {
			out, z = m.th.floatDialog(out, m.dialog, w, 2, h-chromeRows-1)
		} else {
			out, z = m.th.fullScreenDialog(m.dialog, w, h)
		}
		m.zones.merge(z, 0, 0)
	}
	return strings.Join(out, "\n")
}

// modal reports whether a dialog holds the focus.
func (m *homeScreen) modal() bool { return m.dialog != nil }

// headerLine is the header: the wordmark, the project, the plan file, the
// backend and the mode (narrow: the project and the backend), the status
// word at the right.
func (m *homeScreen) headerLine(w int, wide bool) string {
	word, l := m.stateWord()
	return m.th.header(w, m.header(wide), m.th.paint(l, word))
}

// regionTitle is a region's title, marked while it has the focus.
func (m *homeScreen) regionTitle(name string, focused bool) string {
	if focused && !m.modal() {
		return m.th.paint(lookAccentBold, "› "+name)
	}
	return m.th.paint(lookTitle, name)
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

// fitBar lays the bar out in at most rows rows, w cells, records which
// buttons are on it and marks the focused one. The state's own action
// (the first button) is never folded into More….
func (m *homeScreen) fitBar(w, rows int) barLayout {
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
	l := layoutBar(m.th, btns, w, rows, mark, m.modal(), order)
	if mark != actNone && !l.has(mark) {
		mark = l.rows[0][0].act
		l = layoutBar(m.th, btns, w, rows, mark, m.modal(), order)
	}
	m.bar = append(m.bar[:0], l.acts()...)
	if m.focus == homeBar && !m.modal() {
		m.barFocus = mark // under a dialog the focus stays where it was
	}
	m.folded = l.folded
	return l
}

func (m *homeScreen) wideHome(w, h int) []string {
	lw := (w - 3) / 2 // the left column; " │ " then the right one
	rw := columnsRight(lw, w)
	body := h - chromeRows - 1 // without the status line
	bar := m.fitBar(w, 1)

	out := []string{m.headerLine(w, true), m.th.rule(w, "")}
	// The right column: the card, HEALTH and RECENT, each as long as the
	// budget allows; the card comes first.
	health, recent := m.healthLines(), m.recentLines()
	m.lines = append(append([]homeLine{}, health...), recent...)
	card := m.cardLines(rw)
	cardRows := min(len(card), max(body-2-len(health)-1, 1))
	recentRows := min(len(recent), 3, max(body-cardRows-2-len(health), 0))
	healthRows := min(len(health), max(body-cardRows-2-recentRows, 0))

	left := append([]string{m.regionTitle("PHASES", m.focus == homePhases)}, m.phasePane(0, len(out)+1, lw, body-1, w >= homeBarsFrom)...)
	linesFocused := m.focus == homeLines
	right := append([]string{}, card[:cardRows]...)
	x := lw + 3
	right = append(right, m.th.rule(rw, m.regionTitle("HEALTH", linesFocused && m.lineSel < len(health))))
	right = append(right, m.lineRows(0, health[:healthRows], x, len(out)+len(right), rw)...)
	right = append(right, m.th.rule(rw, m.regionTitle("RECENT", linesFocused && m.lineSel >= len(health))))
	right = append(right, m.lineRows(len(health), recent[:recentRows], x, len(out)+len(right), rw)...)
	out = append(out, m.th.columns(left, right, lw, w, body, func(int) bool { return true })...)
	out = append(out, m.th.rule(w, ""))
	out = append(out, m.statusLine(w))
	bar.barZones(&m.zones, len(out), m.modal())
	return append(out, bar.text...)
}

func (m *homeScreen) narrowHome(w, h int) []string {
	bar := m.fitBar(w, barRows)
	// The header, its rule, three section rules, the status line and the
	// bar are fixed; the card, the phases and the lines share the rest.
	rest := max(h-2-3-1-len(bar.rows), 0)
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

	out := []string{m.headerLine(w, false), m.th.rule(w, "")}
	out = append(out, card[:cardRows]...)
	out = append(out, m.th.rule(w, m.regionTitle("PHASES", m.focus == homePhases)))
	phases := m.phasePane(0, len(out), w, phaseRows, w >= homeBarsFrom)
	out = append(out, phases...)
	for range phaseRows - len(phases) {
		out = append(out, "")
	}
	linesFocused := m.focus == homeLines
	out = append(out, m.th.rule(w, m.regionTitle("HEALTH", linesFocused && m.lineSel < len(health))))
	if small {
		out = append(out, m.joinedLines(0, health, len(out), w))
	} else {
		out = append(out, m.lineRows(0, health, 0, len(out), w)...)
	}
	out = append(out, m.th.rule(w, m.regionTitle("RECENT", linesFocused && m.lineSel >= len(health))))
	out = append(out, m.lineRows(len(health), recent[:recentRows], 0, len(out), w)...)
	for len(out) < h-1-len(bar.rows) {
		out = append(out, "")
	}
	out = append(out, m.statusLine(w))
	bar.barZones(&m.zones, len(out), m.modal())
	return append(out, bar.text...)
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
