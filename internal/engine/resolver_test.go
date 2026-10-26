package engine

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// resolveTOML opens a resolver session for a task that doesn't integrate
// (SPEC §6.11).
const resolveTOML = integrationTOML + "on_conflict = \"resolve\"\n"

// isResolver reports whether the latest session of id was given the
// resolver's prompt.
func (h *harness) isResolver(id string) bool {
	p := h.be.Prompts(id)
	return len(p) > 0 && strings.Contains(p[len(p)-1], "You are the **resolver session**")
}

// resolverSignals signals done for every task session and, for a resolver
// session, calls resolver instead (nil: done as well).
func (h *harness) resolverSignals(resolver func(id string) error) {
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if h.isResolver(id) {
			if resolver != nil {
				return resolver(id)
			}
			return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionDone, Note: "kept both"})
		}
		return h.signal(id)
	})
}

// filtered is the run log of task with only the lines of the integration
// and the resolver.
func (h *harness) filtered(task string) []string {
	var got []string
	for _, l := range h.logOf(task) {
		for _, p := range []string{"integration_failed", "resolver_", "task_retried", "needs_you", "integrated", "task_done", "task_skipped"} {
			if strings.HasPrefix(l, p) {
				got = append(got, l)
				break
			}
		}
	}
	return got
}

// A rebase conflict with on_conflict = "resolve" opens one resolver
// session in the task's worktree, mid-rebase; after its done igris
// continues the rebase, verifies the integrated tree and fast-forwards.
// It is no attempt: no rerun, task_done names attempt 1, and its usage
// line is marked (SPEC §6.11).
func TestResolverResolvesConflict(t *testing.T) {
	h := newParallel(t, twoThenOne(), resolveTOML+usageOn)
	r := h.repo()
	cmds := h.commands()
	h.resolverSignals(nil)
	var midRebase bool
	h.on(func(ev Event) {
		switch ev.Kind {
		case RunStarted:
			r.conflictOn(taskWT(ev.Run, "a"), 1, "main.go")
		case ResolverOpened:
			midRebase = r.rebasing(taskWT(h.runBase(), "a"))
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	want := []string{
		"integration_failed/conflict:main.go; then resolve",
		"resolver_opened/conflict:main.go",
		"resolver_done:kept both",
		"integrated:a: One",
		"task_done:did a",
	}
	if got := h.filtered("a"); !slices.Equal(got, want) {
		t.Errorf("log of a %q, want %q", got, want)
	}
	if !midRebase {
		t.Error("the resolver opened on a worktree that was not mid-rebase")
	}
	base := h.runBase()
	wt := taskWT(base, "a")
	for _, c := range []struct {
		sub  string
		want int
	}{
		{wt + ": rebase --abort", 0},
		{wt + ": add -A", 2}, // the task's commit, then the resolution
		{wt + ": -c core.editor=true rebase --continue", 1},
		{".: worktree add -b igris/" + base + "-a", 1}, // no rerun
		{".: merge --ff-only igris/" + base + "-a", 1},
	} {
		if got := r.calls(c.sub); len(got) != c.want {
			t.Errorf("calls %q: %d, want %d", c.sub, len(got), c.want)
		}
	}
	// The task's verify, the integrated tree's after the resolution.
	if got := verifiesIn(cmds, wt); got != 2 {
		t.Errorf("a verified %d times, want 2", got)
	}
	var labels []string
	for _, s := range h.be.Opened() {
		if s.TaskID == "a" {
			labels = append(labels, s.Label)
		}
	}
	if !slices.Equal(labels, []string{"a · sonnet", "a · resolver · sonnet"}) {
		t.Errorf("sessions of a %q", labels)
	}
	p := h.be.Prompts("a")
	if len(p) != 2 {
		t.Fatalf("prompts of a: %d, want the task's and the resolver's", len(p))
	}
	for _, s := range []string{"resolver session", "task a — One", "stopped on these files:\n\n- `main.go`", "igris done a", "igris skip a"} {
		if !strings.Contains(p[1], s) {
			t.Errorf("the resolver's prompt lacks %q:\n%s", s, p[1])
		}
	}
	if done := h.logEvent(state.EventTaskDone, "a"); done.Attempt != 1 {
		t.Errorf("task_done attempt %d, want 1: a resolver is no attempt", done.Attempt)
	}
	opened := h.logEvent(state.EventResolverOpened, "a")
	if opened.Attempt != 1 || opened.Rank != "sonnet" || opened.Session == "" {
		t.Errorf("resolver_opened %+v, want attempt 1, rank sonnet and its session", opened)
	}
	var usage []state.Event
	events, _ := h.dir.Events()
	for _, e := range events {
		if e.Type == state.EventUsage && e.Task == "a" {
			usage = append(usage, e)
		}
	}
	if len(usage) != 2 || usage[0].Resolver || !usage[1].Resolver || usage[1].Attempt != 1 || usage[1].Session != opened.Session {
		t.Errorf("usage lines of a %+v, want the task's then the resolver's (attempt 1, its session)", usage)
	}
	if n := h.count(NeedsYou); n != 0 {
		t.Errorf("%d needs-you events", n)
	}
	if got := h.event(ResolverOpened, "a").Detail; got != "resolving a conflict in 1 file" {
		t.Errorf("resolver_opened card %q", got)
	}
}

// A resolution that leaves nothing to commit (the run branch has the
// task's work already) skips the replayed commit; the task integrates with
// nothing to add (SPEC §6.11).
func TestResolverNothingToCommit(t *testing.T) {
	h := newParallel(t, twoThenOne(), resolveTOML)
	r := h.repo()
	h.commands()
	h.resolverSignals(func(id string) error {
		r.nothingStaged(taskWT(h.runBase(), id))
		return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionDone, Note: "already on the run branch"})
	})
	h.on(func(ev Event) {
		if ev.Kind == RunStarted {
			r.conflictOn(taskWT(ev.Run, "a"), 1, "main.go")
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	wt := taskWT(h.runBase(), "a")
	if got := r.calls(wt + ": -c core.editor=true rebase --skip"); len(got) != 1 {
		t.Errorf("rebase --skip %d times, want 1: %q", len(got), r.calls(wt))
	}
	if got := r.calls(wt + ": -c core.editor=true rebase --continue"); len(got) != 0 {
		t.Errorf("rebase --continue with nothing staged: %q", got)
	}
	if got := h.filtered("a"); !has(got, "resolver_done:already on the run branch") || !has(got, "task_done") {
		t.Errorf("log of a %q", got)
	}
}

// A failed resolution is followed by the one rerun, never a second
// resolver: the resolver gives up, leaves markers, or its resolution fails
// verify on the integrated tree (SPEC §6.11).
func TestResolverFailsThenReruns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolver func(h *harness, r *repo) func(id string) error
		verify   bool // the integrated tree fails verify after the resolution
		failed   string
	}{
		{"gave up", func(h *harness, _ *repo) func(string) error {
			return func(id string) error {
				return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionSkip, Note: "the intents contradict"})
			}
		}, false, "resolver_failed:gave up: the intents contradict; then rerun"},
		{"markers left", func(h *harness, r *repo) func(string) error {
			return func(id string) error {
				r.markersLeft(taskWT(h.runBase(), id), "main.go")
				return h.signal(id)
			}
		}, false, "resolver_failed:markers left in main.go; then rerun"},
		{"verify fails", nil, true, "resolver_failed:verify failed on the integrated tree (profile default: exit status 1); then rerun"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newParallel(t, twoThenOne(), resolveTOML)
			r := h.repo()
			if tc.resolver != nil {
				h.resolverSignals(tc.resolver(h, r))
			} else {
				h.resolverSignals(nil)
			}
			var mu sync.Mutex
			resolved := false
			h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
				mu.Lock()
				defer mu.Unlock()
				if tc.verify && strings.HasSuffix(c.Dir, "-a") && resolved {
					resolved = false
					return runner.Result{ExitCode: 1, Stdout: []byte("--- FAIL: TestShared\n")}, nil
				}
				return runner.Result{}, nil
			}
			h.on(func(ev Event) {
				switch ev.Kind {
				case RunStarted:
					r.conflictOn(taskWT(ev.Run, "a"), 1, "main.go")
				case ResolverDone:
					mu.Lock()
					resolved = true
					mu.Unlock()
				}
			})
			res, err := h.run()
			if err != nil || res.Outcome != Completed {
				t.Fatalf("Run = %s, %v", res.Outcome, err)
			}
			if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
				t.Errorf("statuses: %s", got)
			}
			got := h.filtered("a")
			for _, want := range []string{"integration_failed/conflict:main.go; then resolve", "resolver_opened/conflict:main.go", tc.failed, "task_retried:fresh", "integrated:a: One", "task_done:did a"} {
				if !has(got, want) {
					t.Errorf("log of a lacks %q: %q", want, got)
				}
			}
			if n := strings.Count(strings.Join(got, "\n"), "resolver_opened"); n != 1 {
				t.Errorf("%d resolver sessions, want 1: %q", n, got)
			}
			if done := h.logEvent(state.EventTaskDone, "a"); done.Attempt != 2 {
				t.Errorf("task_done attempt %d, want the rerun's, 2", done.Attempt)
			}
			if n := h.count(NeedsYou); n != 0 {
				t.Errorf("%d needs-you events; the rerun integrates", n)
			}
			wt := taskWT(h.runBase(), "a")
			if tc.verify {
				return // the rebase was continued before verify failed
			}
			if got := r.calls(wt + ": rebase --abort"); len(got) != 1 {
				t.Errorf("the stopped rebase aborted %d times, want once after the failed resolution", len(got))
			}
		})
	}
}

// After the resolver and the rerun, the next failure is Needs you without
// Resolve: resolve → rerun → ask, never a loop (SPEC §6.11).
func TestResolverThenRerunThenNeedsYou(t *testing.T) {
	h := newParallel(t, twoThenOne(), resolveTOML)
	r := h.repo()
	h.commands()
	h.resolverSignals(func(id string) error {
		return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionSkip, Note: "can't"})
	})
	var asked Event
	h.on(func(ev Event) {
		switch {
		case ev.Kind == RunStarted:
			r.conflictOn(taskWT(ev.Run, "a"), 2, "main.go")
		case ev.Kind == Asked && ev.Question == QuestionIntegration:
			asked = ev
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() {
				h.eng.Send(Command{Kind: CmdResolve, Task: "a"}) // refused: the resolver ran
				h.eng.Send(Command{Kind: CmdSkip, Task: "a", Text: "by hand later"})
			})
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if asked.Resolve || strings.Contains(asked.Detail, "resolve it") {
		t.Errorf("question %+v offers Resolve after the resolver ran", asked)
	}
	want := []string{
		"integration_failed/conflict:main.go; then resolve",
		"resolver_opened/conflict:main.go",
		"resolver_failed:gave up: can't; then rerun",
		"task_retried:fresh",
		"integration_failed/conflict:main.go; then needs you",
		"needs_you/integration:needs you (integration failed)",
		"needs_you_clear/integration",
		"task_skipped:by hand later",
	}
	if got := h.filtered("a"); !slices.Equal(got, want) {
		t.Errorf("log of a %q, want %q", got, want)
	}
	if !h.warned("ignored resolve: a resolver session already ran for a in this run") {
		t.Errorf("Resolve was not refused: %s", h.kinds())
	}
}

// With on_conflict = "ask" the question offers Resolve; picking it does
// the rebase again, leaves it where it stops and opens the resolver for
// that one task (SPEC §6.11).
func TestResolverFromTheQuestion(t *testing.T) {
	h := newParallel(t, twoThenOne(), integrationTOML+"on_conflict = \"ask\"\n")
	r := h.repo()
	h.commands()
	h.resolverSignals(nil)
	var asked []Event
	h.on(func(ev Event) {
		switch {
		case ev.Kind == RunStarted:
			r.conflictOn(taskWT(ev.Run, "a"), 2, "main.go")
		case ev.Kind == Asked && ev.Question == QuestionIntegration:
			asked = append(asked, ev)
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { h.eng.Send(Command{Kind: CmdResolve, Task: "a"}) })
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if len(asked) != 1 || !asked[0].Resolve {
		t.Fatalf("questions %+v, want one with Resolve", asked)
	}
	// The rebase done again stops and is logged as such, then resolved.
	want := []string{
		"integration_failed/conflict:main.go; then needs you",
		"needs_you/integration:needs you (integration failed)",
		"needs_you_clear/integration",
		"integration_failed/conflict:main.go; then resolve",
		"resolver_opened/conflict:main.go",
		"resolver_done:kept both",
		"integrated:a: One",
		"task_done:did a",
	}
	if got := h.filtered("a"); !slices.Equal(got, want) {
		t.Errorf("log of a %q, want %q", got, want)
	}
	wt := taskWT(h.runBase(), "a")
	// The first rebase was aborted (ask); the second stopped for the
	// resolver and was continued.
	if got := r.calls(wt + ": rebase --abort"); len(got) != 1 {
		t.Errorf("rebase --abort %d times, want 1", len(got))
	}
	if got := r.calls(wt + ": -c core.editor=true rebase --continue"); len(got) != 1 {
		t.Errorf("rebase --continue %d times, want 1", len(got))
	}
}

// Verify failing on the integrated tree opens the resolver on that tree;
// its fix is folded into the task's commit, verified again and
// fast-forwarded (SPEC §6.11).
func TestResolverFixesIntegratedTree(t *testing.T) {
	h := newParallel(t, twoThenOne(), resolveTOML)
	r := h.repo()
	h.resolverSignals(nil)
	var mu sync.Mutex
	n := 0
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		if !strings.HasSuffix(c.Dir, "-a") {
			return runner.Result{}, nil
		}
		mu.Lock()
		defer mu.Unlock()
		n++
		if n == 2 { // the integrated tree, before the resolver
			return runner.Result{ExitCode: 1, Stdout: []byte("--- FAIL: TestShared\nFAIL\n")}, nil
		}
		return runner.Result{}, nil
	}
	h.signalAt(time.Minute, "b")
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		switch {
		case h.isResolver(id):
			return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionDone, Note: "fixed the test"})
		case id == "a" || id == "b":
			return nil // b integrates first: a's rebase moves it
		}
		return h.signal(id)
	})
	h.clock.At(2*time.Minute, func() {
		if err := h.signal("a"); err != nil {
			t.Error(err)
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	want := []string{
		"integration_failed/verify:profile default: exit status 1; then resolve",
		"resolver_opened/verify:default",
		"resolver_done:fixed the test",
		"integrated:a: One",
		"task_done:did a",
	}
	if got := h.filtered("a"); !slices.Equal(got, want) {
		t.Errorf("log of a %q, want %q", got, want)
	}
	wt := taskWT(h.runBase(), "a")
	if got := r.calls(wt + ": commit --amend --no-edit"); len(got) != 1 {
		t.Errorf("the fix was folded %d times, want once", len(got))
	}
	p := h.be.Prompts("a")
	if last := p[len(p)-1]; !strings.Contains(last, "verify profile `default` (`make check`) failed on the integrated tree") || !strings.Contains(last, "--- FAIL: TestShared") {
		t.Errorf("the resolver's prompt lacks the verify failure:\n%s", last)
	}
	if got := h.event(ResolverOpened, "a").Detail; got != "fixing the integrated tree" {
		t.Errorf("resolver_opened card %q", got)
	}
}

// Stop while the resolver works leaves its session open and the worktree
// mid-rebase, recorded in state.json; resume reattaches it and the
// integration goes on from there (SPEC §6.11, §13).
func TestResolverStopAndResume(t *testing.T) {
	h := newParallel(t, twoThenOne(), resolveTOML)
	r := h.repo()
	h.commands()
	h.resolverSignals(func(string) error { return nil }) // the resolver works on
	h.on(func(ev Event) {
		switch ev.Kind {
		case RunStarted:
			r.conflictOn(taskWT(ev.Run, "a"), 1, "main.go")
		case ResolverOpened:
			h.clock.At(ev.At.Sub(t0)+time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	wt := taskWT(base, "a")
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	cur := run.ActiveTask("a")
	if cur == nil || cur.Resolver == nil || cur.Resolver.Reason != state.FailedConflict {
		t.Fatalf("a's active entry after the stop %+v, want its resolver recorded", cur)
	}
	if opened := h.logEvent(state.EventResolverOpened, "a"); cur.ClaudeSession != opened.Session {
		t.Errorf("the entry's session %q, want the resolver's %q", cur.ClaudeSession, opened.Session)
	}
	if !r.rebasing(wt) || len(r.calls(wt+": rebase --abort")) != 0 {
		t.Error("the stop did not leave the worktree mid-rebase")
	}
	if _, err := os.Stat(h.path(wt)); err != nil {
		t.Errorf("a's worktree after the stop: %v", err)
	}

	h.events = nil
	opened := len(h.be.Opened())
	h.clock.At(2*time.Minute, func() {
		if got := len(h.be.Opened()); got != opened {
			t.Errorf("%d sessions opened on resume, want the resolver reattached", got-opened)
		}
		if err := h.dir.WriteSignal(state.Signal{ID: "a", Action: state.ActionDone, Note: "kept both"}); err != nil {
			t.Error(err)
		}
	})
	res, err = h.run(func(o *Options) { o.Phase = "" })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	got := h.filtered("a")
	// The resumed run lost the task's note and message: the resolver's
	// note and git's subject stand in.
	for _, want := range []string{"resolver_done:kept both", "integrated:", "task_done:kept both"} {
		if !has(got, want) {
			t.Errorf("log of a lacks %q: %q", want, got)
		}
	}
	if n := strings.Count(strings.Join(got, "\n"), "resolver_opened"); n != 1 {
		t.Errorf("%d resolver_opened lines, want 1 across the stop: %q", n, got)
	}
	if got := r.calls(wt + ": -c core.editor=true rebase --continue"); len(got) != 1 {
		t.Errorf("rebase --continue %d times, want 1", len(got))
	}
	if run, err := h.dir.LoadRun(); err == nil && run.ActiveTask("a") != nil {
		t.Errorf("a is still active: %+v", run.ActiveTask("a"))
	}
}

// The owner skipping the task while the resolver works closes it, logs a
// failed resolution and skips the task (SPEC §6.11).
func TestResolverOwnerSkips(t *testing.T) {
	h := newParallel(t, twoThenOne(), resolveTOML)
	r := h.repo()
	h.commands()
	h.resolverSignals(func(string) error { return nil })
	h.on(func(ev Event) {
		switch ev.Kind {
		case RunStarted:
			r.conflictOn(taskWT(ev.Run, "a"), 1, "main.go")
		case ResolverOpened:
			h.clock.At(ev.At.Sub(t0)+time.Minute, func() { h.eng.Send(Command{Kind: CmdSkip, Task: "a", Text: "drop it"}) })
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v\n%s\n%q\n%s", res.Outcome, err, h.kinds(), h.logOf("a"), h.statuses())
	}
	if got := h.statuses(); got != "a=skipped b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	got := h.filtered("a")
	for _, want := range []string{"resolver_failed:the owner skipped the task; then skipped", "task_skipped:drop it"} {
		if !has(got, want) {
			t.Errorf("log of a lacks %q: %q", want, got)
		}
	}
	if closed, opened := len(h.be.Closed()), len(h.be.Opened()); closed != opened {
		t.Errorf("%d of %d sessions closed; the resolver's is still open", closed, opened)
	}
}
