package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// resetAt writes a reset signal for id at offset, like `igris reset` while
// igris runs.
func (h *harness) resetAt(offset time.Duration, id string, force bool) {
	h.clock.At(offset, func() {
		if err := h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionReset, Force: force}); err != nil {
			h.t.Error(err)
		}
	})
}

// confirmResets answers every reset request at once, as the owner would.
func (h *harness) confirmResets(yes bool) {
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionConfirmReset {
			h.eng.Send(Command{Kind: CmdAnswer, Question: QuestionConfirmReset, Task: ev.Task, Yes: yes})
		}
	})
}

// continueWhenPaused turns pause-after-task off once the run holds, with
// every session signalling done from then on.
func (h *harness) continueWhenPaused() {
	h.on(func(ev Event) {
		if ev.Kind == Paused {
			h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
			h.eng.Send(Command{Kind: CmdPause})
		}
	})
}

// Resetting the current task closes its session at once, forgets it,
// rewrites its Status and pauses the run (SPEC §6.2).
func TestResetCurrentTask(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.confirmResets(true)
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working) // busy: a settle wait would run out first
	h.resetAt(9*time.Second, "A-1", false)
	h.on(func(ev Event) {
		if ev.Kind == TaskReset {
			if got := h.statuses(); got != "A-1=ready A-2=blocked A-3=blocked B-1=blocked" {
				t.Errorf("statuses at the reset = %s", got)
			}
			if run, err := h.dir.LoadRun(); err != nil || run.Current != nil {
				t.Errorf("state.json still names a task: %+v, %v", run, err)
			}
			if len(h.be.Closed()) != 1 {
				t.Errorf("closed %d sessions at the reset, want A-1's", len(h.be.Closed()))
			}
		}
	})
	h.continueWhenPaused()
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	ev := h.event(TaskReset, "A-1")
	if ev.Detail != "A-1: in progress → ready" || ev.Title != "One" || len(ev.Changes) != 1 {
		t.Errorf("task_reset = %+v", ev)
	}
	// Polled every 2s: applied at the first poll after 9s, without the idle wait.
	if got := durations(h.times(TaskReset)...); got != "10s" {
		t.Errorf("task_reset at %s, want 10s", got)
	}
	if !strings.Contains(h.kinds(), "task_reset pause_on paused pause_off task_started") {
		t.Errorf("events = %s", h.kinds())
	}
	if got := h.opened(); got != "A-1 A-1 A-2 A-3" {
		t.Errorf("sessions = %s, want A-1 started again after the pause", got)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	events, err := h.dir.Events()
	if err != nil {
		t.Fatal(err)
	}
	var reset []state.Event
	for _, e := range events {
		if e.Type == state.EventTaskReset {
			reset = append(reset, e)
		}
	}
	if len(reset) != 1 || reset[0].Task != "A-1" || reset[0].Detail != "in progress" || reset[0].Rank != "sonnet" || reset[0].Model != "sonnet" {
		t.Errorf("task_reset log = %+v", reset)
	}
	if s, _ := h.dir.ReadSignal("A-1"); s != nil {
		t.Errorf("signal left behind: %+v", s)
	}
	if h.count(TaskDone) != 3 || h.count(StraySignal) != 0 {
		t.Errorf("events = %s", h.kinds())
	}
}

// Another task gets the Status write and the sync; the current task and
// the run go on (SPEC §6.2).
func TestResetOtherTask(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.confirmResets(true)
	h.autoSignalExcept("A-2")
	h.resetAt(20*time.Second, "A-1", true)
	h.on(func(ev Event) {
		if ev.Kind == TaskReset {
			if got := h.statuses(); got != "A-1=ready A-2=in progress A-3=blocked B-1=blocked" {
				t.Errorf("statuses at the reset = %s", got)
			}
			if h.current() != "A-2" {
				t.Errorf("current task = %q, want A-2 still", h.current())
			}
		}
	})
	h.signalAt(40*time.Second, "A-2")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if ev := h.event(TaskReset, "A-1"); ev.Detail != "A-1: done → ready" {
		t.Errorf("task_reset = %+v", ev)
	}
	if got := h.opened(); got != "A-1 A-2 A-1 A-3" {
		t.Errorf("sessions = %s, want A-1 run again after A-2", got)
	}
	if h.count(PauseOn) != 0 {
		t.Errorf("the run paused: %s", h.kinds())
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
}

// The running igris checks the status again: a done task without force,
// or a ready one, is reported and left alone.
func TestResetRecheckedByTheRun(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-2")
	h.resetAt(20*time.Second, "A-1", false)
	h.resetAt(20*time.Second, "A-3", true) // blocked: nothing to reset
	h.signalAt(40*time.Second, "A-2")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	for _, ev := range h.events {
		if ev.Kind == Warning {
			warnings = append(warnings, ev.Detail)
		}
	}
	want := []string{"not reset: A-1 is done; pass --force to reset it", "A-3 is blocked: nothing to reset"}
	if strings.Join(warnings, "|") != strings.Join(want, "|") {
		t.Errorf("warnings = %q, want %q", warnings, want)
	}
	if h.count(TaskReset) != 0 || h.opened() != "A-1 A-2 A-3" {
		t.Errorf("events = %s, sessions %s", h.kinds(), h.opened())
	}
	for _, id := range []string{"A-1", "A-3"} {
		if s, _ := h.dir.ReadSignal(id); s != nil {
			t.Errorf("%s's reset signal left behind", id)
		}
	}
}

// A user task is reset the same way.
func TestResetUserTask(t *testing.T) {
	const userPlan = `## A — First phase

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **Sign** — the owner signs | — | ready | — | user |
| A-2 | **Two** | A-1 | blocked | sonnet | agent |
`
	h := newHarness(t, userPlan, "")
	h.confirmResets(true)
	h.resetAt(5*time.Second, "A-1", false)
	h.on(func(ev Event) {
		if ev.Kind == Paused {
			h.eng.Send(Command{Kind: CmdPause})
		}
		if ev.Kind == YourTurn && h.count(TaskReset) == 1 {
			h.eng.Send(Command{Kind: CmdDone, Text: "signed"})
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if ev := h.event(TaskReset, "A-1"); ev.Detail != "A-1: in progress → ready" {
		t.Errorf("task_reset = %+v", ev)
	}
	if h.count(YourTurn) != 2 || h.count(PauseOn) != 1 {
		t.Errorf("events = %s", h.kinds())
	}
	if got := h.statuses(); got != "A-1=done A-2=done" {
		t.Errorf("statuses = %s", got)
	}
}

// A reset still pending when the next run starts is applied before
// anything is selected: the interrupted task's session is closed and it is
// not picked up again until the owner continues.
func TestResetPendingAtStart(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset}); err != nil {
		t.Fatal(err)
	}
	h.confirmResets(true)
	h.continueWhenPaused()
	res, err := h.run(resumeLast)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed run = %s, %v", res.Outcome, err)
	}
	if h.count(TaskResumed) != 0 {
		t.Errorf("the interrupted task was resumed: %s", h.kinds())
	}
	if !strings.HasPrefix(h.kinds(), "run_started asked task_reset pause_on") {
		t.Errorf("events = %s", h.kinds())
	}
	if closed := h.be.Closed(); len(closed) == 0 || closed[0].PaneID == "" {
		t.Errorf("closed = %v, want the interrupted session closed first", closed)
	}
	if got := h.opened(); got != "A-1 A-1 A-2 A-3" {
		t.Errorf("sessions = %s", got)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	if got := h.logged(); !strings.Contains(got, "run_started notification task_reset task_started") {
		t.Errorf("run log = %s", got)
	}
}

// A reset waits while the plan is invalid and is applied once it is
// valid again.
func TestResetWaitsForAValidPlan(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.confirmResets(true)
	h.autoSignalExcept("A-2")
	h.clock.At(20*time.Second, func() {
		h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| A-3 |", "| A 3 |", 1))
		if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset, Force: true}); err != nil {
			t.Error(err)
		}
	})
	h.clock.At(30*time.Second, func() {
		h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| A 3 |", "| A-3 |", 1))
	})
	h.signalAt(40*time.Second, "A-2")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := durations(h.times(TaskReset)...); got != "30s" {
		t.Errorf("task_reset at %s, want once the plan is valid (30s); events %s", got, h.kinds())
	}
	if w := h.event(Warning, ""); !strings.HasPrefix(w.Detail, "the reset of A-1 waits until the plan is valid") {
		t.Errorf("warning = %q", w.Detail)
	}
}

// A reset written while the current task's verify runs is neither lost
// nor applied in the middle of it: once verify ends, the owner answers it
// and the task is reset instead of accepted (SPEC §6.2), whether verify
// passed or failed.
func TestResetDuringVerify(t *testing.T) {
	for _, pass := range []bool{true, false} {
		t.Run(map[bool]string{true: "passing", false: "failing"}[pass], func(t *testing.T) {
			h := newHarness(t, chainPlan, verifyTOML)
			h.confirmResets(true)
			h.continueWhenPaused()
			n := 0
			h.verifyFn = func(runner.Cmd) (runner.Result, error) {
				n++
				if n == 1 {
					// The owner runs `igris reset A-1` while verify runs.
					if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset}); err != nil {
						t.Error(err)
					}
					if !pass {
						return runner.Result{ExitCode: 1}, nil
					}
				}
				return runner.Result{}, nil
			}
			res, err := h.run()
			if err != nil || res.Outcome != Completed {
				t.Fatalf("Run = %s, %v", res.Outcome, err)
			}
			if ev := h.event(TaskReset, "A-1"); ev.Detail != "A-1: in progress → ready" {
				t.Errorf("task_reset = %+v", ev)
			}
			if h.count(TaskDone) != 3 || h.opened() != "A-1 A-1 A-2 A-3" {
				t.Errorf("events = %s; sessions %s", h.kinds(), h.opened())
			}
			if pass && strings.Contains(h.kinds(), "verify_passed task_done") {
				t.Errorf("A-1 accepted before the reset: %s", h.kinds())
			}
		})
	}
}

// A reset written while the commit question waits is applied right after
// it is answered: no commit, no done.
func TestResetDuringCommitQuestion(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit = \"ask\"\n")
	h.dirtyTree()
	h.confirmResets(true)
	h.continueWhenPaused()
	asked := 0
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionCommit {
			asked++
			at := ev.At.Sub(t0)
			if asked == 1 {
				h.clock.At(at+5*time.Second, func() {
					if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset}); err != nil {
						t.Error(err)
					}
				})
			}
			h.clock.At(at+60*time.Second, func() { h.eng.Send(Command{Kind: CmdAnswer, Yes: true}) })
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	reset := h.event(TaskReset, "A-1")
	if got := reset.At.Sub(t0); got < 60*time.Second {
		t.Errorf("reset at %s, before the commit question was answered", got)
	}
	if h.count(TaskDone) != 3 || h.opened() != "A-1 A-1 A-2 A-3" {
		t.Errorf("events = %s; sessions %s", h.kinds(), h.opened())
	}
	if got := h.commits(); strings.Count(got, "A-1") != 1 {
		t.Errorf("commits = %q, want A-1 committed once, after it ran again", got)
	}
}

// The session's `igris done` right after the owner's reset doesn't replace
// it: the reset has its own slot (SPEC §6.2) and wins.
func TestResetNotReplacedByDone(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.confirmResets(true)
	h.continueWhenPaused()
	h.resetAt(9*time.Second, "A-1", false)
	h.signalAt(9*time.Second+500*time.Millisecond, "A-1")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if ev := h.event(TaskReset, "A-1"); ev.Detail != "A-1: in progress → ready" {
		t.Errorf("task_reset = %+v", ev)
	}
	if got := h.opened(); got != "A-1 A-1 A-2 A-3" {
		t.Errorf("sessions = %s", got)
	}
}

// A reset request is the owner's to confirm: one a session forged, in
// either slot, is never applied on its own; declined, it is dropped and
// the run goes on.
func TestResetNeedsTheOwner(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-2")
	h.confirmResets(false)
	h.clock.At(20*time.Second, func() {
		// What any session with file-write access can do.
		h.write(".igris/signals/A-1.json", `{"id":"A-1","action":"reset","note":"","at":"2020-01-01T00:00:00Z","force":true}`)
		h.write(".igris/signals/A-1.reset.json", `{"id":"A-1","action":"reset","note":"","at":"2020-01-01T00:00:00Z","force":true}`)
	})
	h.signalAt(40*time.Second, "A-2")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if h.count(TaskReset) != 0 || h.opened() != "A-1 A-2 A-3" {
		t.Errorf("forged reset applied: %s; sessions %s", h.kinds(), h.opened())
	}
	ask := h.event(Asked, "A-1")
	if ask.Question != QuestionConfirmReset || ask.Phase != "A" || !strings.Contains(ask.Detail, "reset A-1 (done) to ready/blocked (forced)?") {
		t.Errorf("question = %+v", ask)
	}
	if ev := h.event(ResetDropped, "A-1"); !strings.Contains(ev.Detail, "you declined") {
		t.Errorf("reset_dropped = %+v", ev)
	}
	if s, _ := h.dir.ReadReset("A-1"); s != nil {
		t.Errorf("declined reset left behind: %+v", s)
	}
	// The forged reset in the done slot is unreadable, reported, kept.
	found := false
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, "A-1.json") && strings.Contains(ev.Detail, `action "reset"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("the reset in the done slot was not reported: %s", h.kinds())
	}
}

// A request withdrawn before the owner answers (its file deleted) is
// dropped; a reset for another task is answered while the current one
// goes on, and an answer with no request waiting is rejected.
func TestResetWithdrawn(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-2")
	h.resetAt(20*time.Second, "A-1", true)
	h.clock.At(30*time.Second, func() {
		if err := h.dir.RemoveReset("A-1"); err != nil {
			t.Error(err)
		}
	})
	h.clock.At(35*time.Second, func() { h.eng.Send(Command{Kind: CmdAnswer, Question: QuestionConfirmReset, Yes: true}) })
	h.signalAt(40*time.Second, "A-2")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if ev := h.event(ResetDropped, "A-1"); ev.Detail != "the reset request for A-1 was withdrawn" {
		t.Errorf("reset_dropped = %+v", ev)
	}
	if h.count(TaskReset) != 0 || h.opened() != "A-1 A-2 A-3" {
		t.Errorf("events = %s", h.kinds())
	}
	if w := h.event(Warning, ""); w.Detail != "ignored answer: no reset request waits for an answer" {
		t.Errorf("warning = %q", w.Detail)
	}
}

// Declining the pending reset of the interrupted task at start picks the
// task up again as usual.
func TestResetPendingAtStartDeclined(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset}); err != nil {
		t.Fatal(err)
	}
	h.confirmResets(false)
	h.signalAt(30*time.Second, "A-1")
	res, err := h.run(resumeLast)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed run = %s, %v", res.Outcome, err)
	}
	if !strings.HasPrefix(h.kinds(), "run_started asked reset_dropped task_resumed") {
		t.Errorf("events = %s", h.kinds())
	}
	if h.count(TaskReset) != 0 || h.opened() != "A-1 A-2 A-3" {
		t.Errorf("events = %s; sessions %s", h.kinds(), h.opened())
	}
}

// An interrupted skip-permissions task with a pending reset needs no yolo
// confirmation when the owner confirms the reset (the task isn't picked up
// again); kept, it does.
func TestResetPendingAtStartYolo(t *testing.T) {
	for _, yes := range []bool{true, false} {
		h := newHarness(t, chainPlan, "")
		h.autoSignalExcept("A-1")
		h.stopMidTask(6*time.Second, func(o *Options) { o.Mode, o.ConfirmedYolo = ModeYolo, true })
		if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionReset}); err != nil {
			t.Fatal(err)
		}
		h.confirmResets(yes)
		h.continueWhenPaused()
		res, err := h.run(resumeLast)
		switch {
		case yes && (err != nil || res.Outcome != Completed):
			t.Errorf("confirmed: run = %s, %v", res.Outcome, err)
		case !yes && !errors.Is(err, ErrYoloUnconfirmed):
			t.Errorf("declined: err = %v, want ErrYoloUnconfirmed", err)
		}
	}
}

// Reset events and warnings name the reset task and its phase, not the
// current task's.
func TestResetEventsNameTheirTask(t *testing.T) {
	done := strings.NewReplacer("| ready | sonnet", "| done | sonnet", "| blocked | opus", "| done | opus", "| blocked | fable", "| done | fable", "| A-3 | blocked | sonnet", "| A-3 | ready | sonnet").Replace(chainPlan)
	h := newHarness(t, done, "")
	h.autoSignalExcept("B-1")
	h.confirmResets(true)
	h.resetAt(10*time.Second, "A-1", true)
	h.resetAt(10*time.Second, "A-2", false) // done, not forced
	h.signalAt(30*time.Second, "B-1")
	if _, err := h.run(func(o *Options) { o.Phase = "B" }); err != nil {
		t.Fatal(err)
	}
	if ev := h.event(TaskReset, "A-1"); ev.Phase != "A" || ev.Title != "One" {
		t.Errorf("task_reset = %+v, want phase A", ev)
	}
	w := h.event(Warning, "A-2")
	if w.Phase != "A" || w.Title != "" || w.Detail != "not reset: A-2 is done; pass --force to reset it" {
		t.Errorf("warning = %+v, want A-2 in phase A", w)
	}
}
