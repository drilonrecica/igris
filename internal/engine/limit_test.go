package engine

// Usage limits (SPEC §6.3, §6.9, §10; decisions.md V18-P1), end to end on
// the fake backend and the fake clock: the hold at the threshold and its
// resume at the reset, a quota that falls, Start anyway, a hit mid-task
// with the Timeout paused and the session continuing, the fixed prompt
// after the reset, Enter after a stale resume, and user tasks that are
// never held.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
)

// limitedEvent returns the first SessionLimited event of task that names
// its window: the first one may come before the transcript's hit is read.
func (h *harness) limitedEvent(task string) Event {
	h.t.Helper()
	for _, ev := range h.events {
		if ev.Kind == SessionLimited && ev.Task == task && ev.Limit != nil {
			return ev
		}
	}
	h.t.Fatalf("no session_limited event with a window for %s in: %s", task, h.kinds())
	return Event{}
}

const statuslineOn = "[usage]\nenabled = true\nstatusline = true\n"

// writeQuota records a quota as the status line would: window used percent,
// resetting at reset.
func (h *harness) writeQuota(window string, used float64, reset time.Time) {
	h.t.Helper()
	q := state.Quota{At: h.clock.Now()}
	w := &state.QuotaWindow{Used: used, ResetsAt: reset.Unix()}
	switch window {
	case state.WindowFiveHour:
		q.FiveHour = w
	case state.WindowSevenDay:
		q.SevenDay = w
	case state.WindowSpendLimit:
		q.SpendLimit = w
	}
	data, err := json.Marshal(q)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(state.QuotaPath(h.root), append(data, '\n'), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// hitRecord appends a rate-limit record to the transcript of uuid in dir, as
// Claude Code writes it when the API rejects a request (SPEC §6.9).
func hitRecord(t *testing.T, dir, uuid string, at time.Time, window string, reset time.Time) {
	t.Helper()
	line := fmt.Sprintf(`{"type":"assistant","isSidechain":false,"timestamp":%q,"requestedModel":"claude-sonnet-5-5","isApiErrorMessage":true,"error":"rate_limit","quotaLimits":{"status":"rejected","rateLimitType":%q,"resetsAt":%d},"message":{"id":"msg_err_1","model":"<synthetic>","content":[{"type":"text","text":"You've hit your weekly limit"}],"usage":{"input_tokens":0,"output_tokens":0}}}`+"\n",
		at.Format(time.RFC3339Nano), window, reset.Unix())
	f, err := os.OpenFile(filepath.Join(dir, uuid+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// limitToasts returns the toasts about usage limits: every toast but the
// phase's completion.
func (h *harness) limitToasts() []string {
	var out []string
	for _, t := range h.toasts() {
		if !strings.Contains(t, "phase A: complete") {
			out = append(out, t)
		}
	}
	return out
}

// limitLines returns the run log's limit_* lines as "type window detail".
func (h *harness) limitLines() string {
	var out []string
	for _, e := range h.logEvents() {
		switch e.Type {
		case state.EventLimitHit, state.EventLimitHold, state.EventLimitResume:
			out = append(out, strings.TrimSpace(string(e.Type)+" "+e.Window+" "+e.Detail))
		}
	}
	return strings.Join(out, "; ")
}

// At 96% of the 5h window the next agent task is held, not Needs you: the
// run log, the UI event and one notification say so, and the run goes on
// at the reset plus 60 s (SPEC §6.9).
func TestLimitHoldAtThresholdResumesAtReset(t *testing.T) {
	h := newHarness(t, chainPlan, statuslineOn)
	reset := t0.Add(3*time.Hour + 12*time.Minute)
	h.on(func(ev Event) {
		if ev.Kind == TaskDone && ev.Task == "A-1" {
			h.writeQuota(state.WindowFiveHour, 96.4, reset)
		}
	})
	if res, err := h.run(); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	hold := h.event(LimitHold, "")
	if hold.Task != "A-2" || hold.Limit == nil || hold.Limit.Window != state.WindowFiveHour || !hold.Limit.ResetsAt.Equal(reset) || hold.Limit.Percent != 96.4 {
		t.Errorf("limit_hold = %+v (limit %+v)", hold, hold.Limit)
	}
	if ev := h.event(LimitResume, ""); ev.Detail != "reset" {
		t.Errorf("limit_resume = %+v", ev)
	}
	// Resumed on the first poll past the reset + 60 s.
	started := h.event(TaskStarted, "A-2").At
	if d := started.Sub(reset); d < time.Minute || d > time.Minute+2*h.cfg.PollInterval.Std() {
		t.Errorf("A-2 started %s after the reset, want 60 s plus a poll", d)
	}
	if got := h.limitLines(); got != "limit_hold five_hour 5h at 96%; limit_resume five_hour reset" {
		t.Errorf("limit lines = %s", got)
	}
	line := logged1(t, h.logEvents(), state.EventLimitHold, "")
	if line.ResetsAt != reset.UTC().Format(time.RFC3339) || line.Task != "" {
		t.Errorf("limit_hold line = %+v", line)
	}
	// No Needs you, no pause: a run hold.
	if h.count(NeedsYou) != 0 || h.count(PauseOn) != 0 || h.count(Paused) != 0 {
		t.Errorf("events: %s", h.kinds())
	}
	want := "done: 5h limit at 96% · A-2 held until " + look.UntilIn(reset.Local(), hold.At)
	if got := h.limitToasts(); len(got) != 1 || got[0] != want {
		t.Errorf("toasts = %q, want %q", got, want)
	}
}

// A fresh quota under the threshold ends the hold before the reset.
func TestLimitHoldEndsWhenTheQuotaFalls(t *testing.T) {
	h := newHarness(t, chainPlan, statuslineOn)
	reset := t0.Add(3 * time.Hour)
	h.on(func(ev Event) {
		switch {
		case ev.Kind == TaskDone && ev.Task == "A-1":
			h.writeQuota(state.WindowSevenDay, 97, reset)
		case ev.Kind == LimitHold:
			h.clock.At(ev.At.Sub(t0)+20*time.Minute, func() { h.writeQuota(state.WindowSevenDay, 71.2, reset) })
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.limitLines(); got != "limit_hold seven_day 7d at 97%; limit_resume seven_day quota fell to 71%" {
		t.Errorf("limit lines = %s", got)
	}
	if d := h.event(TaskStarted, "A-2").At.Sub(h.event(LimitHold, "").At); d < 20*time.Minute || d > 21*time.Minute {
		t.Errorf("A-2 started %s after the hold", d)
	}
}

// Start anyway starts the held task once, ignoring the threshold.
func TestLimitHoldStartAnyway(t *testing.T) {
	h := newHarness(t, chainPlan, statuslineOn)
	reset := t0.Add(3 * time.Hour)
	h.on(func(ev Event) {
		switch {
		case ev.Kind == TaskDone && ev.Task == "A-1":
			h.writeQuota(state.WindowFiveHour, 99, reset)
		case ev.Kind == LimitHold && ev.Task == "A-2":
			h.eng.Send(Command{Kind: CmdLimitStart})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A-2 started at once; A-3's start was held again (the quota is still
	// at 99%) and waited for the reset.
	if d := h.event(TaskStarted, "A-2").At.Sub(h.event(LimitHold, "A-2").At); d > h.cfg.PollInterval.Std() {
		t.Errorf("A-2 started %s after Start anyway", d)
	}
	if got := h.limitLines(); got != "limit_hold five_hour 5h at 99%; limit_resume five_hour started anyway; limit_hold five_hour 5h at 99%; limit_resume five_hour reset" {
		t.Errorf("limit lines = %s", got)
	}
	// One notification per episode: the window and its reset.
	if got := h.limitToasts(); len(got) != 1 {
		t.Errorf("toasts = %q", got)
	}
}

// hold_at = 0 turns the hold-ahead off; a window that has reset holds
// nothing either.
func TestLimitHoldOff(t *testing.T) {
	h := newHarness(t, chainPlan, statuslineOn+"hold_at = 0\n")
	h.writeQuota(state.WindowFiveHour, 100, t0.Add(time.Hour))
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.count(LimitHold) != 0 {
		t.Errorf("held with hold_at = 0: %s", h.kinds())
	}
	h = newHarness(t, chainPlan, statuslineOn)
	h.writeQuota(state.WindowFiveHour, 100, t0.Add(-time.Minute))
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.count(LimitHold) != 0 {
		t.Errorf("held by a window that has reset: %s", h.kinds())
	}
}

// heldUserPlan: an agent task, then a user task and an agent task that are
// both ready.
const heldUserPlan = `# Demo plan

## A — First phase

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent |
| A-2 | **Two** — yours | A-1 | blocked | — | user |
| A-3 | **Three** | A-1 | blocked | opus | agent |
`

// A user task needs no session and starts while agent tasks are held.
func TestLimitHoldLetsUserTasksStart(t *testing.T) {
	h := newHarness(t, heldUserPlan, statuslineOn)
	reset := t0.Add(2 * time.Hour)
	h.writeQuota(state.WindowFiveHour, 95, reset)
	h.on(func(ev Event) {
		if ev.Kind == YourTurn {
			h.signalAt(ev.At.Sub(t0)+time.Minute, "A-2")
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// A-1 (agent) held at the start of the run; the user task A-2 can't
	// start before A-1 is done. Once A-1 ran (after the reset), A-2 (user)
	// starts at once while A-3 waits for nothing: the window has reset by
	// then.
	if h.event(LimitHold, "").Task != "A-1" {
		t.Errorf("first hold = %+v", h.event(LimitHold, ""))
	}
	if got := h.opened(); got != "A-1 A-3" {
		t.Errorf("opened %q", got)
	}
}

// userFirstPlan: a user task and an agent task both ready from the start.
const userFirstPlan = `# Demo plan

## A — First phase

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **One** — yours | — | ready | — | user |
| A-2 | **Two** | — | ready | sonnet | agent |
`

func TestLimitHoldStartsUserTaskBesideHeldAgentTask(t *testing.T) {
	h := newHarness(t, userFirstPlan, statuslineOn)
	reset := t0.Add(time.Hour)
	h.writeQuota(state.WindowFiveHour, 98, reset)
	h.on(func(ev Event) {
		if ev.Kind == YourTurn {
			h.signalAt(ev.At.Sub(t0)+time.Minute, "A-1")
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Sequential phase: A-1 (user) is selected first and starts at once;
	// A-2 is held after it until the reset.
	if d := h.event(TaskStarted, "A-1").At.Sub(t0); d > 5*time.Second {
		t.Errorf("the user task started %s in, want at once", d)
	}
	if got := h.event(LimitHold, "").Task; got != "A-2" {
		t.Errorf("held task = %q", got)
	}
	if d := h.event(TaskStarted, "A-2").At.Sub(reset); d < time.Minute {
		t.Errorf("A-2 started %s from the reset", d)
	}
}

// limitPlan: A-1 may run 30 minutes.
const limitPlan = `# Demo plan

## A — First phase

| ID | Task | Deps | Status | Model | Owner | Timeout |
|---|---|---|---|---|---|---|
| A-1 | **One** — the first thing | — | ready | sonnet | agent | 30m |
| A-2 | **Two** — the second thing | A-1 | blocked | opus | agent | — |
`

// limitedSession sets A-1's session up to hit the 7-day limit at hitAt,
// resetting at reset: the hooks record limited and the transcript the hit.
// It returns the transcript directory.
func (h *harness) limitedSession(hitAt time.Duration, reset time.Time) string {
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working)
	h.on(func(ev Event) {
		if ev.Kind != SessionOpened || ev.Task != "A-1" {
			return
		}
		s := ev.ClaudeSession
		h.clock.At(hitAt, func() {
			hitRecord(h.t, dir, s, h.clock.Now(), state.WindowSevenDay, reset)
			rec := state.AgentState{State: backend.Limited, Event: "StopFailure", At: h.clock.Now(), Transcript: filepath.Join(dir, s+".jsonl")}
			if err := state.WriteAgentState(h.root, s, rec); err != nil {
				h.t.Error(err)
			}
			h.be.SetState("A-1", backend.Limited)
		})
	})
	return dir
}

// A session that hits a limit waits: igris logs the hit, says so on the
// card, notifies once, raises no Needs you, stops the Timeout clock, and
// holds the next task until the reset (SPEC §6.3, §6.9).
func TestLimitHitMidTask(t *testing.T) {
	h := newHarness(t, limitPlan, usageOn)
	reset := t0.Add(2 * time.Hour)
	h.limitedSession(5*time.Minute, reset)
	h.on(func(ev Event) {
		switch {
		case ev.Kind == SessionLimited && ev.Task == "A-1" && ev.Limit != nil && ev.Limit.Window != "":
			// Claude Code continues at the reset (UserPromptSubmit) and the
			// session finishes 20 minutes later: 25 minutes of its own
			// time, under the 30m Timeout.
			h.clock.At(reset.Sub(t0)+90*time.Second, func() { h.be.SetState("A-1", backend.Working) })
			h.signalAt(reset.Sub(t0)+20*time.Minute, "A-1")
		}
	})
	if res, err := h.run(); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	lim := h.limitedEvent("A-1")
	if lim.Limit.Window != state.WindowSevenDay || !lim.Limit.ResetsAt.Equal(reset) || lim.Limit.Task != "A-1" {
		t.Errorf("session_limited = %+v (%+v)", lim, lim.Limit)
	}
	if first := h.event(SessionLimited, "A-1"); first.Limit != nil || first.Detail != "waits at a usage limit" {
		t.Errorf("first session_limited = %+v", first)
	}
	if h.count(SessionLimitOver) != 1 || h.count(NeedsYou) != 0 || h.count(TaskOverdue) != 0 || h.count(Nudged) != 0 {
		t.Errorf("events: %s", h.kinds())
	}
	hit := logged1(t, h.logEvents(), state.EventLimitHit, "A-1")
	if hit.Window != state.WindowSevenDay || hit.ResetsAt != reset.UTC().Format(time.RFC3339) || hit.Attempt != 1 || hit.Rank != "sonnet" {
		t.Errorf("limit_hit = %+v", hit)
	}
	// A-1 ended after the reset: nothing was left to hold.
	if got := h.limitLines(); got != "limit_hit seven_day" {
		t.Errorf("limit lines = %s", got)
	}
	want := "done: 7-day limit · A-1 waits, resumes " + look.UntilIn(reset.Local(), lim.At)
	if got := h.limitToasts(); len(got) != 1 || got[0] != want {
		t.Errorf("toasts = %q, want %q", got, want)
	}
	if p := h.be.Prompts("A-1"); len(p) != 1 {
		t.Errorf("A-1 got %d prompts, want only the task", len(p))
	}
}

// The task ends (the owner marks it done) while its hit's reset is ahead:
// the next task is held until the reset, with or without the status line,
// as the hit's episode: one notification for the hit and the hold.
func TestLimitHitHoldsNewStarts(t *testing.T) {
	h := newHarness(t, limitPlan, usageOn)
	reset := t0.Add(time.Hour)
	h.limitedSession(5*time.Minute, reset)
	h.on(func(ev Event) {
		if ev.Kind == SessionLimited && ev.Task == "A-1" && ev.Limit != nil {
			h.eng.Send(Command{Kind: CmdDone, Text: "enough"})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.limitLines(); got != "limit_hit seven_day; limit_hold seven_day after A-1 hit the 7-day limit; limit_resume seven_day reset" {
		t.Errorf("limit lines = %s", got)
	}
	hold := h.event(LimitHold, "")
	if hold.Task != "A-2" || hold.Limit == nil || hold.Limit.Task != "A-1" || hold.Limit.Percent != 0 || !hold.Limit.ResetsAt.Equal(reset) {
		t.Errorf("limit_hold = %+v (%+v)", hold, hold.Limit)
	}
	if d := h.event(TaskStarted, "A-2").At.Sub(reset); d < time.Minute || d > time.Minute+2*h.cfg.PollInterval.Std() {
		t.Errorf("A-2 started %s after the reset", d)
	}
	if got := h.limitToasts(); len(got) != 1 || !strings.HasPrefix(got[0], "done: 7-day limit · A-1 waits") {
		t.Errorf("toasts = %q", got)
	}
}

// Still not working two minutes after the reset, the session gets the fixed
// prompt once; then the ordinary idle rules apply, with the reason
// usage_limit (SPEC §6.3).
func TestLimitFixedPromptAfterReset(t *testing.T) {
	h := newHarness(t, limitPlan, usageOn)
	reset := t0.Add(time.Hour)
	h.limitedSession(5*time.Minute, reset)
	h.on(func(ev Event) {
		if ev.Kind == NeedsYou && ev.Task == "A-1" {
			h.be.SetState("A-1", backend.Working)
			h.signalAt(ev.At.Sub(t0)+time.Minute, "A-1")
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	p := h.be.Prompts("A-1")
	if len(p) != 2 || p[1] != prompt.LimitContinue() {
		t.Fatalf("A-1 prompts = %q", p)
	}
	prompted := h.event(Nudged, "A-1")
	if d := prompted.At.Sub(reset); d < 2*time.Minute || d > 2*time.Minute+2*h.cfg.PollInterval.Std() {
		t.Errorf("fixed prompt %s after the reset, want 2 minutes", d)
	}
	ny := h.event(NeedsYou, "A-1")
	if ny.Reason != state.ReasonUsageLimit || ny.At.Sub(prompted.At) < h.cfg.NeedsInputAfter.Std() {
		t.Errorf("needs_you = %+v, %s after the prompt", ny, ny.At.Sub(prompted.At))
	}
	if got := h.waits(); got != "A-1#1 usage_limit, clear A-1#1 usage_limit" {
		t.Errorf("waits = %s", got)
	}
	// The Timeout clock stopped for the wait: 5m before, then from the
	// prompt on; never overdue.
	if h.count(TaskOverdue) != 0 {
		t.Error("overdue although the clock stopped for the wait")
	}
}

// Claude Code gave up continuing by itself: the fixed prompt goes out at
// once; the machine slept and it waits for Enter: igris presses Enter once.
func TestLimitResumeDisabledAndStale(t *testing.T) {
	for _, tt := range []struct {
		event   string
		prompts int
		enters  int
	}{
		{"quota_auto_resume_disabled", 2, 0},
		{"quota_auto_resume_stale", 1, 1},
	} {
		t.Run(tt.event, func(t *testing.T) {
			h := newHarness(t, limitPlan, usageOn)
			reset := t0.Add(time.Hour)
			dir := h.limitedSession(5*time.Minute, reset)
			h.on(func(ev Event) {
				if ev.Kind == SessionOpened && ev.Task == "A-1" {
					s := ev.ClaudeSession
					h.clock.At(reset.Sub(t0)+30*time.Second, func() {
						rec := state.AgentState{State: backend.Limited, Event: tt.event, At: h.clock.Now(), Transcript: filepath.Join(dir, s+".jsonl")}
						if err := state.WriteAgentState(h.root, s, rec); err != nil {
							h.t.Error(err)
						}
					})
					h.clock.At(reset.Sub(t0)+time.Minute, func() { h.be.SetState("A-1", backend.Working) })
					h.signalAt(reset.Sub(t0)+2*time.Minute, "A-1")
				}
			})
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.be.Prompts("A-1"); len(got) != tt.prompts {
				t.Errorf("prompts = %q, want %d", got, tt.prompts)
			}
			if got := h.be.Submits("A-1"); got != tt.enters {
				t.Errorf("Enter pressed %d times, want %d", got, tt.enters)
			}
			if h.count(NeedsYou) != 0 {
				t.Errorf("events: %s", h.kinds())
			}
		})
	}
}

// A hit whose reset is in the past or over 8 days ahead is no hit (a
// session can forge the transcript): a warning, no hold.
func TestLimitHitOutOfRange(t *testing.T) {
	h := newHarness(t, limitPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	h.on(func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			hitRecord(t, dir, ev.ClaudeSession, ev.At.Add(time.Second), state.WindowFiveHour, t0.Add(9*24*time.Hour))
			h.signalAt(ev.At.Sub(t0)+30*time.Second, "A-1")
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.count(LimitHold) != 0 || h.count(SessionLimited) != 0 || strings.Contains(h.logged(), "limit_hit") {
		t.Errorf("a hit 9 days ahead counted: %s / %s", h.kinds(), h.logged())
	}
	warned := false
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, "usage limit") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning about the hit: %s", h.kinds())
	}
}
