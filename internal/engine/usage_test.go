package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

// Token usage (SPEC §6.9): every session's hooks report its transcript,
// which these tests write the way Claude Code does.

const usageOn = "[usage]\nenabled = true\n"

// reportTranscripts makes every session opened report its transcript in
// its hook record, as Claude Code's hooks do, and returns the directory the
// transcripts are in.
func (h *harness) reportTranscripts() string {
	dir := h.t.TempDir()
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		if ev.Kind == SessionOpened {
			path := filepath.Join(dir, ev.ClaudeSession+".jsonl")
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600); err == nil { //nolint:gosec // test temp dir
				_ = f.Close()
			}
			rec := state.AgentState{State: backend.Working, Event: "UserPromptSubmit", At: h.clock.Now(), Transcript: path}
			if err := state.WriteAgentState(h.root, ev.ClaudeSession, rec); err != nil {
				h.t.Error(err)
			}
		}
		if prev != nil {
			prev(ev)
		}
	}
	return dir
}

// respond appends one response to the transcript of the session uuid in
// dir: blocks records of message id, each repeating its usage, as Claude
// Code writes them; sidechain marks a subagent's file.
func respond(t *testing.T, dir, uuid string, at time.Time, id, model string, in, out int64, blocks int, sidechain bool) {
	t.Helper()
	path := filepath.Join(dir, uuid+".jsonl")
	if sidechain {
		path = filepath.Join(dir, uuid, "subagents", "agent-a1.jsonl")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	for i := range blocks {
		fmt.Fprintf(&b, `{"type":"assistant","isSidechain":%v,"timestamp":%q,"apiBlockIndex":%d,"message":{"id":%q,"model":%q,"content":[{"type":"text","text":"SECRET"}],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":100,"cache_creation_input_tokens":10}}}`+"\n",
			sidechain, at.Add(time.Duration(i)*time.Millisecond).Format(time.RFC3339Nano), i, id, model, in, out)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(b.String()); err != nil {
		t.Fatal(err)
	}
}

// usageLines returns the run log's usage lines of task.
func (h *harness) usageLines(task string) []state.Event {
	var out []state.Event
	for _, e := range h.logEvents() {
		if e.Type == state.EventUsage && e.Task == task {
			out = append(out, e)
		}
	}
	return out
}

func (h *harness) usageWarnings() []string {
	var out []string
	for _, ev := range h.events {
		if ev.Kind == Warning && (strings.Contains(ev.Detail, "token usage") || strings.Contains(ev.Detail, "transcript")) {
			out = append(out, ev.Detail)
		}
	}
	return out
}

// A finished attempt logs what its transcripts recorded after task_done:
// each response once, the subagents apart, nothing from before the attempt
// and no API error.
func TestUsageLoggedWhenTheTaskEnds(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind != SessionOpened || ev.Task != "A-1" {
			return
		}
		s := ev.ClaudeSession
		respond(t, dir, s, ev.At.Add(-time.Hour), "msg_old", "claude-sonnet-5-5", 999, 999, 1, false)
		respond(t, dir, s, ev.At.Add(time.Second), "msg_1", "claude-sonnet-5-5", 2, 30, 3, false)
		respond(t, dir, s, ev.At.Add(2*time.Second), "msg_err", "<synthetic>", 0, 0, 1, false)
		respond(t, dir, s, ev.At.Add(3*time.Second), "msg_sub", "claude-haiku-5-5", 5, 7, 1, true)
		h.clock.At(ev.At.Sub(t0)+15*time.Second, func() {
			// Written while the task runs, read by a later read.
			// Another id, still sonnet: the rank audit ignores [1m].
			respond(t, dir, s, h.clock.Now(), "msg_2", "claude-sonnet-5-5[1m]", 3, 40, 2, false)
		})
		h.signalAt(ev.At.Sub(t0)+30*time.Second, "A-1")
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := h.usageLines("A-1")
	if len(lines) != 1 {
		t.Fatalf("usage lines = %+v, want 1", lines)
	}
	u := lines[0]
	started := logged1(t, h.logEvents(), state.EventTaskStarted, "A-1")
	want := state.Event{
		Usage:     &usage.Counts{Input: 5, Output: 70, CacheRead: 200, CacheWrite: 20, Messages: 2},
		Sub:       &usage.Counts{Input: 5, Output: 7, CacheRead: 100, CacheWrite: 10, Messages: 1},
		Models:    []string{"claude-sonnet-5-5", "claude-sonnet-5-5[1m]"},
		SubModels: []string{"claude-haiku-5-5"},
	}
	if !reflect.DeepEqual(u.Usage, want.Usage) || !reflect.DeepEqual(u.Sub, want.Sub) ||
		!reflect.DeepEqual(u.Models, want.Models) || !reflect.DeepEqual(u.SubModels, want.SubModels) {
		t.Errorf("usage = %+v %+v %q %q", u.Usage, u.Sub, u.Models, u.SubModels)
	}
	if u.Session != started.Session || u.Attempt != 1 || u.Rank != "sonnet" || u.Model != "sonnet" || u.Audit != state.AuditOK || u.Detail != "" {
		t.Errorf("usage line = %+v", u)
	}
	if got := h.logged(); !strings.HasPrefix(got, "run_started task_started task_done usage task_started") {
		t.Errorf("run log = %s", got)
	}
	if w := h.usageWarnings(); len(w) != 0 {
		t.Errorf("warnings = %q", w)
	}
	// Never message content or the transcript path.
	if log := h.read(filepath.Join(state.DirName, "runs.jsonl")); strings.Contains(log, "SECRET") || strings.Contains(log, dir) {
		t.Error("the run log holds transcript content or its path")
	}
}

// Without a transcript the attempt's usage is unavailable: logged as such,
// with one warning, and the task and the run go on.
func TestUsageUnavailable(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	h.autoSignalExcept("A-1")
	h.signalAt(30*time.Second, "A-1")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	lines := h.usageLines("A-1")
	if len(lines) != 1 || lines[0].Audit != state.AuditUnavailable || lines[0].Usage != nil ||
		lines[0].Detail != "no transcript reported by the session's hooks" {
		t.Fatalf("usage lines = %+v", lines)
	}
	var a1 []string
	for _, w := range h.usageWarnings() {
		if strings.Contains(w, "A-1") {
			a1 = append(a1, w)
		}
	}
	if len(a1) != 1 {
		t.Errorf("A-1 warnings = %q, want one", a1)
	}
}

// A continued session writes the same transcript: each attempt counts only
// its own responses, logged when it ends (lost, then done).
func TestUsageContinuedSession(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working, backend.Exited)
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		switch {
		case ev.Kind == Asked && ev.Question == QuestionSessionLost:
			h.eng.Send(Command{Kind: CmdRetry, Continue: true})
		case ev.Kind == SessionOpened && ev.Task == "A-1" && h.count(SessionOpened) == 1:
			respond(t, dir, ev.ClaudeSession, ev.At.Add(time.Millisecond), "msg_1", "claude-sonnet-5-5", 1, 10, 2, false)
		case ev.Kind == SessionOpened && ev.Task == "A-1":
			respond(t, dir, ev.ClaudeSession, ev.At.Add(time.Millisecond), "msg_2", "claude-sonnet-5-5", 2, 20, 1, false)
			h.signalAt(ev.At.Sub(t0)+10*time.Second, "A-1")
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := h.usageLines("A-1")
	if len(lines) != 2 {
		t.Fatalf("usage lines = %+v, want 2", lines)
	}
	for i, want := range []usage.Counts{
		{Input: 1, Output: 10, CacheRead: 100, CacheWrite: 10, Messages: 1},
		{Input: 2, Output: 20, CacheRead: 100, CacheWrite: 10, Messages: 1},
	} {
		if l := lines[i]; l.Attempt != i+1 || l.Usage == nil || *l.Usage != want {
			t.Errorf("attempt %d: %+v usage %+v, want %+v", i+1, l, l.Usage, want)
		}
	}
	if lines[0].Session != lines[1].Session {
		t.Errorf("continued session changed: %q, %q", lines[0].Session, lines[1].Session)
	}
	if got := h.logged(); !strings.Contains(got, "task_started usage needs_you") || !strings.Contains(got, "task_retried task_done usage") {
		t.Errorf("run log = %s", got)
	}
}

// A run stopped mid-task logs the usage so far before run_stopped; the next
// run counts from when it reattaches, so nothing is counted twice.
func TestUsageStopAndResume(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	var uuid string
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			uuid = ev.ClaudeSession
			respond(t, dir, uuid, ev.At.Add(time.Second), "msg_1", "claude-sonnet-5-5", 1, 10, 1, false)
		}
	}
	h.stopMidTask(6 * time.Second)
	if got := h.logged(); got != "run_started task_started usage run_stopped" {
		t.Errorf("first run log = %s", got)
	}
	h.reportTranscripts()
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskResumed && ev.Task == "A-1" {
			respond(t, dir, uuid, h.clock.Now().Add(time.Second), "msg_2", "claude-sonnet-5-5", 2, 20, 1, false)
			h.signalAt(h.clock.Now().Sub(t0)+10*time.Second, "A-1")
		}
	}
	if _, err := h.run(resumeLast); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	lines := h.usageLines("A-1")
	if len(lines) != 2 || lines[0].Usage == nil || lines[1].Usage == nil ||
		lines[0].Usage.Output != 10 || lines[1].Usage.Output != 20 || lines[1].Run == lines[0].Run {
		t.Fatalf("usage lines = %+v", lines)
	}
}

// With [usage] statusline the UI hears of the quota the status line
// recorded, once per change; without it, never (SPEC §6.3).
func TestQuotaEvents(t *testing.T) {
	for _, on := range []bool{true, false} {
		toml := usageOn
		if on {
			toml += "statusline = true\n"
		}
		h := newHarness(t, chainPlan, toml)
		q := state.Quota{At: t0, FiveHour: &state.QuotaWindow{Used: 4, ResetsAt: 1792518000}}
		h.reportTranscripts()
		h.autoSignalExcept("A-1")
		prev := h.onEvent
		h.onEvent = func(ev Event) {
			prev(ev)
			if ev.Kind == SessionOpened && ev.Task == "A-1" {
				if err := state.WriteQuota(h.root, q); err != nil {
					t.Error(err)
				}
				h.clock.At(ev.At.Sub(t0)+15*time.Second, func() {
					q.FiveHour = &state.QuotaWindow{Used: 9, ResetsAt: 1792518000}
					q.At = h.clock.Now()
					if err := state.WriteQuota(h.root, q); err != nil {
						t.Error(err)
					}
				})
				h.signalAt(ev.At.Sub(t0)+40*time.Second, "A-1")
			}
		}
		if _, err := h.run(); err != nil {
			t.Fatalf("Run: %v", err)
		}
		var got []float64
		for _, ev := range h.events {
			if ev.Kind == QuotaRead {
				got = append(got, ev.Quota.FiveHour.Used)
			}
		}
		if want := []float64{4, 9}; on && !reflect.DeepEqual(got, want) {
			t.Errorf("statusline on: quota events %v, want %v", got, want)
		} else if !on && len(got) != 0 {
			t.Errorf("statusline off: quota events %v", got)
		}
	}
}

// A task's settings file has igris's status line only with [usage]
// statusline (SPEC §6.3).
func TestHooksFileStatusLine(t *testing.T) {
	for _, on := range []bool{true, false} {
		dir, err := state.Open(t.TempDir(), state.Options{})
		if err != nil {
			t.Fatal(err)
		}
		path, err := WriteHooks(dir, "A-1", "/bin/igris", nil, nil, on)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path) //nolint:gosec // test temp dir
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(data), `"statusLine"`); got != on {
			t.Errorf("statusline %v: settings file has it = %v\n%s", on, got, data)
		}
	}
}

// The done signal takes one more read of the transcript, whatever the 10 s
// between reads: a short task's tokens all land in its last seconds, and
// the card, the budgets and the owner answering its commit question would
// otherwise see 0 until the attempt ends (found by the V18 gate: `tokens 0
// in · 0 out` on a finished task with 167k, at its commit question).
func TestUsageReadAtDone(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn)
	h.dirtyTree()
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		switch {
		case ev.Kind == SessionOpened && ev.Task == "A-1":
			s := ev.ClaudeSession
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() {
				respond(t, dir, s, h.clock.Now(), "msg_1", "claude-sonnet-5-5", 2, 30, 1, false)
			})
			h.signalAt(ev.At.Sub(t0)+6*time.Second, "A-1")
		case ev.Kind == Asked && ev.Question == QuestionCommit:
			h.clock.At(ev.At.Sub(t0)+time.Minute, func() { h.eng.Send(Command{Kind: CmdAnswer, Yes: true}) })
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	read, asked := -1, -1
	for i, ev := range h.events {
		switch {
		case ev.Task != "A-1":
		case ev.Kind == UsageRead && read < 0 && ev.Tokens != nil && ev.Tokens.In > 0:
			read = i
		case ev.Kind == Asked && asked < 0:
			asked = i
		}
	}
	if read < 0 || asked < 0 || read > asked {
		t.Errorf("the tokens were read at event %d, the commit asked at %d: want them read first", read, asked)
	}
}
