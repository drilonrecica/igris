package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
)

// The home layouts (SPEC §15.6), open screens (chrome.go). Wide
// (≥ 100×13) puts PHASES left, as wide as its rows, and the NOW card,
// HEALTH and RECENT right; the divider ends where the longer column does:
//
//	igris · sinjal · tasks.md · herdr ✓ · mode auto                          Idle
//	──────────────────────────────────────────────────────────────────────────────
//	PHASES                                 │ READY · phase M2  Config & state
//	 ✓ M0  Foundations ██████████ 3/3 done │ next  · M2-02  Lock file  opus
//	                                       │ ── HEALTH ──────────────────────────
//	                                       │ ── RECENT ──────────────────────────
//	──────────────────────────────────────────────────────────────────────────────
//	[›a Arise…‹] [v Preview] … [?] [q Quit]
//
// Narrow is one column: the card, PHASES, HEALTH, RECENT and the bar,
// which folds into More… when it needs more than two rows. GET STARTED
// (no igris.toml, or no plan yet) has no phases to show: the helm and the
// stepper, then HEALTH. The last action's result takes a line over the
// bar while there is one.

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
	switch {
	case m.onboarding():
		out = m.getStartedHome(w, h, wide)
	case wide:
		out = m.wideHome(w, h)
	default:
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
		return "no phases: the plan is invalid"
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
	case strings.HasPrefix(text, look.GlyphFail+" "):
		return m.th.paint(lookFail, text)
	case strings.HasPrefix(text, look.GlyphWarn+" "):
		return m.th.paint(lookWarn, text)
	case strings.HasPrefix(text, glyphs[plan.Done]+" "):
		return m.th.paint(lookDim, text)
	}
	return text
}

// statusLine is the last action's result with the time it happened, as
// the lines it takes: none while there is none.
func (m *homeScreen) statusLine(w int) []string {
	if m.status == "" {
		return nil
	}
	at := m.statusAt.In(m.loc).Format("15:04")
	text := fit(m.status, max(w-textWidth(at)-2, 0))
	return []string{text + strings.Repeat(" ", max(w-textWidth(text)-textWidth(at), 1)) + m.th.paint(lookDim, at)}
}

// foot is the screen's last lines: the rule, the status line while there
// is one, and the bar, whose zones it records; y is the row the rule is
// on.
func (m *homeScreen) foot(bar barLayout, w, y int) []string {
	out := append([]string{m.th.rule(w, "")}, m.statusLine(w)...)
	bar.barZones(&m.zones, y+len(out), m.modal())
	return append(out, bar.text...)
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

// phaseWidth is the PHASES column's width on a w-cell screen: as wide as
// its longest row and no wider (SPEC §15.4), at most half the screen.
func (m *homeScreen) phaseWidth(w int) int {
	half := (w - 3) / 2
	rows := m.phaseRowsData()
	if len(rows) == 0 {
		return min(max(textWidth(m.noPhasesText()), homeMinLeft), half)
	}
	idW, titleW, countW := 0, 0, 0
	for _, r := range rows {
		idW, titleW, countW = max(idW, textWidth(r.id)), max(titleW, textWidth(r.title)), max(countW, textWidth(r.count))
	}
	titleW = max(titleW, 4) // phaseLines keeps a title at least 4 cells wide
	barW := 0
	if w >= homeBarsFrom {
		barW = homeBarWidth + 1
	}
	return min(max(4+idW+2+titleW+1+barW+countW+1+5, homeMinLeft), half) // as phaseLines lays a row out
}

// homeMinLeft is the narrowest the PHASES column gets.
const homeMinLeft = 24

func (m *homeScreen) wideHome(w, h int) []string {
	lw := m.phaseWidth(w) // the left column; " │ " then the right one
	rw := columnsRight(lw, w)
	body := h - chromeRows - len(m.statusLine(w))
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
	// The divider ends with the longer column: no empty right column.
	out = append(out, m.th.columns(left, right, lw, w, body, func(i int) bool { return i < len(left) })...)
	return append(out, m.foot(bar, w, len(out))...)
}

func (m *homeScreen) narrowHome(w, h int) []string {
	bar := m.fitBar(w, barRows)
	// The header, its rule, three section rules, the foot's rule, the
	// status line and the bar are fixed; the card, the phases and the
	// lines share the rest, and what is left over stays below them.
	status := len(m.statusLine(w))
	rest := max(h-2-3-1-status-len(bar.rows), 0)
	health, recent := m.healthLines(), m.recentLines()
	m.lines = append(append([]homeLine{}, health...), recent...)
	small := rest < 14
	healthRows, recentRows := len(health), min(len(recent), 3)
	if small {
		healthRows, recentRows = 1, min(len(recent), 1)
	}
	card := m.cardLines(w)
	cardRows := min(len(card), max(rest-healthRows-recentRows-3, 2))
	phaseRows := max(rest-cardRows-healthRows-recentRows, 0)

	out := []string{m.headerLine(w, false), m.th.rule(w, "")}
	out = append(out, card[:cardRows]...)
	out = append(out, m.th.rule(w, m.regionTitle("PHASES", m.focus == homePhases)))
	out = append(out, m.phasePane(0, len(out), w, phaseRows, w >= homeBarsFrom)...)
	linesFocused := m.focus == homeLines
	out = append(out, m.th.rule(w, m.regionTitle("HEALTH", linesFocused && m.lineSel < len(health))))
	if small {
		out = append(out, m.joinedLines(0, health, len(out), w))
	} else {
		out = append(out, m.lineRows(0, health, 0, len(out), w)...)
	}
	out = append(out, m.th.rule(w, m.regionTitle("RECENT", linesFocused && m.lineSel >= len(health))))
	out = append(out, m.lineRows(len(health), recent[:recentRows], 0, len(out), w)...)
	for len(out) < h-1-status-len(bar.rows) {
		out = append(out, "")
	}
	return append(out, m.foot(bar, w, len(out))...)
}

// getStartedHome is home before there is a plan to show (SPEC §15.6): the
// helm beside GET STARTED and its tagline (the helm dropped below
// look.HelmMinWidth columns), then HEALTH and, once there are runs,
// RECENT.
func (m *homeScreen) getStartedHome(w, h int, wide bool) []string {
	rows := barRows
	if wide {
		rows = 1
	}
	bar := m.fitBar(w, rows)
	health := m.healthLines()
	var recent []homeLine
	if m.snap != nil && len(m.snap.Recent) > 0 {
		recent = m.recentLines()
	}
	m.lines = append(append([]homeLine{}, health...), recent...)

	// The helm stands in a column of its own, 3 cells in and 6 from the
	// card.
	const helmIndent, helmGap = 3, 6
	var helm []string
	cw := w
	if w >= look.HelmMinWidth {
		helm = look.HelmLines(m.th.helmPart)
		cw = w - helmIndent - look.HelmWidth - helmGap
	}
	card := m.cardLines(cw)
	sections := 0
	for _, ls := range [][]homeLine{health, recent} {
		if len(ls) > 0 {
			sections += 1 + len(ls)
		}
	}
	room := max(h-2-sections-1-len(m.statusLine(w))-len(bar.rows), 1)
	if len(card) > room && len(card) > 3 && card[2] == "" {
		card = append(card[:2:2], card[3:]...) // the blank line goes first
	}
	card = card[:min(len(card), room)]

	out := []string{m.headerLine(w, wide), m.th.rule(w, "")}
	for i := range max(len(card), min(len(helm), room)) {
		if helm == nil {
			out = append(out, line(card, i))
			continue
		}
		left := strings.Repeat(" ", helmIndent+look.HelmWidth)
		if i < len(helm) {
			left = strings.Repeat(" ", helmIndent) + helm[i]
		}
		out = append(out, strings.TrimRight(left+strings.Repeat(" ", helmGap)+line(card, i), " "))
	}
	linesFocused := m.focus == homeLines
	if len(health) > 0 {
		out = append(out, m.th.rule(w, m.regionTitle("HEALTH", linesFocused && m.lineSel < len(health))))
		out = append(out, m.lineRows(0, health, 0, len(out), w)...)
	}
	if len(recent) > 0 {
		out = append(out, m.th.rule(w, m.regionTitle("RECENT", linesFocused && m.lineSel >= len(health))))
		out = append(out, m.lineRows(len(health), recent, 0, len(out), w)...)
	}
	for len(out) < h-1-len(m.statusLine(w))-len(bar.rows) {
		out = append(out, "")
	}
	return append(out, m.foot(bar, w, len(out))...)
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
