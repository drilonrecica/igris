package tui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

// The CURRENT card says what the task is about, offers its details and
// points at the session when it needs the owner (owner request at the v0.2
// gate: "in the main window I don't see what the task is about").

// cardPlan has a short task, a long one and a user task.
var cardPlan = `## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Makefile** | — | in progress | sonnet | agent |
| M0-02 | **Config loader** — read igris.toml and report every problem with its line | — | ready | opus | agent |
| M0-03 | **Long task** — ` + strings.Repeat("this sentence describes the work in some detail. ", 40) + `| — | ready | sonnet | agent |
| M0-04 | **Pick a license** — your call, not the agent's | — | ready | — | user |
`

func cardHarness(t *testing.T, w, h int, id string) *harness {
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(cardPlan)
	hs.events(engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: id, Title: "title of " + id, Rank: "opus", Model: "opus", Mode: "plan"},
		engine.Event{Kind: engine.SessionOpened, Task: id, Mode: "plan", Session: &backend.SessionRef{Backend: "herdr", PaneID: "p1"}})
	return hs
}

var cardSizes = [][2]int{{120, 40}, {80, 24}, {50, 20}}

func TestCardShowsTheTaskText(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "read igris.toml") {
				t.Errorf("card lacks the task text:\n%s", v)
			}
			if strings.Contains(v, "Config loader — read") || strings.Contains(v, "**") {
				t.Errorf("card repeats the title or shows markdown:\n%s", v)
			}
			if !strings.Contains(v, "[t Details]") || !strings.Contains(v, "[o Open session]") {
				t.Errorf("card lacks its buttons:\n%s", v)
			}
		})
	}
}

func TestCardTrimsLongTextAndKeepsItsButtons(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-03")
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			for _, want := range []string{"this sentence describes", "… t: details", "[t Details]", "[o Open session]"} {
				if !strings.Contains(v, want) {
					t.Errorf("view lacks %q:\n%s", want, v)
				}
			}
		})
	}
}

func TestCardOfATitleOnlyTask(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-01")
	v := hs.m.View()
	if strings.Contains(v, "… t: details") || !strings.Contains(v, "[t Details]") {
		t.Errorf("a title-only task is trimmed or has no details button:\n%s", v)
	}
}

func TestDetailsKey(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-02")
	hs.key("t")
	if hs.m.page == nil || !strings.Contains(hs.m.View(), "M0-02 — Config loader") {
		t.Fatalf("t didn't open the current task's details:\n%s", hs.m.View())
	}
	hs.key("esc")
	hs.keys("tab", "tab", "up") // the task list, then the task above
	hs.key("t")
	if !strings.Contains(hs.m.View(), "M0-01 — Makefile") {
		t.Fatalf("t didn't open the selected task's details:\n%s", hs.m.View())
	}
}

func TestCardDetailsButtonAndTitle(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.m.View()
			n := 0
			for _, z := range hs.m.zones.list {
				if z.t.act == actDetails {
					n++
				}
			}
			if n < 2 {
				t.Fatalf("want the title and the [t Details] button clickable, got %d zones:\n%s", n, hs.m.View())
			}
			hs.click(isAct(actDetails))
			if hs.m.page == nil || !strings.Contains(hs.m.View(), "M0-02 — Config loader") {
				t.Fatalf("clicking the card opened no details:\n%s", hs.m.View())
			}
		})
	}
}

func TestNeedsYouPointsAtTheSession(t *testing.T) {
	// 100 columns is the narrowest wide layout: the hint gets its own line.
	for _, size := range append([][2]int{{100, 30}}, cardSizes...) {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.events(engine.Event{Kind: engine.NeedsYou, Task: "M0-02", Detail: "the agent is waiting"})
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "o opens the session") || !strings.Contains(v, "[o Open session]") {
				t.Errorf("Needs you doesn't point at o:\n%s", v)
			}
			hs.key("o")
			if len(hs.focus) != 1 {
				t.Errorf("o focused %d sessions", len(hs.focus))
			}
		})
	}
}

// An attached client that can't reach the session's backend (igris attach
// over ssh, outside herdr and tmux) has no Focus: neither the bar, the card
// nor Needs you offer a session o can't open (SPEC §15.3, V14-G).
func TestNoOpenSessionWithoutFocus(t *testing.T) {
	for _, size := range append([][2]int{{100, 30}}, cardSizes...) {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.m.opts.Focus = nil
			hs.events(engine.Event{Kind: engine.NeedsYou, Task: "M0-02", Detail: "the agent is waiting"})
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "Needs you ·") {
				t.Fatalf("no Needs you:\n%s", v)
			}
			if strings.Contains(v, "Open session") || strings.Contains(v, "o opens the session") {
				t.Errorf("Open session offered without a backend to open it:\n%s", v)
			}
			for _, b := range hs.m.buttons() {
				if b.act == actOpen {
					t.Errorf("the bar offers Open session: %v", hs.m.buttons())
				}
			}
			hs.key("o") // does nothing, and doesn't crash
		})
	}
}

// A resumed task's reattached session can be opened, as a new one can
// (SPEC §15.3, V14-G).
func TestResumedSessionCanBeOpened(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.m.loc = time.UTC
	hs.withPlan(cardPlan)
	ref := &backend.SessionRef{Backend: "herdr", PaneID: "p1"}
	hs.events(engine.Event{Kind: engine.TaskResumed, Phase: "M0", Task: "M0-02", Rank: "opus", Model: "opus", Mode: "plan", Session: ref, Detail: "reattached to its session"})
	if v := hs.m.View(); !strings.Contains(v, "[o Open session]") || len(hs.m.buttons()) == 0 || hs.m.buttons()[0].act != actOpen {
		t.Fatalf("no Open session after a resume (bar %v):\n%s", hs.m.buttons(), v)
	}
	hs.key("o")
	if len(hs.focus) != 1 || hs.focus[0] != *ref {
		t.Errorf("o focused %v, want %v", hs.focus, *ref)
	}
	// A resume without a session (it is gone) offers none.
	hs = newHarness(t, 120, 40)
	hs.withPlan(cardPlan)
	hs.events(engine.Event{Kind: engine.TaskResumed, Phase: "M0", Task: "M0-02", Rank: "opus", Model: "opus", Detail: "its session is gone"})
	if v := hs.m.View(); strings.Contains(v, "Open session") {
		t.Errorf("Open session offered without a session:\n%s", v)
	}
}

func TestUserTaskCardHasNoSession(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.m.loc = time.UTC
	hs.withPlan(cardPlan)
	hs.events(userStarted("M0-04"))
	v := hs.m.View()
	if !strings.Contains(v, "your call, not the agent's") || !strings.Contains(v, "[t Details]") {
		t.Errorf("user task card lacks its text or details:\n%s", v)
	}
	if strings.Contains(v, "Open session") || strings.Contains(v, "o opens the session") {
		t.Errorf("user task card offers a session:\n%s", v)
	}
}

// A session on another model says so on its card until the attempt ends
// (SPEC §6.9), in words.
func TestCardShowsRankMismatchUntilTheAttemptEnds(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.events(engine.Event{Kind: engine.RankMismatch, Task: "M0-02", Detail: "on claude-opus-5-5, not its rank sonnet"},
				engine.Event{Kind: engine.NeedsYou, Task: "M0-02", Detail: "the session runs on claude-opus-5-5, not its rank sonnet; igris leaves it running"})
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "Rank mismatch") {
				t.Errorf("card lacks the rank mismatch line:\n%s", v)
			}
			hs.events(engine.Event{Kind: engine.NeedsYouClear, Task: "M0-02"})
			if v := hs.m.View(); !strings.Contains(v, "Rank mismatch") {
				t.Errorf("rank mismatch cleared while the attempt runs:\n%s", v)
			}
			hs.events(engine.Event{Kind: engine.Retrying, Task: "M0-02", Detail: "fresh"})
			if v := hs.m.View(); strings.Contains(v, "Rank mismatch") {
				t.Errorf("rank mismatch kept after the attempt ended:\n%s", v)
			}
		})
	}
}

// An overdue task says so on its card until the attempt ends (SPEC §6.3).
func TestCardShowsOverdueUntilTheAttemptEnds(t *testing.T) {
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			hs.events(engine.Event{Kind: engine.TaskOverdue, Task: "M0-02", Detail: "running longer than its Timeout 45m"},
				engine.Event{Kind: engine.NeedsYou, Task: "M0-02", Detail: "the task is running longer than its Timeout 45m; igris leaves its session running"})
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			if !strings.Contains(v, "Overdue · running longer") {
				t.Errorf("card lacks the overdue line:\n%s", v)
			}
			hs.events(engine.Event{Kind: engine.NeedsYouClear, Task: "M0-02"})
			if v := hs.m.View(); !strings.Contains(v, "Overdue") {
				t.Errorf("overdue cleared while the attempt runs:\n%s", v)
			}
			hs.events(engine.Event{Kind: engine.Retrying, Task: "M0-02", Detail: "fresh"})
			if v := hs.m.View(); strings.Contains(v, "Overdue") {
				t.Errorf("overdue kept after the attempt ended:\n%s", v)
			}
		})
	}
}

// The card counts the attempt's tokens as the engine reads them (SPEC
// §15.1): `tokens 2.3M in · 41k out · model` wide, `2.3M/41k` narrow, and
// `usage unavailable` when the transcript can't be read.
func TestCardShowsTokenUsage(t *testing.T) {
	read := engine.Event{Kind: engine.UsageRead, Task: "M0-02",
		Tokens: &engine.Tokens{In: 2_345_678, Out: 41_900, Models: []string{"claude-sonnet-5-5"}}}
	for _, size := range cardSizes {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := cardHarness(t, size[0], size[1], "M0-02")
			if v := hs.m.View(); strings.Contains(v, "tokens") || strings.Contains(v, "usage unavailable") {
				t.Errorf("card shows usage before any read:\n%s", v)
			}
			hs.events(read)
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			want := "tokens 2.3M in · 41k out · claude-sonnet-5-5"
			if size[0] < wideWidth {
				want = "mode plan · 2.3M/41k" // on the facts line (SPEC §15.2)
			}
			if !strings.Contains(v, want) {
				t.Errorf("card lacks %q:\n%s", want, v)
			}
			// Another task's counter is not this card's.
			hs.events(engine.Event{Kind: engine.UsageRead, Task: "M0-03", Tokens: &engine.Tokens{In: 7}})
			if v := hs.m.View(); !strings.Contains(v, want) {
				t.Errorf("another task's usage replaced the card's:\n%s", v)
			}
			hs.events(engine.Event{Kind: engine.UsageRead, Task: "M0-02", Tokens: &engine.Tokens{Unavailable: true}})
			if v := hs.m.View(); !strings.Contains(v, "usage unavailable") || strings.Contains(v, "tokens 2.3M") {
				t.Errorf("card lacks usage unavailable:\n%s", v)
			}
			// A new attempt starts counting again.
			hs.events(read, engine.Event{Kind: engine.SessionOpened, Task: "M0-02", Mode: "plan", Session: &backend.SessionRef{Backend: "herdr", PaneID: "p2"}})
			if v := hs.m.View(); strings.Contains(v, "tokens") || strings.Contains(v, "2.3M") {
				t.Errorf("card keeps the old attempt's tokens:\n%s", v)
			}
		})
	}
}

// The model id is Ember on a rank mismatch, and it is the only change: the
// words stay the same.
func TestCardTokenModelIsEmberOnMismatch(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-02")
	hs.m.th = colorTheme(t, true, nil)
	hs.events(engine.Event{Kind: engine.UsageRead, Task: "M0-02", Tokens: &engine.Tokens{In: 5, Out: 1, Models: []string{"claude-opus-5-5"}}})
	plain := hs.m.View()
	hs.events(engine.Event{Kind: engine.RankMismatch, Task: "M0-02", Detail: "on claude-opus-5-5, not its rank sonnet"})
	red := hs.m.View()
	paint := hs.m.th.paint(lookWarn, "claude-opus-5-5")
	if !strings.Contains(red, "· "+paint) || strings.Contains(plain, paint) {
		t.Errorf("model id not painted only on a mismatch:\n%s", red)
	}
}

// The header shows the subscription quota once one is recorded (SPEC
// §15.1): amber from 80%, red from 95% (words carry the meaning, so the
// plain text is what is checked), its age once over 10 minutes, dropped
// before the progress bar when the width runs out.
func TestHeaderShowsTheQuota(t *testing.T) {
	hs := cardHarness(t, 120, 40, "M0-02")
	if v := hs.m.View(); strings.Contains(v, "5h ") {
		t.Errorf("quota shown before one is recorded:\n%s", v)
	}
	at := hs.now
	hs.events(engine.Event{Kind: engine.QuotaRead, Quota: &state.Quota{At: at,
		FiveHour: &state.QuotaWindow{Used: 4, ResetsAt: 1}, SevenDay: &state.QuotaWindow{Used: 52, ResetsAt: 2}}})
	first := strings.Split(hs.m.View(), "\n")[0]
	if !strings.Contains(first, "5h 4% · 7d 52%") || strings.Contains(first, "ago") {
		t.Errorf("header = %q", first)
	}
	hs.now = hs.now.Add(25 * time.Minute)
	if first = strings.Split(hs.m.View(), "\n")[0]; !strings.Contains(first, "5h 4% · 7d 52% · 25m ago") {
		t.Errorf("stale header = %q", first)
	}
	// Whatever the width, the header still fits with the quota in it.
	for _, w := range []int{100, 70, 50} {
		n := cardHarness(t, w, 24, "M0-02")
		n.events(engine.Event{Kind: engine.QuotaRead, Quota: &state.Quota{At: n.now, FiveHour: &state.QuotaWindow{Used: 96, ResetsAt: 1}}})
		checkFits(t, n.m.View(), w, 24)
	}
}

func TestQuotaWarningLooks(t *testing.T) {
	m := &model{th: &theme{}, opts: Options{Now: func() time.Time { return t0 }}}
	m.quota = &state.Quota{At: t0, FiveHour: &state.QuotaWindow{Used: 96}, SevenDay: &state.QuotaWindow{Used: 81.4}}
	if got := m.quotaText(); got != "5h 96% · 7d 81%" {
		t.Errorf("quotaText = %q", got)
	}
	m.th = newTheme(lipgloss.NewRenderer(io.Discard), true, nil)
	m.th.r.SetColorProfile(termenv.TrueColor)
	got := m.quotaText()
	if !strings.Contains(got, m.th.paint(lookFail, "5h 96%")) || !strings.Contains(got, m.th.paint(lookWarn, "7d 81%")) {
		t.Errorf("quotaText = %q", got)
	}
}
