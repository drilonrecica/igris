package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
)

// Several active tasks (SPEC §15.1, a parallel phase): one card per active
// task in plan order, one of them selected. Wide, the right column stacks
// the cards, the selected one with a heavier border; narrow, a switcher
// line sits over the selected card. A new card is never selected by
// itself; when the selected task ends, the next card in plan order is.
// Every card is boxed, its head on the top border (SPEC §15.1).

// card is active task id's card, or nil.
func (m *model) card(id string) *current {
	if id == "" {
		return nil
	}
	for _, c := range m.cards {
		if c.id == id {
			return c
		}
	}
	return nil
}

// addCard adds c in plan order. The first card is selected, as there is
// no other; later ones leave the selection where it is.
func (m *model) addCard(c *current) {
	m.cards = append(m.cards, c)
	m.sortCards()
	if m.cur == nil {
		m.cur = c
	}
}

// dropCard removes task id's card. When it was the selected one, the next
// card in plan order is selected, the first after the last.
func (m *model) dropCard(id string) {
	i := slices.IndexFunc(m.cards, func(c *current) bool { return c.id == id })
	if i < 0 {
		return
	}
	was := m.cards[i] == m.cur
	m.cards = slices.Delete(m.cards, i, i+1)
	delete(m.tails, id)
	if !was {
		return
	}
	m.cur = nil
	if len(m.cards) > 0 {
		m.cur = m.cards[i%len(m.cards)]
		m.taskTop = -1
	}
}

// sortCards puts the cards in plan order, the plan as last loaded; cards
// of tasks it doesn't list keep their order, after the others.
func (m *model) sortCards() {
	tasks := m.phaseTasks()
	order := make(map[string]int, len(tasks))
	for i, t := range tasks {
		order[t.ID] = i
	}
	pos := func(c *current) int {
		if i, ok := order[c.id]; ok {
			return i
		}
		return len(tasks)
	}
	slices.SortStableFunc(m.cards, func(a, b *current) int { return pos(a) - pos(b) })
}

// moveCard selects the card by cards along the order the screen shows
// (cardOrder), wrapping around. The task list's selection follows it.
func (m *model) moveCard(by int) {
	cards := m.cardOrder()
	if len(cards) == 0 {
		return
	}
	i := max(slices.Index(cards, m.cur), 0)
	m.selectCard(cards[((i+by)%len(cards)+len(cards))%len(cards)])
}

// cardOrder is the order the cards are shown in: plan order wide; narrow,
// the switcher puts a task that needs you first whatever the plan order
// (SPEC §15.1), so [ and ] follow it.
func (m *model) cardOrder() []*current {
	if m.wide() {
		return m.cards
	}
	out := make([]*current, 0, len(m.cards))
	for _, c := range m.cards {
		if m.needs(c) {
			out = append(out, c)
		}
	}
	for _, c := range m.cards {
		if !m.needs(c) {
			out = append(out, c)
		}
	}
	return out
}

// selectCard makes c the selected card.
func (m *model) selectCard(c *current) {
	m.cur, m.selID, m.taskTop = c, "", -1
}

// pickCard selects the i-th card (a click on a card or a switcher entry).
func (m *model) pickCard(i int) {
	if i >= 0 && i < len(m.cards) {
		m.selectCard(m.cards[i])
	}
}

// multi reports whether several tasks are active: the view then shows
// their cards and their switcher.
func (m *model) multi() bool { return len(m.cards) > 1 }

// sessions are the cards' session refs by task, to tell when one changes.
func (m *model) sessions() map[string]backend.SessionRef {
	out := make(map[string]backend.SessionRef, len(m.cards))
	for _, c := range m.cards {
		if c.session != nil {
			out[c.id] = *c.session
		}
	}
	return out
}

// needsOwner reports whether c's task waits on the owner: its state is
// one only the owner ends.
func (c *current) needsOwner() bool {
	switch c.state {
	case stateNeedsYou, stateYourTurn, stateQuestion, stateLost:
		return true
	}
	return false
}

// needYou are the active tasks that wait on the owner, in plan order.
func (m *model) needYou() []string {
	var ids []string
	for _, c := range m.cards {
		if m.needs(c) {
			ids = append(ids, c.id)
		}
	}
	return ids
}

// cardGlyph is c's glyph on the switcher: "!" while it waits on the owner.
func (m *model) cardGlyph(c *current) string {
	if m.needs(c) {
		return look.GlyphNeedsYou
	}
	return glyphs[plan.InProgress]
}

// paintedCardGlyph is c's switcher glyph in its look: plume for Needs
// you, the accent for a working task.
func (m *model) paintedCardGlyph(c *current) string {
	if m.needs(c) {
		return m.th.paint(lookAlert, look.GlyphNeedsYou)
	}
	return m.th.paint(lookAccentBold, glyphs[plan.InProgress])
}

// slotText is c's slot in its head, " · slot 1", or "".
func slotText(c *current) string {
	if c.slot == nil {
		return ""
	}
	return " · slot " + strconv.Itoa(*c.slot)
}

// cardTitle is c's head: its ID, slot and title, with "›" before the
// selected card's ID when several cards show.
func (m *model) cardTitle(c *current) string {
	mark := ""
	if m.multi() && c == m.cur {
		mark = "› "
	}
	return mark + c.id + slotText(c) + " " + c.title
}

// The heavier border marks the selected card without color (SPEC §15.4).
var (
	cardBox     = [6]string{"┌", "─", "┐", "│", "└", "┘"}
	selectedBox = [6]string{"┏", "━", "┓", "┃", "┗", "┛"}
)

// cardInner is the text width inside a card box w cells wide: "│ " +
// text + " │".
func cardInner(w int) int { return max(w-4, 1) }

// cardsView stacks the cards in rows lines of w cells drawn at (x, y),
// each in its box, and records their zones. The cards share the height:
// each gets what it needs while that fits, else an even share, the rest
// going to the first that want more; the task text gives way first, then
// the tail.
func (m *model) cardsView(x, y, w, rows int) []string {
	inner := cardInner(w)
	need := make([]int, len(m.cards))
	for i, c := range m.cards {
		need[i] = len(m.cardBody(c, inner, 0, 0, -1, false, true))
	}
	give := shareRows(need, max(rows, 0))
	var out []string
	for i, c := range m.cards {
		if give[i] <= 0 {
			continue
		}
		out = append(out, m.drawCard(c, x, y+len(out), w, give[i])...)
	}
	return out
}

// drawCard draws c's card in a box at (x, y), rows lines (borders
// included) of w cells, its head on the top border, and records its
// zones: the selected card's buttons and title, another card as one
// target that selects it. The selected card among several has the heavy
// border (SPEC §15.1).
func (m *model) drawCard(c *current, x, y, w, rows int) []string {
	inner := cardInner(w)
	box, tone := cardBox, lookTitle
	if m.multi() && c == m.cur {
		box, tone = selectedBox, lookAccentBold
	}
	bodyRows := max(rows-2, 0)
	body := m.cardBody(c, inner, x+2, y+1, bodyRows, c == m.cur, true)
	title := fit(m.cardTitle(c), max(inner-2, 1))
	fill := max(w-2-1-textWidth(title)-2, 0) // corner, "─ ", title, " ", fill, corner
	out := []string{m.th.paint(lookFrame, box[0]+box[1]) + " " + m.th.paint(tone, title) + " " + m.th.paint(lookFrame, strings.Repeat(box[1], fill)+box[2])}
	if c == m.cur && !m.modal() {
		m.zones.add(rect{x + 3, y, textWidth(title), 1}, target{act: actDetails})
	}
	side := m.th.paint(lookFrame, box[3])
	for j := range bodyRows {
		out = append(out, side+" "+pad(line(body, j), inner)+" "+side)
	}
	if rows >= 2 {
		out = append(out, m.th.paint(lookFrame, box[4]+strings.Repeat(box[1], max(w-2, 0))+box[5]))
	}
	if c != m.cur && !m.modal() {
		m.zones.add(rect{x, y, w, rows}, target{act: actCardPick, option: slices.Index(m.cards, c)})
	}
	return out[:min(len(out), max(rows, 0))]
}

// cardsNeed is how many rows the stacked cards want, borders included.
func (m *model) cardsNeed(w int) int {
	n := 0
	for _, c := range m.cards {
		n += 2 + len(m.cardBody(c, cardInner(w), 0, 0, -1, false, true))
	}
	return n
}

// shareRows splits rows among boxes that each need need[i] rows of body
// plus two of border.
func shareRows(need []int, rows int) []int {
	give := make([]int, len(need))
	total := 0
	for i, n := range need {
		give[i] = n + 2
		total += give[i]
	}
	if total <= rows || len(need) == 0 {
		return give
	}
	share := rows / len(need)
	left := rows
	for i := range give {
		give[i] = min(give[i], share)
		left -= give[i]
	}
	for i := range give {
		if more := min(need[i]+2-give[i], left); more > 0 {
			give[i] += more
			left -= more
		}
	}
	return give
}

// switcher is the narrow layout's line over the selected card (SPEC
// §15.1): "‹ ! M1-04 · 1 of 2 › ● M1-03", each active task with its glyph,
// a task that needs you first whatever the plan order. ‹ and › select
// the previous and next card, a task's ID selects it; the zones are
// recorded for the line drawn at row y.
func (m *model) switcher(y, w int) string {
	cards := m.cardOrder()
	i := max(slices.Index(cards, m.cur), 0)
	var b strings.Builder
	at := 0
	put := func(text string, t target, painted string) {
		if at > 0 {
			b.WriteString(" ")
			at++
		}
		if at+textWidth(text) > w {
			return
		}
		if t.act != actNone && !m.modal() {
			m.zones.add(rect{at, y, textWidth(text), 1}, t)
		}
		b.WriteString(painted)
		at += textWidth(text)
	}
	put("‹", target{act: actCardPrev}, m.th.paint(lookAccent, "‹"))
	sel := fmt.Sprintf("%s · %d of %d", m.cur.id, i+1, len(cards))
	put(m.cardGlyph(m.cur)+" "+sel, target{}, m.paintedCardGlyph(m.cur)+" "+m.th.paint(lookAccentBold, sel))
	put("›", target{act: actCardNext}, m.th.paint(lookAccent, "›"))
	for _, c := range cards {
		if c == m.cur {
			continue
		}
		put(m.cardGlyph(c)+" "+c.id, target{act: actCardPick, option: slices.Index(m.cards, c)}, m.paintedCardGlyph(c)+" "+c.id)
	}
	return fit(b.String(), w)
}
