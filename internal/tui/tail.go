package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rivo/uniseg"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The live tail (SPEC §15.3): the last lines of each card's session in the
// card, read through Options.Tail every poll interval. It is display only:
// it never goes to the log, and any failure just hides it.

const (
	// tailWide and tailNarrow are how many lines the card shows.
	tailWide   = 6
	tailNarrow = 3
	// tailFetch is how many lines a refresh asks for: the backend reads as
	// many and drops the blank ones and Claude Code's input box, and
	// Claude Code leaves dozens of blank rows above that box.
	tailFetch = 60
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
	// tailMsg is a refresh's result for the sessions of generation gen:
	// the lines read for each task; a task whose read failed has none.
	tailMsg struct {
		m     *model
		gen   int
		lines map[string][]string
	}
)

// TaskTailer reads the last n lines of an active task's session, "" for
// the current task's: the engine, or a client of a served run.
type TaskTailer interface {
	TailTask(ctx context.Context, task string, n int) ([]string, error)
}

// TailOf is the live tail's source for a run steered through sender (the
// engine, a TaskTailer for its active tasks' sessions) and how often to
// read it: none when [tui] tail is off or sender can't tail.
func TailOf(cfg *config.Config, sender Sender) (func(context.Context, string, int) ([]string, error), time.Duration) {
	t, ok := sender.(TaskTailer)
	if cfg == nil || !cfg.TUI.Tail || !ok {
		return nil, 0
	}
	return t.TailTask, cfg.PollInterval.Std()
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

// tailing reports whether c has a tail to show: an agent task whose
// session is open.
func (m *model) tailing(c *current) bool {
	return m.opts.Tail != nil && !m.ended && !m.lost && c != nil && !c.user && c.session != nil && c.state != stateLost
}

// refreshTail starts a refresh and schedules the next tick while the run
// goes on.
func (m *model) refreshTail() tea.Cmd {
	if m.ended {
		return nil // no more ticks
	}
	return tea.Batch(m.fetchTail(), m.tailTick())
}

// fetchTail reads every card's tail, one read at a time (a served run
// answers one tail request of a client at a time); nil when a refresh is
// in flight or there is nothing to tail.
func (m *model) fetchTail() tea.Cmd {
	var ids []string
	for _, c := range m.cards {
		if m.tailing(c) {
			ids = append(ids, c.id)
		}
	}
	if len(ids) == 0 || m.tailBusy {
		return nil
	}
	m.tailBusy = true
	tail, gen, ctx := m.opts.Tail, m.tailGen, m.ctx
	return func() tea.Msg {
		out := make(map[string][]string, len(ids))
		for _, id := range ids {
			ctx, cancel := context.WithTimeout(ctx, tailTimeout)
			lines, err := tail(ctx, id, tailFetch)
			cancel()
			if err == nil {
				out[id] = lines
			}
		}
		return tailMsg{m: m, gen: gen, lines: out}
	}
}

// gotTail takes a refresh's result: one for earlier sessions is dropped,
// and a task whose read failed loses its tail.
func (m *model) gotTail(msg tailMsg) {
	m.tailBusy = false
	if msg.gen != m.tailGen {
		return
	}
	m.tails = make(map[string][]string, len(msg.lines))
	for id, lines := range msg.lines {
		m.tails[id] = tailLines(lines, tailWide)
	}
}

// resetTail forgets the tails when a session changes; a refresh still in
// flight is for the old ones and will be dropped.
func (m *model) resetTail() {
	m.tails = map[string][]string{}
	m.tailGen++
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
	for s != "" {
		g, rest, gw, _ := uniseg.FirstGraphemeClusterInString(s, -1)
		s = rest
		if g == "\t" {
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteString(g)
		col += gw
	}
	return b.String()
}

// tailView is c's card's tail, w cells wide: the last lines that fit the
// layout, dimmed so they don't read as igris's own text. A line wider than
// the card loses its leading spaces before it is clipped: Claude Code pads
// right-aligned text (`◐ medium · /effort`) to the pane's width, and
// clipping the padding would leave a blank row ending in "…".
func (m *model) tailView(c *current, w int) []string {
	tail := m.tails[c.id]
	if !m.tailing(c) || len(tail) == 0 {
		return nil
	}
	n := tailNarrow
	if m.wide() && !m.multi() {
		n = tailWide // several cards share the height: each a shorter tail
	}
	lines := tail[max(len(tail)-n, 0):]
	out := make([]string, len(lines))
	for i, l := range lines {
		if textWidth(l) > w {
			l = strings.TrimLeft(l, " ")
		}
		out[i] = m.th.paint(lookDim, fit(l, w))
	}
	return out
}
