package tui

import (
	"fmt"
	"slices"
	"strings"
)

// page is a scrollable text view drawn over the panes: the help, a task's
// details, the whole log. Like a dialog it is modal; unlike one it offers
// nothing to pick.
type page struct {
	title string
	body  func(w int) []string // the text, laid out for w cells
	// buttons stand left of Close in the footer; the screen holding the
	// page acts on their zones.
	buttons []pageButton
	// closeLabel names the footer's last button (actClose); "" is Close.
	closeLabel string
	top        int // first body line shown; clamped when drawn
	rows       int // body lines shown in the last frame
	total      int // body lines in the last frame
}

// pageButton is a footer button of a page.
type pageButton struct {
	label string
	short string // used when the labels don't fit w; "" keeps label
	act   action
}

// key handles a key press and reports whether it closes the page.
func (p *page) key(k string) bool {
	switch k {
	case "esc", "enter", " ", "?":
		return true
	case "up", "k":
		p.scroll(-1)
	case "down", "j":
		p.scroll(1)
	case "pgup":
		p.scroll(-max(p.rows-1, 1))
	case "pgdown":
		p.scroll(max(p.rows-1, 1))
	case "home":
		p.top = 0
	case "end":
		p.top = maxTop
		p.clamp()
	}
	return false
}

// maxTop asks for the last screenful; clamp brings it down.
const maxTop = 1 << 30

func (p *page) scroll(by int) {
	p.clamp()
	p.top = max(p.top+by, 0)
	p.clamp()
}

// clamp keeps the last screenful drawn in view; before the first frame
// the size isn't known yet and render clamps.
func (p *page) clamp() {
	if p.rows > 0 {
		p.top = min(p.top, max(p.total-p.rows, 0))
	}
}

// render draws the page in exactly h lines of at most w cells: the title,
// a rule, the body and a footer with the Close button. Zones are relative
// to the top-left corner.
func (p *page) render(th *theme, w, h int) ([]string, zones) {
	var z zones
	if h < 3 {
		z.add(rect{0, 0, w, h}, target{region: regionPage})
		return []string{th.paint(lookTitle, fit(p.title, w))}[:min(h, 1)], z
	}
	body := p.body(w)
	p.rows, p.total = h-3, len(body)
	rows := p.rows
	p.clamp()
	out := []string{th.paint(lookTitle, fit(p.title, w)), th.paint(lookFrame, strings.Repeat("─", w))}
	z.add(rect{0, 2, w, rows}, target{region: regionPage})
	for i := range rows {
		out = append(out, fit(line(body, p.top+i), w))
	}
	foot, x := "", 0
	closeLabel := p.closeLabel
	if closeLabel == "" {
		closeLabel = "Close"
	}
	btns := append(slices.Clone(p.buttons), pageButton{label: closeLabel, act: actClose})
	short := false
	if n := len(btns) - 1; n > 0 {
		for _, b := range btns {
			n += textWidth(th.button(b.label, btnNormal))
		}
		short = n > w
	}
	for _, b := range btns {
		label := b.label
		if short && b.short != "" {
			label = b.short
		}
		btn := th.button(label, btnNormal)
		z.add(rect{x, h - 1, min(textWidth(btn), max(w-x, 0)), 1}, target{act: b.act})
		foot += btn + " "
		x += textWidth(btn) + 1
	}
	foot = strings.TrimSuffix(foot, " ")
	if len(body) > rows && len(p.buttons) == 0 {
		foot += th.paint(lookDim, fmt.Sprintf("  %d–%d of %d · ↑↓ scroll", p.top+1, min(p.top+rows, len(body)), len(body)))
	}
	out = append(out, fit(foot, w))
	return out, z
}

// helpPage lists every action, its key and the focus keys (SPEC §15.3).
func helpPage(th *theme) *page {
	return &page{title: "Help · esc closes", body: func(w int) []string {
		out := wrap("Actions: click a button, focus it and press enter, or press its key.", w)
		out = append(out, helpTable(th, helpActions, w)...)
		out = append(out, "", th.paint(lookTitle, "Focus"))
		return append(out, helpTable(th, helpFocus, w)...)
	}}
}

// helpTable lays entries out as "  key  name — what", wrapping the
// description under itself.
func helpTable(th *theme, entries []helpEntry, w int) []string {
	keyW := 0
	for _, e := range entries {
		keyW = max(keyW, textWidth(e.key))
	}
	var out []string
	for _, e := range entries {
		text := e.what
		if e.name != "" {
			text = e.name + " — " + e.what
		}
		out = append(out, hang("  "+th.paint(lookTitle, pad(e.key, keyW))+"  ", text, w)...)
	}
	return out
}

// hang wraps text into w cells after prefix, indenting the following
// lines by the prefix's width.
func hang(prefix, text string, w int) []string {
	lines := wrap(text, w-textWidth(prefix))
	indent := strings.Repeat(" ", textWidth(prefix))
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
		} else {
			lines[i] = indent + lines[i]
		}
	}
	return lines
}
