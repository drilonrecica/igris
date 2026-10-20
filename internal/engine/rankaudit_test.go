package engine

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

// The rank audit (SPEC §6.9): the main thread's model ids against the
// model the task's rank resolved to.

// logLines returns the run log's lines of type typ for task.
func (h *harness) logLines(typ state.EventType, task string) []state.Event {
	var out []state.Event
	for _, e := range h.logEvents() {
		if e.Type == typ && e.Task == task {
			out = append(out, e)
		}
	}
	return out
}

// A session on another model is raised once per attempt while it runs:
// the UI event, Needs you, the urgent notification and the run-log line.
// The session is left alone and the task ends as usual.
func TestRankMismatchLive(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working) // busy throughout: no idle Needs you
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind != SessionOpened || ev.Task != "A-1" {
			return
		}
		s, at := ev.ClaudeSession, ev.At.Sub(t0)
		respond(t, dir, s, ev.At.Add(time.Second), "msg_1", "claude-sonnet-5-5", 1, 10, 1, false)
		respond(t, dir, s, ev.At.Add(2*time.Second), "msg_sub", "claude-opus-5-5", 1, 10, 1, true) // a subagent: not audited
		h.clock.At(at+15*time.Second, func() {
			respond(t, dir, s, h.clock.Now(), "msg_2", "claude-opus-5-5", 1, 10, 1, false)
		})
		h.clock.At(at+35*time.Second, func() {
			respond(t, dir, s, h.clock.Now(), "msg_3", "claude-haiku-5-5", 1, 10, 1, false)
		})
		h.signalAt(at+time.Minute, "A-1")
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	const what = "on claude-opus-5-5, not its rank sonnet"
	if n := h.count(RankMismatch); n != 1 {
		t.Fatalf("%d rank_mismatch events, want 1 (once per attempt)", n)
	}
	ev := h.event(RankMismatch, "A-1")
	if ev.Detail != what {
		t.Errorf("rank_mismatch detail = %q", ev.Detail)
	}
	// Read about every 10 s: the first read after the response at 15 s.
	if got := ev.At.Sub(h.event(SessionOpened, "A-1").At); got < 15*time.Second || got > 30*time.Second {
		t.Errorf("rank_mismatch %s after the session opened, want at the next read", got)
	}
	if ny := h.event(NeedsYou, "A-1"); ny.Detail != "the session runs "+what+"; igris leaves it running" {
		t.Errorf("needs you = %q", ny.Detail)
	}
	if got := h.toasts(); !slices.Contains(got, "request: phase A · A-1 One: needs you ("+what+")") {
		t.Errorf("toasts = %q", got)
	}
	lines := h.logLines(state.EventRankMismatch, "A-1")
	if len(lines) != 1 || lines[0].Rank != "sonnet" || lines[0].Attempt != 1 || lines[0].Detail != what ||
		!reflect.DeepEqual(lines[0].Models, []string{"claude-sonnet-5-5", "claude-opus-5-5"}) {
		t.Errorf("rank_mismatch lines = %+v", lines)
	}
	// The agent works on: igris waits on nobody (Decision W).
	if got := h.waits(); !strings.Contains(got, "A-1#1 rank_mismatch, clear A-1#1 rank_mismatch") {
		t.Errorf("run log waits = %q", got)
	}
	u := h.usageLines("A-1")
	if len(u) != 1 || u[0].Audit != state.AuditMismatch ||
		!reflect.DeepEqual(u[0].Models, []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5"}) {
		t.Errorf("usage lines = %+v", u)
	}
	// Never stopped, switched or told anything.
	if p := h.be.Prompts("A-1"); len(p) != 1 {
		t.Errorf("A-1 got %d prompts, want only its task prompt", len(p))
	}
	if ev := h.event(TaskDone, "A-1"); ev.At.Sub(h.event(SessionOpened, "A-1").At) < time.Minute {
		t.Error("A-1 ended before its own signal")
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=done A-2=done") {
		t.Errorf("statuses = %s", got)
	}
	// A-2 (opus) wrote nothing: nothing to find, nothing raised.
	if u := h.usageLines("A-2"); len(u) != 1 || u[0].Audit != state.AuditOK {
		t.Errorf("A-2 usage = %+v", u)
	}
}

// A response on another model that only the read at the attempt's end
// sees is logged and notified, but Needs you is not raised: nobody is
// waited on any more.
func TestRankMismatchAtTheEnd(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			s, at := ev.ClaudeSession, ev.At.Sub(t0)
			h.clock.At(at+29*time.Second, func() {
				respond(t, dir, s, h.clock.Now(), "msg_1", "claude-fable-5-1", 1, 10, 1, false)
			})
			h.signalAt(at+30*time.Second, "A-1")
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	const what = "on claude-fable-5-1, not its rank sonnet"
	if lines := h.logLines(state.EventRankMismatch, "A-1"); len(lines) != 1 || lines[0].Detail != what {
		t.Fatalf("rank_mismatch lines = %+v", lines)
	}
	if got := h.waits(); strings.Contains(got, "rank_mismatch") {
		t.Errorf("waits = %q, want no rank_mismatch wait after the attempt", got)
	}
	if got := h.toasts(); !slices.Contains(got, "request: phase A · A-1 One: ran "+what) {
		t.Errorf("toasts = %q", got)
	}
	if u := h.usageLines("A-1"); len(u) != 1 || u[0].Audit != state.AuditMismatch {
		t.Errorf("usage lines = %+v", u)
	}
	if got := h.logged(); !strings.Contains(got, "task_done rank_mismatch notification usage") {
		t.Errorf("run log = %s", got)
	}
}

// A [models] value igris can't check is unchecked: whatever ran, nothing is
// raised.
func TestRankAuditUnchecked(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn+"[models]\nsonnet = \"default\"\n")
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			respond(t, dir, ev.ClaudeSession, ev.At.Add(time.Second), "msg_1", "claude-haiku-5-5", 1, 10, 1, false)
			h.signalAt(ev.At.Sub(t0)+30*time.Second, "A-1")
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := h.count(RankMismatch); n != 0 {
		t.Errorf("%d rank_mismatch events on an unchecked value", n)
	}
	if u := h.usageLines("A-1"); len(u) != 1 || u[0].Audit != state.AuditUnchecked || u[0].Model != "default" {
		t.Errorf("usage lines = %+v", u)
	}
}

// With [usage] off nothing is read, so nothing is audited.
func TestRankAuditOff(t *testing.T) {
	h := newHarness(t, chainPlan, "[usage]\nenabled = false\n")
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			respond(t, dir, ev.ClaudeSession, ev.At.Add(time.Second), "msg_1", "claude-opus-5-5", 1, 10, 1, false)
			h.signalAt(ev.At.Sub(t0)+30*time.Second, "A-1")
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := h.count(RankMismatch); n != 0 || len(h.logLines(state.EventRankMismatch, "A-1")) != 0 || len(h.usageLines("A-1")) != 0 {
		t.Errorf("audited with [usage] off: %s", h.logged())
	}
}

// A retry is a new attempt: a mismatch is raised again for it.
func TestRankMismatchOncePerAttempt(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working)
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind != SessionOpened || ev.Task != "A-1" {
			return
		}
		respond(t, dir, ev.ClaudeSession, ev.At.Add(time.Second), "msg_"+ev.ClaudeSession, "claude-opus-5-5", 1, 10, 1, false)
		if h.count(SessionOpened) == 1 {
			h.clock.At(ev.At.Sub(t0)+30*time.Second, func() { h.eng.Send(Command{Kind: CmdRetry}) })
		} else {
			h.signalAt(ev.At.Sub(t0)+30*time.Second, "A-1")
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := h.logLines(state.EventRankMismatch, "A-1")
	if len(lines) != 2 || lines[0].Attempt != 1 || lines[1].Attempt != 2 {
		t.Errorf("rank_mismatch lines = %+v, want one per attempt", lines)
	}
}
