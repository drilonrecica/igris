package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Integration (SPEC §6.11). A task of a parallel phase works in a
// worktree of its own, so its done is not the end: once verified (or
// marked done by the owner), igris commits there, rebases the task's
// branch onto the run branch, verifies the integrated tree and
// fast-forwards the run branch. Only then is the task marked done, so a
// dependent never starts on code that isn't on the run branch. Tasks are
// integrated one at a time, in the order they became ready; the history
// stays linear, one commit per task. The commit question and the
// integrator never hold the other tasks: each runs as the task's step,
// beside the poll loop.

// integStage is how far a task's integration got.
type integStage int

const (
	integStart  integStage = iota // nothing done yet
	integAsk                      // the commit question waits for the owner (commit = "ask")
	integQueued                   // committed; waits for the integrator, which takes one task at a time
	integVerify                   // rebased; its verify runs on the integrated tree (check)
	integFailed                   // didn't integrate, and igris won't run it again by itself: the owner decides
)

// integration is the state of one task's integration, on its launch from
// its accepted done until it is marked done, rerun or let go of.
type integration struct {
	stage integStage
	note  string // the done note
	// owner says the owner marked the task done: verify is skipped on the
	// integrated tree too (SPEC §6.4, §6.11).
	owner bool
	// msg and approved are the commit subject and the approved plan the
	// commit carries, rendered once; msg is "" when the commit was made
	// by an earlier run.
	msg, approved string
	// target and branch are the run branch's head the task was rebased
	// onto and its name ("" with a detached HEAD in the project root).
	target, branch string
}

// maxConflictFiles bounds the conflicting files a failed rebase names.
const maxConflictFiles = 20

// beginIntegration starts integrating l's task after its done v was
// verified, or the owner's done (SPEC §6.11), and takes the first steps
// at once.
func (e *Engine) beginIntegration(ctx context.Context, l *launch, v verdict) (ended, stopped bool, err error) {
	l.integ = &integration{note: v.note, owner: v.sig == nil}
	return e.stepIntegrate(ctx, l)
}

// stepIntegrate is a poll of l's task while it integrates: the owner's
// commands for it, then whatever its stage can do now without waiting.
// ended reports that the task is no longer active (done or skipped);
// a rerun leaves it active with a new session.
func (e *Engine) stepIntegrate(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	if e.resetWaiting(l.t.ID) {
		// A reset request for the task is answered before it is committed
		// or marked (SPEC §6.2); the poll loop applies a confirmed one.
		return false, false, nil
	}
	g := l.integ
	for _, c := range e.takeCommands(l) {
		switch {
		case g.stage == integAsk && c.Kind == CmdAnswer:
			if c.Yes {
				e.waitOver(state.ReasonCommit)
				if err := e.commitChanges(ctx, l, g.msg, g.note, g.approved); err != nil {
					return false, false, err
				}
				e.queueIntegration(l)
				continue
			}
			e.emit(Event{Kind: NotCommitted, Detail: "the owner declined"})
			e.needsYou(ctx, notifyNeedsInput, state.ReasonCommit, "the commit of "+l.t.ID+" was declined; its worktree is kept: answer the commit question again, retry, skip or stop", "commit declined")
			e.askCommit(l, g.msg)
		case g.stage == integAsk && c.Kind == CmdRetry:
			// The owner wants more from the session: its done is withdrawn.
			if err := e.dir.RemoveSignal(l.t.ID); err != nil {
				return false, false, err
			}
			l.integ = nil
			if err := e.retry(ctx, l, c.Continue); err != nil {
				return false, false, err
			}
			l.ep, l.stateErr = episode{}, ""
			return false, false, nil
		case g.stage == integFailed && c.Kind == CmdRetry && !c.Continue:
			return false, false, e.rerun(ctx, l)
		case g.stage == integFailed && c.Kind == CmdRetry:
			e.reject(c, l.t.ID+" is run again from the start after a failed integration; retry fresh")
		case g.stage == integFailed && c.Kind == CmdDone:
			// The owner integrated it by hand (SPEC §6.11).
			e.waitOver(state.ReasonIntegration)
			note := strings.TrimSpace(c.Text)
			if note == "" {
				note = g.note
			}
			e.unqueueIntegration(l)
			l.integ = nil
			return true, false, e.finish(ctx, l, plan.Done, note)
		case c.Kind == CmdSkip && (g.stage == integAsk || g.stage == integFailed):
			e.waitOver(state.ReasonCommit)
			e.waitOver(state.ReasonIntegration)
			e.unqueueIntegration(l)
			l.integ = nil
			return true, false, e.finish(ctx, l, plan.Skipped, c.Text)
		case g.stage == integAsk:
			e.reject(c, "answer the commit question of "+l.t.ID+" first")
		case g.stage == integFailed:
			e.reject(c, l.t.ID+" didn't integrate: retry fresh, mark it done, skip it or stop")
		default:
			e.reject(c, l.t.ID+" is being integrated; it takes no "+c.Kind.String()+" now")
		}
	}
	switch g.stage {
	case integStart:
		return e.startIntegration(ctx, l)
	case integQueued:
		if len(e.integrations) == 0 || e.integrations[0] != l {
			return false, false, nil // another task integrates first
		}
		return e.integrate(ctx, l)
	case integVerify:
		return e.integrationVerified(ctx, l)
	}
	return false, false, nil
}

// startIntegration commits l's work in its worktree, or asks first under
// commit = "ask" (SPEC §6.5, §6.11). Nothing to commit and no commit of
// its own to integrate: the task is marked done without a rebase.
func (e *Engine) startIntegration(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	g := l.integ
	if e.cfg.Run.Commit == CommitNever {
		// A parallel phase never runs with it (parallelOn); a task that
		// kept a worktree from such a run is marked done as is.
		l.integ = nil
		return true, false, e.finish(ctx, l, plan.Done, g.note)
	}
	status, err := e.git(ctx, l, "status", "--porcelain")
	if err != nil {
		return false, false, err
	}
	if strings.TrimSpace(status) == "" {
		if g.target, g.branch, err = e.runTarget(ctx); err != nil {
			return false, false, err
		}
		ahead, err := e.aheadOf(ctx, l, g.target)
		if err != nil {
			return false, false, err
		}
		if ahead == 0 {
			e.emit(Event{Kind: NotCommitted, Detail: "nothing to commit"})
			l.integ = nil
			return true, false, e.finish(ctx, l, plan.Done, g.note)
		}
		// Committed by an earlier run, which stopped before it integrated.
		e.emit(Event{Kind: NotCommitted, Detail: "nothing to commit; its branch has a commit to integrate"})
		e.queueIntegration(l)
		return e.stepIntegrate(ctx, l)
	}
	if g.msg, g.approved, err = e.commitMessage(l, g.note); err != nil {
		return false, false, err
	}
	if e.cfg.Run.Commit == CommitAsk {
		g.stage = integAsk
		e.waitOn(state.ReasonCommit, "commit question")
		e.askCommit(l, g.msg)
		return false, false, nil
	}
	if err := e.commitChanges(ctx, l, g.msg, g.note, g.approved); err != nil {
		return false, false, err
	}
	e.queueIntegration(l)
	return e.stepIntegrate(ctx, l)
}

// askCommit asks the commit question for l's task, without waiting for
// the answer: the task's next steps take it.
func (e *Engine) askCommit(l *launch, msg string) {
	e.emit(Event{Kind: Asked, Question: QuestionCommit, Detail: fmt.Sprintf("commit the changes of %s as %q?", l.t.ID, msg)})
}

// queueIntegration puts l's committed task in line for the integrator.
func (e *Engine) queueIntegration(l *launch) {
	l.integ.stage = integQueued
	if !slices.Contains(e.integrations, l) {
		e.integrations = append(e.integrations, l)
	}
}

// unqueueIntegration takes l out of the integrator's line.
func (e *Engine) unqueueIntegration(l *launch) {
	e.integrations = slices.DeleteFunc(e.integrations, func(o *launch) bool { return o == l })
}

// integrate rebases l's branch onto the run branch and verifies the
// integrated tree (SPEC §6.11): the integrator's work for the task at the
// head of its line. A rebase that changed nothing skips the verify, since
// that tree was just verified; so does the owner's done.
func (e *Engine) integrate(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	g := l.integ
	if g.target, g.branch, err = e.runTarget(ctx); err != nil {
		return false, false, err
	}
	before, err := e.git(ctx, l, "rev-parse", "HEAD")
	if err != nil {
		return false, false, err
	}
	if _, err := e.git(ctx, l, "rebase", g.target); err != nil {
		if ctx.Err() != nil {
			return false, false, err
		}
		files := e.conflictFiles(ctx, l)
		if _, err := e.git(ctx, l, "rebase", "--abort"); err != nil {
			e.warn(fmt.Sprintf("abort the rebase of %s: %v; its worktree %s may be left mid-rebase", l.t.ID, err, l.cur.Worktree.Path))
		}
		why := "the rebase onto the run branch conflicts"
		if len(files) > 0 {
			why += " in " + strings.Join(files, ", ")
		}
		return e.integrationFailed(ctx, l, state.FailedConflict, strings.Join(files, ", "), why, files)
	}
	after, err := e.git(ctx, l, "rev-parse", "HEAD")
	if err != nil {
		return false, false, err
	}
	if strings.TrimSpace(after) == strings.TrimSpace(before) || g.owner || !e.verifies(l.t) {
		return e.fastForward(ctx, l)
	}
	l.check = &check{}
	e.checks = append(e.checks, l)
	g.stage = integVerify
	e.startChecks(ctx)
	return false, false, nil
}

// conflictFiles are the files the rebase of l's task stopped on, at most
// maxConflictFiles, as git names them; none when git can't say.
func (e *Engine) conflictFiles(ctx context.Context, l *launch) []string {
	out, err := e.git(ctx, l, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f = textsafe.Line(strings.TrimSpace(f)); f != "" {
			files = append(files, f)
		}
	}
	if len(files) > maxConflictFiles {
		files = files[:maxConflictFiles]
	}
	return files
}

// integrationVerified acts on the result of l's verify on the integrated
// tree once its check ended: a pass fast-forwards, a failure is a failed
// integration (SPEC §6.11).
func (e *Engine) integrationVerified(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	k := l.check
	if k == nil || !k.started {
		return false, false, nil // waits for its turn (serial_verify)
	}
	select {
	case <-k.done:
	default:
		return false, false, nil
	}
	e.dropCheck(l)
	e.startChecks(ctx)
	took := e.sinceMS(k.began)
	var why string
	switch {
	case errors.Is(k.err, runner.ErrTimeout):
		why = "timed out after " + e.cfg.Run.VerifyTimeout.Std().String()
	case k.err != nil:
		if ctx.Err() != nil {
			return false, false, k.err
		}
		return false, false, fmt.Errorf("run the verify command for %s on the integrated tree: %w", l.t.ID, k.err)
	case k.res.ExitCode != 0:
		why = fmt.Sprintf("exit status %d", k.res.ExitCode)
	default:
		e.log(state.Event{Type: state.EventVerifyPassed, Profile: k.profile, DurationMS: took, Detail: "profile " + k.profile + ": integrated tree"})
		e.emit(Event{Kind: VerifyPassed, Detail: k.script})
		return e.fastForward(ctx, l)
	}
	e.log(state.Event{Type: state.EventVerifyFailed, Profile: k.profile, DurationMS: took, Detail: fmt.Sprintf("profile %s: integrated tree: %s", k.profile, why)})
	e.emit(Event{Kind: VerifyFailed, Detail: fmt.Sprintf("%s (`%s`) failed (%s) on the integrated tree", k.profile, k.script, why)})
	lines := strings.Split(tail(textsafe.Clean(string(k.res.Stdout)), hookTailLines), "\n")
	detail := fmt.Sprintf("profile %s: %s", k.profile, why)
	return e.integrationFailed(ctx, l, state.FailedVerify, detail, "verify failed on the integrated tree ("+detail+")", lines)
}

// fastForward moves the run branch to l's rebased branch, logs
// integrated and marks the task done (SPEC §6.11).
func (e *Engine) fastForward(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	g := l.integ
	wt := l.cur.Worktree
	if _, err := e.gitIn(ctx, e.work, "merge", "--ff-only", wt.Branch); err != nil {
		if ctx.Err() != nil {
			return false, false, err
		}
		// The run branch moved since the rebase (a commit in the project
		// root, say): a rerun starts from its new head.
		why := "the run branch moved since the rebase; fast-forward failed"
		return e.integrationFailed(ctx, l, state.FailedConflict, why, why, nil)
	}
	subject := g.msg
	if subject == "" {
		if out, err := e.git(ctx, l, "log", "-1", "--format=%s"); err == nil {
			subject = textsafe.Line(strings.TrimSpace(out))
		}
	}
	head := e.headIn(ctx, e.work)
	e.log(state.Event{Type: state.EventIntegrated, Commit: head, Branch: g.branch, Detail: subject})
	onto := g.branch
	if onto == "" {
		onto = "the run branch"
	}
	e.emit(Event{Kind: Integrated, Detail: fmt.Sprintf("onto %s: %s", onto, subject)})
	e.unqueueIntegration(l)
	l.integ = nil
	return true, false, e.finish(ctx, l, plan.Done, g.note)
}

// integrationFailed handles a task that didn't integrate (SPEC §6.11): a
// rebase conflict (reason state.FailedConflict; detail names the files,
// lines list them) or verify failing on the integrated tree
// (state.FailedVerify; detail is the profile and why, lines its last lines
// of output). why says it for the owner. With on_conflict = "rerun" the
// task is run again from the start, once per task and run; else, or the
// second time, the owner decides.
func (e *Engine) integrationFailed(ctx context.Context, l *launch, reason, detail, why string, lines []string) (ended, stopped bool, err error) {
	id := l.t.ID
	e.unqueueIntegration(l)
	what := "didn't integrate: " + why
	rerun := e.cfg.Parallel.OnConflictOrDefault() == config.OnConflictRerun && !e.reran[id]
	then := "then needs you"
	if rerun {
		then = "then rerun"
	}
	e.log(state.Event{Type: state.EventIntegrationFailed, Reason: reason, Detail: textsafe.Line(detail + "; " + then)})
	if rerun {
		e.reran[id] = true
		e.emit(Event{Kind: IntegrationFailed, Detail: what + "; igris runs the task again from the start", Output: lines})
		return false, false, e.rerun(ctx, l)
	}
	l.integ.stage = integFailed
	e.emit(Event{Kind: IntegrationFailed, Detail: what + "; its worktree is kept", Output: lines})
	e.needsYou(ctx, notifyNeedsInput, state.ReasonIntegration, id+" "+what+"; rerun it fresh, mark it done, skip it or stop", "integration failed")
	e.emit(Event{Kind: Asked, Question: QuestionIntegration, Detail: id + " " + what + ": rerun it fresh, mark it done (you integrated it by hand), skip it, or stop", Output: lines})
	return false, false, nil
}

// runTarget is what a task's branch is rebased onto: the run branch
// (SPEC §6.11) by name, or by its head commit when the project root has
// no branch checked out. branch is its name, "" then.
func (e *Engine) runTarget(ctx context.Context) (target, branch string, err error) {
	if wt := e.run.Worktree; wt != nil {
		return wt.Branch, wt.Branch, nil
	}
	if out, err := e.gitIn(ctx, e.work, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		if branch = textsafe.Line(strings.TrimSpace(out)); branch != "" {
			return branch, branch, nil
		}
	} else if ctx.Err() != nil {
		return "", "", err
	}
	out, err := e.gitIn(ctx, e.work, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("find the run branch's head in %s: %w", e.work, err)
	}
	sha := strings.TrimSpace(out)
	if !commitSHA.MatchString(sha) {
		return "", "", fmt.Errorf("find the run branch's head: git rev-parse HEAD printed %q, not a commit hash", textsafe.Line(sha))
	}
	return sha, "", nil
}

// aheadOf is how many commits l's branch has that target doesn't.
func (e *Engine) aheadOf(ctx context.Context, l *launch, target string) (int, error) {
	out, err := e.git(ctx, l, "rev-list", "--count", target+"..HEAD")
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); err != nil {
		return 0, fmt.Errorf("count the commits of %s to integrate: git rev-list printed %q", l.t.ID, textsafe.Line(strings.TrimSpace(out)))
	}
	return n, nil
}

// headIn is the commit HEAD points at in dir, for the run log; "" with a
// warning if git can't say.
func (e *Engine) headIn(ctx context.Context, dir string) string {
	out, err := e.gitIn(ctx, dir, "rev-parse", "HEAD")
	sha := strings.TrimSpace(out)
	if err == nil && !commitSHA.MatchString(sha) {
		err = fmt.Errorf("git rev-parse HEAD printed %q, not a commit hash", textsafe.Line(sha))
	}
	if err != nil {
		e.warn(fmt.Sprintf("the commit hash is left out of the run log: %v", err))
		return ""
	}
	return sha
}

// rerun runs l's task again from the start after a failed integration
// (SPEC §6.11): its session is closed, its worktree and branch discarded,
// and a fresh session (Resumed false) opens in a new worktree from the run
// branch's head, as a new attempt.
func (e *Engine) rerun(ctx context.Context, l *launch) error {
	id := l.t.ID
	e.unqueueIntegration(l)
	l.integ = nil
	e.emit(Event{Kind: Retrying, Detail: "fresh"})
	e.dropPlan(l, "the task is run again")
	e.waitsOver()
	l.cur.VerifyAttempts, l.cur.ApprovedPlan = 0, ""
	if err := e.dir.RemoveSignal(id); err != nil {
		return err
	}
	if l.sess != nil && !l.lost {
		if err := l.sess.Close(ctx); err != nil {
			e.warn(fmt.Sprintf("close the session of %s: %v; close its pane by hand", id, err))
		}
	}
	e.endUsage(ctx, l)
	if err := e.discardWorktree(ctx, l.cur); err != nil {
		return err
	}
	l.lost, l.ep, l.stateErr = false, episode{}, ""
	st, err := e.prepareSession(l, startNew)
	if err != nil {
		return err
	}
	e.nextAttempt(l)
	e.log(state.Event{Type: state.EventTaskRetried, Session: st.sessionID, Detail: "fresh", Slot: l.cur.Slot, Branch: e.taskWorktree(id).Branch})
	if err := e.ensureWorktree(ctx, l, startNew); err != nil {
		return err
	}
	if open, err := e.beforeSession(ctx, l); err != nil || !open {
		return err
	}
	return e.openSession(ctx, l, st)
}

// Overlap watcher (SPEC §6.11). Every poll igris runs git status in each
// active task's worktree and warns the first time two active tasks both
// change one file, once per pair of tasks and file. Nothing is held: the
// integration finds out whether it matters.

// watchOverlap looks for files two active tasks both change.
func (e *Engine) watchOverlap(ctx context.Context) {
	if e.opts.noGit {
		return
	}
	var ls []*launch
	for _, l := range e.active {
		if l.cur != nil && l.cur.Worktree != nil && isDir(e.worktreeAbs(l.cur.Worktree)) {
			ls = append(ls, l)
		}
	}
	if len(ls) < 2 {
		return
	}
	changed := map[string][]*launch{}
	for _, l := range ls {
		out, err := e.gitIn(ctx, e.worktreeAbs(l.cur.Worktree), "status", "--porcelain", "-z")
		if err != nil {
			if ctx.Err() == nil {
				e.reportOnce("overlap|"+l.t.ID, Warning, fmt.Sprintf("the overlap check can't read the worktree of %s: %v", l.t.ID, err))
			}
			continue
		}
		files := map[string]bool{}
		for _, f := range porcelainPaths(out) {
			files[f] = true
			changed[f] = append(changed[f], l)
		}
		l.fresh = map[string]bool{}
		for f := range files {
			if !l.files[f] {
				l.fresh[f] = true
			}
		}
		l.files = files
	}
	paths := make([]string, 0, len(changed))
	for f, ls := range changed {
		if len(ls) > 1 {
			paths = append(paths, f)
		}
	}
	slices.Sort(paths)
	for _, f := range paths {
		ls := changed[f]
		for i := 0; i < len(ls); i++ {
			for j := i + 1; j < len(ls); j++ {
				a, b := ls[i], ls[j]
				key := "overlap|" + a.t.ID + "|" + b.t.ID + "|" + f
				if e.reported[key] {
					continue
				}
				e.reported[key] = true
				// The one that changed it last: the one it is new for at
				// this poll, else the later of the two.
				last := b
				if a.fresh[f] && !b.fresh[f] {
					last = a
				}
				detail := fmt.Sprintf("%s and %s both change %s", a.t.ID, b.t.ID, textsafe.Line(f))
				e.logRun(state.Event{Type: state.EventOverlap, Task: last.t.ID, Rank: last.t.Rank, Model: last.model, Attempt: last.attempt, Detail: detail})
				e.emit(last.fill(Event{Kind: Overlap, Detail: detail}))
			}
		}
	}
}

// porcelainPaths are the paths of `git status --porcelain -z`: one per
// entry, the new path of a rename or copy (its original follows as an
// entry of its own and is skipped).
func porcelainPaths(out string) []string {
	var paths []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		ent := entries[i]
		if len(ent) < 4 || ent[2] != ' ' {
			continue
		}
		xy, path := ent[:2], ent[3:]
		if xy == "!!" {
			continue // ignored files are shown only with --ignored
		}
		paths = append(paths, path)
		if xy[0] == 'R' || xy[0] == 'C' || xy[1] == 'R' || xy[1] == 'C' {
			i++ // the original path
		}
	}
	return paths
}
