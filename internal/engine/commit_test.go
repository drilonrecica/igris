package engine

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
)

// dirtyTree makes git report changes and records the add and commit calls
// as "add -A" / "commit -m …" lines, with the plan status of A-1 at commit
// time.
func (h *harness) dirtyTree() *[]string {
	var log []string
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		if c.Dir != h.root || c.Timeout != gitTimeout {
			h.t.Errorf("git command %s in %q with timeout %s", c, c.Dir, c.Timeout)
		}
		switch c.Args[0] {
		case "status":
			return runner.Result{Stdout: []byte(" M main.go\n")}, nil
		case "commit":
			log = append(log, strings.Join(c.Args, " ")+" ["+strings.SplitN(h.statuses(), " A-2", 2)[0]+"]")
		default:
			log = append(log, strings.Join(c.Args, " "))
		}
		return runner.Result{}, nil
	}
	return &log
}

func TestCommitAuto(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit = \"auto\"\n")
	git := h.dirtyTree()
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		// Committed after verification, before the task is marked done (SPEC §6).
		"add -A", "commit -m A-1: One -m did A-1 [A-1=in progress]",
		"add -A", "commit -m A-2: Two -m did A-2 [A-1=done]",
		"add -A", "commit -m A-3: Three -m did A-3 [A-1=done]",
	}
	if got := strings.Join(*git, "\n"); got != strings.Join(want, "\n") {
		t.Errorf("git calls:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	if got := h.count(Committed); got != 3 {
		t.Errorf("%d committed events", got)
	}
	if got := h.event(Committed, "A-2").Detail; got != "A-2: Two" {
		t.Errorf("committed detail %q", got)
	}
	if got := h.logged(); !strings.HasPrefix(got, "run_started task_started committed task_done") {
		t.Errorf("run log = %s", got)
	}
	if got := h.count(Asked); got != 0 {
		t.Errorf("%d questions under commit = auto", got)
	}
}

func TestNothingToCommit(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit = \"auto\"\n")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, c := range h.calls("git") {
		if c.Args[0] != "status" {
			t.Errorf("ran git %s with nothing to commit", strings.Join(c.Args, " "))
		}
	}
	if got := h.event(NotCommitted, "A-1").Detail; got != "nothing to commit" {
		t.Errorf("not_committed detail %q", got)
	}
}

func TestCommitNeverTouchesGit(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit = \"never\"\n")
	h.dirtyTree()
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.calls("git"); len(got) != 0 {
		t.Errorf("git called under commit = never: %v", got)
	}
	if got := h.count(NotCommitted) + h.count(Committed); got != 0 {
		t.Errorf("%d commit events under commit = never", got)
	}
}

func TestCommitAsk(t *testing.T) {
	for _, yes := range []bool{true, false} {
		name := map[bool]string{true: "yes", false: "no"}[yes]
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			git := h.dirtyTree()
			h.onEvent = func(ev Event) {
				if ev.Kind == Asked && ev.Question == QuestionCommit {
					at := ev.At.Sub(t0) + 5*time.Second
					h.clock.At(at, func() { h.eng.Send(Command{Kind: CmdAnswer, Yes: yes}) })
				}
			}
			if _, err := h.run(); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := h.count(Asked); got != 3 {
				t.Errorf("%d questions, want one per task", got)
			}
			if got := h.event(Asked, "A-1").Detail; got != `commit the changes of A-1 as "A-1: One"?` {
				t.Errorf("question %q", got)
			}
			if yes && len(*git) != 6 {
				t.Errorf("git calls = %q, want add and commit per task", *git)
			}
			if !yes {
				if len(*git) != 0 {
					t.Errorf("git calls = %q after declining", *git)
				}
				if got := h.event(NotCommitted, "A-1").Detail; got != "the owner declined" {
					t.Errorf("not_committed detail %q", got)
				}
			}
			if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
				t.Errorf("statuses = %s", got)
			}
		})
	}
}

// Stopping while the commit question is open leaves the task in progress
// with its signal, so the next run verifies and asks again.
func TestStopWhileAskingToCommit(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.dirtyTree()
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked {
			h.eng.Send(Command{Kind: CmdDone}) // must not bypass the question
			h.eng.Send(Command{Kind: CmdStop})
		}
	}
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v; want stopped", res.Outcome, err)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=in progress") {
		t.Errorf("statuses = %s", got)
	}
	if sig, err := h.dir.ReadSignal("A-1"); err != nil || sig == nil {
		t.Errorf("signal not kept: %+v, %v", sig, err)
	}
}

func TestCommitMessageTemplate(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit = \"auto\"\ncommit_message = \"{{.Phase}}/{{.ID}} ({{.Rank}} on {{.Model}}): {{.Title}}\"\n")
	git := h.dirtyTree()
	h.autoSignalExcept("A-1")
	h.onEvent = func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			h.eng.Send(Command{Kind: CmdDone}) // no note, so no body
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := (*git)[1]; got != "commit -m A/A-1 (sonnet on sonnet): One [A-1=in progress]" {
		t.Errorf("first commit = %q", got)
	}
}

func TestBadCommitMessageTemplate(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit_message = \"{{.ID\"\n")
	_, err := New(Options{Config: h.cfg, Backend: h.be, State: h.dir, Phase: "A"})
	if err == nil || !strings.Contains(err.Error(), "run.commit_message") {
		t.Fatalf("New err = %v, want a commit_message error", err)
	}

	// A field that doesn't exist only shows when the message is rendered.
	h = newHarness(t, chainPlan, "[run]\ncommit = \"auto\"\ncommit_message = \"{{.Nope}}\"\n")
	h.dirtyTree()
	if _, err := h.run(); err == nil || !strings.Contains(err.Error(), "commit_message") {
		t.Fatalf("Run err = %v", err)
	}
}

func TestCommitFailureStopsTheRun(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit = \"auto\"\n")
	h.dirtyTree()
	inner := h.gitFn
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		if c.Args[0] == "commit" {
			return runner.Result{ExitCode: 1, Stderr: []byte("pre-commit hook failed\n")}, nil
		}
		return inner(c)
	}
	_, err := h.run()
	var exit *runner.ExitError
	if !errors.As(err, &exit) || !strings.Contains(err.Error(), "pre-commit hook failed") || !strings.Contains(err.Error(), `set commit = "never"`) {
		t.Fatalf("err = %v, want the git failure", err)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=in progress") {
		t.Errorf("statuses = %s", got)
	}
	if got := h.toasts(); len(got) != 1 || !strings.Contains(got[0], "error") {
		t.Errorf("toasts = %q, want the run-error toast", got)
	}
}
