package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
)

// sampleTail is synthetic pane text as a session shows it: older lines,
// blank lines, tabs, escape sequences, a bell and a line too long for the
// card.
var sampleTail = []string{
	"earlier line one",
	"earlier line two",
	"⏺ Running the tests with make test.",
	"",
	"  ⎿  ok\tgithub.com/example/demo\t0.012s",
	"\x1b[31m  FAIL\x1b[0m TestParse: a line long enough to be clipped at the card's width in every layout",
	"\x07",
	"   ",
	"✻ Thinking… (esc to interrupt)\x1b]0;title\x07",
	"> ",
}

// tailSource is a Tail func that records its calls.
type tailSource struct {
	mu    sync.Mutex
	lines []string
	err   error
	calls []int // the n of each call
}

func (s *tailSource) tail(ctx context.Context, n int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, n)
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("tail called without a deadline")
	}
	return slices.Clone(s.lines), s.err
}

func (s *tailSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// withTail makes the session show lines and runs one refresh, as the
// program would on a tick.
func (hs *harness) withTail(lines ...string) *tailSource {
	hs.t.Helper()
	src := &tailSource{lines: lines}
	hs.m.opts.Tail = src.tail
	hs.refreshTail()
	return src
}

// refreshTail runs one refresh and hands its result to the model.
func (hs *harness) refreshTail() {
	if msg := run(hs.m.fetchTail()); msg != nil {
		hs.m.Update(msg)
	}
}

func TestTailLines(t *testing.T) {
	got := tailLines(sampleTail, tailWide)
	want := []string{
		"earlier line two",
		"⏺ Running the tests with make test.",
		"  ⎿  ok github.com/example/demo 0.012s",
		"  FAIL TestParse: a line long enough to be clipped at the card's width in every layout",
		"✻ Thinking… (esc to interrupt)",
		">",
	}
	if !slices.Equal(got, want) {
		t.Errorf("tailLines =\n%q\nwant\n%q", got, want)
	}
	for in, want := range map[string]string{
		"a\tb":       "a       b",
		"abc\td":     "abc     d",
		"\t":         "        ",
		"ünï\tx":     "ünï     x",
		"界\tx":       "界      x", // a wide rune takes two cells
		"no tabs":    "no tabs",
		"12345678\t": "12345678        ",
	} {
		if got := expandTabs(in); got != want {
			t.Errorf("expandTabs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCardShowsTheTail(t *testing.T) {
	for _, tc := range []struct {
		w, h  int
		lines int
	}{{120, 40, tailWide}, {80, 24, tailNarrow}, {50, 20, tailNarrow}} {
		hs := cardHarness(t, tc.w, tc.h, "M0-02")
		src := hs.withTail(sampleTail...)
		v := hs.m.View()
		checkFits(t, v, tc.w, tc.h)
		// One key per cleaned line, oldest first (the last, ">", is left out).
		keys := []string{"earlier line two", "Running the tests", "github.com/example", "FAIL TestParse", "Thinking"}
		for i, k := range keys {
			old := i < tailWide-tc.lines
			if shown := strings.Contains(v, k); shown == old {
				t.Errorf("%dx%d: %q shown %v, want the last %d lines:\n%s", tc.w, tc.h, k, shown, tc.lines, v)
			}
		}
		if strings.Contains(v, "\x1b") || strings.Contains(v, "\a") {
			t.Errorf("%dx%d: escape sequences reached the view", tc.w, tc.h)
		}
		if !strings.Contains(v, "read igris.toml") || !strings.Contains(v, "[t] Details") {
			t.Errorf("%dx%d: the text or the buttons are gone:\n%s", tc.w, tc.h, v)
		}
		if got := src.calls; !slices.Equal(got, []int{tailFetch}) {
			t.Errorf("tail calls %v", got)
		}
	}
}

// TestTailGivesWayToTheCard: in a short card the task text goes first,
// then the tail's oldest lines; the head and the buttons stay.
func TestTailGivesWayToTheCard(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-03")
	hs.withTail(sampleTail...)
	full := hs.m.renderCard(50, 0, 0, -1, false)
	for rows := len(full); rows >= 5; rows-- {
		card := hs.m.renderCard(50, 0, 0, rows, false)
		text := strings.Join(card, "\n")
		if len(card) > rows || !strings.Contains(card[0], "M0-03") || !strings.Contains(card[len(card)-1], "[t] Details") {
			t.Fatalf("%d rows:\n%s", rows, text)
		}
		hasText := strings.Contains(text, "this sentence")
		hasTail := strings.Contains(text, "Thinking")
		if hasText && !hasTail {
			t.Errorf("%d rows: the text stayed and the tail went:\n%s", rows, text)
		}
	}
	short := strings.Join(hs.m.renderCard(50, 0, 0, 7, false), "\n")
	if strings.Contains(short, "this sentence") || !strings.Contains(short, "Thinking") || strings.Contains(short, "earlier line two") {
		t.Errorf("7 rows: want the newest tail lines and no text:\n%s", short)
	}
}

func TestTailIsHidden(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(hs *harness, src *tailSource)
	}{
		{"error", func(hs *harness, src *tailSource) {
			src.err = errors.New("herdr agent read: boom")
			hs.refreshTail()
		}},
		{"gone", func(hs *harness, src *tailSource) {
			src.err = backend.ErrSessionGone
			hs.refreshTail()
		}},
		{"empty", func(hs *harness, src *tailSource) {
			src.lines = []string{"", "  ", "\x1b[0m"}
			hs.refreshTail()
		}},
		{"lost session", func(hs *harness, _ *tailSource) {
			hs.events(engine.Event{Kind: engine.SessionLost, Task: "M0-02", Detail: "the session is gone"})
		}},
		{"retry", func(hs *harness, _ *tailSource) {
			hs.events(engine.Event{Kind: engine.Retrying, Task: "M0-02"})
		}},
		{"task done", func(hs *harness, _ *tailSource) {
			hs.events(engine.Event{Kind: engine.TaskDone, Task: "M0-02"})
		}},
		{"run stopped", func(hs *harness, _ *tailSource) {
			hs.events(engine.Event{Kind: engine.RunStopped, Detail: "stopped"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs := cardHarness(t, 120, 40, "M0-02")
			src := hs.withTail(sampleTail...)
			if !strings.Contains(hs.m.View(), "Thinking") {
				t.Fatal("no tail to begin with")
			}
			tc.setup(hs, src)
			if v := hs.m.View(); strings.Contains(v, "Thinking") {
				t.Errorf("the tail is still shown:\n%s", v)
			}
			if v := hs.m.View(); strings.Contains(v, "boom") || strings.Contains(v, "session is gone") && tc.name != "lost session" {
				t.Errorf("the error reached the view:\n%s", v)
			}
		})
	}
}

// TestTailNotForUserTasks: a user task has no session, so nothing is read.
func TestTailNotForUserTasks(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.withPlan(cardPlan)
	hs.events(engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: "M0-04", Title: "Pick a license"},
		engine.Event{Kind: engine.YourTurn, Task: "M0-04", Detail: "**Pick a license** — your call"})
	src := hs.withTail(sampleTail...)
	if src.count() != 0 || strings.Contains(hs.m.View(), "Thinking") {
		t.Errorf("a user task was tailed: %d calls", src.count())
	}
}

// TestTailOneReadAtATime: a tick while a read is in flight starts none,
// and a read for an earlier session is dropped.
func TestTailOneReadAtATime(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-02")
	src := &tailSource{lines: []string{"old session"}}
	hs.m.opts.Tail = src.tail
	first := hs.m.fetchTail()
	if first == nil || hs.m.fetchTail() != nil {
		t.Fatal("want one read in flight, no second")
	}
	// A retry opens another session before the first read comes back.
	hs.events(engine.Event{Kind: engine.Retrying, Task: "M0-02"},
		engine.Event{Kind: engine.SessionOpened, Task: "M0-02", Mode: "plan", Session: &backend.SessionRef{Backend: "herdr", PaneID: "p2"}})
	hs.m.Update(run(first))
	if strings.Contains(hs.m.View(), "old session") {
		t.Error("a read for the earlier session was drawn")
	}
	src.lines = []string{"new session"}
	hs.refreshTail()
	if !strings.Contains(hs.m.View(), "new session") {
		t.Error("the next read was not drawn")
	}
}

func TestTailOf(t *testing.T) {
	on := config.Default()
	off := config.Default()
	off.TUI.Tail = false
	tl := &tailingSender{}
	if f, every := TailOf(on, tl); f == nil || every != on.PollInterval.Std() {
		t.Errorf("tail on: %v every %s", f != nil, every)
	}
	if f, _ := TailOf(off, tl); f != nil {
		t.Error("[tui] tail = false still tails")
	}
	if f, _ := TailOf(on, &sender{}); f != nil {
		t.Error("a sender that can't tail got a tail")
	}
	if f, _ := TailOf(nil, tl); f != nil {
		t.Error("no config got a tail")
	}
}

// tailingSender is an engine that can tail its session.
type tailingSender struct{ sender }

func (*tailingSender) Tail(context.Context, int) ([]string, error) { return []string{"x"}, nil }

// TestProgramTailsOnEveryPoll runs both layouts in a real program: the
// tail appears and follows the session; without a Tail nothing is read.
func TestProgramTailsOnEveryPoll(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {60, 24}} {
		src := &tailSource{lines: []string{"⏺ first output"}}
		feed := NewFeed()
		for _, ev := range []engine.Event{{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"}, started("M0-03"), opened("M0-03")} {
			ev.At = t0
			feed.Push(ev)
		}
		m := newModel(t.Context(), Options{
			Project: "sinjal", Backend: "herdr", Mode: "plan", Feed: feed, Sender: &sender{},
			Tail: src.tail, TailEvery: 10 * time.Millisecond,
		})
		tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(size[0], size[1]))
		seen(t, tm, "first output")
		src.mu.Lock()
		src.lines = []string{"⏺ first output", "✻ later output"}
		src.mu.Unlock()
		seen(t, tm, "later output")
		_ = tm.Quit()
	}
}

// TestTailMessagesOfAnotherView: in the app a tick or a read may reach a
// later run view; it ignores them.
func TestTailMessagesOfAnotherView(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-02")
	src := &tailSource{lines: []string{"mine"}}
	hs.m.opts.Tail = src.tail
	other := newModel(t.Context(), hs.m.opts)
	if _, cmd := hs.m.Update(tailTickMsg{other}); cmd != nil || src.count() != 0 {
		t.Error("a tick of another view started a read")
	}
	hs.m.Update(tailMsg{m: other, gen: hs.m.tailGen, lines: []string{"theirs"}})
	if strings.Contains(hs.m.View(), "theirs") {
		t.Error("another view's tail was drawn")
	}
}
