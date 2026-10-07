package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// pageScreen shows a page (help, a list, a long text) as a screen of its
// own on the app's stack; closing it pops it.
type pageScreen struct {
	p     *page
	th    *theme
	w, h  int
	zones zones
}

func newPageScreen(th *theme, p *page) *pageScreen {
	return &pageScreen{p: p, th: th, w: 80, h: 24}
}

func (s *pageScreen) Init() tea.Cmd { return nil }

func (s *pageScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case tea.KeyMsg:
		if k := msg.String(); k == "q" || s.p.key(k) {
			return s, pop(nil)
		}
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			break
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			s.p.scroll(-3)
		case tea.MouseButtonWheelDown:
			s.p.scroll(3)
		case tea.MouseButtonLeft:
			if t, ok := s.zones.at(msg.X, msg.Y); ok && t.act == actClose {
				return s, pop(nil)
			}
		}
	}
	return s, nil
}

func (s *pageScreen) View() string {
	lines, z := s.p.render(s.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}
