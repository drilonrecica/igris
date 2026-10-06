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
		out = append(out, e.at.In(m.loc).Format("15:04")+" "+e.text)
	}
	return out
}

// buttons are the actions the bar offers right now.
func (m *model) buttons() []option {
	var out []option
	if m.cur != nil && m.cur.session != nil {
		out = append(out, option{"Open session", actOpen})
	}
	if !m.ended {
		label := "Pause"
		if m.paused {
			label = "Resume"
		}
		out = append(out, option{label, actPause})
	}
	if m.cur != nil && !m.ended {
		out = append(out, option{"Done", actDone})
	}
	return append(out, option{"Quit", actQuit})
}

// since formats the time from t to now, e.g. "4m12s".
func since(now, t time.Time) string {
	if t.IsZero() {
		return "0s"
	}
	return now.Sub(t).Truncate(time.Second).String()
}
