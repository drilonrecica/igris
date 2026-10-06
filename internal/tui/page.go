package tui

import (
	"fmt"
	"strings"
)

// page is a scrollable text view drawn over the panes: the help, a task's
// details, the whole log. Like a dialog it is modal; unlike one it offers
// nothing to pick.
type page struct {
	title string
	body  func(w int) []string // the text, laid out for w cells
	top   int                  // first body line shown; clamped when drawn
	rows  int                  // body lines shown in the last frame
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
	}
	return false
}

// maxTop asks for the last screenful; render clamps it.
const maxTop = 1 << 30

func (p *page) scroll(by int) { p.top = max(p.top+by, 0) }

// render draws the page in exactly h lines of at most w cells: the title,
// a rule, the body and a footer with the Close button. Zones are relative
// to the top-left corner.
func (p *page) render(w, h int) ([]string, zones) {
	var z zones
	if h < 3 {
		z.add(rect{0, 0, w, h}, target{region: regionPage})
		return []string{fit(p.title, w)}[:min(h, 1)], z
	}
	body := p.body(w)
	rows := h - 3
	p.rows = rows
	p.top = min(p.top, max(len(body)-rows, 0))
	out := []string{fit(p.title, w), strings.Repeat("─", w)}
	z.add(rect{0, 2, w, rows}, target{region: regionPage})
	for i := range rows {
		out = append(out, fit(line(body, p.top+i), w))
	}
	close := "[Close]"
	where := ""
	if len(body) > rows {
		where = fmt.Sprintf("  %d–%d of %d · ↑↓ scroll", p.top+1, min(p.top+rows, len(body)), len(body))
	}
	z.add(rect{0, h - 1, textWidth(close), 1}, target{act: actClose})
	out = append(out, fit(close+where, w))
	return out, z
}

// helpPage lists every action, its key and the focus keys (SPEC §15.3).
func helpPage() *page {
	return &page{title: "Help · esc closes", body: func(w int) []string {
		out := []string{"Actions: click a button, focus it and press enter, or press its key."}
		out = append(out, helpTable(helpActions, w)...)
		out = append(out, "", "Focus")
		out = append(out, helpTable(helpFocus, w)...)
		var wrapped []string
		for _, l := range out {
			if strings.HasPrefix(l, "  ") {
				wrapped = append(wrapped, l)
				continue
			}
			wrapped = append(wrapped, wrap(l, w)...)
		}
		return wrapped
	}}
}

// helpTable lays entries out as "  key  name — what", wrapping the
// description under itself.
func helpTable(entries []helpEntry, w int) []string {
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
		out = append(out, hang("  "+pad(e.key, keyW)+"  ", text, w)...)
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
