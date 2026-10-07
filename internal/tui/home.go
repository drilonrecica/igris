package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/report"
)

// homeScreen is the bottom of the app's stack (SPEC §15.6). It reads the
// project when it opens and shows where it is; the dashboard is drawn on
// top of this.
type homeScreen struct {
	ctx  context.Context
	svc  Services
	th   *theme
	w, h int

	snap    *report.Snapshot // nil until the first read is back
	snapErr error

	barFocus int // index into the bar's buttons
	zones    zones
}

// snapshotMsg is a Snapshot read for home.
type snapshotMsg struct {
	s   *report.Snapshot
	err error
}

func newHome(ctx context.Context, svc Services, th *theme) *homeScreen {
	return &homeScreen{ctx: ctx, svc: svc, th: th, w: 80, h: 24}
}

func (m *homeScreen) Init() tea.Cmd { return m.refresh() }

// refresh reads the project again, off the program's loop.
func (m *homeScreen) refresh() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	ctx, svc := m.ctx, m.svc
	return async(m, "snapshot", func() tea.Msg {
		s, err := svc.Snapshot(ctx)
		return snapshotMsg{s, err}
	})
}

// homeBar is home's action bar.
var homeBar = []option{{"?", actHelp}, {"Quit", actQuit}}

func (m *homeScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"?", "Help", "this page"},
		{"q", "Quit", "quit igris"},
	}
}

func (m *homeScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case snapshotMsg:
		m.snap, m.snapErr = msg.s, msg.err
	case tea.KeyMsg:
		switch msg.String() {
		case "?":
			return m, m.activate(actHelp)
		case "q":
			return m, m.activate(actQuit)
		case "left", "shift+tab":
			m.barFocus = (m.barFocus + len(homeBar) - 1) % len(homeBar)
		case "right", "tab":
			m.barFocus = (m.barFocus + 1) % len(homeBar)
		case "enter", " ":
			return m, m.activate(homeBar[m.barFocus].act)
		}
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if t, ok := m.zones.at(msg.X, msg.Y); ok {
				return m, m.activate(t.act)
			}
		}
	}
	return m, nil
}

func (m *homeScreen) activate(a action) tea.Cmd {
	switch a {
	case actHelp:
		return showHelp
	case actQuit:
		return quitApp
	}
	return nil
}

func (m *homeScreen) View() string {
	m.zones.reset()
	head := "igris"
	var body []string
	switch {
	case m.snapErr != nil:
		body = wrap("could not read the project: "+m.snapErr.Error(), m.w)
	case m.snap == nil:
		body = []string{m.th.paint(lookDim, "reading the project…")}
	default:
		head += " · " + m.snap.Project
		body = []string{m.th.paint(lookDim, m.snap.Root)}
	}
	out := []string{m.th.paint(lookAccentBold, fit(head, m.w))}
	rows := max(m.h-2, 0)
	for i := range rows {
		out = append(out, fit(line(body, i), m.w))
	}
	var bar strings.Builder
	x := 0
	for i, o := range homeBar {
		st := btnNormal
		if i == m.barFocus {
			st = btnFocused
		}
		if i > 0 {
			bar.WriteString(" ")
			x++
		}
		b := m.th.button(o.label, st)
		m.zones.add(rect{x, m.h - 1, textWidth(b), 1}, target{act: o.act})
		bar.WriteString(b)
		x += textWidth(b)
	}
	out = append(out, fit(bar.String(), m.w))
	return strings.Join(out[:min(len(out), m.h)], "\n")
}
