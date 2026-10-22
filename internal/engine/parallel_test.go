package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// parallelPlan is a format-3 plan whose phase A is marked parallel: a, b
// and c are independent agent tasks, phase B waits on them.
func parallelPlan(rows ...string) string {
	if len(rows) == 0 {
		rows = []string{
			"| a | **One** | — | ready | sonnet | agent | — |",
			"| b | **Two** | — | ready | sonnet | agent | — |",
			"| c | **Three** | — | ready | sonnet | agent | — |",
		}
	}
	return "<!-- igris:format 3 -->\n# Demo plan\n\n## A — First phase\n\n<!-- igris:parallel -->\n\n" +
		"| ID | Task | Deps | Status | Model | Owner | Group |\n|---|---|---|---|---|---|---|\n" + strings.Join(rows, "\n") + "\n" +
		"\n## B — Second phase\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n| B-1 | **Five** | a, b, c | blocked | sonnet | agent |\n"
}

// parallelTOML runs up to two tasks at once and commits: a parallel phase
// needs a git repository and commit = ask or auto (SPEC §6.11). Idle
// sessions need nobody for an hour, so the tests see the scheduling alone.
const parallelTOML = "needs_input_after = \"1h\"\n[run]\ncommit = \"auto\"\n[parallel]\nmax = 2\n"

// newParallel is a harness on parallelPlan with a git repository that has a
// commit and nothing to commit; sessions signal nothing by themselves.
func newParallel(t *testing.T, planText, tomlText string) *harness {
	h := newHarness(t, planText, tomlText)
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		switch c.Args[0] {
		case "rev-parse":
			return runner.Result{Stdout: []byte(fakeSHA + "\n")}, nil
		case "symbolic-ref":
			return runner.Result{Stdout: []byte("main\n")}, nil
		case "rev-list":
			return runner.Result{Stdout: []byte("0\n")}, nil
		}
		return runner.Result{}, nil
	}
	h.be.SetAutoSignal(func(context.Context, string) error { return nil })
	return h
}

// slots returns "task:slot" for every task_started line of the run log.
func (h *harness) slots() string {
	h.t.Helper()
	events, err := h.dir.Events()
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		if e.Type == state.EventTaskStarted || e.Type == state.EventTaskResumed {
			slot := "-"
			if e.Slot != nil {
				slot = fmt.Sprint(*e.Slot)
			}
			out = append(out, e.Task+":"+slot)
		}
	}
	return strings.Join(out, " ")
}

// A parallel phase starts up to max tasks at once (SPEC §5.1) and the next
// one as soon as a slot frees; each active task holds the lowest free slot
// (SPEC §6.11).
func TestParallelPhaseRunsUpToMax(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	h.on(func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "b" {
			if got := h.opened(); got != "a b" {
				t.Errorf("sessions opened when b started: %q, want a b", got)
			}
		}
	})
	h.clock.At(time.Minute, func() {
		if got := h.opened(); got != "a b" {
			t.Errorf("after a minute: sessions opened for %q, want a b (max = 2)", got)
		}
		if got := h.statuses(); got != "a=in progress b=in progress c=ready B-1=blocked" {
			t.Errorf("after a minute: %s", got)
		}
	})
	h.signalAt(2*time.Minute, "a")
	h.clock.At(3*time.Minute, func() {
		if got := h.opened(); got != "a b c" {
			t.Errorf("after a is done: sessions opened for %q, want a b c", got)
		}
		if got := h.statuses(); got != "a=done b=in progress c=in progress B-1=blocked" {
			t.Errorf("after a is done: %s", got)
		}
	})
	h.signalAt(4*time.Minute, "c")
	h.signalAt(5*time.Minute, "b")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	if got := h.slots(); got != "a:0 b:1 c:0" {
		t.Errorf("slots: %s, want a:0 b:1 c:0", got)
	}
	if got := h.event(PhaseDone, "").At; !got.Equal(t0.Add(5 * time.Minute)) {
		t.Errorf("phase done at %v, want when b was done", got.Sub(t0))
	}
	h.assertUnlocked()
}

// --parallel N replaces [parallel] max for the run (SPEC §14).
func TestParallelFlagOverridesMax(t *testing.T) {
	h := newParallel(t, parallelPlan(), strings.Replace(parallelTOML, "max = 2", "max = 1", 1))
	h.clock.At(time.Minute, func() {
		if got := h.opened(); got != "a b c" {
			t.Errorf("sessions opened for %q, want a b c (--parallel 3)", got)
		}
		for _, id := range []string{"a", "b", "c"} {
			if err := h.signal(id); err != nil {
				t.Fatal(err)
			}
		}
	})
	res, err := h.run(func(o *Options) { o.Parallel = 3 })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.slots(); got != "a:0 b:1 c:2" {
		t.Errorf("slots: %s", got)
	}
}

// With exclusive_owner_tasks (the default) a user or agent + user task
// starts only when nothing else is active, and nothing starts beside it
// (SPEC §5.1).
func TestParallelOwnerTaskRunsAlone(t *testing.T) {
	h := newParallel(t, parallelPlan(
		"| a | **One** | — | ready | sonnet | agent | — |",
		"| b | **Owner** | — | ready | — | user | — |",
		"| c | **Three** | — | ready | sonnet | agent | — |",
		"| d | **Four** | — | ready | sonnet | agent | — |",
	), parallelTOML)
	h.clock.At(time.Minute, func() {
		// a runs; b waits for it and holds c and d back.
		if got := h.statuses(); got != "a=in progress b=ready c=ready d=ready B-1=blocked" {
			t.Errorf("after a minute: %s", got)
		}
	})
	h.signalAt(2*time.Minute, "a")
	h.clock.At(3*time.Minute, func() {
		if got := h.statuses(); got != "a=done b=in progress c=ready d=ready B-1=blocked" {
			t.Errorf("while the owner task runs: %s", got)
		}
		if got := h.opened(); got != "a" {
			t.Errorf("sessions opened for %q, want only a", got)
		}
	})
	h.signalAt(4*time.Minute, "b")
	h.clock.At(5*time.Minute, func() {
		// Two at once again.
		if got := h.statuses(); got != "a=done b=done c=in progress d=in progress B-1=blocked" {
			t.Errorf("after the owner task: %s", got)
		}
		// The user task is never committed or integrated (SPEC §6.5): no
		// git ran in the project root for it between a's done and now.
		for _, c := range h.calls("git") {
			if c.Dir == h.root && !slices.Contains([]string{"rev-parse", "symbolic-ref", "worktree", "branch", "merge"}, c.Args[0]) {
				t.Errorf("git %v ran in the project root; a user task needs no git", c.Args)
			}
		}
	})
	h.signalAt(6*time.Minute, "c")
	h.signalAt(7*time.Minute, "d")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done d=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	if got := h.slots(); got != "a:0 b:0 c:0 d:1" {
		t.Errorf("slots: %s", got)
	}
	if got := h.count(YourTurn); got != 1 {
		t.Errorf("YourTurn events: %d", got)
	}
}

// Without commit = ask or auto a parallel phase runs one task at a time,
// with a warning (SPEC §6.11).
func TestParallelWithoutCommitRunsOneAtATime(t *testing.T) {
	h := newParallel(t, parallelPlan(), strings.Replace(parallelTOML, "auto", "never", 1))
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.opened(); got != "a b c" {
		t.Errorf("sessions opened for %q", got)
	}
	// The next session opens only once the task before it is done.
	for i, ev := range h.events {
		if ev.Kind == TaskStarted && ev.Task != "a" {
			if prev := h.events[i-1]; prev.Kind != TaskDone {
				t.Errorf("%s started after %s, want after a task_done", ev.Task, prev.Kind)
			}
		}
	}
	want := `phase A is marked parallel, but commit = "never"; it runs one task at a time`
	if ev := h.event(Warning, ""); !strings.Contains(ev.Detail, want) {
		t.Errorf("warning %q, want %q", ev.Detail, want)
	}
	if got := h.slots(); got != "a:- b:- c:-" {
		t.Errorf("slots: %s, want none", got)
	}
}

// Without a git repository with a commit the same holds.
func TestParallelWithoutGitRunsOneAtATime(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		if c.Args[0] == "rev-parse" {
			return runner.Result{ExitCode: 128, Stdout: []byte("fatal: not a git repository")}, nil
		}
		return runner.Result{}, nil
	}
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	want := "phase A is marked parallel, but there is no git repository with a commit here; it runs one task at a time"
	if ev := h.event(Warning, ""); !strings.Contains(ev.Detail, want) {
		t.Errorf("warning %q, want %q", ev.Detail, want)
	}
	if got := h.slots(); got != "a:- b:- c:-" {
		t.Errorf("slots: %s, want none", got)
	}
}

// An owner command names its task; without one it goes to the first active
// task, the current one (SPEC §13).
func TestParallelCommandsTargetTheirTask(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	h.clock.At(time.Minute, func() {
		h.eng.Send(Command{Kind: CmdDone, Task: "b", Text: "by hand"})
	})
	h.clock.At(2*time.Minute, func() {
		if got := h.statuses(); got != "a=in progress b=done c=in progress B-1=blocked" {
			t.Errorf("after done b: %s", got)
		}
		h.eng.Send(Command{Kind: CmdSkip, Text: "the first active task"})
	})
	h.clock.At(3*time.Minute, func() {
		if got := h.statuses(); got != "a=skipped b=done c=in progress B-1=blocked" {
			t.Errorf("after skip: %s", got)
		}
		h.eng.Send(Command{Kind: CmdDone, Task: "zz"})
		h.eng.Send(Command{Kind: CmdDone, Task: "c"})
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=skipped b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	found := false
	for _, ev := range h.events {
		found = found || ev.Kind == Warning && strings.Contains(ev.Detail, "ignored done: zz is not an active task")
	}
	if !found {
		t.Errorf("no warning for the command to zz in: %s", h.kinds())
	}
}

// A stopped run leaves every active task's session open; the next run
// reattaches each of them before starting anything new (SPEC §13).
func TestParallelResumeReattachesEveryActiveTask(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	h.clock.At(time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=in progress b=in progress c=ready B-1=blocked" {
		t.Errorf("after the stop: %s", got)
	}
	run, err := h.dir.LoadRun()
	if err != nil || len(run.Active) != 2 || run.Active[0].TaskID != "a" || run.Active[1].TaskID != "b" {
		t.Fatalf("state after the stop: %+v, %v", run, err)
	}

	h.events = nil
	h.clock.At(2*time.Minute, func() {
		if got := h.opened(); got != "a b" {
			t.Errorf("sessions opened for %q, want the two reattached, no new one", got)
		}
		if err := h.signal("a"); err != nil {
			t.Fatal(err)
		}
	})
	h.signalAt(3*time.Minute, "b")
	h.signalAt(4*time.Minute, "c")
	res, err = h.run(func(o *Options) { o.Phase = "" })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed Run = %s, %v", res.Outcome, err)
	}
	if got := h.count(TaskResumed); got != 2 {
		t.Errorf("TaskResumed events: %d, want 2 in %s", got, h.kinds())
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	if got := h.opened(); got != "a b c" {
		t.Errorf("sessions opened for %q", got)
	}
	// The reattached tasks keep their slots; c takes the one a freed.
	if got := h.slots(); !strings.HasSuffix(got, "a:0 b:1 c:0") {
		t.Errorf("slots: %s", got)
	}
}

// A resumed run fills a slot as soon as a reattached task frees it, while
// the others still run (SPEC §5.1): the phase is known to be parallel from
// its first poll, though nothing was ever selected with nothing active.
func TestParallelResumeFillsAFreedSlot(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	h.clock.At(time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
	if res, err := h.run(); err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}

	h.events = nil
	h.signalAt(2*time.Minute, "a")
	h.clock.At(3*time.Minute, func() {
		if got := h.opened(); got != "a b c" {
			t.Errorf("with a done and b running, sessions opened for %q; want c in a's slot", got)
		}
		if err := h.signal("b"); err != nil {
			t.Fatal(err)
		}
	})
	h.signalAt(4*time.Minute, "c")
	res, err := h.run(func(o *Options) { o.Phase = "" })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
}

// Needs you raised for several tasks in one poll is one notification,
// "2 tasks need you: a, b" (SPEC §10); a single task's goes out as before.
// Every event of an active task carries its slot, and each task's session
// can be tailed (SPEC §15.1).
func TestParallelNeedsYouIsOneNotification(t *testing.T) {
	h := newParallel(t, parallelPlan(), "[run]\ncommit = \"auto\"\n[parallel]\nmax = 2\n")
	h.be.Script("a", states(backend.Idle, 120)...)
	h.be.Script("b", states(backend.Idle, 120)...)
	h.be.Script("c", states(backend.Working, 120)...)
	h.be.SetTail("a", "⏺ working on a")
	h.be.SetTail("b", "⏺ working on b")
	h.clock.At(time.Minute, func() {
		for id, want := range map[string]string{"a": "on a", "b": "on b", "": "on a"} {
			lines, err := h.eng.TailTask(context.Background(), id, 10)
			if err != nil || !strings.Contains(strings.Join(lines, "\n"), want) {
				t.Errorf("TailTask(%q) = %q, %v; want %q", id, lines, err, want)
			}
		}
	})
	h.signalAt(5*time.Minute, "a")
	h.signalAt(5*time.Minute, "b")
	h.signalAt(6*time.Minute, "c")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	var needs []string
	for _, n := range h.toasts() {
		if strings.Contains(n, "need") {
			needs = append(needs, n)
		}
	}
	if len(needs) != 1 || needs[0] != "request: phase A: 2 tasks need you: a, b" {
		t.Errorf("needs-you toasts = %q, want one for both tasks", needs)
	}
	if got := h.count(NeedsYou); got != 2 {
		t.Errorf("%d NeedsYou events, want one per task", got)
	}
	for _, id := range []string{"a", "b"} {
		for _, kind := range []EventKind{TaskStarted, SessionOpened, NeedsYou} {
			if ev := h.event(kind, id); ev.Slot == nil {
				t.Errorf("%s of %s has no slot", kind, id)
			}
		}
	}
	if s := h.event(TaskStarted, "b").Slot; s == nil || *s != 1 {
		t.Errorf("b's slot = %v, want 1", s)
	}
}

// One task needing the owner while another works is told on its own.
func TestParallelNeedsYouAlone(t *testing.T) {
	h := newParallel(t, parallelPlan(), "[run]\ncommit = \"auto\"\n[parallel]\nmax = 2\n")
	h.be.Script("a", states(backend.Idle, 120)...)
	h.be.Script("b", states(backend.Working, 120)...)
	h.be.Script("c", states(backend.Working, 120)...)
	h.signalAt(5*time.Minute, "a")
	h.signalAt(6*time.Minute, "b")
	h.signalAt(7*time.Minute, "c")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	var needs []string
	for _, n := range h.toasts() {
		if strings.Contains(n, "need") {
			needs = append(needs, n)
		}
	}
	if len(needs) != 1 || !strings.HasPrefix(needs[0], "request: phase A · a One: needs you") {
		t.Errorf("needs-you toasts = %q, want a's own", needs)
	}
}
