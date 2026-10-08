package engine

// End-to-end runs of a realistic two-phase plan on the fake backend and a
// fake clock, one per SPEC §17 "Engine" scenario: done, verify failure +
// retry, verify limit, session lost, user tasks, skip, pause, resume after
// restart, stray signals. Each checks the final plan bytes, the run log, the
// notifications and state.json.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// e2eRows are the tasks of the e2e plan with their initial status.
var e2eRows = []struct{ id, format, status string }{
	{"P1-01", "| P1-01 | **Module** — init the module | — | §1 | %s | sonnet | agent | — |", "ready"},
	{"P1-02", "| P1-02 | **Config** — loader for `a \\| b` values | P1-01 | §2 | %s | opus | agent | accept |", "blocked"},
	{"P1-03", "| P1-03 | **Domain** — buy it at any registrar | — | — | %s | — | user | — |", "ready"},
	{"P1-G", "| P1-G | **P1 gate** — owner sign-off | P1-01…P1-03 | — | %s | fable | agent + user | — |", "blocked"},
	{"P2-01", "| P2-01 | **API** | P1-G | %s | opus | agent |", "blocked"},
	{"P2-02", "| P2-02 | **Docs** | P2-01, P1-03 | %s | sonnet | agent |", "blocked"},
}

// e2ePlan renders the plan with the given statuses (initial ones for IDs
// not in the map). Everything but the Status cells stays byte-identical.
func e2ePlan(statuses map[string]string) string {
	row := func(i int) string {
		st := e2eRows[i].status
		if s, ok := statuses[e2eRows[i].id]; ok {
			st = s
		}
		return fmt.Sprintf(e2eRows[i].format, st)
	}
	return "# Synthetic product plan\r\n\r\nSome intro.\r\n\r\n## P1 — Foundations\r\n\r\n" +
		"| ID | Task | Deps | Spec | Status | Model | Owner | Mode |\r\n|---|---|---|---|---|---|---|---|\r\n" +
		row(0) + "\r\n" + row(1) + "\r\n" + row(2) + "\r\n" + row(3) + "\r\n" +
		"\r\n## P2 — Features\r\n\r\n| ID | Task | Deps | Status | Model | Owner |\r\n|---|---|---|---|---|---|\r\n" +
		row(4) + "\r\n" + row(5) // no trailing newline
}

func allDone() map[string]string {
	m := map[string]string{}
	for _, r := range e2eRows {
		m[r.id] = "done"
	}
	return m
}

const e2eTOML = "[run]\nverify = \"make check\"\ncommit = \"auto\"\n"

// newE2E sets up the e2e project: verify passes, the tree is always dirty,
// sessions signal done at their first prompt, and P1-03 (the user task) is
// finished from another terminal a minute after igris hands it over.
func newE2E(t *testing.T) *harness {
	h := newHarness(t, e2ePlan(nil), e2eTOML)
	h.verifyFn = func(runner.Cmd) (runner.Result, error) { return runner.Result{}, nil }
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		switch c.Args[0] {
		case "status":
			return runner.Result{Stdout: []byte(" M file.go\n")}, nil
		case "rev-parse":
			return runner.Result{Stdout: []byte(fakeSHA + "\n")}, nil
		}
		return runner.Result{}, nil
	}
	h.on(func(ev Event) {
		if ev.Kind == YourTurn && ev.Task == "P1-03" {
			h.signalAt(ev.At.Sub(t0)+time.Minute, "P1-03")
		}
	})
	return h
}

// on adds an event handler after the existing ones.
func (h *harness) on(fn func(Event)) {
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		if prev != nil {
			prev(ev)
		}
		fn(ev)
	}
}

func runThrough(o *Options) { o.Phase, o.Through = "P1", "P2" }

// commits returns the subjects committed, in order.
func (h *harness) commits() string {
	var out []string
	for _, c := range h.calls("git") {
		if c.Args[0] == "commit" {
			out = append(out, c.Args[2])
		}
	}
	return strings.Join(out, ", ")
}

// check asserts the end state of an e2e run.
func (h *harness) check(statuses map[string]string, wantLog string, wantToasts ...string) {
	h.t.Helper()
	if got, want := h.read("tasks.md"), e2ePlan(statuses); got != want {
		h.t.Errorf("plan after the run:\n%q\nwant:\n%q", got, want)
	}
	if got := h.logged(); got != wantLog {
		h.t.Errorf("run log:\n%s\nwant:\n%s", got, wantLog)
	}
	if got := strings.Join(h.toasts(), "\n"); got != strings.Join(wantToasts, "\n") {
		h.t.Errorf("toasts:\n%s\nwant:\n%s", got, strings.Join(wantToasts, "\n"))
	}
	run, err := h.dir.LoadRun()
	if err != nil {
		h.t.Fatal(err)
	}
	if run.Current != nil || strings.Join(run.Phases, " ") != "P1 P2" || run.Through != "P2" {
		h.t.Errorf("state.json = %+v (current %+v)", run, run.Current)
	}
	if sigs, bad, err := h.dir.ListSignals(); err != nil || len(sigs)+len(bad) != 0 {
		h.t.Errorf("signals left: %v %v %v", sigs, bad, err)
	}
	h.assertUnlocked()
}

// Log fragments.
const (
	logAgentDone = " task_started verify_passed committed task_done"
	logUserTask  = " task_started notification task_done"
)

const (
	toastYourTurn = "request: phase P1 · P1-03 Domain: your turn: do it, then mark it done or skipped in igris"
	toastP1Done   = "done: phase P1: complete"
	toastP2Done   = "done: phase P2: complete"
)

func TestE2EDone(t *testing.T) {
	h := newE2E(t)
	res, err := h.run(runThrough)
	if err != nil || res.Outcome != Completed || res.Phase != "P2" {
		t.Fatalf("Run = %s in %q, %v", res.Outcome, res.Phase, err)
	}
	if got := h.opened(); got != "P1-01 P1-02 P1-G P2-01 P2-02" {
		t.Errorf("sessions opened for %q", got)
	}
	specs := h.be.Opened()
	for i, want := range []struct{ model, mode string }{{"sonnet", "auto"}, {"opus", "acceptEdits"}, {"fable", "auto"}, {"opus", "auto"}, {"sonnet", "auto"}} {
		if arg(specs[i], "--model") != want.model || arg(specs[i], "--permission-mode") != want.mode {
			t.Errorf("%s args %q, want model %s mode %q", specs[i].TaskID, specs[i].Args, want.model, want.mode)
		}
	}
	if got := h.commits(); got != "P1-01: Module, P1-02: Config, P1-G: P1 gate, P2-01: API, P2-02: Docs" {
		t.Errorf("commits = %s", got)
	}
	h.check(allDone(),
		"run_started"+logAgentDone+logAgentDone+logUserTask+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		toastYourTurn, toastP1Done, toastP2Done)
}

func TestE2EVerifyFailureAndRetry(t *testing.T) {
	h := newE2E(t)
	n := 0
	h.verifyFn = func(runner.Cmd) (runner.Result, error) {
		if n++; n == 2 { // P1-02's first attempt
			return runner.Result{ExitCode: 2, Stdout: []byte("config_test.go:12: want 3, got 4\nFAIL\n")}, nil
		}
		return runner.Result{}, nil
	}
	h.resignal(1)
	if _, err := h.run(runThrough); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := h.be.Prompts("P1-02"); len(p) != 2 || !strings.Contains(p[1], "config_test.go:12: want 3, got 4") {
		t.Errorf("P1-02 prompts = %q, want the failure sent back", p)
	}
	h.check(allDone(),
		"run_started"+logAgentDone+" task_started verify_failed verify_passed committed task_done"+logUserTask+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		toastYourTurn, toastP1Done, toastP2Done)
}

// P2-01 keeps failing; at the limit the owner is called, skips it, and the
// run goes on with its dependent.
func TestE2EVerifyLimit(t *testing.T) {
	h := newE2E(t)
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		if h.current() == "P2-01" {
			return runner.Result{ExitCode: 1, Stdout: []byte("boom\n")}, nil
		}
		return runner.Result{}, nil
	}
	h.resignal(2)
	h.on(func(ev Event) {
		if ev.Kind == VerifyLimit {
			h.eng.Send(Command{Kind: CmdSkip, Text: "API moves to v2"})
		}
	})
	if _, err := h.run(runThrough); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := h.be.Prompts("P2-01"); len(p) != 3 {
		t.Errorf("P2-01 got %d prompts, want the task and two failures (limit 3)", len(p))
	}
	want := allDone()
	want["P2-01"] = "skipped"
	h.check(want,
		"run_started"+logAgentDone+logAgentDone+logUserTask+logAgentDone+" notification"+
			" task_started verify_failed verify_failed verify_failed needs_you notification task_skipped"+logAgentDone+" notification run_stopped",
		toastYourTurn, toastP1Done, "request: phase P2 · P2-01 API: verification keeps failing; needs you", toastP2Done)
}

// current returns the ID of the task the run is working on. Call it on the
// engine's goroutine (from a fake command or an event handler).
func (h *harness) current() string {
	if h.eng.task == nil {
		return ""
	}
	return h.eng.task.t.ID
}

func TestE2ESessionLost(t *testing.T) {
	h := newE2E(t)
	h.autoSignalExcept("P1-02")
	h.be.Script("P1-02", backend.Working, backend.Working, backend.Exited)
	h.on(func(ev Event) {
		switch {
		case ev.Kind == Asked && ev.Question == QuestionSessionLost:
			h.eng.Send(Command{Kind: CmdRetry, Continue: true})
		case ev.Kind == SessionOpened && ev.Task == "P1-02" && h.count(SessionOpened) > 2:
			h.signalAt(ev.At.Sub(t0)+10*time.Second, "P1-02")
		}
	})
	if _, err := h.run(runThrough); err != nil {
		t.Fatalf("Run: %v", err)
	}
	specs := h.be.Opened()
	if arg(specs[2], "--resume") == "" || arg(specs[2], "--resume") != arg(specs[1], "--session-id") {
		t.Errorf("continued session args %q, want --resume of %q", specs[2].Args, arg(specs[1], "--session-id"))
	}
	h.check(allDone(),
		"run_started"+logAgentDone+" task_started needs_you notification needs_you_clear task_retried verify_passed committed task_done"+logUserTask+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		"request: phase P1 · P1-02 Config: session lost", toastYourTurn, toastP1Done, toastP2Done)
}

// User tasks: P1-03 by `igris done` from another terminal (see newE2E); a
// variant skips it from the owner's side.
func TestE2EUserTaskSkippedByTheOwner(t *testing.T) {
	h := newE2E(t)
	h.onEvent = nil // no `igris done` for P1-03
	h.on(func(ev Event) {
		if ev.Kind == YourTurn {
			h.eng.Send(Command{Kind: CmdSkip, Text: "domain already owned"})
		}
	})
	if _, err := h.run(runThrough); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.event(TaskSkipped, "P1-03").Detail; got != "domain already owned" {
		t.Errorf("skip reason %q", got)
	}
	want := allDone()
	want["P1-03"] = "skipped"
	h.check(want,
		"run_started"+logAgentDone+logAgentDone+" task_started notification task_skipped"+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		toastYourTurn, toastP1Done, toastP2Done)
}

// The P1-02 session asks to skip; the owner confirms, and its dependents
// still run (skipped satisfies a dependency).
func TestE2ESkip(t *testing.T) {
	h := newE2E(t)
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "P1-02" {
			return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionSkip, Note: "config comes from the env"})
		}
		return h.signal(id)
	})
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionConfirmSkip {
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true})
		}
	})
	if _, err := h.run(runThrough); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := allDone()
	want["P1-02"] = "skipped"
	h.check(want,
		"run_started"+logAgentDone+" task_started needs_you notification task_skipped"+logUserTask+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		"request: phase P1 · P1-02 Config: needs you (the session asks to skip)", toastYourTurn, toastP1Done, toastP2Done)
	if got := h.commits(); strings.Contains(got, "P1-02") {
		t.Errorf("a skipped task was committed: %s", got)
	}
}

func TestE2EPause(t *testing.T) {
	h := newE2E(t)
	h.on(func(ev Event) {
		switch {
		case ev.Kind == TaskStarted && ev.Task == "P1-G":
			h.eng.Send(Command{Kind: CmdPause})
		case ev.Kind == Paused:
			h.clock.At(ev.At.Sub(t0)+5*time.Minute, func() { h.eng.Send(Command{Kind: CmdPause}) })
		}
	})
	if _, err := h.run(runThrough); err != nil {
		t.Fatalf("Run: %v", err)
	}
	held := h.event(Paused, "")
	if held.Task != "P2-01" || held.Phase != "P2" {
		t.Errorf("held before %s in %s, want P2-01 in P2", held.Task, held.Phase)
	}
	if got := h.event(TaskStarted, "P2-01").At.Sub(held.At); got != 5*time.Minute {
		t.Errorf("P2-01 started %s after the hold, want 5m", got)
	}
	h.check(allDone(),
		"run_started"+logAgentDone+logAgentDone+logUserTask+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		toastYourTurn, toastP1Done, toastP2Done)
}

// igris stops during P1-02 (e.g. `x`, or the machine went down); the next
// `igris arise` without arguments reattaches and finishes the whole range.
func TestE2EResumeAfterRestart(t *testing.T) {
	h := newE2E(t)
	h.autoSignalExcept("P1-02")
	h.on(func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "P1-02" {
			h.eng.Send(Command{Kind: CmdStop})
		}
	})
	if res, err := h.run(runThrough); err != nil || res.Outcome != Stopped {
		t.Fatalf("first run = %s, %v", res.Outcome, err)
	}
	if got := h.read("tasks.md"); got != e2ePlan(map[string]string{"P1-01": "done", "P1-02": "in progress"}) {
		t.Errorf("plan after the stop:\n%q", got)
	}

	// While igris is down the session finishes and runs `igris done`.
	if err := h.signal("P1-02"); err != nil {
		t.Fatal(err)
	}
	h.events = nil
	if res, err := h.run(resumeLast); err != nil || res.Outcome != Completed || res.Phase != "P2" {
		t.Fatalf("resumed run = %s in %q, %v", res.Outcome, res.Phase, err)
	}
	if got := h.opened(); got != "P1-01 P1-02 P1-G P2-01 P2-02" {
		t.Errorf("sessions opened for %q, want no second P1-02 session", got)
	}
	h.check(allDone(),
		"run_started"+logAgentDone+" task_started run_stopped"+
			" run_started task_resumed verify_passed committed task_done"+logUserTask+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		toastYourTurn, toastP1Done, toastP2Done)
}

// `igris done P2-02` run long before P2-02 starts is reported as stray,
// then as stale once P2-02 runs; P2-02 needs its own signal.
func TestE2EStraySignals(t *testing.T) {
	h := newE2E(t)
	if err := h.dir.WriteSignal(state.Signal{ID: "P2-02", Action: state.ActionDone, Note: "too early"}); err != nil {
		t.Fatal(err)
	}
	// P2-02's session takes 10s, so igris sees the early signal first.
	h.autoSignalExcept("P2-02")
	h.on(func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "P2-02" {
			h.signalAt(ev.At.Sub(t0)+10*time.Second, "P2-02")
		}
	})
	if _, err := h.run(runThrough); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(StraySignal); got != 1 {
		t.Errorf("%d stray_signal events, want 1", got)
	}
	if ev := h.event(StaleSignal, "P2-02"); !strings.Contains(ev.Detail, "written before the task started") {
		t.Errorf("stale_signal = %q", ev.Detail)
	}
	if got := h.event(TaskDone, "P2-02").Detail; got != "did P2-02" {
		t.Errorf("P2-02 accepted with note %q, want its own signal's", got)
	}
	h.check(allDone(),
		"run_started"+logAgentDone+logAgentDone+logUserTask+logAgentDone+" notification"+logAgentDone+logAgentDone+" notification run_stopped",
		toastYourTurn, toastP1Done, toastP2Done)
}
