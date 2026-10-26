package tui

// Usage limits in the run view (SPEC §6.9, §15): the hold in the header
// with its countdown and Start anyway in the bar, a limited session's card
// line, the log lines and a restored snapshot.

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

var limitReset = t0.Add(3*time.Hour + 12*time.Minute) // 12:53 UTC

func holdEvent() engine.Event {
	return engine.Event{Kind: engine.LimitHold, Phase: "M0", Task: "M0-02", Title: "Config loader", Rank: "opus", Detail: "5h at 96%",
		Limit: &engine.Limit{Window: state.WindowFiveHour, ResetsAt: limitReset, Percent: 96.4}}
}

func TestLimitHoldHeaderAndStartAnyway(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}} {
		hs := newHarness(t, size[0], size[1])
		hs.m.loc = time.UTC
		hs.withPlan(cardPlan)
		hs.events(holdEvent())
		view := hs.m.View()
		checkFits(t, view, size[0], size[1])
		for _, want := range []string{"Paused · 5h limit 96% · resumes 12:53 (≈ 3h12m)", "[a Start anyway]", "M0-02"} {
			if !strings.Contains(view, want) {
				t.Errorf("%dx%d: view lacks %q:\n%s", size[0], size[1], want, view)
			}
		}
		if hs.m.needsOwner() {
			t.Errorf("%dx%d: a hold is drawn as Needs you", size[0], size[1])
		}
		hs.key("a")
		if got := hs.s.take(); len(got) != 1 || got[0].Kind != engine.CmdLimitStart {
			t.Errorf("%dx%d: a sent %+v, want start anyway", size[0], size[1], got)
		}
		hs.events(engine.Event{Kind: engine.LimitResume, Detail: "started anyway", Limit: holdEvent().Limit})
		if view := hs.m.View(); strings.Contains(view, "Start anyway") || strings.Contains(view, "5h limit") {
			t.Errorf("%dx%d: after the resume:\n%s", size[0], size[1], view)
		}
	}
}

// A session waiting at a usage limit is shown calmly with the window and
// the reset once they are known; it needs nobody (SPEC §6.3, §15).
func TestLimitedCard(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {60, 24}} {
		hs := newHarness(t, size[0], size[1])
		hs.m.loc = time.UTC
		hs.events(started("M0-03"), opened("M0-03"))
		hs.events(engine.Event{Kind: engine.SessionLimited, Task: "M0-03", Detail: "waits at a usage limit"})
		view := hs.m.View()
		if !strings.Contains(view, "◌ Usage limit") || strings.Contains(view, "Needs you") || strings.Contains(view, "continues") {
			t.Errorf("%dx%d: limited view:\n%s", size[0], size[1], view)
		}
		if stateLook(hs.m.cur) != lookPlain || hs.m.needsOwner() {
			t.Errorf("%dx%d: a limited session is drawn as an alert", size[0], size[1])
		}
		if r := hs.m.rows(); r.needsOwner(&plan.Task{ID: "M0-03"}) {
			t.Errorf("%dx%d: the task row marks a limited session as needing the owner", size[0], size[1])
		}
		hs.events(engine.Event{Kind: engine.SessionLimited, Task: "M0-03", Detail: "waits at the 7-day limit · continues 12:53",
			Limit: &engine.Limit{Window: state.WindowSevenDay, ResetsAt: limitReset, Task: "M0-03"}})
		hs.now = hs.now.Add(time.Minute)
		if view := hs.m.View(); !strings.Contains(view, "◌ Usage limit · 7d · continues 12:53 (≈ 3h11m)") {
			t.Errorf("%dx%d: with the window:\n%s", size[0], size[1], view)
		}
		hs.events(engine.Event{Kind: engine.SessionLimitOver, Task: "M0-03", Detail: "works again after the usage limit"})
		if hs.m.cur.state != stateWorking || hs.m.cur.limit != nil {
			t.Errorf("%dx%d: after the wait: state %v, limit %v", size[0], size[1], hs.m.cur.state, hs.m.cur.limit)
		}
	}
}

func TestLimitLogLines(t *testing.T) {
	tests := []struct {
		ev   engine.Event
		want string
	}{
		{holdEvent(), "◌ paused before M0-02 · 5h at 96%"},
		{engine.Event{Kind: engine.LimitResume, Detail: "reset"}, "resumed · reset"},
		{engine.Event{Kind: engine.SessionLimited, Task: "M0-03", Detail: "waits at the 7-day limit · continues 12:53"}, "◌ M0-03 waits at the 7-day limit · continues 12:53"},
		{engine.Event{Kind: engine.SessionLimitOver, Task: "M0-03", Detail: "works again after the usage limit"}, "M0-03 works again after the usage limit"},
		{engine.Event{Kind: engine.Nudged, Task: "M0-03", Detail: "didn't continue two minutes after the usage limit's reset; igris told it to continue"}, "M0-03 didn't continue two minutes after the usage limit's reset; igris told it to continue"},
		{engine.Event{Kind: engine.Nudged, Task: "M0-03", Detail: "the agent stopped without igris done; igris reminded it once"}, "M0-03 stopped without igris done; igris reminded it once"},
	}
	for _, tt := range tests {
		if got := strings.Join(logLines(tt.ev), "\n"); got != tt.want {
			t.Errorf("logLines(%s) = %q, want %q", tt.ev.Kind, got, tt.want)
		}
	}
}

// An attached view restores the hold and a limited card from the snapshot.
func TestRestoreLimited(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.m.loc = time.UTC
	lim := &serve.Limited{Window: state.WindowSevenDay, ResetsAt: limitReset, Task: "M0-03", Detail: "waits at the 7-day limit"}
	hold := &serve.Limited{Window: state.WindowFiveHour, ResetsAt: limitReset, Percent: 97, Task: "M0-04", Detail: "5h at 97%"}
	hs.m.restore(serve.Snapshot{Run: "r", Phase: "M0", Limited: hold,
		Active: []serve.Current{{Task: "M0-03", State: serve.StateLimited, Limit: lim, Since: t0}}})
	if hs.m.limited == nil || hs.m.limited.Window != state.WindowFiveHour || hs.m.heldTask != "M0-04" {
		t.Errorf("hold = %+v, held %q", hs.m.limited, hs.m.heldTask)
	}
	c := hs.m.card("M0-03")
	if c == nil || c.state != stateLimited || c.limit == nil || c.limit.Window != state.WindowSevenDay {
		t.Errorf("card = %+v", c)
	}
	if view := hs.m.View(); !strings.Contains(view, "◌ Usage limit · 7d · continues 12:53") || !strings.Contains(view, "[a Start anyway]") {
		t.Errorf("view:\n%s", view)
	}
}
