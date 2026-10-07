package tui

import (
	"fmt"
	"time"
)

// wideWidth is where the two-column layout starts (SPEC §15.1).
const wideWidth = 100

// View draws the frame and records its zones.
func (m *model) View() string {
	m.zones.reset()
	if m.width >= wideWidth && m.height >= wideMinHeight {
		return m.wideView()
	}
	return m.narrowView()
}

// stateText says what the current task needs, in words.
func (m *model) stateText() string {
	c := m.cur
	switch c.state {
	case stateNeedsYou:
		return fmt.Sprintf("NEEDS YOU (idle %s)", since(m.opts.Now(), c.since))
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
	if e.look == lookPlain {
		return m.th.marks(text)
	}
	return m.th.paint(e.look, text)
}

// stateLook is how the current task's state is drawn: waiting on the
// owner stands out.
func (m *model) stateLook() look {
	switch m.cur.state {
	case stateNeedsYou, stateYourTurn, stateQuestion, stateLost:
		return lookAlert
	case stateVerifying:
		return lookAccent
	}
	return lookPlain
}

// buttons are the actions the bar offers right now (SPEC §15.3): only
// those that would do something.
func (m *model) buttons() []option {
	var out []option
	running := m.cur != nil && !m.ended
	if m.asked != nil && m.dialog == nil {
		out = append(out, option{"Answer…", actAnswer})
	}
	if m.cur != nil && m.cur.session != nil {
		out = append(out, option{"Open session", actOpen})
	}
	if !m.ended {
		out = append(out, option{"Mode", actMode})
	}
	if m.taskModeTarget() != nil {
		out = append(out, option{"Task mode", actTaskMode})
	}
	if !m.ended {
		label := "Pause"
		if m.paused {
			label = "Resume"
		}
		out = append(out, option{label, actPause})
	}
	if running {
		out = append(out, option{"Done", actDone})
		if !m.cur.user {
			out = append(out, option{"Retry", actRetry})
		}
		out = append(out, option{"Skip", actSkip})
	}
	if !m.ended {
		out = append(out, option{"Stop", actStopAsk})
	}
	return append(out, option{"?", actHelp}, option{"Quit", actQuit})
}

// modal reports whether a dialog or page holds the focus.
func (m *model) modal() bool { return m.dialog != nil || m.page != nil }

// barFocused reports whether the action bar has the focus.
func (m *model) barFocused() bool { return m.focus == focusBar && !m.modal() }

// barState is how the bar button for a is drawn: focused is the button
// with the focus, if the bar has it.
func barState(a, focused action, modal bool) btnState {
	switch {
	case modal:
		return btnInactive
	case a == focused:
		return btnFocused
	case a == actAnswer:
		return btnAttention
	}
	return btnNormal
}

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
