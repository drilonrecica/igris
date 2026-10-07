package tui

import (
	"strings"
)

// detailPage shows task id in full (SPEC §15.3). It reads the plan as last
// loaded each time it is drawn.
func (m *model) detailPage(id string) *page {
	return &page{title: id + " · esc closes", body: func(w int) []string { return m.rows().detail(id, w) }}
}

// fields lays facts out as "Name   value" with the values aligned and
// wrapped under themselves. A value may hold painted single words (a
// glyph, a rank); the skip-permissions badge is painted after wrapping.
func fields(th *theme, facts []fact, w int) []string {
	nameW := 0
	for _, f := range facts {
		nameW = max(nameW, textWidth(f.name))
	}
	var out []string
	for _, f := range facts {
		for i, l := range f.lines {
			name := ""
			if i == 0 {
				name = f.name
			}
			for _, hl := range hang(th.paint(lookDim, pad(name, nameW))+"  ", l, w) {
				out = append(out, th.marks(hl))
			}
		}
	}
	return out
}

// logPage shows the whole log kept in memory, newest at the bottom.
func (m *model) logPage() *page {
	return &page{title: "Log · esc closes", top: maxTop, body: func(w int) []string {
		var out []string
		for _, e := range m.log {
			at := e.at.In(m.loc).Format("15:04") + " "
			for i, l := range wrap(e.text, w-textWidth(at)) {
				if i == 0 {
					out = append(out, m.th.paint(lookDim, at)+m.logText(e, l))
				} else {
					out = append(out, strings.Repeat(" ", textWidth(at))+m.logText(e, l))
				}
			}
		}
		if len(out) == 0 {
			out = []string{"nothing logged yet"}
		}
		return out
	}}
}
