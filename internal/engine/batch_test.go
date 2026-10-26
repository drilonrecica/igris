package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/hook"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// gitEnv isolates a test's git from the owner's: no global or system
// config (no signing, no hooks), a fixed author.
var gitEnv = []string{
	"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	"GIT_AUTHOR_NAME=igris", "GIT_AUTHOR_EMAIL=igris@example.invalid",
	"GIT_COMMITTER_NAME=igris", "GIT_COMMITTER_EMAIL=igris@example.invalid",
}

// realGit makes h's git a real repository in its root with one commit;
// .igris/ is ignored, as `igris init` sets up. Each session writes
// <ID>.txt before it signals done, so every task has a change to commit.
func (h *harness) realGit() {
	h.t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		h.t.Skip("git is not installed")
	}
	h.write(".gitignore", ".igris/\n")
	h.gitFn = func(c runner.Cmd) (runner.Result, error) {
		c.Env = append(slices.Clone(c.Env), gitEnv...)
		return runner.Exec{}.Run(context.Background(), c)
	}
	h.git("init", "-q", "-b", "main")
	h.git("add", "-A")
	h.git("commit", "-q", "-m", "start")
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		h.write(id+".txt", "work of "+id+"\n")
		return h.signal(id)
	})
}

// git runs git in h's root and returns its trimmed output.
func (h *harness) git(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // the test's own arguments
	cmd.Dir = h.root
	cmd.Env = append(os.Environ(), gitEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// answerBatch answers the batch question with yes (Keep) or no (Undo).
func (h *harness) answerBatch(yes bool) {
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionCommitBatch {
			h.eng.Send(Command{Kind: CmdAnswer, Question: QuestionCommitBatch, Yes: yes})
		}
	})
}

// savedRun is state.json as the run left it.
func (h *harness) savedRun() *state.Run {
	h.t.Helper()
	r, err := h.dir.LoadRun()
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

const batchTOML = "[run]\ncommit = \"batch\"\n"

// Under batch every task is committed at once, without a question, and
// the run asks once at its end: Keep leaves the commits.
func TestBatchKeep(t *testing.T) {
	h := newHarness(t, chainPlan, batchTOML)
	h.realGit()
	base := h.git("rev-parse", "HEAD")
	n := &askNotifier{h: h}
	h.answerBatch(true)
	if res, err := h.run(n.use); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.git("rev-list", "--count", base+"..HEAD"); got != "3" {
		t.Errorf("%s commits since the base, want 3", got)
	}
	var asked []Event
	for _, ev := range h.events {
		if ev.Kind == Asked {
			asked = append(asked, ev)
		}
	}
	if len(asked) != 1 || asked[0].Question != QuestionCommitBatch {
		t.Fatalf("questions %+v, want the batch question alone", asked)
	}
	q := asked[0]
	if q.Detail != "Keep the 3 commits of this run?" || !q.Undo || q.Task != "" {
		t.Errorf("question %q (undo %v, task %q)", q.Detail, q.Undo, q.Task)
	}
	if want := []string{"A-1: One", "A-2: Two", "A-3: Three"}; !slices.Equal(q.Output, want) {
		t.Errorf("subjects %q, want %q", q.Output, want)
	}
	msgs := n.asking(QuestionCommitBatch)
	if len(msgs) != 1 || msgs[0].m.What != "run completed · keep the 3 commits?" || msgs[0].m.Event != notify.NeedsInput || msgs[0].m.TaskID != "" {
		t.Errorf("batch messages %+v", msgs)
	}
	if got := h.logged(); !strings.Contains(got, "needs_you needs_you_clear run_stopped") || strings.Contains(got, "commits_undone") {
		t.Errorf("run log = %s", got)
	}
	if r := h.savedRun(); r.BatchBase != "" {
		t.Errorf("batch_base %q kept after the answer", r.BatchBase)
	}
}

// Undo is git reset --soft to the base: every commit of the run is gone,
// every change is still in the tree and the index.
func TestBatchUndo(t *testing.T) {
	h := newHarness(t, chainPlan, batchTOML)
	h.realGit()
	base := h.git("rev-parse", "HEAD")
	h.answerBatch(false)
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.git("rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD %s, want the base %s", got, base)
	}
	staged := h.git("diff", "--cached", "--name-only")
	for _, f := range []string{"A-1.txt", "A-2.txt", "A-3.txt", "tasks.md"} {
		if !strings.Contains(staged, f) {
			t.Errorf("%s is not in the index after Undo: %q", f, staged)
		}
	}
	if got := h.read("A-2.txt"); got != "work of A-2\n" {
		t.Errorf("A-2.txt = %q", got)
	}
	ev := h.logEvent(state.EventCommitsUndone, "")
	if ev.Commit != base || ev.Detail != "3 commits undone; their changes are in the tree" {
		t.Errorf("commits_undone = %+v", ev)
	}
	if h.count(CommitsUndone) != 1 {
		t.Error("no commits_undone event")
	}
	// The plan's Status cells stay as they are.
	if got := h.statuses(); !strings.Contains(got, "A-1=done A-2=done A-3=done") {
		t.Errorf("statuses %s", got)
	}
}

// A commit igris didn't make in base..HEAD: Undo is not offered, and the
// question says why and what to run.
func TestBatchUndoRefusedAfterAForeignCommit(t *testing.T) {
	h := newHarness(t, chainPlan, batchTOML)
	h.realGit()
	base := h.git("rev-parse", "HEAD")
	h.on(func(ev Event) {
		switch {
		case ev.Kind == Committed && ev.Task == "A-2":
			h.write("mine.txt", "the owner's\n")
			h.git("add", "mine.txt")
			h.git("commit", "-q", "-m", "mine")
		case ev.Kind == Asked && ev.Question == QuestionCommitBatch:
			h.eng.Send(Command{Kind: CmdAnswer}) // Undo: refused
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	q := h.event(Asked, "")
	if q.Undo || !strings.Contains(q.Detail, "HEAD moved outside igris") || !strings.Contains(q.Detail, "git reset --soft "+base) {
		t.Errorf("question %q (undo %v)", q.Detail, q.Undo)
	}
	if !h.warned("HEAD moved outside igris; undo them yourself") {
		t.Error("Undo was not refused")
	}
	if got := h.git("rev-list", "--count", base+"..HEAD"); got != "4" {
		t.Errorf("%s commits, want the run's 3 and the owner's", got)
	}
}

// Leaving without an answer keeps the commits and the base: the resumed
// run asks about every commit since it, and Undo undoes them all.
func TestBatchResumeKeepsItsBase(t *testing.T) {
	h := newHarness(t, chainPlan, batchTOML)
	h.realGit()
	base := h.git("rev-parse", "HEAD")
	h.on(func(ev Event) {
		if ev.Kind == Committed && ev.Task == "A-1" {
			h.cancel() // Ctrl-C
		}
	})
	if res, err := h.run(); err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if r := h.savedRun(); r.BatchBase != base {
		t.Fatalf("batch_base %q, want %s", r.BatchBase, base)
	}
	h.events = nil
	h.onEvent = nil
	h.answerBatch(false)
	if _, err := h.run(func(o *Options) { o.Phase = "" }); err != nil {
		t.Fatal(err)
	}
	if got := h.event(Asked, "").Detail; got != "Keep the 3 commits of this run?" {
		t.Errorf("question %q, want the first run's commit too", got)
	}
	if got := h.git("rev-parse", "HEAD"); got != base {
		t.Errorf("HEAD %s, want the first run's base %s", got, base)
	}
}

// With nothing committed there is nothing to ask.
func TestBatchNothingCommitted(t *testing.T) {
	h := newHarness(t, chainPlan, batchTOML)
	h.realGit()
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	h.write(".gitignore", ".igris/\ntasks.md\n")
	h.git("rm", "-q", "--cached", "tasks.md")
	h.git("commit", "-q", "-am", "untrack the plan")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.count(Asked); got != 0 {
		t.Errorf("%d questions with nothing committed", got)
	}
}

// --no-tui's stdin ended: nobody can answer, so the commits are kept.
func TestBatchUnattendedKeeps(t *testing.T) {
	h := newHarness(t, chainPlan, batchTOML)
	h.realGit()
	base := h.git("rev-parse", "HEAD")
	h.on(func(ev Event) {
		if ev.Kind == RunStarted {
			h.eng.Unattended()
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if h.count(Asked) != 1 || h.count(CommitsUndone) != 0 {
		t.Errorf("events %s", h.kinds())
	}
	if got := h.git("rev-list", "--count", base+"..HEAD"); got != "3" {
		t.Errorf("%s commits, want 3 kept", got)
	}
}

// The task prompt says igris commits: for the session batch reads as auto.
func TestBatchPromptReadsAsAuto(t *testing.T) {
	h := newHarness(t, chainPlan, batchTOML)
	h.realGit()
	h.answerBatch(true)
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	p := h.be.Prompts("A-1")
	if len(p) == 0 || !strings.Contains(p[0], "igris commits your changes after verification passes") {
		t.Errorf("prompt of A-1 doesn't say igris commits")
	}
}

// Commit all on the first commit question commits it and every later
// task without asking: commit_override in state.json, commit_policy in
// the run log, a feed event; igris.toml is untouched.
func TestCommitAllSequential(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	toml := h.read(state.ConfigFile)
	git := h.dirtyTree()
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionCommit {
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true, Text: CommitAllText})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.count(Asked); got != 1 {
		t.Errorf("%d commit questions, want only the first", got)
	}
	if got := strings.Count(strings.Join(*git, "\n"), "commit -m"); got != 3 {
		t.Errorf("%d commits, want 3: %q", got, *git)
	}
	ev := h.logEvent(state.EventCommitPolicy, "")
	if ev.Detail != "auto: the owner's answer to A-1's commit question" {
		t.Errorf("commit_policy = %+v", ev)
	}
	if h.count(CommitPolicy) != 1 {
		t.Error("no commit_policy event")
	}
	if r := h.savedRun(); r.CommitOverride != "auto" {
		t.Errorf("commit_override %q", r.CommitOverride)
	}
	if got := h.read(state.ConfigFile); got != toml {
		t.Errorf("igris.toml = %q", got)
	}
}

// In a parallel phase Commit all answers the commit questions already
// pending Commit too, and the tasks after them aren't asked.
func TestCommitAllParallel(t *testing.T) {
	h := newParallel(t, parallelPlan(), "[run]\ncommit = \"ask\"\n[parallel]\nmax = 2\n")
	r := h.repo()
	h.signalAt(5*60e9, "a")
	h.signalAt(5*60e9, "b")
	h.signalAt(10*60e9, "c")
	asked := 0
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionCommit {
			asked++
			if ev.Task == "b" {
				h.eng.Send(Command{Kind: CmdAnswer, Task: "b", Yes: true, Text: CommitAllText})
			}
		}
	})
	if res, err := h.run(); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if asked != 2 {
		t.Errorf("%d commit questions, want a's and b's", asked)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !has(h.logOf(id), "integrated:") {
			t.Errorf("%s didn't integrate: %q", id, h.logOf(id))
		}
	}
	if n := len(r.calls(": commit -m")); n != 3 {
		t.Errorf("%d commits, want 3", n)
	}
}

// The body of a blocked session's needs_input says why, from the hooks'
// record (V18-P3), as the TUI's and the web UI's Needs-you line does.
func TestNeedsInputBodyPerCause(t *testing.T) {
	tests := []struct {
		fixture string
		want    string
	}{
		{"pretooluse_askuserquestion.json", "asks you a question in its pane"},
		{"pretooluse_exitplanmode.json", "plan to approve"},
		{"permissionrequest.json", "waits for a permission (Bash)"},
		{"notification_permission_prompt.json", "waits for you in its pane"},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			h := newHarness(t, chainPlan, "needs_input_after = \"30s\"\n")
			h.be.SetAutoSignal(func(context.Context, string) error { return nil })
			h.be.Script("A-1", states(backend.Blocked, 10)...)
			h.on(func(ev Event) {
				if ev.Kind == SessionOpened {
					h.hookFixture(tt.fixture, ev.ClaudeSession)
				}
			})
			h.clock.At(5*time.Minute, func() { h.cancel() })
			if _, err := h.run(); err != nil {
				t.Fatal(err)
			}
			ny := h.event(NeedsYou, "A-1")
			if NeedsWords(ny.Reason, ny.Cause, ny.Tool) != tt.want {
				t.Errorf("event %+v says %q, want %q", ny, NeedsWords(ny.Reason, ny.Cause, ny.Tool), tt.want)
			}
			if got := h.toasts(); len(got) == 0 || got[0] != "request: phase A · A-1 One: "+tt.want {
				t.Errorf("toasts %q", got)
			}
		})
	}
}

// hookFixture records the agent state a recorded hook payload maps to for
// the session uuid, as `igris hook` does.
func (h *harness) hookFixture(name, uuid string) {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "hook", "testdata", name)) //nolint:gosec // the hook package's fixtures
	if err != nil {
		h.t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(data, &p); err != nil {
		h.t.Fatal(err)
	}
	p["session_id"] = uuid
	data, _ = json.Marshal(p)
	if err := hook.Run(context.Background(), h.root, bytes.NewReader(data), h.clock.Now); err != nil {
		h.t.Fatal(err)
	}
}

// An answer to the batch question during the run answers nothing else:
// it is refused, and the commit question still waits for its own.
func TestBatchAnswerBeforeTheEnd(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.dirtyTree()
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionCommit && ev.Task == "A-1" {
			h.eng.Send(Command{Kind: CmdAnswer, Question: QuestionCommitBatch}) // a stale Undo
		}
		if ev.Kind == Warning && strings.Contains(ev.Detail, "the batch question is asked once the run ends") {
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true, Text: CommitAllText})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if h.count(NotCommitted) != 0 || h.count(Committed) != 3 {
		t.Errorf("events %s", h.kinds())
	}
}
