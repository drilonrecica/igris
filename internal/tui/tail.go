package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The live tail (SPEC §15.3): the last lines of the current session in the
// card, read through Options.Tail every poll interval. It is display only:
// it never goes to the log, and any failure just hides it.

const (
	// tailWide and tailNarrow are how many lines the card shows.
	tailWide   = 6
	tailNarrow = 3
	// tailFetch is how many lines a refresh asks for: blank lines are
	// dropped, and Claude Code leaves many between its messages.
	tailFetch = 2 * tailWide
	// tailTimeout bounds one refresh.
	tailTimeout = 2 * time.Second
	// tailEvery is the refresh interval when Options.TailEvery is not set.
	tailEvery = 2 * time.Second
	// tabWidth is where tabs in the tail stop.
	tabWidth = 8
)

// The tail's messages name their model: in the app another screen may be
// on top when they arrive, or a later run view.
type (
	tailTickMsg struct{ m *model }
	// tailMsg is a refresh's result for the session of generation gen.
	tailMsg struct {
		m     *model
		gen   int
		lines []string
		err   error
	}
)

// TailOf is the live tail's source for a run steered through sender (the
// engine, a backend.Tailer for its current session) and how often to read
// it: none when [tui] tail is off or sender can't tail.
func TailOf(cfg *config.Config, sender Sender) (func(context.Context, int) ([]string, error), time.Duration) {
	t, ok := sender.(backend.Tailer)
	if cfg == nil || !cfg.TUI.Tail || !ok {
		return nil, 0
	}
	return t.Tail, cfg.PollInterval.Std()
}

// tailTick schedules the next refresh; nil without a Tail.
func (m *model) tailTick() tea.Cmd {
	if m.opts.Tail == nil {
		return nil
	}
	every := m.opts.TailEvery
	if every <= 0 {
		every = tailEvery
	}
	return tea.Tick(every, func(time.Time) tea.Msg { return tailTickMsg{m} })
}

// tailing reports whether the current task has a tail to show: an agent
// task whose session is open.
func (m *model) tailing() bool {
	c := m.cur
	return m.opts.Tail != nil && !m.ended && c != nil && !c.user && c.session != nil && c.state != stateLost
}

// refreshTail starts a refresh and schedules the next tick while the run
// goes on.
func (m *model) refreshTail() tea.Cmd {
	if m.ended {
		return nil // no more ticks
	}
	return tea.Batch(m.fetchTail(), m.tailTick())
}

// fetchTail reads the tail, one read at a time; nil when one is in flight
// or there is nothing to tail.
func (m *model) fetchTail() tea.Cmd {
	if !m.tailing() || m.tailBusy {
		return nil
	}
	m.tailBusy = true
	tail, gen, ctx := m.opts.Tail, m.tailGen, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, tailTimeout)
		defer cancel()
		lines, err := tail(ctx, tailFetch)
		return tailMsg{m: m, gen: gen, lines: lines, err: err}
	}
}

// gotTail takes a refresh's result: one for an earlier session is
// dropped, and an error hides the tail.
func (m *model) gotTail(msg tailMsg) {
	m.tailBusy = false
	if msg.gen != m.tailGen {
		return
	}
	if msg.err != nil {
		m.tail = nil
		return
	}
	m.tail = tailLines(msg.lines, tailWide)
}

// resetTail forgets the tail when the session changes; a refresh still in
// flight is for the old one and will be dropped.
func (m *model) resetTail() {
	m.tail = nil
	m.tailGen++
}

// sessionOf is the current task's session ref, nil when there is none.
func (m *model) sessionOf() *backend.SessionRef {
	if m.cur == nil {
		return nil
	}
	return m.cur.session
}

// tailLines cleans raw pane lines for drawing (SPEC §16): escape sequences
// and control characters out, tabs expanded, trailing spaces trimmed,
// blank lines dropped. It keeps the last n.
func tailLines(raw []string, n int) []string {
	var out []string
	for _, l := range raw {
		l = strings.TrimRight(textsafe.Line(expandTabs(textsafe.Clean(l))), " ")
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// expandTabs replaces tabs with spaces up to the next tab stop.
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += textWidth(string(r))
	}
	return b.String()
}

// tailView is the card's tail, w cells wide: the last lines that fit the
// layout, dimmed so they don't read as igris's own text.
func (m *model) tailView(w int) []string {
	if !m.tailing() || len(m.tail) == 0 {
		return nil
	}
	n := tailNarrow
	if m.wide() {
		n = tailWide
	}
	lines := m.tail[max(len(m.tail)-n, 0):]
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = m.th.paint(lookDim, fit(l, w))
	}
	return out
}
