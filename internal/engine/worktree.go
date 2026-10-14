package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
// its ID.
func (e *Engine) pickWorktree(prev *state.Run, resumed, carried bool) error {
	c := ChooseWorktree(e.dir.Root(), prev, e.worktreeOn(), resumed, carried)
	if wt := c.Gone; wt != nil {
		id := prev.Current.TaskID
		return fmt.Errorf("the interrupted task %s ran in the worktree %s (branch %s), which is gone: restore it with `git worktree add %s %s`, or reset the task with `igris reset %s`",
			id, wt.Path, wt.Branch, wt.Path, wt.Branch, id)
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

// planFile is the plan's path as the task prompt names it: as configured,
// or the absolute path in the project root from a worktree, whose own copy
// of the plan igris doesn't update (SPEC §6.8).
func (e *Engine) planFile() string {
	if e.run != nil && e.run.Worktree != nil {
		return e.planPath
	}
	return e.cfg.Plan
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// gitIn runs git in dir and returns its output; a non-zero exit is an
// error.
func (e *Engine) gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := e.runner.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: dir, Timeout: gitTimeout})
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
