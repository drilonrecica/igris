package serve

// Usage limits on the socket and the web UI (SPEC §6.9, §13, §15.8): the
// hold in the snapshot and the status word, a limited session's card, the
// `limit` command (Start anyway) and the feed lines.

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/state"
)

var limitReset = uiT0.Add(3*time.Hour + 12*time.Minute)

func holdEvent(at time.Time) engine.Event {
	return engine.Event{At: at, Kind: engine.LimitHold, Phase: "P1", Task: "P1-04", Title: "Docs", Rank: "sonnet", Detail: "5h at 96%",
		Limit: &engine.Limit{Window: state.WindowFiveHour, ResetsAt: limitReset, Percent: 96.4}}
}

// The snapshot carries the hold while it holds, and a limited session's
// card its window and reset.
func TestSnapshotLimits(t *testing.T) {
	s := New(Options{Version: "1.8.0", Mode: "auto"})
	s.Publish(engine.Event{At: uiT0, Kind: engine.RunStarted, Run: "r", Phase: "P1"})
	s.Publish(engine.Event{At: uiT0, Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Rank: "sonnet", Model: "m"})
	s.Publish(engine.Event{At: uiT0.Add(time.Minute), Kind: engine.SessionLimited, Task: "P1-03", Detail: "waits at a usage limit"})
	got := snap(t, s)
	if c := got.Current; c == nil || c.State != StateLimited || c.Limit != nil {
		t.Fatalf("current = %+v, want limited without a window yet", c)
	}
	s.Publish(engine.Event{At: uiT0.Add(2 * time.Minute), Kind: engine.SessionLimited, Task: "P1-03", Detail: "waits at the 7-day limit · continues 12:12",
		Limit: &engine.Limit{Window: state.WindowSevenDay, ResetsAt: limitReset, Task: "P1-03"}})
	got = snap(t, s)
	if c := got.Current; c == nil || c.State != StateLimited || c.Limit == nil || c.Limit.Window != state.WindowSevenDay || !c.Limit.ResetsAt.Equal(limitReset) || c.Limit.Detail == "" {
		t.Fatalf("current = %+v (%+v)", got.Current, got.Current.Limit)
	}
	s.Publish(engine.Event{At: uiT0.Add(3 * time.Minute), Kind: engine.SessionLimitOver, Task: "P1-03"})
	if c := snap(t, s).Current; c == nil || c.State != StateWorking || c.Limit != nil {
		t.Fatalf("after the wait: %+v", c)
	}

	s.Publish(engine.Event{At: uiT0.Add(4 * time.Minute), Kind: engine.TaskDone, Task: "P1-03"})
	s.Publish(holdEvent(uiT0.Add(5 * time.Minute)))
	got = snap(t, s)
	if l := got.Limited; l == nil || l.Window != state.WindowFiveHour || !l.ResetsAt.Equal(limitReset) || l.Percent != 96.4 || l.Detail != "5h at 96%" || l.Task != "P1-04" {
		t.Fatalf("limited = %+v", l)
	}
	s.Publish(engine.Event{At: uiT0.Add(6 * time.Minute), Kind: engine.LimitResume, Detail: "reset", Limit: &engine.Limit{Window: state.WindowFiveHour, ResetsAt: limitReset}})
	if l := snap(t, s).Limited; l != nil {
		t.Fatalf("after the resume: %+v", l)
	}
}

// An event's limit crosses the socket both ways.
func TestWireLimit(t *testing.T) {
	ev := holdEvent(uiT0)
	w := fromEngine(ev, "r")
	if w.Limit == nil || w.Limit.Window != state.WindowFiveHour || w.Limit.Percent != 96.4 || !w.Limit.ResetsAt.Equal(limitReset) {
		t.Fatalf("wire limit = %+v", w.Limit)
	}
	back, ok := w.Engine()
	if !ok || back.Limit == nil || *back.Limit != *ev.Limit {
		t.Fatalf("back = %+v (%+v)", back, back.Limit)
	}
	// A window igris doesn't know, or a use out of range, is dropped.
	w.Limit.Window, w.Limit.Percent = "\x1b[31m", 1e9
	back, _ = w.Engine()
	if back.Limit != nil {
		t.Errorf("a bad limit kept: %+v", back.Limit)
	}
}

// The status word while the hold holds: Paused with the window, its use
// and the reset with its countdown (SPEC §15.4); Needs you still outranks
// it.
func TestUIStatusLimited(t *testing.T) {
	now := uiT0.Add(4*time.Minute + 12*time.Second)
	lim := &Limited{Window: state.WindowFiveHour, ResetsAt: limitReset, Percent: 96.4, Detail: "5h at 96%"}
	got := statusOf(Snapshot{Run: "r", Started: uiT0, Limited: lim}, now)
	if got.Word != look.Paused.String() || got.Line != "5h limit 96% · resumes 12:12 (≈ 3h08m)" || got.Tone != "plain" {
		t.Errorf("statusOf = %+v", got)
	}
	hit := &Limited{Window: state.WindowSevenDay, ResetsAt: limitReset, Task: "P1-03", Detail: "after P1-03 hit the 7-day limit"}
	if got := statusOf(Snapshot{Run: "r", Started: uiT0, Limited: hit}, now); got.Line != "7d limit · resumes 12:12 (≈ 3h08m)" {
		t.Errorf("after a hit: %+v", got)
	}
	got = statusOf(Snapshot{Run: "r", Started: uiT0, Limited: lim, Active: []Current{{Task: "A", State: StateLost}}}, now)
	if got.Word != look.NeedsYou.String() {
		t.Errorf("needs you lost to the hold: %+v", got)
	}
}

// The page offers Start anyway while the hold holds, says the hold in its
// status, and a limited session's card its window and reset.
func TestUILimitPage(t *testing.T) {
	w := uiWeb(t, "plan", true)
	for _, ev := range []engine.Event{
		{At: uiT0, Kind: engine.RunStarted, Run: "r1", Phase: "P1", Detail: "phase P1"},
		{At: uiT0.Add(time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Rank: "sonnet", Model: "claude-sonnet-5-5", Mode: "accept"},
		{At: uiT0.Add(time.Minute), Kind: engine.SessionLimited, Phase: "P1", Task: "P1-03", Detail: "waits at the 7-day limit · continues 12:12",
			Limit: &engine.Limit{Window: state.WindowSevenDay, ResetsAt: limitReset, Task: "P1-03"}},
		holdEvent(uiT0.Add(2 * time.Minute)),
	} {
		w.srv.Publish(ev)
	}
	code, body, _ := w.get("/", "cookie")
	if code != 200 {
		t.Fatalf("GET / = %d", code)
	}
	for _, want := range []string{
		`<h1 class="word plain">Paused</h1>`,
		"5h limit 96% · resumes 12:12 (≈ 3h08m)",
		`name="type" value="limit"`, `name="text" value="start"`, ">Start anyway<",
		"usage limit: 7d · continues 12:12 (≈ 3h08m)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
	// The form posts the command as the socket's `limit` with text start.
	code, _, _ = w.postForm("/act", url.Values{"type": {"limit"}, "text": {"start"}})
	if code != 303 {
		t.Errorf("POST /act = %d", code)
	}
	if cmds := w.send.got(); len(cmds) != 1 || cmds[0].Kind != engine.CmdLimitStart {
		t.Errorf("engine got %+v, want start anyway", cmds)
	}
}

// Feed lines of the limit events (SPEC §15.8).
func TestUIRecentLimits(t *testing.T) {
	now := uiT0.Add(time.Hour)
	tests := []struct {
		ev   Event
		want recentView
	}{
		{Event{Type: string(engine.LimitHold), Task: "P1-04", Detail: "5h at 96%", At: now}, recentView{Glyph: "◌", Text: "Paused before P1-04 · 5h at 96%", Ago: "0s ago"}},
		{Event{Type: string(engine.LimitResume), Detail: "reset", At: now}, recentView{Glyph: "●", Text: "Resumed · reset", Ago: "0s ago"}},
		{Event{Type: string(engine.SessionLimited), Task: "P1-03", Detail: "waits at the 7-day limit · continues 12:12", At: now}, recentView{Glyph: "◌", Text: "P1-03 waits at the 7-day limit · continues 12:12", Ago: "0s ago"}},
		{Event{Type: string(engine.SessionLimitOver), Task: "P1-03", Detail: "works again after the usage limit", At: now}, recentView{Glyph: "●", Text: "P1-03 works again after the usage limit", Ago: "0s ago"}},
	}
	for _, tt := range tests {
		got := recentOf(tt.ev, now)
		tt.want.At = tt.ev.At
		if got != tt.want {
			t.Errorf("recentOf(%s) = %+v, want %+v", tt.ev.Type, got, tt.want)
		}
	}
}
