package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// Hover (SPEC §15.4, §15.5): with the mouse on, the element a click would
// activate is underlined while the pointer is over it. Hover is drawn on
// top of a finished frame, so it never changes focus or what a screen
// draws, and the underline is no color, so it stays under NO_COLOR.

// SGR underline on and off. Like bold and reverse, not a color.
const (
	sgrUnderline   = "\x1b[4m"
	sgrNoUnderline = "\x1b[24m"
)

// hoverer says which clickable element, if any, is at (x, y) in the frame
// it drew last, and the rectangle it was drawn in. Only what a left click
// there would act on counts: an element under an open dialog does not.
type hoverer interface {
	hoverAt(x, y int) (rect, bool)
}

// hoverModel is a program's root model that can say what is under the
// pointer.
type hoverModel interface {
	tea.Model
	hoverer
}

// hit returns the rectangle of the topmost zone at (x, y) when ok accepts
// its target. A zone that ok refuses is not looked through: what is drawn
// on top hides what lies under it.
func (z *zones) hit(x, y int, ok func(target) bool) (rect, bool) {
	for i := len(z.list) - 1; i >= 0; i-- {
		if zn := z.list[i]; zn.r.contains(x, y) {
			if ok(zn.t) {
				return zn.r, true
			}
			return rect{}, false
		}
	}
	return rect{}, false
}

// clickable is the hover rule of a screen without a dialog: every zone
// that stands for an action, not only for a scrollable region.
func clickable(t target) bool { return t.act != actNone }

// inDialog is the hover rule while a dialog is open: its options and its
// text field.
func inDialog(t target) bool { return t.act == actOption || t.act == actField }

// rowAt is the hover rectangle of a page body row that a click selects:
// owner maps body lines to what they show, -1 for none (the phase, doctor
// and history pages).
func rowAt(p *page, owner []int, w, x, y int) (rect, bool) {
	i := y - 2 // under the header and its rule
	if x < 0 || x >= w || i < 0 || i >= p.rows || p.top+i >= len(owner) || owner[p.top+i] < 0 {
		return rect{}, false
	}
	return rect{0, y, w, 1}, true
}

// hover wraps a program's root model: it remembers where the pointer is
// and underlines the element under it in every frame. Motion events stop
// here; the screens act on presses and the wheel only.
type hover struct {
	m      hoverModel
	x, y   int
	in     bool   // the pointer is over the window, as far as igris knows
	frame  string // the last frame drawn, hover included
	r      rect   // the element underlined in frame
	on     bool   // whether one is
	reused bool   // the next View may return frame as it is
}

func newHover(m hoverModel) *hover { return &hover{m: m} }

// unwrap returns the model m wraps, as a program returns it.
func unwrap(m tea.Model) tea.Model {
	if h, ok := m.(*hover); ok {
		return h.m
	}
	return m
}

func (h *hover) Init() tea.Cmd { return h.m.Init() }

func (h *hover) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	h.reused = false
	switch msg := msg.(type) {
	case tea.MouseMsg:
		h.x, h.y, h.in = msg.X, msg.Y, true
		if msg.Action == tea.MouseActionMotion {
			// Moving the pointer changes nothing but the underline. When
			// it stays on the same element (or off any), the last frame
			// is still right and the screen isn't drawn again.
			r, on := h.m.hoverAt(h.x, h.y)
			h.reused = h.frame != "" && on == h.on && (!on || r == h.r)
			return h, nil
		}
	case tea.BlurMsg:
		h.in = false // the pointer went to another window
	}
	m, cmd := h.m.Update(msg)
	if hm, ok := m.(hoverModel); ok {
		h.m = hm
	}
	return h, cmd
}

func (h *hover) View() string {
	if h.reused {
		return h.frame
	}
	f := h.m.View()
	h.on = false
	if h.in {
		h.r, h.on = h.m.hoverAt(h.x, h.y)
	}
	if h.on {
		f = underlineRect(f, h.r)
	}
	h.frame = f
	return f
}

// underlineRect underlines the text drawn inside r in frame.
func underlineRect(frame string, r rect) string {
	lines := strings.Split(frame, "\n")
	for y := max(r.y, 0); y < min(r.y+r.h, len(lines)); y++ {
		lines[y] = underlineSpan(lines[y], r.x, r.x+r.w)
	}
	return strings.Join(lines, "\n")
}

// underlineSpan underlines the cells from..to-1 of a drawn line, leaving
// out the blanks and box sides ("│") at either end, so a full-width row
// or a dialog option underlines its text only. The underline is turned on
// again after every SGR sequence inside the span, since a reset there
// would end it.
func underlineSpan(s string, from, to int) string {
	// Find the cells the underline covers.
	first, last, col := -1, -1, 0
	for i := 0; i < len(s); {
		if n := sgrLen(s[i:]); n > 0 {
			i += n
			continue
		}
		g, _, gw, _ := uniseg.FirstGraphemeClusterInString(s[i:], -1)
		if col >= from && col+gw <= to && g != " " && g != "│" {
			if first < 0 {
				first = col
			}
			last = col + gw
		}
		col += gw
		i += len(g)
	}
	if first < 0 {
		return s
	}
	var b strings.Builder
	col, on := 0, false
	for i := 0; i < len(s); {
		if n := sgrLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			if on {
				b.WriteString(sgrUnderline)
			}
			i += n
			continue
		}
		g, _, gw, _ := uniseg.FirstGraphemeClusterInString(s[i:], -1)
		if col == first {
			b.WriteString(sgrUnderline)
			on = true
		}
		if col == last && on {
			b.WriteString(sgrNoUnderline)
			on = false
		}
		b.WriteString(g)
		col += gw
		i += len(g)
	}
	if on {
		b.WriteString(sgrNoUnderline)
	}
	return b.String()
}

// The hover rules of the screens: what a left click on each would act on.

func (a *appModel) hoverAt(x, y int) (rect, bool) {
	if !a.o.Mouse || a.tooSmall() || len(a.stack) == 0 {
		return rect{}, false
	}
	if s, ok := a.stack[len(a.stack)-1].(hoverer); ok {
		return s.hoverAt(x, y)
	}
	return rect{}, false
}

func (m *model) hoverAt(x, y int) (rect, bool) {
	switch {
	case m.dialog != nil:
		return m.zones.hit(x, y, inDialog)
	case m.page != nil:
		return m.zones.hit(x, y, func(t target) bool { return t.act == actClose })
	}
	return m.zones.hit(x, y, func(t target) bool { return clickable(t) && !inDialog(t) })
}

func (r *review) hoverAt(x, y int) (rect, bool) {
	if r.dialog != nil {
		return r.zones.hit(x, y, func(t target) bool { return t.act == actOption })
	}
	return r.zones.hit(x, y, clickable)
}

func (m *homeScreen) hoverAt(x, y int) (rect, bool) {
	if m.dialog != nil {
		return m.zones.hit(x, y, inDialog)
	}
	return m.zones.hit(x, y, clickable)
}

func (s *pageScreen) hoverAt(x, y int) (rect, bool) {
	return s.zones.hit(x, y, func(t target) bool { return t.act == actClose })
}

func (s *previewScreen) hoverAt(x, y int) (rect, bool)  { return s.zones.hit(x, y, clickable) }
func (s *settingsScreen) hoverAt(x, y int) (rect, bool) { return s.zones.hit(x, y, clickable) }
func (s *taskScreen) hoverAt(x, y int) (rect, bool)     { return s.zones.hit(x, y, clickable) }
func (s *checkScreen) hoverAt(x, y int) (rect, bool)    { return s.zones.hit(x, y, clickable) }
func (s *notifyScreen) hoverAt(x, y int) (rect, bool)   { return s.zones.hit(x, y, clickable) }
func (s *adaptScreen) hoverAt(x, y int) (rect, bool)    { return s.zones.hit(x, y, clickable) }
func (s *draftScreen) hoverAt(x, y int) (rect, bool)    { return s.zones.hit(x, y, clickable) }
func (s *initScreen) hoverAt(x, y int) (rect, bool)     { return s.zones.hit(x, y, clickable) }

func (s *phaseScreen) hoverAt(x, y int) (rect, bool) {
	if r, ok := s.zones.hit(x, y, clickable); ok {
		return r, true
	}
	return rowAt(s.p, s.owner, s.w, x, y)
}

func (s *doctorScreen) hoverAt(x, y int) (rect, bool) {
	if r, ok := s.zones.hit(x, y, clickable); ok {
		return r, true
	}
	return rowAt(s.p, s.owner, s.w, x, y)
}

func (s *historyScreen) hoverAt(x, y int) (rect, bool) {
	if r, ok := s.zones.hit(x, y, func(t target) bool { return t.act == actClose }); ok {
		return r, true
	}
	return rowAt(s.p, s.owner, s.w, x, y)
}
