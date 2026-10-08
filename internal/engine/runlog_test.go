package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// logEvents returns the run log, failing the test if it can't be read.
func (h *harness) logEvents() []state.Event {
	h.t.Helper()
	events, err := h.dir.Events()
	if err != nil {
		h.t.Fatal(err)
	}
	return events
}

// logged1 returns the first run log line of typ for task ("" = any).
func logged1(t *testing.T, events []state.Event, typ state.EventType, task string) state.Event {
	t.Helper()
	for _, e := range events {
		if e.Type == typ && (task == "" || e.Task == task) {
			return e
		}
	}
	t.Fatalf("no %s line for %q in the run log", typ, task)
	return state.Event{}
}

// Every line a run writes is v1 with the run's ID; task lines carry what
// SPEC §13 lists for them.
func TestRunLogV1Fields(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\nverify = \"make test\"\ncommit = \"auto\"\n")
	h.dirtyTree()
	h.verifyFn = func(runner.Cmd) (runner.Result, error) { return runner.Result{}, nil }
	h.autoSignalExcept("A-1")
	h.signalAt(2*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := h.logEvents()
	runID := events[0].Run
	if !state.ValidRunID(runID) || !strings.HasPrefix(runID, "20261006-120000-") {
		t.Fatalf("run ID %q, want the start in UTC and 4 hex characters", runID)
	}
	for _, e := range events {
		if e.V != state.LogVersion || e.Run != runID {
			t.Errorf("%s line: v %d, run %q; want v1 and the run ID on every line", e.Type, e.V, e.Run)
		}
	}

	started := logged1(t, events, state.EventTaskStarted, "A-1")
	sessionID := arg(h.be.Opened()[0], "--session-id")
	if started.Attempt != 1 || started.Session != sessionID || !backend.ValidClaudeSession(sessionID) {
		t.Errorf("task_started attempt %d, session %q; want 1 and %q", started.Attempt, started.Session, sessionID)
	}
	// The title as notifications show it: no markdown.
	if started.Phase != "A" || started.Title != "One" || started.Owner != "agent" || started.Model != "sonnet" {
		t.Errorf("task_started = %+v", started)
	}
	if got := logged1(t, events, state.EventTaskStarted, "A-3").Owner; got != "agent + user" {
		t.Errorf("A-3 owner %q", got)
	}
	if v := logged1(t, events, state.EventVerifyPassed, "A-1"); v.Profile != "default" || v.Detail != "profile default" || v.Attempt != 1 {
		t.Errorf("verify_passed = %+v", v)
	}
	if c := logged1(t, events, state.EventCommitted, "A-1"); c.Commit != fakeSHA || c.Detail != "A-1: One" || c.Attempt != 1 {
		t.Errorf("committed = %+v", c)
	}
	done := logged1(t, events, state.EventTaskDone, "A-1")
	if done.DurationMS < (2*time.Minute).Milliseconds() || done.Attempt != 1 {
		t.Errorf("task_done duration %dms, attempt %d; want at least the 2m to its signal", done.DurationMS, done.Attempt)
	}
	stopped := events[len(events)-1]
	if stopped.Type != state.EventRunStopped || stopped.Task != "" || stopped.DurationMS < done.DurationMS {
		t.Errorf("run_stopped = %+v, want no task and the run's length", stopped)
	}
	// What a v0 reader needs is unchanged.
	if started.At.IsZero() || started.Rank != "sonnet" || done.Detail != "did A-1" {
		t.Errorf("v0 fields: started %+v, done %+v", started, done)
	}
}

// A retry is logged with its new session and attempt; the needs-you wait of
// the lost session ends before it.
func TestRunLogRetry(t *testing.T) {
	for _, cont := range []bool{false, true} {
		name := map[bool]string{false: "fresh", true: "continue"}[cont]
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			h.autoSignalExcept("A-1")
			h.be.Script("A-1", backend.Working, backend.Exited)
			h.onEvent = func(ev Event) {
				switch {
				case ev.Kind == Asked && ev.Question == QuestionSessionLost:
					h.eng.Send(Command{Kind: CmdRetry, Continue: cont})
				case ev.Kind == SessionOpened && ev.Task == "A-1" && h.count(SessionOpened) == 2:
					h.signalAt(ev.At.Sub(t0)+10*time.Second, "A-1")
				}
			}
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got, want := h.waits(), "A-1#1 session_lost, clear A-1#1 session_lost, retried A-1#2"; got != want {
				t.Errorf("waits = %q, want %q", got, want)
			}
			events := h.logEvents()
			first := logged1(t, events, state.EventTaskStarted, "A-1").Session
			retried := logged1(t, events, state.EventTaskRetried, "A-1")
			if retried.Detail != name || retried.Attempt != 2 || !backend.ValidClaudeSession(retried.Session) {
				t.Errorf("task_retried = %+v", retried)
			}
			if same := retried.Session == first; same != cont {
				t.Errorf("retry session %q, first %q: same = %v, want %v", retried.Session, first, same, cont)
			}
			if done := logged1(t, events, state.EventTaskDone, "A-1"); done.Attempt != 2 {
				t.Errorf("task_done attempt %d, want 2", done.Attempt)
			}
		})
	}
}

// A resumed task is attempt 1 of the new run, with the session state.json
// recorded; run_stopped of the stopped run names no task.
func TestRunLogResume(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	first := h.logEvents()
	firstRun := first[0].Run
	if stop := first[len(first)-1]; stop.Type != state.EventRunStopped || stop.Task != "" || stop.Detail != "stopped" {
		t.Errorf("run_stopped mid-task = %+v", stop)
	}
	sessionID := logged1(t, first, state.EventTaskStarted, "A-1").Session

	h.signalAt(20*time.Second, "A-1")
	if _, err := h.run(resumeLast); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	events := h.logEvents()[len(first):]
	if events[0].Run == firstRun || !state.ValidRunID(events[0].Run) {
		t.Errorf("second run ID %q, first %q; want a new one", events[0].Run, firstRun)
	}
	r := logged1(t, events, state.EventTaskResumed, "A-1")
	if r.Attempt != 1 || r.Session != sessionID || r.Phase != "A" || r.Title != "One" || r.Owner != "agent" || r.Model != "sonnet" {
		t.Errorf("task_resumed = %+v, want attempt 1 and session %q", r, sessionID)
	}
	// Measured from the resume, not from the first run's start.
	if d := logged1(t, events, state.EventTaskDone, "A-1"); d.DurationMS <= 0 || d.DurationMS > (time.Minute).Milliseconds() {
		t.Errorf("task_done duration %dms", d.DurationMS)
	}
}

// User tasks have no attempts and no session.
func TestRunLogUserTask(t *testing.T) {
	h := newHarness(t, strings.Replace(chainPlan, "| A-1 | **One** — the first thing | — | ready | sonnet | agent |", "| A-1 | **One** — the first thing | — | ready | — | user |", 1), "")
	h.on(func(ev Event) {
		if ev.Kind == YourTurn {
			h.eng.Send(Command{Kind: CmdDone, Text: "done by hand"})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := h.logEvents()
	s := logged1(t, events, state.EventTaskStarted, "A-1")
	if s.Attempt != 0 || s.Session != "" || s.Owner != "user" || s.Detail != "user task" {
		t.Errorf("user task_started = %+v", s)
	}
	if d := logged1(t, events, state.EventTaskDone, "A-1"); d.Attempt != 0 {
		t.Errorf("user task_done = %+v", d)
	}
}

// verify_failed keeps its v0.4 detail and names the profile.
func TestRunLogVerifyFailed(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(failWith(1, "boom\n"), pass)
	h.resignal(1)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if v := logged1(t, h.logEvents(), state.EventVerifyFailed, "A-1"); v.Profile != "default" || v.Detail != "profile default: attempt 1 of 2: exit status 1" || v.Attempt != 1 {
		t.Errorf("verify_failed = %+v", v)
	}
}

// A reset request waits on the owner for its task, run-wide.
func TestRunLogResetRequest(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.signalAt(time.Minute, "A-1")
	h.clock.At(4*time.Second, func() {
		if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset}); err != nil {
			t.Error(err)
		}
	})
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionConfirmReset {
			h.clock.At(ev.At.Sub(t0)+10*time.Second, func() {
				h.eng.Send(Command{Kind: CmdAnswer, Question: QuestionConfirmReset, Task: "A-1", Yes: false})
			})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := h.waits(), "A-1#1 reset_request, clear A-1#1 reset_request, A-1#1 idle"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
}

func TestTaskInfoTitle(t *testing.T) {
	long := strings.Repeat("é", 100)
	tests := []struct{ title, want string }{
		{"Config loader", "Config loader"},
		{"`igris.toml` **loader**.", "igris.toml loader"},
		{"red \x1b[31mtitle\x1b[0m", "red title"},
		{long, strings.Repeat("é", maxLogTitle)},
	}
	for _, tt := range tests {
		got := taskInfo(state.Event{}, &plan.Task{ID: "A-1", Title: tt.title, Owner: plan.OwnerAgent})
		if got.Title != tt.want || got.Owner != "agent" || got.Phase != "" {
			t.Errorf("taskInfo(%q) = %+v, want title %q", tt.title, got, tt.want)
		}
	}
}
