package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// Worktree mode (SPEC §6.8): the run's sessions, verify, task hooks and
// commits happen in a git worktree on a branch of its own, so the owner's
// checkout stays as it is. The plan, igris.toml and .igris/ stay in the
// project root: igris reads and writes the plan there, and a session's
// `igris done` finds the root from inside the worktree (state.FindRoot).
// igris never merges the branch.

// WorktreeResult is the run's worktree when it ended, for the closing text.
type WorktreeResult struct {
	Path    string // relative to the project root
	Branch  string
	Removed bool // the run completed clean and the worktree was removed; the branch stays
	Done    bool // the run completed; otherwise the next igris arise continues in it
}

// worktreeOn says whether this run asked for worktree mode.
func (e *Engine) worktreeOn() bool { return e.opts.Worktree || e.cfg.Run.WorktreeOn() }

// worktreeAbs is the absolute path of a recorded worktree.
func (e *Engine) worktreeAbs(wt *state.Worktree) string {
	return inRoot(e.dir.Root(), wt.Path)
}

// WorktreeChoice is where a run works (SPEC §6.8).
type WorktreeChoice struct {
	Reuse    *state.Worktree // the last run's worktree, which this run continues in
	New      bool            // the run makes a new worktree
	Leftover *state.Worktree // the last run's worktree, still there; this run leaves it alone
	Gone     *state.Worktree // the worktree the interrupted task ran in, no longer there
}

// ChooseWorktree decides where a run in project root works. on: the run
// asks for worktree mode (--worktree or [run] worktree). resumes: no phase
// or selection is named, so the last run (prev, nil for none) is resumed.
// carried: a task in progress in that run is picked up again. The last
// run's worktree, while it is there, is used when the run asks for
// worktree mode, resumes or picks up its task; otherwise it is left alone.
// The engine, the dry run and the start-up checks share this.
func ChooseWorktree(root string, prev *state.Run, on, resumes, carried bool) WorktreeChoice {
	var c WorktreeChoice
	if prev != nil && prev.Worktree != nil {
		wt := prev.Worktree
		switch {
		case !isDir(inRoot(root, wt.Path)):
			if carried {
				c.Gone = wt
			}
		case on || resumes || carried:
			c.Reuse = wt
		default:
			c.Leftover = wt
		}
	}
	c.New = on && c.Reuse == nil
	return c
}

// pickWorktree decides, in prepare, which worktree the run uses
// (ChooseWorktree). A new one is made by enterWorktree once the run has
// its ID. carried is the interrupted task picked up first, if any.
func (e *Engine) pickWorktree(prev *state.Run, resumed bool, carried *state.Current) error {
	c := ChooseWorktree(e.dir.Root(), prev, e.worktreeOn(), resumed, carried != nil)
	if wt := c.Gone; wt != nil {
		id := carried.TaskID
		return fmt.Errorf("the interrupted task %s ran in the worktree %s (branch %s), which is gone (or no longer a directory): restore it with `git worktree add %s %s`, or reset the task with `igris reset %s`",
			id, wt.Path, wt.Branch, wt.Path, wt.Branch, id)
	}
	if c.New && prev != nil {
		// A task of a parallel phase works in a worktree off the project's
		// branch, and state.json holds the worktrees of one run.
		for _, a := range prev.Active {
			if wt := a.Worktree; wt != nil {
				return fmt.Errorf("the interrupted task %s works in its worktree %s, made from the project's branch, so this run can't work in a worktree of its own: finish it in a run without worktree mode, or reset it with `igris reset %s`", a.TaskID, wt.Path, a.TaskID)
			}
		}
	}
	e.run.Worktree, e.newWorktree, e.leftover = c.Reuse, c.New, c.Leftover
	return nil
}

// enterWorktree makes the run's new worktree if it needs one, and points
// the work dir at the run's worktree. It runs once the run has its ID.
func (e *Engine) enterWorktree(ctx context.Context) error {
	if e.newWorktree {
		wt := &state.Worktree{Path: filepath.Join(state.WorktreesDir, e.runID), Branch: "igris/" + e.runID}
		if _, err := e.gitIn(ctx, e.dir.Root(), "worktree", "add", "-b", wt.Branch, wt.Path, "HEAD"); err != nil {
			return fmt.Errorf("worktree mode: make the worktree %s: %w (it needs a git repository with at least one commit)", wt.Path, err)
		}
		e.run.Worktree = wt
		if err := e.dir.SaveRun(e.run); err != nil {
			return err
		}
	}
	if e.run.Worktree != nil {
		e.work = e.worktreeAbs(e.run.Worktree)
	}
	return nil
}

// announceWorktree reports the worktree the run works in, or the last
// run's that it leaves alone, after run_started.
func (e *Engine) announceWorktree(reused bool) {
	if wt := e.run.Worktree; wt != nil {
		how := "new worktree"
		if reused {
			how = "the worktree"
		}
		e.emit(Event{Kind: WorktreeReady, Detail: fmt.Sprintf("sessions, verify and commits run in %s %s on branch %s", how, wt.Path, wt.Branch)})
	}
	if wt := e.leftover; wt != nil {
		e.warn(fmt.Sprintf("the last run's worktree is still at %s (branch %s); this run works in the project root. Merge or remove it: git worktree remove %s", wt.Path, wt.Branch, wt.Path))
	}
}

// leaveWorktree ends the run's use of its worktree: a completed run whose
// worktree has nothing uncommitted removes it (the branch stays). Any
// other end keeps it, to resume or to look at.
func (e *Engine) leaveWorktree(ctx context.Context, res *Result) {
	wt := e.run.Worktree
	if wt == nil {
		return
	}
	res.Worktree = &WorktreeResult{Path: wt.Path, Branch: wt.Branch, Done: res.Outcome == Completed}
	defer func() { e.emit(Event{Kind: WorktreeLeft, Detail: strings.Join(res.Worktree.Closing(), "; ")}) }()
	if res.Outcome != Completed {
		return
	}
	out, err := e.gitIn(ctx, e.work, "status", "--porcelain")
	if err != nil || strings.TrimSpace(out) != "" {
		return
	}
	if _, err := e.gitIn(ctx, e.dir.Root(), "worktree", "remove", wt.Path); err != nil {
		e.warn(fmt.Sprintf("keep the worktree %s: %v", wt.Path, err))
		return
	}
	res.Worktree.Removed = true
	e.run.Worktree = nil
	e.work = e.dir.Root()
	if err := e.dir.SaveRun(e.run); err != nil {
		e.warn(err.Error())
	}
}

// worktreeAllow are the permission allow rules of a session in the run's
// worktree (SPEC §6.8, §7.4). The worktree lacks the project's untracked
// .claude/settings.local.json, so the session gets the `igris done` rules
// that file allows, no more, and may read the plan's files, which are
// outside the worktree. Claude Code reads a rule's path as a pattern, so a
// file whose path holds pattern characters gets no rule (the session asks
// instead): it could match more than that file.
func (e *Engine) worktreeAllow() []string {
	allow := checks.ProjectDoneRules(e.dir.Root())
	for _, f := range e.planFiles() {
		if !strings.ContainsAny(f, `*?[]{}!\`) {
			allow = append(allow, "Read(/"+f+")")
		}
	}
	return allow
}

// planFiles are the absolute paths of the plan's files as igris last read
// them: the main plan, then each file it includes (SPEC §3.7).
func (e *Engine) planFiles() []string {
	return append([]string{e.planPath}, e.included...)
}

// inWorktree says whether l's sessions work in a worktree: the run's
// (SPEC §6.8) or the task's own (§6.11).
func (e *Engine) inWorktree(l *launch) bool {
	return (e.run != nil && e.run.Worktree != nil) || l.isolated()
}

// planFile is the plan's path as l's task prompt names it: as configured,
// or the absolute path in the project root from a worktree, whose own copy
// of the plan igris doesn't update (SPEC §6.8, §6.11).
func (e *Engine) planFile(l *launch) string {
	if e.inWorktree(l) {
		return e.planPath
	}
	return e.cfg.Plan
}

// taskFile is the file l's task's row is in as the task prompt names it
// (SPEC §6.1): planFile for a row of the main plan, else the include's
// path joined to planFile's directory, so in the same form.
func (e *Engine) taskFile(l *launch) string {
	if t := l.t; t.Phase != nil && t.Phase.Include != "" {
		return filepath.Join(filepath.Dir(e.planFile(l)), filepath.FromSlash(t.Phase.Include))
	}
	return e.planFile(l)
}

// isDir reports whether path is a directory itself, not a symlink to one:
// a session could put a link to the owner's checkout in place of the
// run's worktree (V06-07).
func isDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}

// gitIn runs git in dir and returns its output; a non-zero exit is an
// error.
func (e *Engine) gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := e.runner.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: dir, Env: e.gitEnv(), Timeout: gitTimeout})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}

// Closing returns the lines that tell the owner where a worktree run's
// work is (SPEC §6.8); nil when the run used no worktree.
func (w *WorktreeResult) Closing() []string {
	if w == nil {
		return nil
	}
	if !w.Done {
		return []string{fmt.Sprintf("the run's work so far is on branch %s, in the worktree %s; igris arise continues there", w.Branch, w.Path)}
	}
	lines := []string{fmt.Sprintf("the run's work is on branch %s; igris doesn't merge it: git merge %s", w.Branch, w.Branch)}
	if !w.Removed {
		lines = append(lines, fmt.Sprintf("its worktree %s has uncommitted changes and is kept", w.Path))
	}
	return lines
}

// A task's own worktree in a parallel phase (SPEC §6.11): its sessions,
// verify, task hooks and the git of its commit run in
// .igris/worktrees/<run-id>-<ID> on igris/<run-id>-<ID>, made from the run
// branch's head. The plan, igris.toml and .igris/ stay in the project
// root, as in worktree mode; the worktree's copy of the plan is never
// written.

// isolated says whether l's task works in a worktree of its own: it holds
// a slot in a parallel phase, or state.json records its worktree.
func (l *launch) isolated() bool {
	return l.cur != nil && (l.cur.Slot != nil || l.cur.Worktree != nil)
}

// workDir is where l's sessions, verify, task hooks and git run: its own
// worktree once it has one, else where the run works (the project root or
// the run's worktree, SPEC §6.8).
func (e *Engine) workDir(l *launch) string {
	if l != nil && l.cur != nil && l.cur.Worktree != nil {
		return e.worktreeAbs(l.cur.Worktree)
	}
	return e.work
}

// slotEnv is IGRIS_SLOT for l's task in a parallel phase, for its session,
// verify and task hooks (SPEC §6.11); nil outside one. run.env doesn't
// drop it: it is added after the environment is limited.
func slotEnv(l *launch) []string {
	if l == nil || l.cur == nil || l.cur.Slot == nil {
		return nil
	}
	return []string{fmt.Sprintf("IGRIS_SLOT=%d", *l.cur.Slot)}
}

// taskWorktree is the worktree task id gets in a parallel phase. Its
// <run-id> is the run's worktree's, or else that of the worktrees of the
// tasks still active, which a resumed run keeps (state.json holds the
// worktrees of one run, SPEC §13), or else this run's ID.
func (e *Engine) taskWorktree(id string) *state.Worktree {
	base := e.runID
	if wt := e.run.Worktree; wt != nil {
		base = strings.TrimPrefix(wt.Branch, "igris/")
	} else {
		for _, c := range e.run.Active {
			if c.Worktree != nil {
				base = strings.TrimSuffix(strings.TrimPrefix(c.Worktree.Branch, "igris/"), "-"+c.TaskID)
				break
			}
		}
	}
	return &state.Worktree{Path: filepath.Join(state.WorktreesDir, base+"-"+id), Branch: "igris/" + base + "-" + id}
}

// runBranch is the start point of a task's worktree: the run branch, the
// run's worktree's branch in worktree mode, else the branch checked out in
// the project root (SPEC §6.11).
func (e *Engine) runBranch() string {
	if wt := e.run.Worktree; wt != nil {
		return wt.Branch
	}
	return "HEAD"
}

// ensureWorktree gives l's task in a parallel phase its worktree before a
// session igris opens for it (SPEC §6.11): after in progress is written,
// before before_task. A worktree state.json records is used again while it
// is there (a retry, a resumed run); one that is gone is made again from
// its branch if that is still there, else anew from the run branch's
// head. A worktree there but not recorded (kept for its uncommitted
// changes after a reset, or left by a crash before state.json recorded
// it) is taken over; how says whether a first session does, with a
// warning. A dry run makes none.
func (e *Engine) ensureWorktree(ctx context.Context, l *launch, how startKind) error {
	if !l.t.Owner.IsAgent() || !l.isolated() || e.opts.noGit {
		return nil
	}
	id, root := l.t.ID, e.dir.Root()
	wt := l.cur.Worktree
	if wt == nil {
		wt = e.taskWorktree(id)
	}
	abs := e.worktreeAbs(wt)
	switch _, err := os.Lstat(abs); {
	case isDir(abs):
		if l.cur.Worktree == nil && how == startNew {
			// Kept for its uncommitted changes when the task was reset:
			// they stay for the next session, as in the shared tree.
			e.warn(fmt.Sprintf("%s works in the worktree %s kept from an earlier attempt, with its changes (branch %s)", id, wt.Path, wt.Branch))
		}
	case err == nil:
		return fmt.Errorf("%s is not a directory igris made for %s: remove it, then run `igris arise` again", wt.Path, id)
	default:
		var gitErr error
		if _, err := e.gitIn(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+wt.Branch); err == nil {
			// The branch outlived its worktree: git still lists the
			// missing directory until it is pruned.
			if _, gitErr = e.gitIn(ctx, root, "worktree", "prune"); gitErr == nil {
				_, gitErr = e.gitIn(ctx, root, "worktree", "add", wt.Path, wt.Branch)
			}
		} else {
			_, gitErr = e.gitIn(ctx, root, "worktree", "add", "-b", wt.Branch, wt.Path, e.runBranch())
		}
		if gitErr != nil {
			return fmt.Errorf("make the worktree %s of %s: %w; fix what git reports, then run `igris arise` again", wt.Path, id, gitErr)
		}
	}
	if l.cur.Worktree == nil {
		l.cur.Worktree = wt
		return e.dir.SaveRun(e.run)
	}
	return nil
}

// discardWorktree throws away the worktree and the branch of rec's task,
// with whatever they hold, before the task is run again from the start
// after a failed integration (SPEC §6.11). rec forgets the worktree, so
// the next session gets a new one from the run branch's head.
func (e *Engine) discardWorktree(ctx context.Context, rec *state.Current) error {
	wt := rec.Worktree
	if wt == nil || e.opts.noGit {
		return nil
	}
	root := e.dir.Root()
	if isDir(e.worktreeAbs(wt)) {
		if _, err := e.gitIn(ctx, root, "worktree", "remove", "--force", wt.Path); err != nil {
			return fmt.Errorf("discard the worktree %s of %s: %w; remove it by hand, then run `igris arise` again", wt.Path, rec.TaskID, err)
		}
	} else if _, err := e.gitIn(ctx, root, "worktree", "prune"); err != nil {
		e.warn(fmt.Sprintf("prune the worktrees: %v", err))
	}
	if _, err := e.gitIn(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+wt.Branch); err == nil {
		if _, err := e.gitIn(ctx, e.work, "branch", "-D", wt.Branch); err != nil {
			return fmt.Errorf("discard the branch %s of %s: %w; delete it by hand, then run `igris arise` again", wt.Branch, rec.TaskID, err)
		}
	}
	rec.Worktree = nil
	return e.dir.SaveRun(e.run)
}

// dropWorktree removes the worktree of rec's task once the task is done,
// skipped or reset (SPEC §6.11), and its branch with it. A worktree with
// uncommitted changes is kept and named, and so is a branch with commits
// the run branch doesn't have (`git branch -d` refuses it).
func (e *Engine) dropWorktree(ctx context.Context, rec *state.Current) {
	if rec == nil || rec.Worktree == nil || e.opts.noGit {
		return
	}
	wt, root := rec.Worktree, e.dir.Root()
	if abs := e.worktreeAbs(wt); isDir(abs) {
		out, err := e.gitIn(ctx, abs, "status", "--porcelain")
		if err != nil || strings.TrimSpace(out) != "" {
			e.warn(fmt.Sprintf("the worktree %s of %s has uncommitted changes and is kept (branch %s)", wt.Path, rec.TaskID, wt.Branch))
			return
		}
		if _, err := e.gitIn(ctx, root, "worktree", "remove", wt.Path); err != nil {
			e.warn(fmt.Sprintf("keep the worktree %s of %s: %v", wt.Path, rec.TaskID, err))
			return
		}
	}
	if _, err := e.gitIn(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+wt.Branch); err != nil {
		return // gone already
	}
	// In the run's work dir, where the run branch is checked out: -d
	// deletes only a branch merged into it.
	if _, err := e.gitIn(ctx, e.work, "branch", "-d", wt.Branch); err != nil {
		e.warn(fmt.Sprintf("the branch %s of %s has commits the run branch doesn't and is kept: git merge %s", wt.Branch, rec.TaskID, wt.Branch))
	}
}
