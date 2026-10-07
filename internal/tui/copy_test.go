package tui

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
)

const testUUID = "0b1a2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

// lockedBuf is a writer the program and the test can share.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// osc52Of is the sequence that copies text.
func osc52Of(text string) string {
	var b bytes.Buffer
	_ = copyText(&b, text)
	return b.String()
}

func TestOSC52Sequence(t *testing.T) {
	// "hi" is aGk= in base64.
	if got, want := osc52Of("hi"), "\x1b]52;c;aGk=\x07"; got != want {
		t.Errorf("sequence = %q, want %q", got, want)
	}
}

var runningEvents = []engine.Event{
	{Kind: engine.TaskStarted, Task: "M0-02", Title: "Parser", Rank: "sonnet", Model: "sonnet", Mode: "plan"},
	{Kind: engine.SessionOpened, Task: "M0-02", Session: &backend.SessionRef{Backend: "herdr", PaneID: "p1"}, ClaudeSession: testUUID},
}

func TestCopyResumeCommandInProgram(t *testing.T) {
	out := &lockedBuf{}
	feed := NewFeed()
	for _, ev := range runningEvents {
		ev.At = t0
		feed.Push(ev)
	}
	m := newModel(context.Background(), Options{Project: "sinjal", Backend: "herdr", Feed: feed, Sender: &sender{}, Out: out})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))
	t.Cleanup(func() { _ = tm.Quit() })
	seen(t, tm, "M0-02")
	key(tm, "y")
	seen(t, tm, "copied")
	if got, want := out.String(), osc52Of("claude --resume "+testUUID); got != want {
		t.Errorf("clipboard sequence = %q, want %q", got, want)
	}
}

func TestCopyTargets(t *testing.T) {
	newH := func(out *lockedBuf, evs ...engine.Event) *harness {
		hs := newHarness(t, 120, 40)
		hs.m.opts.Out = out
		hs.events(evs...)
		return hs
	}

	t.Run("log line when the log has the focus", func(t *testing.T) {
		out := &lockedBuf{}
		hs := newH(out, runningEvents...)
		hs.keys("tab") // the bar has the focus first; the log is next
		hs.key("y")
		want := osc52Of(hs.m.log[len(hs.m.log)-1].text)
		if out.String() != want || !strings.Contains(hs.m.View(), "copied") {
			t.Errorf("sequence %q, want %q; view:\n%s", out.String(), want, hs.m.View())
		}
	})

	t.Run("selected log line when scrolled back", func(t *testing.T) {
		out := &lockedBuf{}
		hs := newH(out, runningEvents...)
		hs.keys("tab", "up")
		hs.key("y")
		want := osc52Of(hs.m.log[len(hs.m.log)-2].text)
		if out.String() != want {
			t.Errorf("sequence %q, want %q", out.String(), want)
		}
	})

	t.Run("no session, nothing to copy", func(t *testing.T) {
		out := &lockedBuf{}
		hs := newH(out)
		hs.key("y")
		if out.String() != "" || !strings.Contains(hs.m.View(), "nothing to copy") {
			t.Errorf("wrote %q; view:\n%s", out.String(), hs.m.View())
		}
	})

	t.Run("lost session, nothing to copy", func(t *testing.T) {
		out := &lockedBuf{}
		hs := newH(out, append(append([]engine.Event{}, runningEvents...), engine.Event{Kind: engine.SessionLost, Task: "M0-02"})...)
		hs.key("y")
		if out.String() != "" {
			t.Errorf("wrote %q", out.String())
		}
	})

	t.Run("notice goes at the next key", func(t *testing.T) {
		hs := newH(&lockedBuf{}, runningEvents...)
		hs.key("y")
		hs.key("down")
		if strings.Contains(hs.m.View(), "copied") {
			t.Error("notice stayed")
		}
	})

	t.Run("notice goes after a few seconds", func(t *testing.T) {
		hs := newH(&lockedBuf{}, runningEvents...)
		hs.key("y")
		hs.now = hs.now.Add(noticeFor)
		hs.m.Update(tickMsg{})
		if strings.Contains(hs.m.View(), "copied") {
			t.Error("notice stayed")
		}
	})

	t.Run("NO_COLOR and narrow", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		out := &lockedBuf{}
		hs := newHarness(t, 50, 20)
		hs.m.opts.Out = out
		hs.events(runningEvents...)
		hs.key("y")
		if out.String() != osc52Of("claude --resume "+testUUID) || !strings.Contains(hs.m.View(), "copied") {
			t.Errorf("sequence %q; view:\n%s", out.String(), hs.m.View())
		}
	})
}

func TestHelpListsCopy(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.key("?")
	if !strings.Contains(hs.m.View(), "Copy") {
		t.Errorf("help lacks Copy:\n%s", hs.m.View())
	}
}

func TestReviewCopiesProposalPath(t *testing.T) {
	out := &lockedBuf{}
	o := reviewOpts()
	o.ProposalPath, o.Out = ".igris/proposals/tasks.md", out
	rh := newReviewHarness(t, 80, 24, o)
	rh.keys("y")
	if got, want := out.String(), osc52Of(".igris/proposals/tasks.md"); got != want {
		t.Errorf("sequence %q, want %q", got, want)
	}
	if rh.quit || !strings.Contains(rh.r.View(), "copied") {
		t.Errorf("view:\n%s", rh.r.View())
	}
}
