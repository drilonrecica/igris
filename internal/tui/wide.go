package tui

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// The wide layout (SPEC §15.1), an open screen (chrome.go): the header
// and its rule, TASKS beside the cards, divided by one line, the LOG rule,
// the log, a rule and the action bar.
//
//	igris · sinjal      phase M0 · 3/7 · 42% ████░░░░░░ · mode plan · herdr    ● Working
//	──────────────────────────────────────────────────────────────────────────────────
//	TASKS                          │ ┌─ M0-03  Makefile ─────────────────────────────┐
//	 ✓ M0-01  Go module     sonnet │ │ rank sonnet → model sonnet                    │
//	── LOG ───────────────────────────────────────────────────────────────────────────
//	09:41 M0-02 done
//	──────────────────────────────────────────────────────────────────────────────────
//	[o Open session] [m Mode] [p Pause] [d Done] [?] [q Home]

// wideMinHeight is the least height the wide layout needs: the chrome,
// the TASKS title plus three rows, and three log lines.
const wideMinHeight = 13

// wideLogMin is the least the log keeps when the panes want the height
// (SPEC §15.1): the panes are sized by their content and LOG takes what
// is left under the cards.
const wideLogMin = 4

// chromeRows is what the open screen's chrome takes: the header, its
// rule, the rule over the bar and the bar.
const chromeRows = 4

// The TASKS column is as wide as its longest row (SPEC §15.1), within
// these bounds: never narrower than its title and a short row, never past
// half the screen, so the cards keep the wider part.
const (
	tasksMinWidth = 24
	tasksMaxShare = 2 // of the width
)

// wideScreen draws the wide layout's h lines of w cells and records its
// zones; a page or a dialog is drawn over it by View.
func (m *model) wideScreen(w, h int) []string {
	lw := min(max(m.rows().natural(), tasksMinWidth), (w-3)/tasksMaxShare) // the left column; " │ " then the right one
	rw := columnsRight(lw, w)
	avail := h - chromeRows - 1                                           // the panes and the log, without the LOG rule
	paneRows := min(max(m.paneNeed(lw, rw), 4), max(avail-wideLogMin, 4)) // incl. the TASKS title
	logRows := max(avail-paneRows, 0)

	out := []string{m.header(w), m.th.rule(w, "")}
	m.zones.add(rect{0, len(out) + 1, lw, paneRows - 1}, target{region: regionTasks})
	left := append([]string{m.areaTitle("TASKS", focusTasks)}, m.taskPane(0, len(out)+1, lw, paneRows-1)...)
	right := m.rightPane(lw+3, len(out), rw, paneRows)
	out = append(out, m.th.columns(left, right, lw, w, paneRows, func(int) bool { return true })...)
	out = append(out, m.th.rule(w, m.areaTitle("LOG", focusLog)))
	m.zones.add(rect{0, len(out), w, logRows}, target{region: regionLog})
	logs := m.logView(logRows)
	for i := range logRows {
		out = append(out, fit(line(logs, i), w))
	}
	out = append(out, m.th.rule(w, ""))
	bar := m.fitBar(w, 1)
	bar.barZones(&m.zones, len(out), m.modal())
	return append(out, bar.text...)
}

// line returns lines[i], or "" past the end.
func line(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

// rightPane is the wide layout's right column at (x, y), rows lines of w
// cells: the Needs-you banner while anything waits on the owner, then the
// cards.
func (m *model) rightPane(x, y, w, rows int) []string {
	banner, ok := m.banner(x, y, w)
	if !ok {
		return m.cardPane(x, y, w, rows)
	}
	return append([]string{banner}, m.cardPane(x, y+1, w, rows-1)...)
}

// bannerRows is what the banner takes when it shows.
func (m *model) bannerRows() int {
	if _, ok := m.bannerTask(); ok {
		return 1
	}
	return 0
}

// bannerTask is the task the Needs-you banner names: the first in plan
// order that needs you (SPEC §15.1), else the task of the first pending
// question; "" and false while nothing waits on the owner.
func (m *model) bannerTask() (string, bool) {
	if m.ended {
		return "", false
	}
	if ids := m.needYou(); len(ids) > 0 {
		return ids[0], true
	}
	if q := m.question(); q != nil {
		return q.Task, true
	}
	return "", false
}

// banner draws the Needs-you banner w cells wide at (x, y) and records
// its zones: `! M1-04 needs you · idle without a signal`, `· 2 tasks need
// you` with more, and `[Answer…]` at the right while a question waits.
func (m *model) banner(x, y, w int) (string, bool) {
	id, ok := m.bannerTask()
	if !ok {
		return "", false
	}
	kind := engine.Question("").Kind()
	if c := m.card(id); c != nil {
		kind = m.needKind(c)
	} else if q := m.question(); q != nil {
		kind = q.Question.Kind()
	}
	// A question of the run (a budget, the batch question) names no task.
	text := strings.TrimSpace(look.GlyphNeedsYou+" "+id) + " needs you · " + kind
	n := len(m.needYou())
	for _, q := range m.asks {
		if m.card(q.Task) == nil {
			n++ // a question for a task no card shows
		}
	}
	if n > 1 {
		text += " · " + look.Count(n, "task needs", "tasks need") + " you"
	}
	btn := ""
	if m.question() != nil && m.dialog == nil {
		btn = "[Answer…]"
	}
	room := w
	if btn != "" {
		room -= textWidth(btn) + 2
	}
	line := m.th.paint(lookAlert, fit(text, max(room, 1)))
	if btn != "" {
		gap := max(w-textWidth(line)-textWidth(btn), 1)
		if !m.modal() {
			m.zones.add(rect{x + textWidth(line) + gap, y, textWidth(btn), 1}, target{act: actAnswer})
		}
		line += strings.Repeat(" ", gap) + m.th.paint(lookAccentBold, btn)
	}
	return fit(line, w), true
}

// header is the run view's header line (SPEC §15.1): the project, the
// run's facts and the status word at the right. The side count goes
// first when the width runs out, then the quota, then the progress bar
// and the percentage; the project, the phase and the word never drop.
func (m *model) header(w int) string {
	status := m.statusText()
	narrow := !m.wide()
	facts := ""
	for _, d := range factsDrops {
		if facts = m.facts(narrow, d); textWidth(m.opts.Project+" · "+facts) <= headerRoom(w, status) {
			break
		}
	}
	return m.th.header(w, m.opts.Project+" · "+facts, status)
}

// factsDrop says which of the header's facts to leave out: how much of
// the progress to show (progressLevels), which of the quota and the side
// count to add, and whether the backend and the mode go.
type factsDrop struct {
	level, extras int
	noBackend     bool
	noMode        bool
}

// factsDrops is the order the header drops its facts in when the width
// runs out (SPEC §15.1): side sessions, the quota, the progress bar, the
// percentage, the backend, the mode. The project and the phase never go.
var factsDrops = []factsDrop{
	{progressFull, factsAll, false, false},
	{progressFull, factsQuota, false, false},
	{progressFull, factsNone, false, false},
	{progressNoBar, factsNone, false, false},
	{progressCount, factsNone, false, false},
	{progressCount, factsNone, true, false},
	{progressCount, factsNone, true, true},
}

// The header's facts that go when the width runs out, in this order
// (SPEC §15.1): facts(…, factsAll) has them all.
const (
	factsNone  = iota // neither
	factsQuota        // the subscription quota
	factsAll          // the quota and the side session count
)

// facts are the header's run facts: phase and its progress, mode,
// backend, side sessions, the quota. The narrow header has the shorter
// bar and, next to the progress, the phase without "phase"; d says what
// to leave out.
func (m *model) facts(narrow bool, d factsDrop) string {
	level, extras := d.level, d.extras
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
	if m.multi() && !m.ended {
		// Several active tasks (SPEC §15.1): how many; the Needs-you queue
		// is the banner's.
		parts = append(parts, fmt.Sprintf("%d active", len(m.cards)))
	}
	if m.mode != "" && !d.noMode && !m.ended {
		parts = append(parts, m.th.marks("mode "+m.mode+badge(m.mode)))
	}
	if !engine.SkipsPermissions(m.mode) && slices.ContainsFunc(m.cards, func(c *current) bool { return engine.SkipsPermissions(c.mode) }) {
		// The run mode isn't yolo but a session is (SPEC §7.3).
		parts = append(parts, m.th.paint(lookAlert, skipBadge))
	}
	switch {
	case d.noBackend:
	default:
		parts = append(parts, m.backendName())
	}
	if m.servedBy != "" {
		parts = append(parts, "served by "+m.servedBy)
	}
	if m.lost {
		parts = append(parts, m.th.paint(lookWarn, look.GlyphWarn+" reconnecting…"))
	}
	if n := m.side.count(); n > 0 && extras >= factsAll {
		parts = append(parts, fmt.Sprintf("side %d", n))
	}
	if q := m.quotaText(); extras >= factsQuota && q != "" {
		parts = append(parts, q)
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

// paneNeed is how many rows the panes want: the TASKS title and its rows,
// or the banner and the cards.
func (m *model) paneNeed(lw, rw int) int {
	tasks, _, _ := m.taskLines(lw)
	return max(1+len(tasks), m.bannerRows()+m.cardNeed(rw))
}

// cardPane renders the cards at (x, y), rows lines of w cells, and
// records their zones: every card stacked when wide (SPEC §15.1), the
// selected card alone when narrow; without a task, or once the run has
// ended, the words that say so.
func (m *model) cardPane(x, y, w, rows int) []string {
	switch {
	case m.cur == nil || m.ended:
		return m.renderCard(w, rows)
	case m.wide():
		return m.cardsView(x, y, w, rows)
	}
	return m.drawCard(m.cur, x, y, w, rows)
}

// cardNeed is how many rows cardPane wants for w cells.
func (m *model) cardNeed(w int) int {
	switch {
	case m.cur == nil || m.ended:
		return len(m.renderCard(w, -1))
	case m.wide():
		return m.cardsNeed(w)
	}
	return 2 + len(m.cardBody(m.cur, cardInner(w), 0, 0, -1, false, true))
}

// yourTurnText follows the "Needs you · your turn" state line: a user task is the owner's own work,
// and how it ends. Each part wraps on its own, so the keys stay together.
var yourTurnText = []string{"Yours to do outside igris (no session).", "Then: d done · s skip"}

// openHint follows the Needs you state when the session can be opened.
const openHint = " · o opens the session"

// moreText ends the task text when the card cut it.
const moreText = " … t: details"

// cardTextMax is the most lines of the task's text a card shows, however
// much room it has (SPEC §15.1): the card is a glance, Details the whole.
const cardTextMax = 6

// renderCard says why there is no card: the run is over (the summary
// screen says how, View), or no task runs; at most rows lines (all of
// them when rows < 0).
func (m *model) renderCard(w, rows int) []string {
	var lines []string
	switch {
	case m.ended:
		lines = wrap(m.endText, w)
	case m.holding:
		lines = []string{"paused before the next task", "press p to continue"}
	case m.limited != nil:
		// The usage hold (SPEC §6.9): what holds, until when, and the task
		// that would start.
		lines = []string{m.limitText("resumes"), "a: start anyway · p: pause · x: stop"}
		if m.heldTask != "" {
			lines = append([]string{"held before " + m.heldTask}, lines...)
		}
	case m.budgetAsk != nil:
		// A budget reached holds new starts (SPEC §6.9).
		title := "a budget is reached"
		if b := m.budgetAsk.Budget; b != nil {
			title = b.Title()
		}
		lines = []string{"held: " + title, "Answer…: raise it, continue or stop"}
	default:
		lines = []string{"no task running"}
	}
	if !m.ended {
		lines = append(lines, m.budgetLines(w)...)
	}
	if rows >= 0 && len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}

// cardBody lays c's card out in at most rows lines (all of them when
// rows < 0). The head leads with the state line (SPEC §15.1: `● Working ·
// 4m12s · ≈ 14m left`), then the facts (`sonnet → claude-sonnet-5-5 ·
// mode plan`), the usage and the warnings; the buttons come last, the
// live tail before them; the task's text gets the rows left over and is
// cut with moreText. boxed leaves the title out: the box's border carries
// it.
func (m *model) cardBody(c *current, w, x, y, rows int, record, boxed bool) []string {
	var head []string
	if !boxed {
		head = append(head, m.th.paint(lookTitle, fit(m.cardTitle(c), w)))
	}
	// The elapsed time and the ETA go on the state line while the task
	// works; a state of its own (idle 45s) keeps them on the facts line.
	timing := since(m.opts.Now(), c.started)
	if eta := m.eta(c); eta != "" {
		timing += " · " + eta
	}
	onState := c.state == stateWorking || c.state == stateVerifying
	state := m.th.paint(stateLook(c), m.stateText(c))
	if onState {
		state += m.th.paint(lookPlain, " · "+timing)
	}
	stateAt := len(head) // the state's line, which a tight box keeps longest
	if c.state == stateNeedsYou {
		// Each reason stays whole: the line breaks between them rather
		// than cut one off (needParts).
		lines := packParts(append([]string{look.NeedsYou.Label()}, m.needParts(c)...), w)
		for _, l := range lines[:len(lines)-1] {
			head = append(head, fit(m.th.paint(stateLook(c), l), w))
		}
		state = m.th.paint(stateLook(c), lines[len(lines)-1])
	}
	if c.state == stateNeedsYou && m.canOpen(c) {
		// Say how to get there, on the state line when it fits.
		hint := m.th.paint(stateLook(c), openHint)
		if textWidth(state)+textWidth(openHint) <= w {
			state += hint
		} else {
			head = append(head, fit(state, w))
			state = m.th.paint(stateLook(c), strings.TrimPrefix(openHint, " · "))
		}
	}
	head = append(head, fit(state, w))
	facts := "user task"
	if !c.user {
		facts = m.th.rank(c.rank, false) + " → " + c.model + m.th.marks(" · mode "+c.mode+badge(c.mode))
	}
	if !onState {
		facts += " · " + timing
	}
	if !m.wide() {
		if t := c.tokens; t != nil && !t.Unavailable {
			facts += " · " + look.InOutNarrow(t.In, t.Out) // the narrow card's usage (SPEC §15.2)
		}
	}
	head = append(head, fit(facts, w))
	if line := m.tokensLine(c, w); line != "" {
		head = append(head, line)
	}
	if len(m.cards) > 0 && m.cards[0] == c {
		head = append(head, m.budgetLines(w)...) // the run's, on its first card (SPEC §6.9)
	}
	if c.overdue != "" && c.state != stateNeedsYou {
		// Until the attempt ends (SPEC §6.3), in words, not color alone;
		// on the Needs-you line while the task needs the owner (needParts).
		head = append(head, fit(m.th.paint(lookWarn, look.GlyphWarn+" Overdue · "+c.overdue), w))
	}
	if c.rankMismatch != "" && c.state != stateNeedsYou {
		// Until the attempt ends (SPEC §6.9), like Overdue.
		head = append(head, fit(m.th.paint(lookWarn, look.GlyphWarn+" Rank mismatch · "+c.rankMismatch), w))
	}
	if c.state == stateYourTurn {
		// A user task has no session: say whose it is and how it ends.
		for _, part := range yourTurnText {
			for _, l := range wrap(part, w) {
				head = append(head, m.th.paint(stateLook(c), l))
			}
		}
	}
	switch _, rest := needWhat(c); {
	case c.state == stateNeedsYou && rest != "":
		head = append(head, fit(rest, w))
	case c.state == stateLost:
		head = append(head, fit(c.detail, w))
	}

	tail := m.tailView(c, w)

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
		if len(text) > cardTextMax {
			text = m.cutText(text, cardTextMax, w) // a card, not a page: Details has the rest
		}
	}

	// The buttons carry their keys as the bar's do (SPEC §15.1): what
	// answers igris's wait comes first.
	btns := []option{{"[t Details]", actDetails}}
	if c.user {
		btns = append([]option{{"[d Done…]", actDone}, {"[s Skip…]", actSkip}}, btns...)
	}
	if m.canOpen(c) {
		btns = append([]option{{"[o Open session]", actOpen}}, btns...)
	}
	// Alone, the card answers whatever waits; among several, its own.
	if m.dialog == nil && (m.cardAsk(c) != nil || (!m.multi() && m.question() != nil)) {
		btns = append([]option{{"[Answer…]", actAnswerCard}}, btns...)
	}
	btnLines, btnZones := m.cardButtons(c, btns, w)

	if rows >= 0 {
		free := max(rows-len(head)-len(btnLines), 0)
		// The text gives way first, then the tail's oldest lines.
		tail = tail[len(tail)-min(len(tail), free):]
		if free -= len(tail); free < len(text) {
			text = m.cutText(text, free, w)
		}
		if over := len(head) + len(tail) + len(text) + len(btnLines) - rows; over > 0 && boxed {
			// In a box the title is on the border: the facts go first,
			// then the buttons (the bar and the keys have them too), and
			// the state stays longest.
			kept := head[:0:0]
			for i, l := range head {
				if i != stateAt && over > 0 {
					over--
					continue
				}
				kept = append(kept, l)
			}
			head = kept
			btnLines = btnLines[:max(len(btnLines)-over, 0)]
		} else if over > 0 {
			head = head[:max(len(head)-over, 0)]
		}
	}
	if record && len(head) > 0 && !boxed {
		m.zones.add(rect{x, y, min(textWidth(m.cardTitle(c)), w), 1}, target{act: actDetails})
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

// packParts joins parts with " · " on as few lines of w cells as it can,
// breaking only between parts; a part wider than a line is cut.
func packParts(parts []string, w int) []string {
	var out []string
	line := ""
	for _, p := range parts {
		switch {
		case line == "":
			line = p
		case textWidth(line+" · "+p) <= w:
			line += " · " + p
		default:
			out = append(out, fit(line, w))
			line = p
		}
	}
	return append(out, fit(line, w))
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
func (m *model) cardButtons(c *current, btns []option, w int) ([]string, []zone) {
	var lines []string
	var zs []zone
	line, used := "", 0
	for _, b := range btns {
		lw := min(textWidth(b.label), w)
		if used > 0 && used+1+lw > w {
			lines, line, used = append(lines, line), "", 0
		}
		if used > 0 {
			line += " "
			used++
		}
		l := lookAccent
		if b.act == actAnswerCard || (b.act == actOpen && c.state == stateNeedsYou) {
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

// fitBar lays the bar out in at most rows rows of w cells, records its
// buttons and marks the focused one. The focus moves to the first button
// when its button was folded away or is gone.
func (m *model) fitBar(w, rows int) barLayout {
	btns := m.buttons()
	mark := actNone
	if m.barFocused() {
		mark = m.barFocus
	}
	l := layoutBar(m.th, btns, w, rows, mark, m.modal(), foldOrder)
	if m.barFocused() && !l.has(mark) && len(l.rows) > 0 && len(l.rows[0]) > 0 {
		mark = l.rows[0][0].act
		l = layoutBar(m.th, btns, w, rows, mark, m.modal(), foldOrder)
	}
	m.bar = append(m.bar[:0], l.acts()...)
	m.folded = l.folded
	return l
}

// foldOrder lists the actions the bar folds into More… first when it
// doesn't fit (SPEC §15.2); actions not listed are never folded: Answer…,
// Open session, Done, ? and Home always show.
var foldOrder = []action{actTaskMode, actSide, actStopAsk, actRetry, actSkip, actPause, actMode}

// tokensLine is the wide card's usage counter (SPEC §15.1): `tokens 2.3M
// in · 41k out · claude-sonnet-5-5`, `usage unavailable` when the
// transcript can't be read; "" until something is read, and in the narrow
// layout, whose facts line carries `2.3M/41k`. The model id is Ember on a
// rank mismatch.
func (m *model) tokensLine(c *current, w int) string {
	t := c.tokens
	switch {
	case t == nil:
		return ""
	case t.Unavailable:
		return fit(m.th.paint(lookDim, "usage unavailable"), w)
	case !m.wide():
		return ""
	}
	line := "tokens " + look.InOut(t.In, t.Out)
	if t.Priced {
		line += " · " + look.Dollars(t.USD)
	}
	if len(t.Models) > 0 {
		id := strings.Join(t.Models, ", ")
		if c.rankMismatch != "" {
			id = m.th.paint(lookWarn, id)
		}
		line += " · " + id
	}
	return fit(line, w)
}

// quotaStale is how old the recorded quota may get before the header says
// so (SPEC §15.1).
const quotaStale = 10 * time.Minute

// quotaText is the header's subscription quota, `5h 4% · 7d 52%` (amber from
// 80%, red from 95%, with the age once it is over 10 minutes old); "" when
// none was recorded.
func (m *model) quotaText() string {
	q := m.quota
	if q == nil {
		return ""
	}
	var parts []string
	for _, w := range []struct {
		name string
		w    *state.QuotaWindow
	}{{"5h", q.FiveHour}, {"7d", q.SevenDay}} {
		if w.w == nil {
			continue
		}
		tone := lookPlain
		switch {
		case w.w.Used >= 95:
			tone = lookFail
		case w.w.Used >= 80:
			tone = lookWarn
		}
		parts = append(parts, m.th.paint(tone, fmt.Sprintf("%s %.0f%%", w.name, math.Round(w.w.Used))))
	}
	if len(parts) == 0 {
		return ""
	}
	text := strings.Join(parts, " · ")
	if age := m.opts.Now().Sub(q.At); age > quotaStale {
		text += " · " + m.th.paint(lookDim, look.Age(age))
	}
	return text
}
