package engine

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// integrationTOML verifies with make check and commits by itself.
const integrationTOML = "needs_input_after = \"1h\"\n[run]\ncommit = \"auto\"\nverify = \"make check\"\n[parallel]\nmax = 2\n"

// twoThenOne is a parallel phase whose c waits on a and b.
func twoThenOne() string {
	return parallelPlan(
		"| a | **One** | — | ready | sonnet | agent | — |",
		"| b | **Two** | — | ready | sonnet | agent | — |",
		"| c | **Three** | a, b | blocked | sonnet | agent | — |",
	)
}

// logOf returns the run log's lines for task, as "type[:detail]" with the
// reason, the branch and the attempt where set, in order.
func (h *harness) logOf(task string) []string {
	h.t.Helper()
	events, err := h.dir.Events()
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		if e.Task != task {
			continue
		}
		s := string(e.Type)
		if e.Reason != "" {
			s += "/" + e.Reason
		}
		if e.Detail != "" {
			s += ":" + e.Detail
		}
		out = append(out, s)
	}
	return out
}

// logEvent returns the first run log line of typ for task.
func (h *harness) logEvent(typ state.EventType, task string) state.Event {
	h.t.Helper()
	events, err := h.dir.Events()
	if err != nil {
		h.t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == typ && e.Task == task {
			return e
		}
	}
	h.t.Fatalf("no %s line for %s in %v", typ, task, h.logged())
	return state.Event{}
}

// has reports whether lines has one that starts with prefix.
func has(lines []string, prefix string) bool {
	return slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) }) >= 0
}

// verifiesIn counts the verify commands that ran in dir.
func verifiesIn(cmds func() []ran, dir string) int {
	n := 0
	for _, c := range cmds() {
		if c.dir == dir && c.name == "make check" {
			n++
		}
	}
	return n
}

// A verified task of a parallel phase is committed in its worktree,
// rebased onto the run branch, verified there again when the rebase moved
// it, and fast-forwarded, in completion order; it is marked done only
// then, and the run log says so (SPEC §6.11, §13).
func TestIntegrationInCompletionOrder(t *testing.T) {
	h := newParallel(t, twoThenOne(), integrationTOML)
	r := h.repo()
	cmds := h.commands()
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "c" {
			return h.signal(id)
		}
		return nil
	})
	var mu sync.Mutex
	var atFF []string // the plan's statuses when each fast-forward ran
	git := h.gitFn
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		if c.Args[0] == "worktree" && c.Args[1] == "add" {
			// Each task changes a file of its own: no overlap here.
			r.mu.Lock()
			r.changes[c.Args[4]] = []string{c.Args[3] + ".go"}
			r.mu.Unlock()
		}
		if c.Args[0] == "merge" {
			s := h.statuses()
			mu.Lock()
			atFF = append(atFF, c.Args[2]+" "+s)
			mu.Unlock()
		}
		return git(c)
	}
	h.signalAt(time.Minute, "b")
	h.signalAt(2*time.Minute, "a")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	// b first: nothing moved, no second verify. Then a, rebased onto b's
	// commit and verified there. Then c, started from both.
	want := []string{".: merge --ff-only igris/" + base + "-b", ".: merge --ff-only igris/" + base + "-a", ".: merge --ff-only igris/" + base + "-c"}
	if got := r.calls("merge --ff-only"); !slices.Equal(got, want) {
		t.Errorf("fast-forwards %q, want %q", got, want)
	}
	for id, n := range map[string]int{"a": 2, "b": 1, "c": 1} {
		if got := verifiesIn(cmds, taskWT(base, id)); got != n {
			t.Errorf("%s verified %d times, want %d", id, got, n)
		}
		if got := r.calls(taskWT(base, id) + ": rebase "); len(got) != 1 {
			t.Errorf("%s rebased by %q, want once in its worktree", id, got)
		}
	}
	wantB := []string{"task_started:mode auto", "verify_passed:profile default", "committed:b: Two", "integrated:b: Two", "task_done:did b"}
	if got := h.logOf("b"); !slices.Equal(got, wantB) {
		t.Errorf("log of b %q, want %q", got, wantB)
	}
	wantA := []string{"task_started:mode auto", "verify_passed:profile default", "committed:a: One", "verify_passed:profile default: integrated tree", "integrated:a: One", "task_done:did a"}
	if got := h.logOf("a"); !slices.Equal(got, wantA) {
		t.Errorf("log of a %q, want %q", got, wantA)
	}
	for _, id := range []string{"a", "b"} {
		started := h.logEvent(state.EventTaskStarted, id)
		if want := "igris/" + base + "-" + id; started.Branch != want {
			t.Errorf("%s task_started branch %q, want %s", id, started.Branch, want)
		}
	}
	for i, id := range []string{"b", "a", "c"} {
		in := h.logEvent(state.EventIntegrated, id)
		if want := fmt.Sprintf("%040d", 1000001+i); in.Commit != want || in.Branch != "main" || in.Attempt != 1 {
			t.Errorf("%s integrated line %+v, want commit %s on main, attempt 1", id, in, want)
		}
	}
	if got := h.event(Integrated, "a").Detail; got != "onto main: a: One" {
		t.Errorf("integrated event detail %q", got)
	}
	// Marked done only after the fast-forward.
	wantFF := []string{
		"igris/" + base + "-b a=in progress b=in progress c=blocked B-1=blocked",
		"igris/" + base + "-a a=in progress b=done c=blocked B-1=blocked",
		"igris/" + base + "-c a=done b=done c=in progress B-1=blocked",
	}
	if !slices.Equal(atFF, wantFF) {
		t.Errorf("statuses at the fast-forwards %q, want %q", atFF, wantFF)
	}
	if h.warned("is kept") {
		t.Errorf("a warning about a kept worktree or branch: %s", h.kinds())
	}
	if len(r.branches) != 0 || len(r.wts) != 0 {
		t.Errorf("left behind: branches %v, worktrees %v", r.branches, r.wts)
	}
}

// A task with nothing to commit and no commit of its own is marked done
// without a rebase (SPEC §6.11).
func TestIntegrationNothingToCommit(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.count(NotCommitted); got != 3 {
		t.Errorf("%d not_committed events, want 3", got)
	}
	for _, c := range h.calls("git") {
		if c.Args[0] == "rebase" || c.Args[0] == "merge" || c.Args[0] == "commit" {
			t.Errorf("git %v ran with nothing to commit", c.Args)
		}
	}
	if strings.Contains(h.logged(), "integrated") {
		t.Errorf("an integrated line with nothing to integrate: %s", h.logged())
	}
}

// A rebase conflict discards the task's worktree and branch and runs the
// task again from the start, once: a fresh session in a new worktree from
// the run branch's head, a new attempt (SPEC §6.11).
func TestIntegrationConflictRerunsOnce(t *testing.T) {
	h := newParallel(t, twoThenOne(), integrationTOML)
	r := h.repo()
	cmds := h.commands()
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	h.on(func(ev Event) {
		if ev.Kind == RunStarted {
			r.conflictOn(taskWT(ev.Run, "a"), 1, "main.go", "util.go")
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	wt, branch := taskWT(base, "a"), "igris/"+base+"-a"
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	log := h.logOf("a")
	if !has(log, "integration_failed/conflict:main.go, util.go; then rerun") {
		t.Errorf("log of a lacks the failed integration: %q", log)
	}
	retried := h.logEvent(state.EventTaskRetried, "a")
	if retried.Detail != "fresh" || retried.Attempt != 2 || retried.Branch != branch || retried.Slot == nil {
		t.Errorf("task_retried %+v, want fresh, attempt 2, branch %s with a slot", retried, branch)
	}
	if n := strings.Count(strings.Join(log, " "), "integration_failed"); n != 1 {
		t.Errorf("%d failed integrations, want 1: %q", n, log)
	}
	if !has(log, "integrated:a: One") || !has(log, "task_done") {
		t.Errorf("a was not integrated after the rerun: %q", log)
	}
	for _, c := range []struct {
		sub  string
		want int
	}{
		{".: worktree add -b " + branch + " " + wt + " HEAD", 2},
		{".: worktree remove --force " + wt, 1},
		{".: branch -D " + branch, 1},
		{wt + ": rebase --abort", 1},
		{wt + ": diff --name-only --diff-filter=U", 1},
		{".: merge --ff-only " + branch, 1},
	} {
		if got := r.calls(c.sub); len(got) != c.want {
			t.Errorf("calls %q: %d, want %d", c.sub, len(got), c.want)
		}
	}
	var sessions []string
	for _, spec := range h.be.Opened() {
		if spec.TaskID == "a" {
			sessions = append(sessions, spec.ClaudeSession)
		}
	}
	if len(sessions) != 2 || sessions[0] == sessions[1] {
		t.Errorf("sessions of a: %q, want two", sessions)
	}
	if p := h.be.Prompts("a"); len(p) != 2 || strings.Contains(p[1], "Resumed task") || !strings.Contains(p[1], "exactly one task") {
		t.Errorf("the rerun's prompt should be a fresh task prompt: %q", p)
	}
	if got := h.event(IntegrationFailed, "a").Detail; got != "didn't integrate: the rebase onto the run branch conflicts in main.go, util.go; igris runs the task again from the start" {
		t.Errorf("integration_failed event detail %q", got)
	}
	if got := h.event(Retrying, "a").Detail; got != "fresh" {
		t.Errorf("retrying detail %q", got)
	}
	// Both attempts ran before_task-less verify in the worktree; the hooks
	// of the first attempt's done are not repeated.
	if got := verifiesIn(cmds, wt); got < 2 {
		t.Errorf("a verified %d times, want one per attempt at least", got)
	}
	if got := h.count(NeedsYou); got != 0 {
		t.Errorf("%d needs-you events; the first failure reruns by itself", got)
	}
}

// A second failure, or the first with on_conflict = "ask", is Needs you
// with the conflicting files and the worktree kept; the owner reruns it
// fresh, marks it done or skips it (SPEC §6.11).
func TestIntegrationFailureNeedsYou(t *testing.T) {
	for _, tc := range []struct {
		name     string
		toml     string
		times    int // conflicts in a row
		cmd      Command
		statuses string
		log      []string
	}{
		{"rerun", integrationTOML, 2, Command{Kind: CmdRetry, Task: "a"}, "a=done b=done c=done B-1=ready",
			[]string{"integration_failed/conflict:main.go; then rerun", "task_retried:fresh", "integration_failed/conflict:main.go; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration", "task_retried:fresh", "integrated:a: One", "task_done:did a"}},
		{"ask", integrationTOML + "on_conflict = \"ask\"\n", 1, Command{Kind: CmdRetry, Task: "a"}, "a=done b=done c=done B-1=ready",
			[]string{"integration_failed/conflict:main.go; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration", "task_retried:fresh", "integrated:a: One", "task_done:did a"}},
		{"done", integrationTOML, 2, Command{Kind: CmdDone, Task: "a", Text: "merged by hand"}, "a=done b=done c=done B-1=ready",
			[]string{"integration_failed/conflict:main.go; then rerun", "task_retried:fresh", "integration_failed/conflict:main.go; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration", "task_done:merged by hand"}},
		{"skip", integrationTOML, 2, Command{Kind: CmdSkip, Task: "a", Text: "not worth it"}, "a=skipped b=done c=done B-1=ready",
			[]string{"integration_failed/conflict:main.go; then rerun", "task_retried:fresh", "integration_failed/conflict:main.go; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration", "task_skipped:not worth it"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newParallel(t, twoThenOne(), tc.toml)
			r := h.repo()
			h.commands()
			h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
			var asked Event
			h.on(func(ev Event) {
				switch {
				case ev.Kind == RunStarted:
					r.conflictOn(taskWT(ev.Run, "a"), tc.times, "main.go")
				case ev.Kind == Asked && ev.Question == QuestionIntegration:
					asked = ev
					if _, err := os.Stat(h.path(taskWT(h.runBase(), "a"))); err != nil {
						t.Errorf("a's worktree at the question: %v, want kept", err)
					}
					at := ev.At.Sub(t0) + 5*time.Second
					h.clock.At(at, func() { h.eng.Send(tc.cmd) })
				}
			})
			res, err := h.run()
			if err != nil || res.Outcome != Completed {
				t.Fatalf("Run = %s, %v", res.Outcome, err)
			}
			if got := h.statuses(); got != tc.statuses {
				t.Errorf("statuses: %s", got)
			}
			if asked.Task != "a" || !slices.Equal(asked.Output, []string{"main.go"}) || !strings.Contains(asked.Detail, "a didn't integrate: the rebase onto the run branch conflicts in main.go: rerun it fresh, mark it done (you integrated it by hand), skip it, or stop") {
				t.Errorf("question %+v", asked)
			}
			var got []string
			for _, l := range h.logOf("a") {
				if strings.HasPrefix(l, "integration_failed") || strings.HasPrefix(l, "task_retried") || strings.HasPrefix(l, "needs_you") || strings.HasPrefix(l, "integrated") || strings.HasPrefix(l, "task_done") || strings.HasPrefix(l, "task_skipped") {
					got = append(got, l)
				}
			}
			if !slices.Equal(got, tc.log) {
				t.Errorf("log of a %q, want %q", got, tc.log)
			}
			base := h.runBase()
			branch := "igris/" + base + "-a"
			if tc.cmd.Kind != CmdRetry {
				// Marked done (integrated by hand, says the owner) or
				// skipped: the branch with its commit is kept and named.
				if !h.warned("the branch " + branch + " of a has commits the run branch doesn't and is kept") {
					t.Errorf("no warning that a's branch is kept: %s", h.kinds())
				}
			} else if _, ok := r.branches[branch]; ok {
				t.Errorf("a's branch is still there")
			}
			if n := len(h.toasts()); n < 1 || !strings.Contains(h.toasts()[0], "needs you (integration failed)") {
				t.Errorf("toasts %q, want needs you (integration failed) first", h.toasts())
			}
		})
	}
}

// Verify failing on the integrated tree is a failed integration too: the
// output's last lines go to the owner, never into the session, and a rerun
// fresh is offered (SPEC §6.11).
func TestIntegrationVerifyFailsOnIntegratedTree(t *testing.T) {
	h := newParallel(t, twoThenOne(), integrationTOML+"on_conflict = \"ask\"\n")
	h.repo()
	var mu sync.Mutex
	n := map[string]int{}
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		id := c.Dir[len(c.Dir)-1:]
		mu.Lock()
		defer mu.Unlock()
		n[id]++
		if id == "a" && n[id] == 2 {
			return runner.Result{ExitCode: 1, Stdout: []byte("--- FAIL: TestShared\nFAIL\n")}, nil
		}
		return runner.Result{}, nil
	}
	sessions := 0
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "a" {
			sessions++
		}
		if id == "c" || (id == "a" && sessions == 2) {
			return h.signal(id) // c, and the rerun's session of a
		}
		return nil
	})
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionIntegration {
			if want := []string{"--- FAIL: TestShared", "FAIL"}; !slices.Equal(ev.Output, want) {
				t.Errorf("question output %q, want %q", ev.Output, want)
			}
			at := ev.At.Sub(t0) + 5*time.Second
			h.clock.At(at, func() { h.eng.Send(Command{Kind: CmdRetry, Task: "a"}) })
		}
	})
	h.signalAt(time.Minute, "b")
	h.signalAt(2*time.Minute, "a")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	log := h.logOf("a")
	for _, want := range []string{
		"verify_failed:profile default: integrated tree: exit status 1",
		"integration_failed/verify:profile default: exit status 1; then needs you",
		"needs_you/integration:needs you (integration failed)",
		"task_retried:fresh",
		"integrated:a: One",
	} {
		if !has(log, want) {
			t.Errorf("log of a lacks %q: %q", want, log)
		}
	}
	if p := h.be.Prompts("a"); len(p) != 2 || strings.Contains(p[0]+p[1], "FAIL") {
		t.Errorf("prompts of a %q: the integrated tree's failure goes to the owner, not the session", p)
	}
	if got := h.event(IntegrationFailed, "a").Detail; got != "didn't integrate: verify failed on the integrated tree (profile default: exit status 1); its worktree is kept" {
		t.Errorf("integration_failed event detail %q", got)
	}
	// The rerun's worktree was made from the run branch's head, so its
	// rebase moves nothing and the integrated tree is not verified again.
	if got := n["a"]; got != 3 {
		t.Errorf("a verified %d times, want 3: done, integrated tree, the rerun's done", got)
	}
}

// git's refusal of a fast-forward with files in the way, as it prints it.
const (
	untrackedInTheWay = "error: The following untracked working tree files would be overwritten by merge:\n\tnew.txt\n\tdocs/new.md\nPlease move or remove them before you merge.\nAborting\n"
	changedInTheWay   = "error: Your local changes to the following files would be overwritten by merge:\n\tmain.go\nPlease commit your changes or stash them before you merge.\nAborting\n"
)

// A fast-forward that fails says why (SPEC §6.11). Files where the run
// branch is checked out that the branch would overwrite are named, and no
// rerun is tried, whatever on_conflict says: the owner moves them and
// tries again, which integrates the committed branch again with no new
// session. A run branch that moved since the rebase reruns the task, as
// before; anything else keeps git's first line.
func TestFastForwardFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refuse []string // git's errors for a's fast-forwards
		move   bool     // the run branch moves between a's rebase and its fast-forward
		ask    bool     // Needs you with QuestionFilesInTheWay, answered with Try again
		files  []string // the question's output
		log    []string
		reruns int // fresh sessions of a after the first
	}{
		{name: "untracked file", refuse: []string{untrackedInTheWay}, ask: true, files: []string{"new.txt", "docs/new.md"},
			log: []string{"integration_failed/conflict:the fast-forward would overwrite uncommitted files in the project root: new.txt, docs/new.md; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration", "integrated:a: One", "task_done:did a"},
		},
		{name: "changed tracked file", refuse: []string{changedInTheWay}, ask: true, files: []string{"main.go"},
			log: []string{"integration_failed/conflict:the fast-forward would overwrite uncommitted files in the project root: main.go; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration", "integrated:a: One", "task_done:did a"},
		},
		{name: "still in the way", refuse: []string{untrackedInTheWay, untrackedInTheWay}, ask: true, files: []string{"new.txt", "docs/new.md"},
			log: []string{"integration_failed/conflict:the fast-forward would overwrite uncommitted files in the project root: new.txt, docs/new.md; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration",
				"integration_failed/conflict:the fast-forward would overwrite uncommitted files in the project root: new.txt, docs/new.md; then needs you", "needs_you/integration:needs you (integration failed)", "needs_you_clear/integration", "integrated:a: One", "task_done:did a"},
		},
		{name: "run branch moved", move: true,
			log:    []string{"integration_failed/conflict:the run branch moved since the rebase; fast-forward failed; then rerun", "task_retried:fresh", "integrated:a: One", "task_done:did a"},
			reruns: 1},
		{name: "something else", refuse: []string{"fatal: cannot lock ref 'refs/heads/main'\nhint: try later\n"},
			log:    []string{"integration_failed/conflict:fast-forward failed: fatal: cannot lock ref 'refs/heads/main'; then rerun", "task_retried:fresh", "integrated:a: One", "task_done:did a"},
			reruns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newParallel(t, twoThenOne(), integrationTOML)
			r := h.repo()
			cmds := h.commands()
			// a first and alone: nothing else moves the run branch while
			// it waits for the owner; b is done long after.
			sessions := 0
			h.be.SetAutoSignal(func(_ context.Context, id string) error {
				if id == "a" {
					sessions++
				}
				if id == "c" || (id == "a" && sessions == 2) {
					return h.signal(id)
				}
				return nil
			})
			h.signalAt(time.Minute, "a")
			h.signalAt(time.Hour, "b")
			var asks []Event
			h.on(func(ev Event) {
				switch {
				case ev.Kind == RunStarted:
					r.refuseFF("igris/"+ev.Run+"-a", tc.refuse...)
				case ev.Kind == Asked && ev.Question == QuestionIntegration:
					t.Errorf("a rerun offered: %+v", ev)
				case ev.Kind == Asked && ev.Question == QuestionFilesInTheWay:
					asks = append(asks, ev)
					at := ev.At.Sub(t0) + 5*time.Second
					h.clock.At(at, func() { h.eng.Send(Command{Kind: CmdRetry, Task: "a"}) })
				}
			})
			if tc.move {
				git := h.gitFn
				moved := false
				h.gitFn = func(c runner.Cmd) (runner.Result, error) {
					if c.Args[0] == "merge" && strings.HasSuffix(c.Args[2], "-a") && !moved {
						moved = true
						r.mu.Lock()
						r.runHead++ // a commit where the run branch is checked out
						r.mu.Unlock()
					}
					return git(c)
				}
			}
			res, err := h.run()
			if err != nil || res.Outcome != Completed {
				t.Fatalf("Run = %s, %v", res.Outcome, err)
			}
			if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
				t.Errorf("statuses: %s", got)
			}
			var got []string
			for _, l := range h.logOf("a") {
				for _, p := range []string{"integration_failed", "task_retried", "needs_you", "integrated", "task_done"} {
					if strings.HasPrefix(l, p) {
						got = append(got, l)
					}
				}
			}
			if !slices.Equal(got, tc.log) {
				t.Errorf("log of a %q, want %q", got, tc.log)
			}
			if want := len(tc.refuse); !tc.ask {
				if len(asks) != 0 {
					t.Errorf("asked %d times, want none", len(asks))
				}
			} else if len(asks) != want {
				t.Errorf("asked %d times, want %d", len(asks), want)
			}
			for _, ask := range asks {
				want := "a didn't integrate: the fast-forward would overwrite uncommitted files in the project root: " + strings.Join(tc.files, ", ") + ": commit, move or remove them, then try again"
				if !slices.Equal(ask.Output, tc.files) || !strings.HasPrefix(ask.Detail, want) || strings.Contains(ask.Detail, "rerun") {
					t.Errorf("question %+v, want output %q and %q", ask, tc.files, want)
				}
			}
			if sessions != 1+tc.reruns {
				t.Errorf("%d sessions of a, want %d", sessions, 1+tc.reruns)
			}
			// Each session's done is verified; Try again rebased onto a run
			// branch that didn't move, so only its fast-forward ran.
			if got := verifiesIn(cmds, taskWT(h.runBase(), "a")); got != 1+tc.reruns {
				t.Errorf("a verified %d times, want %d", got, 1+tc.reruns)
			}
			if got, want := len(r.calls("merge --ff-only igris/"+h.runBase()+"-a")), len(tc.refuse)+1; !tc.move && got != want {
				t.Errorf("%d fast-forwards of a, want %d", got, want)
			}
			if tc.ask {
				want := "didn't integrate: the fast-forward would overwrite uncommitted files in the project root: " + strings.Join(tc.files, ", ") + "; its branch is kept"
				if got := h.event(IntegrationFailed, "a").Detail; got != want {
					t.Errorf("integration_failed event detail %q, want %q", got, want)
				}
			}
		})
	}
}

// The files in the way are git's tab-indented lines, whatever language
// the lines around them are in, cleaned for the terminal and bounded.
func TestInTheWay(t *testing.T) {
	many := "error: x\n"
	for i := range maxConflictFiles + 3 {
		many += fmt.Sprintf("\tf%d\n", i)
	}
	for _, tc := range []struct {
		name, stderr string
		want         []string
	}{
		{"untracked", untrackedInTheWay, []string{"new.txt", "docs/new.md"}},
		{"german", "Fehler: Ihre lokalen Änderungen in den folgenden Dateien würden durch den Merge\nüberschrieben werden:\n\tt.txt\nAbbruch\n", []string{"t.txt"}},
		{"escapes", "error: x\n\tevil\x1b[31m.go\n", []string{"evil.go"}},
		{"none", "fatal: not possible to fast-forward, aborting.\n", nil},
		{"many", many, append(func() []string {
			var fs []string
			for i := range maxConflictFiles {
				fs = append(fs, fmt.Sprintf("f%d", i))
			}
			return fs
		}(), "and 3 more")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inTheWay(tc.stderr); !slices.Equal(got, tc.want) {
				t.Errorf("inTheWay = %q, want %q", got, tc.want)
			}
		})
	}
}

// The commit question of a parallel phase holds no other task: each task
// waits for its own answer, and the rest go on; a declined commit is Needs
// you until the owner answers again (SPEC §5.1, §6.11).
func TestIntegrationCommitAskDoesNotHold(t *testing.T) {
	h := newParallel(t, twoThenOne(), "needs_input_after = \"1h\"\n[run]\ncommit = \"ask\"\nverify = \"make check\"\n[parallel]\nmax = 2\n")
	r := h.repo()
	h.commands()
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "c" {
			return h.signal(id)
		}
		return nil
	})
	answer := func(at time.Duration, task string, yes bool) {
		h.clock.At(at, func() { h.eng.Send(Command{Kind: CmdAnswer, Task: task, Yes: yes}) })
	}
	h.signalAt(time.Minute, "a")
	h.signalAt(2*time.Minute, "b")
	h.clock.At(150*time.Second, func() {
		asked := 0
		for _, ev := range h.events {
			if ev.Kind == Asked && ev.Question == QuestionCommit {
				asked++
			}
		}
		if asked != 2 || len(r.calls(": commit ")) != 0 {
			t.Errorf("after both are done: %d commit questions and %d commits, want 2 and 0", asked, len(r.calls(": commit ")))
		}
	})
	answer(3*time.Minute, "b", true)
	answer(4*time.Minute, "a", false)
	h.clock.At(270*time.Second, func() {
		if _, err := os.Stat(h.path(taskWT(h.runBase(), "a"))); err != nil {
			t.Errorf("a's worktree after the declined commit: %v, want kept", err)
		}
		if got := h.statuses(); got != "a=in progress b=done c=blocked B-1=blocked" {
			t.Errorf("after declining a's commit: %s", got)
		}
	})
	answer(5*time.Minute, "a", true)
	answer(6*time.Minute, "c", true)
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	base := h.runBase()
	want := []string{".: merge --ff-only igris/" + base + "-b", ".: merge --ff-only igris/" + base + "-a", ".: merge --ff-only igris/" + base + "-c"}
	if got := r.calls("merge --ff-only"); !slices.Equal(got, want) {
		t.Errorf("fast-forwards %q, want %q", got, want)
	}
	if got, want := h.waits(), "a#1 commit, b#1 commit, clear b#1 commit, clear a#1 commit, c#1 commit, clear c#1 commit"; got != want {
		t.Errorf("waits %q, want %q", got, want)
	}
	if got := h.event(NotCommitted, "a").Detail; got != "the owner declined" {
		t.Errorf("not_committed detail %q", got)
	}
	if got := h.event(NeedsYou, "a").Detail; !strings.Contains(got, "the commit of a was declined; its worktree is kept") {
		t.Errorf("needs-you detail %q", got)
	}
	asked := 0
	for _, ev := range h.events {
		if ev.Kind == Asked && ev.Question == QuestionCommit && ev.Task == "a" {
			asked++
		}
	}
	if asked != 2 {
		t.Errorf("a's commit question asked %d times, want twice: once, and again after the decline", asked)
	}
	if got := h.event(Asked, "b").Detail; got != `commit the changes of b as "b: Two"?` {
		t.Errorf("b's question %q", got)
	}
}

// A run stopped after a task's commit, before its integration, integrates
// the commit when it resumes instead of committing again (SPEC §6.11).
func TestIntegrationResumesCommitted(t *testing.T) {
	h := newParallel(t, twoThenOne(), integrationTOML)
	r := h.repo()
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	var mu sync.Mutex
	seen := map[string]int{}
	stopped := ""
	release := make(chan struct{})
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		id := c.Dir[len(c.Dir)-1:]
		mu.Lock()
		seen[id]++
		second := seen[id] == 2 && stopped == ""
		if second {
			stopped = id
		}
		mu.Unlock()
		if second {
			// The verify of the integrated tree runs until the owner stops
			// the run (below, on the engine's goroutine, so no step sees
			// it end first).
			<-release
		}
		return runner.Result{}, nil
	}
	h.clock.At(time.Minute, func() {
		h.eng.Send(Command{Kind: CmdStop})
		close(release)
	})
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	mu.Lock()
	id := stopped
	mu.Unlock()
	if id == "" {
		t.Fatal("no task was verified on an integrated tree")
	}
	base := h.runBase()
	wt := taskWT(base, id)
	if got := r.calls(wt + ": commit "); len(got) != 1 {
		t.Fatalf("%s committed by %q, want once", id, got)
	}
	if run, err := h.dir.LoadRun(); err != nil || run.ActiveTask(id) == nil || run.ActiveTask(id).Worktree == nil {
		t.Fatalf("state after the stop: %+v, %v; want %s active with its worktree", run, err, id)
	}
	if strings.Contains(strings.Join(h.logOf(id), " "), "integrated") {
		t.Fatalf("%s integrated before the stop: %q", id, h.logOf(id))
	}

	h.events = nil
	h.verifyFn = func(runner.Cmd) (runner.Result, error) { return runner.Result{}, nil }
	res, err = h.run(func(o *Options) { o.Phase = "" })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
	if got := r.calls(wt + ": commit "); len(got) != 1 {
		t.Errorf("%s committed by %q across both runs, want once", id, got)
	}
	if got := h.event(NotCommitted, id).Detail; got != "nothing to commit; its branch has a commit to integrate" {
		t.Errorf("not_committed detail %q", got)
	}
	if got := h.event(Integrated, id); got.Task != id {
		t.Errorf("no integrated event for %s: %s", id, h.kinds())
	}
	if got := r.calls(".: merge --ff-only igris/" + base + "-" + id); len(got) != 1 {
		t.Errorf("%s fast-forwarded by %q, want once", id, got)
	}
}

// The overlap watcher warns once per pair of tasks and file both change,
// in the feed and the run log, naming the task that changed it last; it
// holds nothing (SPEC §6.11).
func TestOverlapWarning(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	r := h.repo()
	h.on(func(ev Event) {
		if ev.Kind == RunStarted {
			r.mu.Lock()
			r.changes[taskWT(ev.Run, "a")] = []string{"a.go", "shared.go", "both.go"}
			r.changes[taskWT(ev.Run, "b")] = []string{"shared.go", "b.go", "both.go"}
			r.mu.Unlock()
		}
	})
	h.signalAt(2*time.Minute, "a")
	h.signalAt(3*time.Minute, "b")
	h.signalAt(4*time.Minute, "c")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	var got []string
	for _, ev := range h.events {
		if ev.Kind == Overlap {
			got = append(got, ev.Task+": "+ev.Detail)
		}
	}
	if want := []string{"b: a and b both change both.go", "b: a and b both change shared.go"}; !slices.Equal(got, want) {
		t.Errorf("overlap events %q, want %q", got, want)
	}
	events, err := h.dir.Events()
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	for _, e := range events {
		if e.Type == state.EventOverlap {
			logged = append(logged, fmt.Sprintf("%s#%d %s: %s", e.Task, e.Attempt, e.Rank, e.Detail))
		}
	}
	if want := []string{"b#1 sonnet: a and b both change both.go", "b#1 sonnet: a and b both change shared.go"}; !slices.Equal(logged, want) {
		t.Errorf("overlap lines %q, want %q", logged, want)
	}
	if got := h.event(Overlap, "").At.Sub(t0); got > 10*time.Second {
		t.Errorf("first overlap warned at %v, want at the first polls", got)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
}

func TestPorcelainPaths(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want []string
	}{
		{"", nil},
		{" M main.go\x00", []string{"main.go"}},
		{"?? new.go\x00M  a/b.go\x00 D gone.go\x00", []string{"new.go", "a/b.go", "gone.go"}},
		{"R  new.go\x00old.go\x00 M x.go\x00", []string{"new.go", "x.go"}},
		{"!! ignored\x00 M x.go\x00", []string{"x.go"}},
		{"garbage", nil},
	} {
		if got := porcelainPaths(tc.out); !slices.Equal(got, tc.want) {
			t.Errorf("porcelainPaths(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
}
