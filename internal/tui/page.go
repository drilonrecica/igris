package tui

import (
	"fmt"
	"slices"
	"strings"
)

// page is a scrollable text view drawn as an open screen of its own
// (SPEC §15.1): the help, a task's details, the whole log, home's pages.
// Like a dialog it is modal; unlike one it offers nothing to pick but its
// bar's buttons.
type page struct {
	title string
	// project and status fill the header: the wordmark, the project, the
	// title, and the status word, painted, at the right ("" for none).
	project, status string
	body            func(w int) []string // the text, laid out for w cells
	// buttons stand left of Back in the bar; the screen holding the page
	// acts on their zones.
	buttons []button
	// closeLabel names the bar's last button (actClose); "" is Back.
	closeLabel string
	top        int // first body line shown; clamped when drawn
	rows       int // body lines shown in the last frame
	total      int // body lines in the last frame
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

// pageChrome is what the page's chrome takes: the header, its rule, the
// rule over the bar and the bar.
const pageChrome = 4

// render draws the page in exactly h lines of at most w cells: the
// header, a rule, the body, a rule and the bar with the page's buttons
// and Back, each with its key. Zones are relative to the top-left corner.
func (p *page) render(th *theme, w, h int) ([]string, zones) {
	var z zones
	// The title is what the page is about and is never cut for the status
	// word: the word goes first when the width runs out.
	facts := strings.TrimPrefix(p.project+" · "+p.title, " · ")
	status := p.status
	if textWidth(facts) > headerRoom(w, status) {
		status = ""
	}
	head := th.header(w, facts, status)
	if h < pageChrome+1 {
		z.add(rect{0, 0, w, h}, target{region: regionPage})
		return []string{head}[:min(h, 1)], z
	}
	body := p.body(w)
	p.rows, p.total = h-pageChrome, len(body)
	rows := p.rows
	p.clamp()
	out := []string{head, th.rule(w, "")}
	z.add(rect{0, 2, w, rows}, target{region: regionPage})
	for i := range rows {
		out = append(out, fit(line(body, p.top+i), w))
	}
	out = append(out, th.rule(w, ""))
	closeLabel := p.closeLabel
	if closeLabel == "" {
		closeLabel = "Back"
	}
	btns := append(slices.Clone(p.buttons), button{label: closeLabel, key: "q", act: actClose})
	bar := layoutBar(th, btns, w, 1, actNone, false, nil)
	bar.barZones(&z, h-1, false)
	foot := bar.text[0]
	if len(body) > rows && len(p.buttons) == 0 {
		foot += th.paint(lookDim, fmt.Sprintf("  %d–%d of %d · ↑↓ scroll", p.top+1, min(p.top+rows, len(body)), len(body)))
	}
	out = append(out, fit(foot, w))
	return out, z
}

// helpPage lists every action, its key and the focus keys (SPEC §15.3).
func helpPage(th *theme, attached bool) *page {
	actions := helpActions
	if attached {
		actions = attachedHelp()
	}
	return &page{title: "Help", body: func(w int) []string {
		out := wrap("Actions: click a button, focus it and press enter, or press its key.", w)
		out = append(out, helpTable(th, actions, w)...)
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
