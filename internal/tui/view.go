package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/look"
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
	case m.paused && m.cur != nil:
		return look.Working.Label() + " · pauses after " + m.cur.id, lookAccentBold
	}
	return look.Working.Label(), lookAccentBold
}

// statusText is the status word, painted.
func (m *model) statusText() string {
	word, l := m.status()
	return m.th.paint(l, word)
}

// needsOwner reports whether anything waits on the owner: a task that
// needs them, a pending question or a reset request (SPEC §15.4).
func (m *model) needsOwner() bool {
	if len(m.asks) > 0 || m.resetAsk != nil {
		return true
	}
	return slices.ContainsFunc(m.cards, (*current).needsOwner)
}

// wide reports whether the frame uses the wide layout.
func (m *model) wide() bool { return m.width >= wideWidth && m.height >= wideMinHeight }

// stateText says what c's task needs, in words.
func (m *model) stateText(c *current) string {
	switch c.state {
	case stateNeedsYou:
		word := "idle"
		if c.waiting {
			word = "waiting"
		}
		return fmt.Sprintf("NEEDS YOU (%s %s)", word, since(m.opts.Now(), c.since))
	case stateWaiting:
		return fmt.Sprintf("waiting on background work (%s)", since(m.opts.Now(), c.since))
	case stateYourTurn:
		return "YOUR TURN"
	case stateVerifying:
		return "verifying"
	case stateQuestion:
		return "WAITING FOR YOUR ANSWER"
	case stateLost:
		return "SESSION LOST"
	}
	return "working"
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
	quit := "Quit"
	if m.opts.Leave != nil {
		quit = "Home"
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
