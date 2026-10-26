package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// repo answers git like a repository would for the worktrees of a
// parallel phase (SPEC §6.11). `worktree add` (with -b or onto an existing
// branch) and `worktree remove` make and remove the directory; a new
// worktree has uncommitted changes (its session's work, the files of
// changes or main.go) until a commit in it; `rev-parse HEAD` names a
// worktree's base on the run branch and its commits, so a rebase that
// moves the base changes it; `rebase` conflicts where conflicts says so;
// `merge --ff-only` moves the run branch's head unless it moved since the
// rebase (`merge-base --is-ancestor` says so too) or refuse says to fail
// with git's error; `branch -d` refuses a
// branch with a commit the run branch lacks. Every call is logged as
// "<dir>: <args>" with the root shown as ".".
type repo struct {
	h        *harness
	mu       sync.Mutex
	log      []string
	runHead  int                // commits on the run branch
	wts      map[string]*fakeWT // worktree dir (relative) → its state
	branches map[string]*fakeWT // branch → the state of its commits
	// conflicts: a worktree dir whose next rebases (times of them) fail on
	// files.
	conflicts map[string]*conflict
	// changes are the files a dirty worktree's status shows; main.go
	// without an entry.
	changes map[string][]string
	// refuse: a branch whose next fast-forwards fail with these errors, one
	// per fast-forward, as git refuses one with files in the way.
	refuse map[string][]string
	// markers: a worktree dir whose next `diff --check` reports leftover
	// conflict markers in these files (a resolver that left them).
	markers map[string][]string
	// empty: a worktree dir whose next `diff --cached --quiet` finds
	// nothing staged (a resolution that is already on the base).
	empty map[string]bool
}

type fakeWT struct {
	base, commits int
	dirty         bool
	integrated    bool // its commits are on the run branch
	run           bool // the run's own worktree (worktree mode), not a task's
	// rebasing: a rebase stopped on a conflict and was neither aborted
	// nor continued (REBASE_HEAD exists); rebased: ORIG_HEAD exists.
	rebasing, rebased bool
}

type conflict struct {
	files []string
	times int
}

func (h *harness) repo() *repo {
	r := &repo{h: h, wts: map[string]*fakeWT{}, branches: map[string]*fakeWT{}, conflicts: map[string]*conflict{}, changes: map[string][]string{}, refuse: map[string][]string{}, markers: map[string][]string{}, empty: map[string]bool{}}
	h.gitFn = r.git
	return r
}

// conflictOn makes the next times rebases in the worktree dir fail on files.
func (r *repo) conflictOn(dir string, times int, files ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conflicts[dir] = &conflict{files: files, times: times}
}

// refuseFF makes the next fast-forwards of branch fail with git's errors,
// one per fast-forward.
func (r *repo) refuseFF(branch string, stderr ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refuse[branch] = stderr
}

// markersLeft makes the next `diff --check` in the worktree dir report
// leftover conflict markers in files.
func (r *repo) markersLeft(dir string, files ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markers[dir] = files
}

// nothingStaged makes the next `diff --cached --quiet` in the worktree dir
// find nothing staged.
func (r *repo) nothingStaged(dir string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.empty[dir] = true
}

// rebasing reports whether the worktree dir is mid-rebase.
func (r *repo) rebasing(dir string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	wt := r.wts[dir]
	return wt != nil && wt.rebasing
}

// sha is the commit hash HEAD names in dir: 40 digits, so it looks like one.
func (r *repo) sha(dir string) string {
	if wt := r.wts[dir]; wt != nil && !wt.run {
		return fmt.Sprintf("%040d", wt.base*1000+wt.commits)
	}
	return fmt.Sprintf("%040d", 1000000+r.runHead)
}

// runSHA is the run branch's head after n commits, as rev-parse in the
// root printed it then.
func runSHA(n int) string { return fmt.Sprintf("%040d", 1000000+n) }

func (r *repo) git(c runner.Cmd) (runner.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir, _ := filepath.Rel(r.h.root, c.Dir)
	a := c.Args
	r.log = append(r.log, dir+": "+strings.Join(a, " "))
	for len(a) > 1 && a[0] == "-c" {
		a = a[2:] // git -c key=value <command>
	}
	fail := runner.Result{ExitCode: 1}
	out := func(s string) (runner.Result, error) { return runner.Result{Stdout: []byte(s)}, nil }
	wt := r.wts[dir]
	switch {
	case a[0] == "rev-parse" && len(a) == 4 && a[3] == "REBASE_HEAD":
		if wt == nil || !wt.rebasing {
			return fail, nil
		}
	case a[0] == "merge-base" && len(a) == 3: // merge-base <target> ORIG_HEAD
		if wt == nil || !wt.rebased {
			return fail, nil
		}
		return out(runSHA(wt.base) + "\n")
	case a[0] == "log" && len(a) == 3 && strings.HasPrefix(a[1], "--format="): // the commits of base..target
		var b strings.Builder
		for n := r.runHead; wt != nil && n > wt.base; n-- {
			b.WriteString(runSHA(n) + "\tsubject " + fmt.Sprint(n) + "\n")
		}
		return out(b.String())
	case a[0] == "diff" && slices.Contains(a, "--check"):
		files := r.markers[dir]
		delete(r.markers, dir)
		if len(files) == 0 {
			return runner.Result{}, nil
		}
		var b strings.Builder
		for _, f := range files {
			b.WriteString(f + ":1: leftover conflict marker\n")
		}
		return runner.Result{ExitCode: 2, Stdout: []byte(b.String())}, nil
	case a[0] == "diff" && slices.Contains(a, "--cached"):
		if r.empty[dir] {
			delete(r.empty, dir)
			return runner.Result{}, nil
		}
		return fail, nil // something is staged
	case a[0] == "add":
	case a[0] == "commit" && slices.Contains(a, "--amend"):
	case a[0] == "rebase" && (a[1] == "--continue" || a[1] == "--skip"):
		if wt == nil || !wt.rebasing {
			return runner.Result{ExitCode: 128, Stderr: []byte("fatal: No rebase in progress?\n")}, nil
		}
		wt.rebasing, wt.base = false, r.runHead
		if a[1] == "--skip" {
			wt.commits = 0
		}
	case a[0] == "worktree" && a[1] == "add" && a[2] == "-b":
		branch, path := a[3], a[4]
		if _, ok := r.branches[branch]; ok {
			return fail, nil
		}
		wt := &fakeWT{base: r.runHead, dirty: true, run: !strings.Contains(strings.TrimPrefix(branch, "igris/"), "-") || len(filepath.Base(path)) == 20}
		if wt.run {
			wt.dirty = false
		}
		r.branches[branch], r.wts[path] = wt, wt
		return runner.Result{}, os.MkdirAll(filepath.Join(c.Dir, path), 0o750)
	case a[0] == "worktree" && a[1] == "add":
		path, branch := a[2], a[3]
		wt := r.branches[branch]
		if wt == nil {
			return fail, nil
		}
		r.wts[path] = wt
		return runner.Result{}, os.MkdirAll(filepath.Join(c.Dir, path), 0o750)
	case a[0] == "worktree" && a[1] == "remove":
		path := a[len(a)-1]
		delete(r.wts, path)
		return runner.Result{}, os.RemoveAll(filepath.Join(c.Dir, path))
	case a[0] == "rev-parse" && len(a) == 4: // --verify --quiet refs/heads/<branch>
		if _, ok := r.branches[strings.TrimPrefix(a[3], "refs/heads/")]; !ok {
			return fail, nil
		}
	case a[0] == "rev-parse":
		return out(r.sha(dir) + "\n")
	case a[0] == "symbolic-ref":
		return out("main\n")
	case a[0] == "status":
		wt := r.wts[dir]
		if wt == nil || !wt.dirty {
			return runner.Result{}, nil
		}
		files := r.changes[dir]
		if len(files) == 0 {
			files = []string{"main.go"}
		}
		sep := "\n"
		if slices.Contains(a, "-z") {
			sep = "\x00"
		}
		var b strings.Builder
		for _, f := range files {
			b.WriteString(" M " + f + sep)
		}
		return out(b.String())
	case a[0] == "commit":
		if wt := r.wts[dir]; wt != nil {
			wt.dirty, wt.commits = false, wt.commits+1
		}
	case a[0] == "rev-list":
		n := 0
		if wt := r.wts[dir]; wt != nil && !wt.integrated {
			n = wt.commits
		}
		return out(fmt.Sprintf("%d\n", n))
	case a[0] == "rebase" && a[1] == "--abort":
		if wt != nil {
			wt.rebasing = false
		}
	case a[0] == "rebase":
		if wt != nil {
			wt.rebased = true
		}
		if k := r.conflicts[dir]; k != nil && k.times > 0 {
			k.times--
			if wt != nil {
				wt.rebasing = true
			}
			return fail, nil
		}
		if wt != nil {
			wt.base = r.runHead
		}
	case a[0] == "diff":
		if k := r.conflicts[dir]; k != nil {
			return out(strings.Join(k.files, "\n") + "\n")
		}
	case a[0] == "merge-base": // --is-ancestor HEAD <branch>
		if wt := r.branches[a[3]]; wt == nil || wt.base != r.runHead {
			return fail, nil
		}
	case a[0] == "merge":
		branch := a[len(a)-1]
		wt := r.branches[branch]
		if wt == nil || wt.base != r.runHead {
			return fail, nil
		}
		if errs := r.refuse[branch]; len(errs) > 0 {
			r.refuse[branch] = errs[1:]
			return runner.Result{ExitCode: 1, Stderr: []byte(errs[0])}, nil
		}
		r.runHead += wt.commits
		wt.integrated = true
	case a[0] == "log":
		return out("subject\n")
	case a[0] == "branch" && a[1] == "-d":
		wt := r.branches[a[2]]
		if wt != nil && wt.commits > 0 && !wt.integrated {
			return fail, nil
		}
		delete(r.branches, a[2])
	case a[0] == "branch" && a[1] == "-D":
		delete(r.branches, a[2])
	}
	return runner.Result{}, nil
}

// calls returns the logged git calls, and those containing sub.
func (r *repo) calls(sub string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.log {
		if strings.Contains(c, sub) {
			out = append(out, c)
		}
	}
	return out
}

// ran is a command other than git (verify or a task hook): where it ran
// and the IGRIS_SLOT it got.
type ran struct{ name, dir, slot string }

// commands records every command that isn't git; it is safe for the
// verifies that run beside the poll loop.
func (h *harness) commands() func() []ran {
	var mu sync.Mutex
	var out []ran
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		dir, _ := filepath.Rel(h.root, c.Dir)
		slot := "-"
		for _, kv := range c.Env {
			if v, ok := strings.CutPrefix(kv, "IGRIS_SLOT="); ok {
				slot = v
			}
		}
		name := c.Name
		if name == "sh" {
			name = c.Args[1]
		}
		mu.Lock()
		out = append(out, ran{name, dir, slot})
		mu.Unlock()
		return runner.Result{}, nil
	}
	return func() []ran {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(out)
	}
}

// runBase is the <run-id> of the task worktrees: the ID of the run that
// made them.
func (h *harness) runBase() string { return h.event(RunStarted, "").Run }

// taskWT is the worktree path of task id in run base, relative to the root.
func taskWT(base, id string) string { return filepath.Join(".igris", "worktrees", base+"-"+id) }

const taskWorktreeTOML = "needs_input_after = \"1h\"\n[run]\ncommit = \"auto\"\nverify = \"make check\"\n" +
	"[hooks]\nbefore_task = [\"./prep\"]\nafter_task = [\"./post\"]\n[parallel]\nmax = 2\n"

// Each task of a parallel phase works in a worktree of its own, made from
// the run branch before before_task: its session, verify, task hooks and
// commit run there with IGRIS_SLOT, the prompt names the project's plan,
// which igris writes in the project root, and the worktree is removed once
// the task is done (SPEC §6.11). Without integration (V15-05) its branch
// keeps the commit, and is kept and named.
func TestParallelTaskWorktrees(t *testing.T) {
	h := newParallel(t, parallelPlan(), taskWorktreeTOML)
	h.write(".claude/settings.local.json", `{"permissions": {"allow": ["Bash(igris done:*)"]}}`)
	r := h.repo()
	cmds := h.commands()
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("project plan statuses = %s", got)
	}
	if got := h.slots(); got != "a:0 b:1 c:0" {
		t.Fatalf("slots: %s", got)
	}
	slot := map[string]string{"a": "0", "b": "1", "c": "0"}
	for _, id := range []string{"a", "b", "c"} {
		wt := taskWT(base, id)
		if got, want := r.calls(" "+wt+" "), []string{".: worktree add -b igris/" + base + "-" + id + " " + wt + " HEAD"}; !slices.Equal(got, want) {
			t.Errorf("%s: worktree made by %q, want %q", id, got, want)
		}
		if got := r.calls(": worktree remove " + wt); len(got) != 1 || !strings.HasPrefix(got[0], ".: ") {
			t.Errorf("%s: worktree removed by %q, want once in the root", id, got)
		}
		if got := r.calls(wt + ": commit -m " + id + ": "); len(got) != 1 {
			t.Errorf("%s: commits in its worktree %q, want one", id, got)
		}
		if _, err := os.Stat(h.path(wt)); !os.IsNotExist(err) {
			t.Errorf("%s: the worktree is still there: %v", id, err)
		}
		var got []ran
		for _, c := range cmds() {
			if c.dir == wt {
				got = append(got, c)
			}
		}
		// prep, verify, maybe verify again on the integrated tree, post.
		if n := len(got); n < 3 || n > 4 || got[0] != (ran{"./prep", wt, slot[id]}) || got[1] != (ran{"make check", wt, slot[id]}) || got[n-1] != (ran{"./post", wt, slot[id]}) {
			t.Errorf("%s: commands %+v", id, got)
		}
		branch := "igris/" + base + "-" + id
		if got := r.calls(".: merge --ff-only " + branch); len(got) != 1 {
			t.Errorf("%s: fast-forwarded by %q, want once in the root", id, got)
		}
		if got := r.calls(".: branch -d " + branch); len(got) != 1 {
			t.Errorf("%s: branch deleted by %q, want once", id, got)
		}
	}
	if h.warned("is kept") {
		t.Errorf("a warning about a kept worktree or branch: %s", h.kinds())
	}
	if n := len(cmds()); n < 9 || n > 11 {
		t.Errorf("%d commands, want 9 in the task worktrees plus up to two verifies of integrated trees", n)
	}
	for _, c := range r.calls(": commit ") {
		if strings.HasPrefix(c, ".: ") {
			t.Errorf("a commit in the project root: %s", c)
		}
	}
	for _, spec := range h.be.Opened() {
		wt := taskWT(base, spec.TaskID)
		if spec.Dir != h.path(wt) || !slices.Equal(spec.Env, []string{"IGRIS_SLOT=" + slot[spec.TaskID]}) {
			t.Errorf("%s session in %q with %q, want %s with IGRIS_SLOT=%s", spec.TaskID, spec.Dir, spec.Env, wt, slot[spec.TaskID])
		}
		if p := h.be.Prompts(spec.TaskID); len(p) == 0 || !strings.Contains(p[0], h.path("tasks.md")) {
			t.Errorf("%s prompt doesn't name %s", spec.TaskID, h.path("tasks.md"))
		}
		s, err := os.ReadFile(arg(spec, "--settings"))
		for _, rule := range []string{`"Bash(igris done:*)"`, `"Read(/` + h.path("tasks.md") + `)"`} {
			if err != nil || !strings.Contains(string(s), rule) {
				t.Errorf("%s session settings lack %s: %v\n%s", spec.TaskID, rule, err, s)
			}
		}
	}
	// The worktrees' copies of the plan are never written: nothing but
	// git made them, and igris wrote only the project's plan.
	if run, err := h.dir.LoadRun(); err != nil || len(run.Active) != 0 {
		t.Errorf("state.json active = %+v, %v; want none", run.Active, err)
	}
}

// warned reports whether a warning event's detail contains text.
func (h *harness) warned(text string) bool {
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, text) {
			return true
		}
	}
	return false
}

// An integrated branch, which the run branch has, is deleted with its
// worktree (SPEC §6.11).
func TestParallelTaskBranchDeletedWhenMerged(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	r := h.repo()
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := r.calls(": branch -d "); len(got) != 3 {
		t.Errorf("branch deletes %q, want 3", got)
	}
	if h.warned("is kept") {
		t.Errorf("a warning about a kept worktree or branch: %s", h.kinds())
	}
	if len(r.branches) != 0 {
		t.Errorf("branches left: %v", r.branches)
	}
}

// Verify of a parallel phase runs beside the other sessions, two at once;
// with serial_verify one at a time, in the order the done signals came in
// (SPEC §6.11).
func TestParallelSerialVerify(t *testing.T) {
	for _, serial := range []bool{false, true} {
		t.Run(map[bool]string{false: "parallel", true: "serial"}[serial], func(t *testing.T) {
			toml := "needs_input_after = \"1h\"\n[run]\ncommit = \"auto\"\nverify = \"make check\"\n[parallel]\nmax = 2\n"
			if serial {
				toml += "serial_verify = true\n"
			}
			h := newParallel(t, parallelPlan(
				"| a | **One** | — | ready | sonnet | agent | — |",
				"| b | **Two** | — | ready | sonnet | agent | — |",
				"| c | **Three** | a, b | blocked | sonnet | agent | — |",
			), toml)
			h.be.SetAutoSignal(func(_ context.Context, id string) error {
				if id == "c" {
					return h.signal(id)
				}
				return nil
			})
			h.repo()
			release, aStarted := make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			var order []string
			bDone := false
			h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
				id := c.Dir[len(c.Dir)-1:]
				mu.Lock()
				order = append(order, id)
				first := !slices.Contains(order[:len(order)-1], id)
				overlap := id == "a" && !bDone
				mu.Unlock()
				switch {
				case id == "b" && first:
					<-release
					mu.Lock()
					bDone = true
					mu.Unlock()
				case id == "a" && first:
					if overlap != !serial {
						t.Errorf("a's verify started while b's ran: %v, want %v", overlap, !serial)
					}
					close(aStarted)
				}
				return runner.Result{}, nil
			}
			h.signalAt(time.Minute, "b")
			h.signalAt(2*time.Minute, "a")
			h.clock.At(3*time.Minute, func() {
				if !serial {
					select { // a's verify runs beside b's
					case <-aStarted:
					case <-time.After(10 * time.Second):
						t.Error("a's verify didn't start while b's ran")
					}
				}
				close(release)
			})
			res, err := h.run()
			if err != nil || res.Outcome != Completed {
				t.Fatalf("Run = %s, %v", res.Outcome, err)
			}
			// The second task to integrate is verified again on the
			// integrated tree (SPEC §6.11): a when b passed first, b when
			// a's verify passed while b's still ran.
			want := "b a b c"
			if serial {
				want = "b a a c"
			}
			if got := strings.Join(order, " "); got != want {
				t.Errorf("verify order %q, want %s", got, want)
			}
			if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
				t.Errorf("statuses: %s", got)
			}
		})
	}
}

// A failed verify of a parallel phase goes back into its session and the
// task stays in progress, as anywhere (SPEC §6.4).
func TestParallelVerifyFails(t *testing.T) {
	h := newParallel(t, parallelPlan(
		"| a | **One** | — | ready | sonnet | agent | — |",
		"| b | **Two** | a | blocked | sonnet | agent | — |",
		"| c | **Three** | a | blocked | sonnet | agent | — |",
	), "needs_input_after = \"1h\"\n[run]\ncommit = \"auto\"\nverify = \"make check\"\n[parallel]\nmax = 2\n")
	h.repo()
	var mu sync.Mutex
	n := 0
	h.verifyFn = func(runner.Cmd) (runner.Result, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n == 1 {
			return runner.Result{ExitCode: 2, Stdout: []byte("FAIL: TestX\n")}, nil
		}
		return runner.Result{}, nil
	}
	h.signalAt(time.Minute, "a")
	h.signalAt(5*time.Minute, "a")
	h.signalAt(6*time.Minute, "b")
	h.signalAt(7*time.Minute, "c")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if p := h.be.Prompts("a"); len(p) != 2 || !strings.Contains(p[1], "FAIL: TestX") {
		t.Errorf("prompts of a: %q, want the failure sent back", p)
	}
	if got := h.event(TaskDone, "a").At.Sub(t0); got < 5*time.Minute {
		t.Errorf("done at %v, want after the second signal", got)
	}
}

// A stopped run keeps the task worktrees; the next run works in them
// again (SPEC §6.11, §13).
func TestParallelResumeReusesWorktrees(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	r := h.repo()
	h.clock.At(time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	run, err := h.dir.LoadRun()
	if err != nil || len(run.Active) != 2 {
		t.Fatalf("state after the stop: %+v, %v", run, err)
	}
	for _, c := range run.Active {
		if c.Worktree == nil || c.Worktree.Path != taskWT(base, c.TaskID) {
			t.Errorf("%s recorded worktree %+v", c.TaskID, c.Worktree)
		}
		if _, err := os.Stat(h.path(taskWT(base, c.TaskID))); err != nil {
			t.Errorf("%s worktree after the stop: %v", c.TaskID, err)
		}
	}

	h.events = nil
	h.signalAt(2*time.Minute, "a")
	h.signalAt(4*time.Minute, "b")
	h.signalAt(5*time.Minute, "c")
	res, err = h.run(func(o *Options) { o.Phase = "" })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed Run = %s, %v", res.Outcome, err)
	}
	if h.runBase() == base {
		t.Fatal("the resumed run has the same ID")
	}
	for _, id := range []string{"a", "b"} {
		if got := r.calls(": worktree add -b igris/" + base + "-" + id + " "); len(got) != 1 {
			t.Errorf("%s's worktree made %d times, want once: %q", id, len(got), got)
		}
		if got := r.calls(": worktree remove " + taskWT(base, id)); len(got) != 1 {
			t.Errorf("%s's worktree removed by %q, want once", id, got)
		}
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
}

// A task's worktree is named after the run's worktree, else after the
// worktrees of the tasks still active, else after the run (SPEC §6.11):
// state.json holds the worktrees of one run.
func TestTaskWorktreeName(t *testing.T) {
	old := &state.Worktree{Path: taskWT("20261001-090000-aaaa", "a"), Branch: "igris/20261001-090000-aaaa-a"}
	for _, tc := range []struct {
		name string
		run  state.Run
		want string
	}{
		{"new", state.Run{}, "20261006-120000-bbbb"},
		{"worktree mode", state.Run{Worktree: &state.Worktree{Path: filepath.Join(".igris", "worktrees", "20261002-090000-cccc"), Branch: "igris/20261002-090000-cccc"}}, "20261002-090000-cccc"},
		{"active", state.Run{Active: state.ActiveTasks{{TaskID: "x"}, {TaskID: "a", Worktree: old}}}, "20261001-090000-aaaa"},
	} {
		e := &Engine{runID: "20261006-120000-bbbb", run: &tc.run}
		got := e.taskWorktree("M1-03")
		if want := (state.Worktree{Path: taskWT(tc.want, "M1-03"), Branch: "igris/" + tc.want + "-M1-03"}); *got != want {
			t.Errorf("%s: %+v, want %+v", tc.name, *got, want)
		}
	}
}

// A task worktree that is gone is made again for the next session from
// its branch (SPEC §6.11).
func TestParallelGoneWorktreeIsRemade(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	r := h.repo()
	h.clock.At(time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
	if res, err := h.run(); err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	h.be.Kill(*run.Active[0].Session)
	wt := taskWT(base, "a")
	if err := os.RemoveAll(h.path(wt)); err != nil {
		t.Fatal(err)
	}

	h.events = nil
	h.on(func(ev Event) {
		if ev.Kind == SessionLost && ev.Task == "a" {
			h.eng.Send(Command{Kind: CmdRetry, Task: "a"})
		}
	})
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	h.signalAt(2*time.Minute, "b")
	res, err := h.run(func(o *Options) { o.Phase = "" })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed Run = %s, %v", res.Outcome, err)
	}
	got := r.calls(".: worktree ")
	i := slices.Index(got, ".: worktree prune")
	if want := ".: worktree add " + wt + " igris/" + base + "-a"; i < 0 || i+1 >= len(got) || got[i+1] != want {
		t.Errorf("git worktree calls %q, want a prune, then %q", got, want)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
}

// A reset task's worktree with uncommitted changes is kept and named; the
// task's next session works in it again, with its changes (SPEC §6.11).
func TestParallelResetKeepsDirtyWorktree(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	r := h.repo()
	h.confirmResets(true)
	h.resetAt(time.Minute, "a", false)
	h.continueWhenPaused()
	h.signalAt(5*time.Minute, "b")
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	wt := taskWT(base, "a")
	if !h.warned("the worktree " + wt + " of a has uncommitted changes and is kept") {
		t.Errorf("no warning that a's worktree is kept: %s", h.kinds())
	}
	if !h.warned("a works in the worktree " + wt + " kept from an earlier attempt") {
		t.Errorf("no warning that a works in its kept worktree: %s", h.kinds())
	}
	if got := r.calls(": worktree add -b igris/" + base + "-a "); len(got) != 1 {
		t.Errorf("a's worktree made %d times: %q", len(got), got)
	}
	if got := h.statuses(); got != "a=done b=done c=done B-1=ready" {
		t.Errorf("statuses: %s", got)
	}
}

// A run that would make a worktree of its own refuses while a task it
// picks up works in a worktree off the project's branch: state.json holds
// the worktrees of one run.
func TestParallelTaskWorktreeBlocksWorktreeMode(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	h.repo()
	h.clock.At(time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
	if res, err := h.run(); err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	_, err := h.run(func(o *Options) { o.Phase, o.Worktree = "", true })
	if err == nil || !strings.Contains(err.Error(), "the interrupted task a works in its worktree") {
		t.Fatalf("Run = %v, want a refusal", err)
	}
	if run, err := h.dir.LoadRun(); err != nil || run.Worktree != nil || len(run.Active) != 2 {
		t.Errorf("state.json after the refusal: %+v, %v", run, err)
	}
}

// In worktree mode a task's worktree is made from the run's branch and
// named after the run's worktree (SPEC §6.11).
func TestParallelTaskWorktreeInWorktreeMode(t *testing.T) {
	h := newParallel(t, parallelPlan(), parallelTOML)
	r := h.repo()
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	res, err := h.run(withWorktree)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	base := h.runBase()
	run := filepath.Join(".igris", "worktrees", base)
	if got, want := r.calls(" "+taskWT(base, "a")+" "), []string{".: worktree add -b igris/" + base + "-a " + taskWT(base, "a") + " igris/" + base}; !slices.Equal(got, want) {
		t.Errorf("a's worktree made by %q, want %q", got, want)
	}
	if got := r.calls(": branch -d igris/" + base + "-a"); len(got) != 1 || !strings.HasPrefix(got[0], run+": ") {
		t.Errorf("a's branch deleted by %q, want in the run's worktree", got)
	}
}
