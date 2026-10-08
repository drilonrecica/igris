package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"os/exec"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

const hooksTOML = `[hooks]
before_task = ["./scripts/prep", "--task"]
after_task = ["post-status"]
timeout = "30s"
`

// hookCall is one task hook run, with what igris had done when it ran.
type hookCall struct {
	cmd            runner.Cmd
	opened, closed int // sessions opened and closed before the call
}

// recordHooks answers the hook commands with answer (nil: success) and
// records them.
func (h *harness) recordHooks(answer func(runner.Cmd) (runner.Result, error)) *[]hookCall {
	var calls []hookCall
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		if c.Name != "./scripts/prep" && c.Name != "post-status" {
			return runner.Result{}, fmt.Errorf("unexpected command %s", c)
		}
		calls = append(calls, hookCall{cmd: c, opened: len(h.be.Opened()), closed: len(h.be.Closed())})
		if answer != nil {
			return answer(c)
		}
		return runner.Result{}, nil
	}
	return &calls
}

// env returns the value of name in c's environment; the last one counts,
// as for exec.
func env(c runner.Cmd, name string) (string, bool) {
	val, ok := "", false
	for _, kv := range c.Env {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			val, ok = v, true
		}
	}
	return val, ok
}

// Hooks run as argv in the project root around every session, with the
// IGRIS_* variables, before_task before the pane opens and after_task once
// the session is closed (SPEC §6.7).
func TestHooksRunAroundEachSession(t *testing.T) {
	h := newHarness(t, chainPlan, hooksTOML)
	t.Setenv("IGRIS_TEST_INHERITED", "yes")
	calls := h.recordHooks(nil)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	if len(*calls) != 6 {
		t.Fatalf("%d hook calls, want 6 (before and after each of 3 tasks)", len(*calls))
	}
	models := map[string]string{"A-1": "sonnet", "A-2": "opus", "A-3": "fable"}
	for i, id := range []string{"A-1", "A-2", "A-3"} {
		before, after := (*calls)[2*i], (*calls)[2*i+1]
		if got := strings.Join(append([]string{before.cmd.Name}, before.cmd.Args...), " "); got != "./scripts/prep --task" {
			t.Errorf("%s before_task argv = %q", id, got)
		}
		if got := strings.Join(append([]string{after.cmd.Name}, after.cmd.Args...), " "); got != "post-status" {
			t.Errorf("%s after_task argv = %q", id, got)
		}
		if before.opened != i || after.opened != i+1 || after.closed != i+1 {
			t.Errorf("%s: before_task after %d opened, after_task after %d opened/%d closed; want %d, %d/%d", id, before.opened, after.opened, after.closed, i, i+1, i+1)
		}
		for _, c := range []runner.Cmd{before.cmd, after.cmd} {
			if c.Dir != h.root || c.Timeout != 30*time.Second || !c.Combined {
				t.Errorf("%s %s: dir %q, timeout %s, combined %v", id, c.Name, c.Dir, c.Timeout, c.Combined)
			}
			for name, want := range map[string]string{
				"IGRIS_TASK_ID": id, "IGRIS_PHASE": "A", "IGRIS_RANK": models[id], "IGRIS_MODEL": models[id], "IGRIS_TEST_INHERITED": "yes",
			} {
				if got, _ := env(c, name); got != want {
					t.Errorf("%s %s: %s = %q, want %q", id, c.Name, name, got, want)
				}
			}
		}
		if _, ok := env(before.cmd, "IGRIS_RESULT"); ok {
			t.Errorf("%s before_task has IGRIS_RESULT", id)
		}
		if got, _ := env(after.cmd, "IGRIS_RESULT"); got != "done" {
			t.Errorf("%s after_task IGRIS_RESULT = %q, want done", id, got)
		}
	}
	if h.count(HookFailed) != 0 {
		t.Errorf("hook_failed events: %s", h.kinds())
	}
	// The feed says when a hook starts (TUI and --no-tui).
	if ev := h.event(HookStarted, "A-1"); ev.Detail != "before_task" {
		t.Errorf("hook_started = %+v", ev)
	}
	if !strings.HasPrefix(h.kinds(), "run_started phase_started task_started hook_started session_opened") || h.count(HookStarted) != 6 {
		t.Errorf("events = %s", h.kinds())
	}
}

// A stop sent while before_task runs opens no session: the run stops with
// the task in progress, as after a crash there (SPEC §6.7).
func TestStopDuringBeforeTask(t *testing.T) {
	h := newHarness(t, chainPlan, hooksTOML)
	h.recordHooks(func(c runner.Cmd) (runner.Result, error) {
		if c.Name == "./scripts/prep" {
			h.eng.Send(Command{Kind: CmdStop})
		}
		return runner.Result{}, nil
	})
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v; want stopped", res.Outcome, err)
	}
	if got := h.opened(); got != "" {
		t.Errorf("sessions opened after the stop: %q", got)
	}
	if got := h.statuses(); got != "A-1=in progress A-2=blocked A-3=blocked B-1=blocked" {
		t.Errorf("statuses = %s", got)
	}
}

// The resolved --model value, not the rank, is IGRIS_MODEL.
func TestHooksModelIsResolved(t *testing.T) {
	h := newHarness(t, chainPlan, hooksTOML+"[models]\nsonnet = \"claude-sonnet-9\"\n")
	calls := h.recordHooks(nil)
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := (*calls)[0].cmd
	if rank, _ := env(c, "IGRIS_RANK"); rank != "sonnet" {
		t.Errorf("IGRIS_RANK = %q", rank)
	}
	if model, _ := env(c, "IGRIS_MODEL"); model != "claude-sonnet-9" {
		t.Errorf("IGRIS_MODEL = %q", model)
	}
}

// A failing before_task opens no session: the task stays in progress, the
// owner is asked, the log gets the reason and run_error goes out. Retry
// runs the hook again (SPEC §6.7).
func TestBeforeTaskFailure(t *testing.T) {
	tests := []struct {
		name   string
		answer func() (runner.Result, error)
		reason string
	}{
		{"exit status", func() (runner.Result, error) {
			return runner.Result{ExitCode: 1, Stdout: []byte("checking db\n\x1b[31mdb is down\x1b[0m\n")}, nil
		}, "before_task hook failed: exit status 1"},
		{"timeout", func() (runner.Result, error) {
			return runner.Result{ExitCode: -1}, fmt.Errorf("run ./scripts/prep: %w after 30s", runner.ErrTimeout)
		}, "before_task hook failed: timed out after 30s"},
		// The reason is fixed words: the error names the argv, which may
		// hold a token from igris.toml.
		{"can't start", func() (runner.Result, error) {
			return runner.Result{ExitCode: -1}, errors.New("run ./scripts/prep --token=SECRET: permission denied")
		}, "before_task hook failed: can't start it"},
		{"not found", func() (runner.Result, error) {
			return runner.Result{ExitCode: -1}, fmt.Errorf("run ./scripts/prep --token=SECRET: %w", exec.ErrNotFound)
		}, "before_task hook failed: not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, hooksTOML)
			failures := 1
			calls := h.recordHooks(func(c runner.Cmd) (runner.Result, error) {
				if c.Name == "./scripts/prep" && failures > 0 {
					failures--
					return tt.answer()
				}
				return runner.Result{}, nil
			})
			h.onEvent = func(ev Event) {
				if ev.Kind != Asked || ev.Question != QuestionHookFailed {
					return
				}
				if got := h.statuses(); got != "A-1=in progress A-2=blocked A-3=blocked B-1=blocked" {
					t.Errorf("while asking: %s", got)
				}
				if got := len(h.be.Opened()); got != 0 {
					t.Errorf("%d sessions opened before the hook passed", got)
				}
				h.eng.Send(Command{Kind: CmdRetry})
			}
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
				t.Errorf("statuses = %s", got)
			}
			if got := h.opened(); got != "A-1 A-2 A-3" {
				t.Errorf("sessions opened for %q", got)
			}
			if n := len(*calls); n != 7 {
				t.Errorf("%d hook calls, want 7 (the failed before_task ran again)", n)
			}
			failed := h.event(HookFailed, "A-1")
			if failed.Detail != tt.reason {
				t.Errorf("hook_failed detail = %q, want %q", failed.Detail, tt.reason)
			}
			if tt.name == "exit status" && !slices.Equal(failed.Output, []string{"checking db", "db is down"}) {
				t.Errorf("hook_failed output = %q, want the cleaned lines", failed.Output)
			}
			if got := h.event(NeedsYou, "A-1").Detail; got != tt.reason+"; no session was opened" {
				t.Errorf("needs_you = %q", got)
			}
			if got := h.event(Asked, "A-1").Detail; !strings.Contains(got, "retry (runs the hook again), mark the task done, skip it, or stop") {
				t.Errorf("question = %q", got)
			}
			if h.count(SessionLost) != 0 {
				t.Errorf("a failed hook is no lost session: %s", h.kinds())
			}
			// The retry ends the wait and is the task's second attempt.
			if got, want := h.waits(), "A-1#1 hook_failed, clear A-1#1 hook_failed, retried A-1#2"; got != want {
				t.Errorf("run log waits = %q, want %q", got, want)
			}
			events, err := h.dir.Events()
			if err != nil {
				t.Fatal(err)
			}
			var errs []state.Event
			for _, e := range events {
				if e.Type == state.EventError {
					errs = append(errs, e)
				}
			}
			if len(errs) != 1 || errs[0].Task != "A-1" || errs[0].Detail != tt.reason {
				t.Errorf("logged errors = %+v", errs)
			}
			toasts := h.toasts()
			found := false
			for _, s := range toasts {
				found = found || strings.Contains(s, tt.reason)
				if strings.Contains(s, "db is down") || strings.Contains(s, "needs you") {
					t.Errorf("toast %q carries the output or a needs_input", s)
				}
			}
			if !found {
				t.Errorf("toasts = %q, want the run_error with the reason", toasts)
			}
		})
	}
}

// After a failed before_task the owner can skip the task: no session ever
// opens for it and after_task reports skipped.
func TestBeforeTaskFailureThenSkip(t *testing.T) {
	h := newHarness(t, chainPlan, hooksTOML)
	calls := h.recordHooks(func(c runner.Cmd) (runner.Result, error) {
		if c.Name == "./scripts/prep" && slices.Contains(c.Env, "IGRIS_TASK_ID=A-1") {
			return runner.Result{ExitCode: 3}, nil
		}
		return runner.Result{}, nil
	})
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionHookFailed {
			h.eng.Send(Command{Kind: CmdSkip, Text: "no db today"})
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.statuses(); got != "A-1=skipped A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	if got := h.opened(); got != "A-2 A-3" {
		t.Errorf("sessions opened for %q", got)
	}
	after := (*calls)[1].cmd
	if got, _ := env(after, "IGRIS_RESULT"); after.Name != "post-status" || got != "skipped" {
		t.Errorf("second hook = %s with IGRIS_RESULT=%q, want after_task with skipped", after, got)
	}
}

// A failing after_task is a warning: the run goes on.
func TestAfterTaskFailureContinues(t *testing.T) {
	h := newHarness(t, chainPlan, hooksTOML)
	h.recordHooks(func(c runner.Cmd) (runner.Result, error) {
		if c.Name == "post-status" && slices.Contains(c.Env, "IGRIS_TASK_ID=A-1") {
			return runner.Result{ExitCode: 2, Stdout: []byte(strings.Repeat("line\n", 30) + "last\n")}, nil
		}
		return runner.Result{}, nil
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	failed := h.event(HookFailed, "A-1")
	if failed.Detail != "after_task hook failed: exit status 2" {
		t.Errorf("hook_failed = %q", failed.Detail)
	}
	if len(failed.Output) != hookTailLines || failed.Output[hookTailLines-1] != "last" {
		t.Errorf("output = %d lines ending %q, want the last %d", len(failed.Output), failed.Output[len(failed.Output)-1], hookTailLines)
	}
	if h.count(NeedsYou)+h.count(Asked) != 0 {
		t.Errorf("an after_task failure asked the owner: %s", h.kinds())
	}
	if got := h.logged(); !strings.Contains(got, "task_done error") {
		t.Errorf("run log = %s, want the error after task_done", got)
	}
}

// User tasks get no hooks.
func TestHooksSkipUserTasks(t *testing.T) {
	h := newHarness(t, userPlan, hooksTOML)
	calls := h.recordHooks(nil)
	h.clock.At(10*time.Second, func() { h.eng.Send(Command{Kind: CmdDone}) })
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range *calls {
		if id, _ := env(c.cmd, "IGRIS_TASK_ID"); id != "A-2" {
			t.Errorf("hook ran for %s", id)
		}
	}
	if len(*calls) != 2 {
		t.Errorf("%d hook calls, want 2 (A-2 only)", len(*calls))
	}
}

// before_task runs again for a retry (fresh or continue), never for a
// reattach on resume.
func TestBeforeTaskOnRetryNotOnReattach(t *testing.T) {
	for _, cont := range []bool{false, true} {
		t.Run(map[bool]string{true: "continue", false: "fresh"}[cont], func(t *testing.T) {
			h := newHarness(t, chainPlan, hooksTOML)
			calls := h.recordHooks(nil)
			h.autoSignalExcept("A-1")
			h.clock.At(4*time.Second, func() { h.eng.Send(Command{Kind: CmdRetry, Continue: cont}) })
			h.stopMidTask(6 * time.Second)
			if n := len(*calls); n != 2 || (*calls)[1].opened != 1 || (*calls)[1].closed != 1 {
				t.Fatalf("hook calls before the stop = %d, want before_task twice, the second after the first session closed", n)
			}
			h.signalAt(20*time.Second, "A-1")
			h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
			if _, err := h.run(resumeLast); err != nil {
				t.Fatalf("resumed run: %v", err)
			}
			if got := h.event(TaskResumed, "A-1").Detail; got != "reattached to its session" {
				t.Fatalf("resumed: %q", got)
			}
			first := (*calls)[2].cmd
			if id, _ := env(first, "IGRIS_TASK_ID"); first.Name != "post-status" || id != "A-1" {
				t.Errorf("first hook after the resume = %s for %s, want A-1's after_task", first, id)
			}
		})
	}
}

// realHooks runs the hooks as real processes and git through the fake.
type realHooks struct{ fake *runner.Fake }

func (r realHooks) Run(ctx context.Context, c runner.Cmd) (runner.Result, error) {
	if c.Name == "git" {
		return r.fake.Run(ctx, c)
	}
	return runner.Exec{}.Run(ctx, c)
}

// A hook's failure reason is fixed words, never the runner's error with
// the argv (which may carry a token from igris.toml): not in the run log,
// the feed or the notification. A hook killed by a signal says so.
func TestHookFailureReasons(t *testing.T) {
	tests := []struct {
		name, toml, reason string
	}{
		{"not found", "[hooks]\nafter_task = [\"/nonexistent/notify\", \"--token=SECRET123\"]\n", "after_task hook failed: not found"},
		{"killed", "[hooks]\nafter_task = [\"sh\", \"-c\", \"kill -9 $$\", \"--token=SECRET123\"]\n", "after_task hook failed: killed by signal 9 (killed)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, tt.toml)
			if _, err := h.run(func(o *Options) { o.Runner = realHooks{h.cmds} }); err != nil {
				t.Fatal(err)
			}
			if ev := h.event(HookFailed, "A-1"); ev.Detail != tt.reason {
				t.Errorf("hook_failed = %q, want %q", ev.Detail, tt.reason)
			}
			if data := h.read(".igris/runs.jsonl"); strings.Contains(data, "SECRET123") || !strings.Contains(data, tt.reason) {
				t.Errorf("run log leaks the argv or lacks the reason:\n%s", data)
			}
			for _, ev := range h.events {
				if strings.Contains(ev.Detail, "SECRET123") {
					t.Errorf("%s event leaks the argv: %q", ev.Kind, ev.Detail)
				}
			}
			for _, n := range h.toasts() {
				if strings.Contains(n, "SECRET123") {
					t.Errorf("notification leaks the argv: %q", n)
				}
			}
		})
	}
}
