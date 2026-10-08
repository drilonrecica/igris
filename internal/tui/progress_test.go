package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/engine"
)

func TestRoundBar(t *testing.T) {
	for _, tc := range []struct {
		done, total, n int
		want           string
	}{
		{7, 12, 10, "██████░░░░"}, // the SPEC §15.1 example: 58%
		{0, 12, 10, "░░░░░░░░░░"},
		{12, 12, 10, "██████████"},
		{1, 50, 10, "█░░░░░░░░░"},  // started shows, never empty
		{49, 50, 10, "█████████░"}, // unfinished never looks full
		{3, 7, 6, "███░░░"},
	} {
		if got := roundBar(tc.done, tc.total, tc.n); got != tc.want {
			t.Errorf("roundBar(%d, %d, %d) = %q, want %q", tc.done, tc.total, tc.n, got, tc.want)
		}
	}
}

func TestProgressText(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"})
	for level, want := range []string{"3/7 · 42% ████░░░░░░", "3/7 · 42%", "3/7"} {
		if got := hs.m.progressText(level, wideProgressBar); got != want {
			t.Errorf("level %d: %q, want %q", level, got, want)
		}
	}
	// A phase with none of the run's tasks shows no progress at all.
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0; only P0-05", Slice: []string{"P0-05"}})
	if got := hs.m.progressText(progressFull, wideProgressBar); got != "" {
		t.Errorf("no slice tasks in the phase: %q", got)
	}
	if top := hs.m.topRule(120); strings.Contains(top, "/") || !strings.Contains(top, "phase M0 · mode") {
		t.Errorf("header with no progress: %q", top)
	}
	// A new run without a slice counts the whole phase again.
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"})
	if got := hs.m.progressText(progressCount, wideProgressBar); got != "3/7" {
		t.Errorf("whole run after a slice: %q", got)
	}
}

// TestProgressGivesWayToWidth: the bar goes first, then the percentage.
func TestProgressGivesWayToWidth(t *testing.T) {
	for _, tc := range []struct {
		w          int
		want, gone string
	}{
		{100, "3/7 · 42% ████░░░░░░", ""},
		{58, "· 3/7 · 42% ·", "█"},
		{51, "· 3/7 · mode", "%"},
	} {
		hs := newHarness(t, tc.w, 24)
		hs.withPlan(demoPlan)
		hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"})
		top := hs.m.topRule(tc.w)
		if tc.w < 100 {
			top = hs.m.statusBar(tc.w)
		}
		if !strings.Contains(top, tc.want) || (tc.gone != "" && strings.Contains(top, tc.gone)) || textWidth(top) > tc.w {
			t.Errorf("%d columns: %q, want %q without %q", tc.w, top, tc.want, tc.gone)
		}
	}
}

func TestETA(t *testing.T) {
	pts := func(mins ...int) []time.Duration {
		var ds []time.Duration
		for _, m := range mins {
			ds = append(ds, time.Duration(m)*time.Minute)
		}
		return ds
	}
	for _, tc := range []struct {
		name    string
		points  []time.Duration
		elapsed time.Duration
		user    bool
		want    string
	}{
		{"too few points", pts(10, 20), time.Minute, false, ""},
		{"median of an odd count", pts(30, 10, 14), 4*time.Minute + 12*time.Second, false, "≈ 10m left"},
		{"median of an even count", pts(10, 12, 20, 40), 0, false, "≈ 16m left"},
		{"under a minute left", pts(10, 10, 10), 9*time.Minute + 50*time.Second, false, "≈ 1m left"},
		{"past the median", pts(10, 14, 30), 18 * time.Minute, false, "over ≈ 14m typical"},
		{"hours", pts(70, 65, 80), 0, false, "≈ 1h10m left"},
		{"user task", pts(10, 14, 30), 0, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHarness(t, 120, 30)
			hs.m.opts.RankDurations = map[string][]time.Duration{"sonnet": tc.points}
			c := &current{rank: "sonnet", user: tc.user, started: t0}
			hs.now = t0.Add(tc.elapsed)
			if got := hs.m.eta(c); got != tc.want {
				t.Errorf("eta = %q, want %q", got, tc.want)
			}
		})
	}
	hs := newHarness(t, 120, 30)
	if got := hs.m.eta(&current{rank: "opus", started: t0}); got != "" {
		t.Errorf("a rank without history: %q", got)
	}
}

// TestProgramShowsProgressAndETA runs both layouts in a real program: the
// slice's progress in the header (or status bar) and the card's ETA.
func TestProgramShowsProgressAndETA(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {60, 24}} {
		hs := newHarness(t, size[0], size[1])
		hs.withPlan(demoPlan)
		s := &sender{}
		feed := NewFeed()
		for _, ev := range []engine.Event{
			{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0; only M0-02, M0-03", Slice: []string{"M0-02", "M0-03"}},
			started("M0-03"), opened("M0-03"),
		} {
			ev.At = t0
			feed.Push(ev)
		}
		m := newModel(t.Context(), Options{
			Project: "sinjal", Backend: "herdr", Mode: "plan", Feed: feed, Sender: s,
			PlanPath: hs.m.opts.PlanPath, Now: func() time.Time { return t0.Add(2 * time.Minute) },
			RankDurations: map[string][]time.Duration{"sonnet": {5 * time.Minute, 6 * time.Minute, 7 * time.Minute}},
		})
		tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(size[0], size[1]))
		seen(t, tm, "slice 1/2 · 50%", "2m0s · ≈ 4m left")
		_ = tm.Quit()
	}
}
