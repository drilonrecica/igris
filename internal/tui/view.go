package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/state"
)

// wideWidth is where the two-column layout starts (SPEC §15.1).
const wideWidth = 100

// View draws the screen and records its zones: the wide or the narrow
// layout, a page in its place, and a dialog floating over the dimmed
// screen (the whole screen when narrow, SPEC §15.5).
func (m *model) View() string {
	m.zones.reset()
	w, h := max(m.width, 10), max(m.height, 3)
	var screen []string
	switch {
	case m.page != nil:
		m.page.project, m.page.status = m.opts.Project, m.statusText()
		var z zones
		screen, z = m.page.render(m.th, w, h)
		m.zones.merge(z, 0, 0)
	case m.ended:
		screen = m.summaryScreen(w, h)
	case m.wide():
		screen = m.wideScreen(w, h)
	default:
		screen = m.narrowScreen(w, h)
	}
	if m.dialog != nil {
		m.zones.reset() // what is under the dialog takes no clicks
		var z zones
		if m.wide() {
			screen, z = m.th.floatDialog(screen, m.dialog, w, 2, h-chromeRows)
		} else {
			screen, z = m.th.fullScreenDialog(m.dialog, w, h)
		}
		m.zones.merge(z, 0, 0)
	}
	return strings.Join(screen, "\n")
}

// status is the run's status word (SPEC §15.4) and how it is drawn:
// what igris is doing, in one of the seven words.
func (m *model) status() (string, tone) {
	switch {
	case m.ended:
		return look.Done.String(), lookTitle
	case m.needsOwner():
		return look.NeedsYou.Label(), lookAlert
	case m.holding && m.cur == nil:
		return look.Paused.String(), lookTitle
	case m.limited != nil && m.cur == nil:
		// The usage hold (SPEC §6.9, §15.4): the window, its use and the
		// reset with its countdown.
		return look.Paused.String() + " · " + m.limitText("resumes"), lookTitle
	case m.paused && m.cur != nil:
		return look.Working.Label() + " · pauses after " + m.cur.id, lookAccentBold
	case m.limited != nil:
		// Tasks still run; the next start waits for the reset.
		return look.Working.Label() + " · " + m.limitText("next start"), lookAccentBold
	}
	return look.Working.Label(), lookAccentBold
}

// limitText is the hold's words, `5h limit 96% · resumes 09:00 (≈ 3h12m)`,
// with verb before the reset.
func (m *model) limitText(verb string) string {
	l := m.limited
	return l.Label() + " · " + verb + " " + look.Until(l.ResetsAt.In(m.loc), m.opts.Now())
}

// statusText is the status word, painted.
func (m *model) statusText() string {
	word, l := m.status()
	return m.th.paint(l, word)
}

// needsOwner reports whether anything waits on the owner: a task that
// needs them, a pending question or a reset request (SPEC §15.4).
func (m *model) needsOwner() bool {
	if len(m.asks) > 0 || m.resetAsk != nil || m.budgetAsk != nil {
		return true
	}
	return slices.ContainsFunc(m.cards, (*current).needsOwner)
}

// wide reports whether the frame uses the wide layout.
func (m *model) wide() bool { return m.width >= wideWidth && m.height >= wideMinHeight }

// stateText is c's state line (SPEC §15.4): the task's glyph and a short
// phrase in sentence case, the status words' vocabulary and no other.
func (m *model) stateText(c *current) string {
	switch c.state {
	case stateNeedsYou:
		return strings.Join(append([]string{look.NeedsYou.Label()}, m.needParts(c)...), " · ")
	case stateWaiting:
		return fmt.Sprintf("%s Waiting on background work · %s", look.GlyphWaiting, since(m.opts.Now(), c.since))
	case stateLimited:
		// The window and the reset with its countdown, once the
		// transcript's hit named them (SPEC §6.3, §15).
		s := look.GlyphWaiting + " Usage limit"
		if l := c.limit; l != nil {
			s += " · " + look.Window(l.Window) + " · continues " + look.Until(l.ResetsAt.In(m.loc), m.opts.Now())
		}
		return s
	case stateYourTurn:
		return look.NeedsYou.Label() + " · your turn"
	case stateVerifying:
		return look.GlyphRunning + " Verifying"
	case stateQuestion:
		return look.NeedsYou.Label() + " · your answer"
	case stateLost:
		return look.NeedsYou.Label() + " · session lost"
	}
	return look.Working.Label()
}

// episodic reports whether a Needs you of reason is an idle, blocked or
// waiting episode, which the state line times; "" is one too (an igris
// before 1.7 sends no reason).
func episodic(reason string) bool {
	switch reason {
	case "", state.ReasonIdle, state.ReasonBlocked, state.ReasonWaiting:
		return true
	}
	return false
}

// needWhat splits c's Needs-you detail into what the state line says and
// the rest, the line below it: an episode's detail stays whole below its
// timed state line; any other reads "<what>; <what to do>" (engine.Event).
func needWhat(c *current) (what, rest string) {
	if episodic(c.reason) {
		return "", c.detail
	}
	what, rest, _ = strings.Cut(c.detail, "; ")
	return what, rest
}

// needParts follow "! Needs you" on c's state line (SPEC §15.3): how long
// an idle or waiting episode has lasted (`idle 45s`), else what the task
// waits for (`verify failed 3 times`). An attempt that is overdue or on
// another model says so here too, not as a second state.
func (m *model) needParts(c *current) []string {
	var parts []string
	switch what, _ := needWhat(c); {
	case c.reason == state.ReasonTaskOverdue && c.overdue != "", c.reason == state.ReasonRankMismatch && c.rankMismatch != "":
		// Said below, once.
	case !episodic(c.reason):
		if what != "" {
			parts = append(parts, what)
		}
	case c.waiting:
		parts = append(parts, "waiting "+since(m.opts.Now(), c.since))
	default:
		parts = append(parts, "idle "+since(m.opts.Now(), c.since))
	}
	if c.overdue != "" {
		parts = append(parts, "overdue · "+c.overdue)
	}
	if c.rankMismatch != "" {
		parts = append(parts, "rank mismatch · "+c.rankMismatch)
	}
	return parts
}

// needKind says in a few words what task c waits on the owner for, for
// the Needs-you banner (SPEC §15.1): its question's kind, else its state.
func (m *model) needKind(c *current) string {
	if q := m.cardAsk(c); q != nil {
		return q.Question.Kind()
	}
	switch c.state {
	case stateYourTurn:
		return "your turn"
	case stateLost:
		return "session lost"
	case stateNeedsYou:
		if what, _ := needWhat(c); what != "" {
			return what
		}
		if c.detail != "" {
			return strings.TrimPrefix(c.detail, "the session is ")
		}
		if c.waiting {
			return "waiting without a signal"
		}
		return "idle without a signal"
	}
	return "your answer"
}

// logRows is how many log lines the frame shows.
func (m *model) logRows() int { return max(m.height-3, 1) }

// logView returns the n log lines ending m.scroll lines before the newest.
func (m *model) logView(n int) []string {
	end := len(m.log) - min(m.scroll, max(len(m.log)-1, 0))
	start := max(end-n, 0)
	out := make([]string, 0, n)
	for _, e := range m.log[start:end] {
		out = append(out, m.th.paint(lookDim, e.at.In(m.loc).Format("15:04"))+" "+m.logText(e, e.text))
	}
	return out
}

// logText draws text, a line of entry e, in the entry's look.
func (m *model) logText(e logEntry, text string) string {
	if e.tone == lookPlain {
		return m.th.marks(text)
	}
	return m.th.paint(e.tone, text)
}

// stateLook is how c's state is drawn: waiting on the owner stands out.
func stateLook(c *current) tone {
	switch c.state {
	case stateNeedsYou, stateYourTurn, stateQuestion, stateLost:
		return lookAlert
	case stateVerifying:
		return lookAccent
	case stateWorking:
		return lookAccent
	}
	return lookPlain
}

// buttons are the actions the bar offers right now (SPEC §15.3): only
// those that would do something, each with its key, and ? and Home (Quit
// when the view has no home beneath it) last.
func (m *model) buttons() []button {
	if m.lost {
		return []button{{label: "Quit", key: "q", act: actQuit}} // until the view reconnects
	}
	quit := "Quit"
	if m.opts.Leave != nil {
		quit = "Home"
	}
	if m.ended {
		// The summary (SPEC §15.3): Home first and focused, History, the
		// report command, ?.
		out := []button{{label: quit, key: "q", act: actQuit}}
		if m.opts.History != nil {
			out = append(out, button{label: "History", key: "h", act: actHistory})
		}
		out = append(out, button{"Copy report command", "Copy", "y", actCopy})
		return append(out, button{key: "?", act: actHelp})
	}
	var out []button
	running := m.cur != nil && !m.ended
	if m.question() != nil && m.dialog == nil {
		out = append(out, button{label: "Answer…", act: actAnswer})
	}
	if m.canOpen(m.cur) {
		out = append(out, button{"Open session", "Open", "o", actOpen})
	}
	if !m.ended {
		out = append(out, button{label: "Mode", key: "m", act: actMode})
	}
	if m.taskModeTarget() != nil {
		out = append(out, button{label: "Task mode", key: "M", act: actTaskMode})
	}
	if m.limited != nil && !m.ended {
		// The usage hold's one action of its own (SPEC §6.9): the next
		// start ignores the threshold once.
		out = append(out, button{"Start anyway", "Start", "a", actLimitStart})
	}
	if !m.ended {
		label := "Pause"
		if m.paused {
			label = "Resume"
		}
		out = append(out, button{label: label, key: "p", act: actPause})
	}
	if running {
		done := "Done"
		if m.cur.user {
			done = "Done…" // it asks for a note
		}
		out = append(out, button{done, "Done", "d", actDone})
		if !m.cur.user {
			out = append(out, button{"Retry…", "Retry", "r", actRetry})
		}
		out = append(out, button{"Skip…", "Skip", "s", actSkip})
	}
	if !m.ended {
		out = append(out, button{"Stop…", "Stop", "x", actStopAsk})
	}
	if m.side != nil {
		out = append(out, button{label: "Side…", key: "S", act: actSide})
	}
	return append(out, button{key: "?", act: actHelp}, button{label: quit, key: "q", act: actQuit})
}

// modal reports whether a dialog or page holds the focus.
func (m *model) modal() bool { return m.dialog != nil || m.page != nil }

// barFocused reports whether the action bar has the focus.
func (m *model) barFocused() bool { return m.focus == focusBar && !m.modal() }

// rowMark is the marker column of a list row: "›" on the selected row
// while its list has the focus.
func rowMark(selected bool) string {
	if selected {
		return "›"
	}
	return " "
}

// areaTitle is a region's title, marked while it has the focus.
func (m *model) areaTitle(name string, a area) string {
	if m.focus == a && !m.modal() {
		return m.th.paint(lookAccentBold, "› "+name)
	}
	return m.th.paint(lookTitle, name)
}

// since formats the time from t to now, e.g. "4m12s".
func since(now, t time.Time) string {
	if t.IsZero() {
		return "0s"
	}
	return now.Sub(t).Truncate(time.Second).String()
}
