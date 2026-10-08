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
//	├─ LOG ──────────────────┴───────────────────────────┤
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

	v := m.th.paint(lookFrame, "│")
	var out []string
	out = append(out, m.topRule(w))
	switch body := paneRows + 1 + logRows; { // the panes, their rule and the log
	case m.dialog != nil:
		out = append(out, m.dialogBody(len(out), w, body)...)
	case m.page != nil:
		out = append(out, m.pageBody(len(out), w, body)...)
	default:
		m.zones.add(rect{2, len(out) + 1, lw, paneRows - 1}, target{region: regionTasks})
		left := m.taskPane(2, len(out)+1, lw, paneRows-1)
		right := m.card(lw+5, len(out)+1, rw, paneRows-1)
		out = append(out, v+" "+pad(m.areaTitle("TASKS", focusTasks), lw)+" "+v+" "+pad(m.th.paint(lookTitle, "CURRENT"), rw)+" "+v)
		for i := range paneRows - 1 {
			out = append(out, v+" "+pad(line(left, i), lw)+" "+v+" "+pad(line(right, i), rw)+" "+v)
		}
		out = append(out, m.logRule(lw, rw))
		m.zones.add(rect{2, len(out), inner, logRows}, target{region: regionLog})
		logs := m.logView(logRows)
		for i := range logRows {
			out = append(out, v+" "+pad(fit(line(logs, i), inner), inner)+" "+v)
		}
	}
	out = append(out, m.th.paint(lookFrame, "├"+strings.Repeat("─", w-2)+"┤"))
	out = append(out, v+" "+pad(m.barAt(2, len(out), inner), inner)+" "+v)
	out = append(out, m.th.paint(lookFrame, "└"+strings.Repeat("─", w-2)+"┘"))
	return strings.Join(out, "\n")
}

// logRule is the rule between the panes and the log; it carries the log's
// title: "├─ LOG ────┴────┤".
func (m *model) logRule(lw, rw int) string {
	title := m.areaTitle("LOG", focusLog)
	rest := []rune(strings.Repeat("─", lw+2) + "┴" + strings.Repeat("─", rw+2) + "┤")
	used := 1 + 1 + textWidth(title) + 1 // "─ " + title + " "
	return m.th.paint(lookFrame, "├─") + " " + title + " " + m.th.paint(lookFrame, string(rest[used:]))
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
	title := " " + m.th.paint(lookAccentBold, "igris") + " · " + m.opts.Project + " "
	facts := ""
	for level := range progressLevels {
		facts = " " + m.facts(false, level) + " "
		if w-2-textWidth(title)-textWidth(facts) >= 1 {
			break
		}
	}
	fill := w - 2 - textWidth(title) - textWidth(facts)
	if fill < 1 {
		facts = " " + fit(strings.TrimSpace(facts), max(w-4-textWidth(title)-1, 0)) + " "
		fill = max(w-2-textWidth(title)-textWidth(facts), 0)
	}
	return fit(m.th.paint(lookFrame, "┌")+title+m.th.paint(lookFrame, strings.Repeat("─", fill))+facts+m.th.paint(lookFrame, "┐"), w)
}

// facts are the header's run facts: phase and its progress, mode,
// backend, pause. The narrow status bar has the shorter bar and, next to
// the progress, the phase without "phase"; level says how much of the
// progress to show.
func (m *model) facts(narrow bool, level int) string {
	var parts []string
	if m.phase != "" {
		cells := wideProgressBar
		if narrow {
			cells = narrowProgressBar
		}
		switch p := m.progressText(level, cells); {
		case p == "":
			parts = append(parts, "phase "+m.phase)
		case narrow:
			parts = append(parts, m.phase, p) // "M1 · 7/12 · 58% ██████"
		default:
			parts = append(parts, "phase "+m.phase, p)
		}
	}
	if m.mode != "" {
		parts = append(parts, m.th.marks("mode: "+m.mode+badge(m.mode)))
	}
	if m.mode != engine.ModeYolo && m.cur != nil && m.cur.mode == engine.ModeYolo {
		// The run mode isn't yolo but this session is (SPEC §7.3).
		parts = append(parts, m.th.paint(lookAlert, "SKIP PERMISSIONS"))
	}
	parts = append(parts, m.opts.Backend)
	if m.paused {
		parts = append(parts, m.th.paint(lookTitle, "PAUSE AFTER TASK"))
	}
	if m.notice != "" {
		parts = append(parts, m.th.paint(lookAccent, m.notice))
	}
	return strings.Join(parts, " · ")
}

// phaseTasks are the tasks of the phase being run.
func (m *model) phaseTasks() []*plan.Task { return m.rows().tasks() }

// taskLines renders the task list w cells wide (see rows.lines), with the
// selection shown while the list has the focus.
func (m *model) taskLines(w int) (lines []string, follow int, owner []int) {
	return m.rows().lines(w, m.listSel())
}

// taskPane is the visible part of the task list drawn at (x, y): it
// follows the current task unless the owner scrolled. Each task row is
// recorded as a click target.
func (m *model) taskPane(x, y, w, rows int) []string {
	lines, cur, owner := m.taskLines(w)
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
	end := min(top+rows, len(lines))
	for i := top; i < end; i++ {
		if owner[i] >= 0 && !m.modal() {
			m.zones.add(rect{x, y + i - top, w, 1}, target{act: actTaskRow, option: owner[i]})
		}
	}
	return lines[top:end]
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
	tasks, _, _ := m.taskLines(lw)
	return 1 + max(len(tasks), len(m.cardLines(rw, 0, 0, false)))
}

// card renders the current-task card at (x, y), rows lines of w cells, and
// records its zones.
func (m *model) card(x, y, w, rows int) []string {
	return m.renderCard(w, x, y, rows, true)
}

// cardLines renders the whole card, its full text included; with record
// set, its zones are recorded for a card drawn at (x, y).
func (m *model) cardLines(w, x, y int, record bool) []string {
	return m.renderCard(w, x, y, -1, record)
}

// yourTurnText follows YOUR TURN: a user task is the owner's own work,
// and how it ends. Each part wraps on its own, so the keys stay together.
var yourTurnText = []string{"Yours to do outside igris (no session).", "Then: d done · s skip"}

// openHint follows NEEDS YOU when the session can be opened.
const openHint = " — o opens the session"

// moreText ends the task text when the card cut it.
const moreText = " … t: details"

// renderCard lays the card out in at most rows lines (all of them when
// rows < 0). The head (title, facts, state) and the buttons come first,
// then the live tail; the task's text gets the rows left over and is cut
// with moreText.
func (m *model) renderCard(w, x, y, rows int, record bool) []string {
	var lines []string
	switch {
	case m.ended:
		// The run is over; a task it was on stays as the plan says.
		if m.opts.Leave != nil {
			lines = wrap(m.endText+" — press q to go home", w)
		} else {
			lines = wrap(m.endText+" — press q to quit", w)
		}
	case m.cur == nil && m.holding:
		lines = []string{"paused before the next task", "press p to continue"}
	case m.cur == nil:
		lines = []string{"no task running"}
	}
	if m.cur == nil || m.ended {
		if rows >= 0 && len(lines) > rows {
			lines = lines[:rows]
		}
		return lines
	}

	c := m.cur
	head := []string{m.th.paint(lookTitle, fit(c.id+" "+c.title, w))}
	if c.user {
		head = append(head, "user task · "+since(m.opts.Now(), c.started))
	} else {
		head = append(head, fit("rank "+m.th.rank(c.rank, false)+" → model "+c.model, w))
		timing := since(m.opts.Now(), c.started)
		if eta := m.eta(c); eta != "" {
			timing += " · " + eta
		}
		head = append(head, fit(m.th.marks("mode "+c.mode+badge(c.mode)+" · "+timing), w))
	}
	state := "state: " + m.th.paint(m.stateLook(), m.stateText())
	if c.state == stateNeedsYou && c.session != nil {
		// Say how to get there, on the state line when it fits.
		hint := m.th.paint(m.stateLook(), openHint)
		if textWidth(state)+textWidth(openHint) <= w {
			state += hint
		} else {
			head = append(head, fit(state, w))
			state = m.th.paint(m.stateLook(), strings.TrimPrefix(openHint, " — "))
		}
	}
	head = append(head, fit(state, w))
	if c.overdue != "" {
		// Until the attempt ends (SPEC §6.3), in words, not color alone.
		head = append(head, fit(m.th.paint(lookAlert, "OVERDUE: "+c.overdue), w))
	}
	if c.state == stateYourTurn {
		// A user task has no session: say whose it is and how it ends.
		for _, part := range yourTurnText {
			for _, l := range wrap(part, w) {
				head = append(head, m.th.paint(m.stateLook(), l))
			}
		}
	}
	if c.state == stateNeedsYou || c.state == stateLost {
		head = append(head, fit(c.detail, w))
	}

	tail := m.tailView(w)

	// The task's text: for a user task the engine sends it with Your turn;
	// otherwise it comes from the plan as last loaded.
	a := ""
	if c.state == stateYourTurn && c.detail != "" {
		a = aboutText(c.detail, c.title)
	} else if t := m.rows().task(c.id); t != nil {
		a = about(t)
	}
	var text []string
	if a != "" {
		text = wrap(a, w)
	}

	btns := []option{{"[t] Details", actDetails}}
	if c.user {
		btns = append([]option{{"[d] Done…", actDone}, {"[s] Skip…", actSkip}}, btns...)
	}
	if c.session != nil {
		btns = append([]option{{"[o] Open session", actOpen}}, btns...)
	}
	if m.question() != nil && m.dialog == nil {
		btns = append(btns, option{"[Answer…]", actAnswer})
	}
	btnLines, btnZones := m.cardButtons(btns, w)

	if rows >= 0 {
		free := max(rows-len(head)-len(btnLines), 0)
		// The text gives way first, then the tail's oldest lines.
		tail = tail[len(tail)-min(len(tail), free):]
		if free -= len(tail); free < len(text) {
			text = m.cutText(text, free, w)
		}
		if over := len(head) + len(tail) + len(text) + len(btnLines) - rows; over > 0 {
			head = head[:max(len(head)-over, 0)]
		}
	}
	if record && len(head) > 0 {
		m.zones.add(rect{x, y, min(textWidth(c.id+" "+c.title), w), 1}, target{act: actDetails})
	}
	out := append(append(append(head, tail...), text...), btnLines...)
	if record {
		top := y + len(head) + len(tail) + len(text)
		for _, z := range btnZones {
			if z.r.y < len(btnLines) {
				m.zones.add(rect{x + z.r.x, top + z.r.y, z.r.w, 1}, z.t)
			}
		}
	}
	if rows >= 0 && len(out) > rows {
		out = out[:rows]
	}
	return out
}

// cutText keeps the first n lines of text, the last one ending in
// moreText, so the owner knows there is more and how to see it.
func (m *model) cutText(text []string, n, w int) []string {
	if n <= 0 {
		return nil
	}
	text = append([]string{}, text[:n]...)
	last := text[n-1]
	if room := w - textWidth(moreText); textWidth(last) > room {
		last = strings.TrimSuffix(fit(last, max(room, 0)), "…")
	}
	text[n-1] = fit(last+m.th.paint(lookDim, moreText), w)
	return text
}

// cardButtons lays the card's buttons out on as few lines of w cells as
// they fit, and returns their zones relative to the first of those lines.
func (m *model) cardButtons(btns []option, w int) ([]string, []zone) {
	var lines []string
	var zs []zone
	line, used := "", 0
	for _, b := range btns {
		lw := min(textWidth(b.label), w)
		if used > 0 && used+2+lw > w {
			lines, line, used = append(lines, line), "", 0
		}
		if used > 0 {
			line += "  "
			used += 2
		}
		l := lookAccent
		if b.act == actAnswer || (b.act == actOpen && m.cur.state == stateNeedsYou) {
			l = lookAccentBold // it answers what igris waits for
		}
		zs = append(zs, zone{rect{used, len(lines), lw, 1}, target{act: b.act}})
		line += m.th.paint(l, fit(b.label, w))
		used += lw
	}
	if used > 0 {
		lines = append(lines, line)
	}
	return lines, zs
}

// pageBody fills rows lines below row y with the open page and records
// its zones.
func (m *model) pageBody(y, w, rows int) []string {
	inner := w - 4
	lines, z := m.page.render(m.th, inner, rows)
	m.zones.merge(z, 2, y)
	v := m.th.paint(lookFrame, "│")
	out := make([]string, 0, rows)
	for _, l := range lines {
		out = append(out, v+" "+pad(l, inner)+" "+v)
	}
	return out
}

// dialogBody fills rows lines below row y with the open dialog, centered,
// and records its options.
func (m *model) dialogBody(y, w, rows int) []string {
	box, z := m.dialog.render(m.th, min(w-4, 72), rows)
	top := max((rows-len(box))/2, 0)
	left := max((w-textWidth(box[0]))/2, 1)
	m.zones.merge(z, left, y+top)
	v := m.th.paint(lookFrame, "│")
	out := make([]string, 0, rows)
	for i := range rows {
		l := ""
		if i >= top && i-top < len(box) {
			l = strings.Repeat(" ", left-1) + box[i-top]
		}
		out = append(out, v+pad(fit(l, w-2), w-2)+v)
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
		label := m.th.button(o.label, barState(o.act, mark, m.modal()))
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
