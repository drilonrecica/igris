package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
)

// worktreeGit answers git like a repository would for worktree mode
// (SPEC §6.8): `worktree add` and `worktree remove` make and remove the
// directory, `status` reports changes while dirty() says so, and every
// call is recorded as "<dir>: <args>" with the root shown as ".".
func (h *harness) worktreeGit(dirty func() bool) *[]string {
	var log []string
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		dir, _ := filepath.Rel(h.root, c.Dir)
		log = append(log, dir+": "+strings.Join(c.Args, " "))
		switch {
		case len(c.Args) == 6 && c.Args[0] == "worktree" && c.Args[1] == "add":
			return runner.Result{}, os.MkdirAll(filepath.Join(c.Dir, c.Args[4]), 0o750)
		case len(c.Args) == 3 && c.Args[0] == "worktree" && c.Args[1] == "remove":
			return runner.Result{}, os.RemoveAll(filepath.Join(c.Dir, c.Args[2]))
		case c.Args[0] == "status" && dirty():
			return runner.Result{Stdout: []byte(" M main.go\n")}, nil
		case c.Args[0] == "rev-parse":
			return runner.Result{Stdout: []byte(fakeSHA + "\n")}, nil
		}
		return runner.Result{}, nil
	}
	return &log
}

// otherDirs records the directory of every command that isn't git: verify
// and task hooks.
func (h *harness) otherDirs() *[]string {
	var dirs []string
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		dir, _ := filepath.Rel(h.root, c.Dir)
		dirs = append(dirs, dir)
		return runner.Result{}, nil
	}
	return &dirs
}

func withWorktree(o *Options) { o.Worktree = true }

const worktreeToml = "[run]\ncommit = \"auto\"\nverify = \"make check\"\n[hooks]\nbefore_task = [\"./prep\"]\n"

// A worktree run makes its worktree from HEAD, runs every session, verify,
// task hook and commit there, writes the plan in the project root, and
// removes the worktree once it completes with nothing uncommitted.
func TestWorktreeRun(t *testing.T) {
	h := newHarness(t, chainPlan, worktreeToml)
	committed := false
	git := h.worktreeGit(func() bool { return !committed })
	h.onEvent = func(ev Event) { committed = ev.Kind == Committed && ev.Task == "A-3" || committed }
	dirs := h.otherDirs()
	res, err := h.run(withWorktree)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	wt := res.Worktree
	if wt == nil || !wt.Removed || !wt.Done || !strings.HasPrefix(wt.Branch, "igris/") {
		t.Fatalf("result worktree = %+v, want a removed one", wt)
	}
	id := strings.TrimPrefix(wt.Branch, "igris/")
	rel := filepath.Join(".igris", "worktrees", id)
	if wt.Path != rel {
		t.Errorf("worktree path %q, want %q", wt.Path, rel)
	}
	if got, want := (*git)[0], ".: worktree add -b igris/"+id+" "+rel+" HEAD"; got != want {
		t.Errorf("first git call %q, want %q", got, want)
	}
	if got, want := (*git)[len(*git)-1], ".: worktree remove "+rel; got != want {
		t.Errorf("last git call %q, want %q", got, want)
	}
	for _, c := range (*git)[1 : len(*git)-1] {
		if !strings.HasPrefix(c, rel+": ") {
			t.Errorf("git call %q outside the worktree", c)
		}
	}
	for _, d := range *dirs {
		if d != rel {
			t.Errorf("verify or hook ran in %q, want %q", d, rel)
		}
	}
	if len(*dirs) != 6 {
		t.Errorf("%d verify and hook runs, want 6", len(*dirs))
	}
	for _, spec := range h.be.Opened() {
		if spec.Dir != h.path(rel) {
			t.Errorf("%s session in %q, want the worktree", spec.TaskID, spec.Dir)
		}
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("project plan statuses = %s", got)
	}
	// The prompt names the project's plan, which igris keeps up to date,
	// not the worktree's copy; the session may run `igris done` there.
	if p := h.be.Prompts("A-1"); len(p) == 0 || !strings.Contains(p[0], h.path("tasks.md")) {
		t.Errorf("A-1 prompt doesn't name %s", h.path("tasks.md"))
	}
	// The session may run `igris done` and read the plan outside the
	// worktree (an absolute path is "//path" in a Claude Code rule).
	s, err := os.ReadFile(arg(h.be.Opened()[0], "--settings"))
	for _, rule := range []string{`"Bash(igris done:*)"`, `"Read(/` + h.path("tasks.md") + `)"`} {
		if err != nil || !strings.Contains(string(s), rule) {
			t.Errorf("session settings lack %s: %v\n%s", rule, err, s)
		}
	}
	if got := h.event(WorktreeReady, "").Detail; got != "sessions, verify and commits run in new worktree "+rel+" on branch igris/"+id {
		t.Errorf("worktree_ready = %q", got)
	}
	if got := h.event(WorktreeLeft, "").Detail; got != "the run's work is on branch igris/"+id+"; igris doesn't merge it: git merge igris/"+id {
		t.Errorf("worktree_left = %q", got)
	}
	if _, err := os.Stat(h.path(rel)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if run, err := h.dir.LoadRun(); err != nil || run.Worktree != nil {
		t.Errorf("state.json worktree = %+v, %v; want none after removal", run.Worktree, err)
	}
	if log := h.read(".igris/runs.jsonl"); !strings.Contains(log, `"type":"run_started","run":"`+id+`"`) || !strings.Contains(log, `"branch":"igris/`+id+`"`) {
		t.Errorf("run_started lacks the branch:\n%s", log)
	}
}

// Without worktree mode nothing changes: sessions and prompts use the
// project root and the plan as configured, and settings carry hooks only.
func TestNoWorktree(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	git := h.worktreeGit(func() bool { return false })
	res, err := h.run()
	if err != nil || res.Worktree != nil {
		t.Fatalf("Run = %+v, %v", res.Worktree, err)
	}
	for _, c := range *git {
		if strings.Contains(c, "worktree") || !strings.HasPrefix(c, ".: ") {
			t.Errorf("git call %q", c)
		}
	}
	if spec := h.be.Opened()[0]; spec.Dir != h.root {
		t.Errorf("session in %q", spec.Dir)
	}
	if s, _ := os.ReadFile(arg(h.be.Opened()[0], "--settings")); strings.Contains(string(s), "permissions") {
		t.Error("session settings carry permissions outside worktree mode")
	}
	if h.count(WorktreeReady)+h.count(WorktreeLeft) != 0 {
		t.Error("worktree events without worktree mode")
	}
}

// A worktree with uncommitted changes is kept when the run completes.
func TestWorktreeKeptWhenDirty(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\nworktree = true\ncommit = \"never\"\n")
	git := h.worktreeGit(func() bool { return true })
	res, err := h.run()
	if err != nil || res.Worktree == nil || res.Worktree.Removed {
		t.Fatalf("Run = %+v, %v; want a kept worktree", res.Worktree, err)
	}
	for _, c := range *git {
		if strings.Contains(c, "worktree remove") {
			t.Errorf("git call %q", c)
		}
	}
	if got := res.Worktree.Closing(); len(got) != 2 || !strings.Contains(got[1], "has uncommitted changes and is kept") {
		t.Errorf("closing = %q", got)
	}
	if run, _ := h.dir.LoadRun(); run.Worktree == nil {
		t.Error("state.json lost the kept worktree")
	}
}

// A stopped run keeps its worktree, and the next arise (no phase named)
// continues there without making another one, also without --worktree.
func TestWorktreeResume(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	git := h.worktreeGit(func() bool { return false })
	h.autoSignalExcept("A-1")
	h.stopMidTask(6*time.Second, withWorktree)
	run, err := h.dir.LoadRun()
	if err != nil || run.Worktree == nil {
		t.Fatalf("state.json worktree = %v, %v", run, err)
	}
	h.signalAt(20*time.Second, "A-1")
	res, err := h.run(resumeLast)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed run = %s, %v", res.Outcome, err)
	}
	adds := 0
	for _, c := range *git {
		if strings.Contains(c, "worktree add") {
			adds++
		}
	}
	if adds != 1 {
		t.Errorf("%d worktree add calls, want 1", adds)
	}
	for _, spec := range h.be.Opened() {
		if spec.Dir != h.path(run.Worktree.Path) {
			t.Errorf("%s session in %q, want %s", spec.TaskID, spec.Dir, run.Worktree.Path)
		}
	}
	if got := h.event(WorktreeReady, "").Detail; !strings.Contains(got, "in the worktree "+run.Worktree.Path) {
		t.Errorf("worktree_ready = %q", got)
	}
	if res.Worktree == nil || !res.Worktree.Removed {
		t.Errorf("result worktree = %+v", res.Worktree)
	}
}

// A new run without worktree mode leaves the last run's kept worktree
// alone and says so; it works in the project root.
func TestWorktreeLeftover(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\ncommit = \"never\"\n")
	git := h.worktreeGit(func() bool { return true })
	first, err := h.run(withWorktree)
	if err != nil || first.Worktree == nil || first.Worktree.Removed {
		t.Fatalf("first run = %+v, %v", first.Worktree, err)
	}
	h.events, *git = nil, nil
	res, err := h.run(func(o *Options) { o.Phase = "B" })
	if err != nil || res.Worktree != nil {
		t.Fatalf("second run = %+v, %v", res.Worktree, err)
	}
	if got := h.event(Warning, "").Detail; !strings.Contains(got, "the last run's worktree is still at "+first.Worktree.Path) {
		t.Errorf("warning = %q", got)
	}
	if spec := h.be.Opened()[len(h.be.Opened())-1]; spec.Dir != h.root {
		t.Errorf("B session in %q, want the project root", spec.Dir)
	}
	for _, c := range *git {
		if strings.Contains(c, "worktree") {
			t.Errorf("git call %q", c)
		}
	}
}

// A run that asks for a worktree and can't make one doesn't start.
func TestWorktreeAddFails(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		return runner.Result{}, errors.New("fatal: not a git repository")
	}
	_, err := h.run(withWorktree)
	if err == nil || !strings.Contains(err.Error(), "worktree mode: make the worktree") || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("Run err = %v", err)
	}
	if len(h.be.Opened()) != 0 {
		t.Error("a session was opened")
	}
}

// A task in progress in a worktree that is gone is not resumed elsewhere.
func TestWorktreeGone(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.worktreeGit(func() bool { return false })
	h.autoSignalExcept("A-1")
	h.stopMidTask(6*time.Second, withWorktree)
	run, _ := h.dir.LoadRun()
	if err := os.RemoveAll(h.path(run.Worktree.Path)); err != nil {
		t.Fatal(err)
	}
	_, err := h.run(resumeLast)
	if err == nil || !strings.Contains(err.Error(), "A-1 ran in the worktree "+run.Worktree.Path) || !strings.Contains(err.Error(), "igris reset A-1") {
		t.Fatalf("Run err = %v", err)
	}
}
