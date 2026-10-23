package tui

import (
	"strings"
)

// The open screen (SPEC §15.1, docs/design.md "Layout"; decisions.md
// V17-P1): one header, one rule and one action bar for the run view, home
// and every page. No outer frame; two columns are divided by one line;
// only task cards and dialogs are boxed. A dialog floats over the dimmed
// screen, so what it asks about stays visible (SPEC §15.5).
//
//	igris · sinjal      phase M1 · 3/8 · 37% ███░░░░░░░ · mode plan      ! Needs you
//	────────────────────────────────────────────────────────────────────────────────
//	TASKS                        │ ┌─ M1-03  Parser ─────────────────────────────┐
//	── LOG ─────────────────────────────────────────────────────────────────────────
//	────────────────────────────────────────────────────────────────────────────────
//	[›Answer…‹] [o Open] [m Mode] [p Pause] [d Done] [x Stop] [?] [q Home]

// wordmark is the brand mark that opens every header (SPEC §15.4): igris
// in bold, the second i in plume. The plain theme writes the word.
func (th *theme) wordmark() string {
	if !th.on {
		return "igris"
	}
	return th.paint(lookTitle, "igr") + th.paint(lookBrand, "i") + th.paint(lookTitle, "s")
}

// headerGap is the least space between the header's facts and its status
// word.
const headerGap = 2

// header draws the top line of a screen in w cells: the wordmark, "·",
// facts (the project and what the screen is about, joined by " · ") and
// the status word at the right, painted. The word never drops; the facts
// are cut when they don't fit, so a screen with facts to spare drops them
// until headerRoom holds the rest (SPEC §15.1).
func (th *theme) header(w int, facts, status string) string {
	line := th.wordmark()
	if facts != "" {
		line += " · " + facts
	}
	if status == "" {
		return fit(line, w)
	}
	room := w - textWidth(status) - headerGap
	if room < 8 {
		return fit(line, w) // no screen to speak of: the facts, as far as they go
	}
	line = fit(line, room)
	return line + strings.Repeat(" ", w-textWidth(line)-textWidth(status)) + status
}

// headerRoom is how many cells the header's facts may take in w cells
// beside the status word.
func headerRoom(w int, status string) int {
	room := w - textWidth("igris · ")
	if status != "" {
		room -= textWidth(status) + headerGap
	}
	return room
}

// rule draws a rule w cells wide; with a label it reads "── LOG ────",
// the label painted by the caller (a region's title, marked while it has
// the focus).
func (th *theme) rule(w int, label string) string {
	if label == "" {
		return th.paint(lookFrame, strings.Repeat("─", max(w, 0)))
	}
	fill := max(w-3-textWidth(label)-1, 0) // "── " + label + " "
	return fit(th.paint(lookFrame, "──")+" "+label+" "+th.paint(lookFrame, strings.Repeat("─", fill)), w)
}

// columns joins a left and a right column into rows lines of w cells,
// divided by one line: "left │ right". The left column is lw cells, the
// right gets the rest after the divider and its spaces (columnsRight). A
// row past the right column's end keeps the divider while divider says
// so, else it is the left column alone.
func (th *theme) columns(left, right []string, lw, w, rows int, divider func(i int) bool) []string {
	rw := columnsRight(lw, w)
	v := th.paint(lookFrame, "│")
	out := make([]string, 0, rows)
	for i := range rows {
		l := pad(fit(line(left, i), lw), lw)
		if i >= len(right) && divider != nil && !divider(i) {
			out = append(out, strings.TrimRight(l, " "))
			continue
		}
		r := fit(line(right, i), rw)
		if r == "" {
			out = append(out, l+" "+v)
			continue
		}
		out = append(out, l+" "+v+" "+r)
	}
	return out
}

// columnsRight is the width of the right column beside a left one of lw
// cells in w: a space, the divider and a space go between them.
func columnsRight(lw, w int) int { return max(w-lw-3, 1) }

// button is an action-bar button (SPEC §15.3): its label, the shorter
// label the bar falls back to when it runs out of room (the verb; "" keeps
// the label), the key that runs it ("" for none) and its action. The bar
// draws it as "[o Open session]"; a key alone is "[?]".
type button struct {
	label, short, key string
	act               action
}

// text is the button's text, in brackets, with its key; short picks the
// shorter label.
func (b button) text(short bool) string {
	label := b.label
	if short && b.short != "" {
		label = b.short
	}
	switch {
	case b.key == "":
		return label
	case label == "":
		return b.key
	}
	return b.key + " " + label
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
	folded []button // the actions behind More…
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

// acts lists the bar's actions in order.
func (l barLayout) acts() []action {
	var out []action
	for _, row := range l.rows {
		for _, p := range row {
			out = append(out, p.act)
		}
	}
	return out
}

// barTail says a is one of the buttons that end every bar (SPEC §15.3):
// ? and Home / Quit / Back. They are never folded, and More… goes before
// them.
func barTail(a action) bool { return a == actHelp || a == actQuit || a == actClose }

// layoutBar lays btns out in at most maxRows rows of w cells and marks
// the focused one; under a dialog or page (modal) every button is drawn
// inactive and none is marked. The full labels are used when they fit,
// else the short ones; when that isn't enough, the actions in order are
// folded one by one into a More… button, placed before the bar's tail,
// until the rest fits. On a screen too narrow even for that, the other
// buttons fold too, from the end, all but the first and the tail; what
// still doesn't fit is cut.
func layoutBar(th *theme, btns []button, w, maxRows int, focused action, modal bool, order []action) barLayout {
	keep := append([]button{}, btns...)
	var folded []button
	try := func() (barLayout, bool) {
		if l := placeBar(th, keep, w, focused, modal, false); len(l.rows) <= maxRows {
			return l, true
		}
		if l := placeBar(th, keep, w, focused, modal, true); len(l.rows) <= maxRows {
			return l, true
		}
		return barLayout{}, false
	}
	fold := func(i int) {
		folded = append(folded, keep[i])
		keep = append(keep[:i:i], keep[i+1:]...)
		if len(folded) == 1 {
			at := len(keep)
			for at > 0 && barTail(keep[at-1].act) {
				at--
			}
			keep = append(keep[:at:at], append([]button{{label: "More…", act: actMore}}, keep[at:]...)...)
		}
	}
	for _, a := range order {
		if l, ok := try(); ok {
			l.folded = folded
			return l
		}
		for i, b := range keep {
			if b.act == a {
				fold(i)
				break
			}
		}
	}
	for {
		if l, ok := try(); ok {
			l.folded = folded
			return l
		}
		// The last button before More… and the tail, unless it is the
		// first: that one always shows.
		i := len(keep) - 1
		for i > 0 && (barTail(keep[i].act) || keep[i].act == actMore) {
			i--
		}
		if i <= 0 {
			break
		}
		fold(i)
	}
	l := placeBar(th, keep, w, focused, modal, true) // can't fit even folded: cut the bar
	l.rows, l.text = l.rows[:min(maxRows, len(l.rows))], l.text[:min(maxRows, len(l.text))]
	l.folded = folded
	return l
}

// placeBar wraps the buttons into rows of w cells.
func placeBar(th *theme, btns []button, w int, focused action, modal, short bool) barLayout {
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
	for _, b := range btns {
		text := fit(th.button(b.text(short), barState(b.act, focused, modal)), w)
		tw := textWidth(text)
		if x > 0 && x+1+tw > w {
			flush()
		}
		if x > 0 {
			x++
		}
		row = append(row, placed{text, x, b.act})
		x += tw
	}
	if len(row) > 0 {
		flush()
	}
	return l
}

// barState is how a bar button for a is drawn: focused is the button
// with the focus, if the bar has it; under a dialog or page every button
// is inactive.
func barState(a, focused action, modal bool) btnState {
	switch {
	case modal:
		return btnInactive
	case a == focused:
		return btnFocused
	case a == actAnswer:
		return btnAttention
	}
	return btnNormal
}

// barZones records the laid-out bar's buttons drawn from row y on in z,
// unless the bar is inactive.
func (l barLayout) barZones(z *zones, y int, modal bool) {
	if modal {
		return
	}
	for i, row := range l.rows {
		for _, b := range row {
			z.add(rect{b.x, y + i, textWidth(b.text), 1}, target{act: b.act})
		}
	}
}

// moreDialog lists the actions the bar folded away.
func moreDialog(folded []button) *dialog {
	d := &dialog{title: "More actions", cancel: actClose}
	for _, b := range folded {
		d.options = append(d.options, option{b.label, b.act})
	}
	return d
}

// dim draws a finished line faint all through, for the screen under a
// floating dialog: a reset inside it turns the faint back on. Faint is
// no color, so the plain theme leaves the line as it is.
func (th *theme) dim(s string) string {
	if !th.on || s == "" {
		return s
	}
	on := "\x1b[" + sgrFaint + "m"
	return on + strings.ReplaceAll(s, sgrReset, sgrReset+on) + sgrReset
}

// floatBox draws box over screen, dimmed, with its top-left corner at
// (x, y), and returns the lines. Each box line replaces the cells it
// covers; the screen's text on either side stays.
func (th *theme) floatBox(screen, box []string, x, y, w int) []string {
	out := make([]string, len(screen))
	for i, l := range screen {
		out[i] = th.dim(l)
	}
	for i, b := range box {
		if y+i < 0 || y+i >= len(out) {
			continue
		}
		bw := textWidth(b)
		l := out[y+i]
		out[y+i] = pad(cells(l, 0, x), x) + b + cells(l, x+bw, w)
	}
	return out
}

// floatDialog draws d floating over screen, centred in the rows from top
// on, as wide as it needs up to width, and returns the lines and the
// dialog's zones. Narrow screens draw the dialog alone instead
// (fullScreenDialog).
func (th *theme) floatDialog(screen []string, d *dialog, w, top, rows int) ([]string, zones) {
	width := dialogWidth
	if d.wide {
		width = dialogWideWidth
	}
	box, z := d.render(th, min(w-4, width), rows)
	y := top + max((rows-len(box))/2, 0)
	x := max((w-textWidth(box[0]))/2, 0)
	var out zones
	out.merge(z, x, y)
	return th.floatBox(screen, box, x, y, w), out
}

// The usual and the wide dialog width (SPEC §15.5): a plan presented for
// approval and a side session's first prompt may grow to 100 columns.
const (
	dialogWidth     = 72
	dialogWideWidth = 100
)

// fullScreenDialog draws d alone in w×h: the narrow layout's dialog
// (SPEC §15.2).
func (th *theme) fullScreenDialog(d *dialog, w, h int) ([]string, zones) {
	box, z := d.render(th, w, h)
	if len(box) > h {
		box = box[:h]
	}
	for i := range box {
		box[i] = fit(box[i], w)
	}
	return box, z
}
