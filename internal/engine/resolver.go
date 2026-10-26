package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The resolver session (SPEC §6.11, decisions.md V18-P2). A task that
// doesn't integrate for a rebase conflict or for verify failing on the
// integrated tree can get one resolver session: a fresh session of the
// task, in its worktree, with the conflict, both sides and the task's text,
// on the task's rank or [parallel] resolver_rank. It ends by the completion
// protocol only: `igris done` means resolved, `igris skip` that it gave up.
// After its done igris finishes the rebase (or folds the fix into the
// task's commit), checks the branch's shape, verifies the integrated tree
// and fast-forwards as for any task. A failed resolution is followed by the
// one rerun, then Needs you: resolve → rerun → ask, at most one resolver
// and one rerun per task and run. The integrator's line is held while the
// resolver works; the other sessions go on. It is not an attempt: its
// usage line carries the task's attempt and `"resolver": true`.

// failure is why a task didn't integrate, kept with its integration so the
// owner's Resolve can open the resolver for it later (SPEC §6.11).
type failure struct {
	reason  string   // state.FailedConflict or FailedVerify
	detail  string   // the run log's detail: the files or the verify profile and why
	why     string   // for the owner: "the rebase onto the run branch conflicts in a.go"
	lines   []string // the conflicting files or the verify output's last lines, for the UI and the resolver
	profile string   // the verify profile, for reason verify
	// resolvable says a resolver can settle it: a rebase conflict with
	// files, or verify failing on the integrated tree. A refused
	// fast-forward has nothing to resolve.
	resolvable bool
}

// resolverRun is the resolver session of a task while it works, and what
// it was opened for.
type resolverRun struct {
	reason      string // state.FailedConflict or FailedVerify
	rank, model string // its rank and the model it resolved to (invariant 1)
	files       []string
	lines       []string
	profile     string
	taskModel   string // l.model before the resolver, restored after it
}

// cardText is the card's words while the resolver works (SPEC §15.1).
func (r *resolverRun) cardText() string {
	if r.reason == state.FailedVerify {
		return "fixing the integrated tree"
	}
	if n := len(r.files); n > 0 {
		return "resolving a conflict in " + look.Count(n, "file", "files")
	}
	return "resolving a conflict"
}

// resolving is l's resolver session while one works on the task, else nil.
func (l *launch) resolving() *resolverRun {
	if l.integ != nil && l.integ.stage == integResolve {
		return l.integ.resolver
	}
	return nil
}

// rank is the rank of l's session now: the resolver's while one works
// (SPEC §6.11), else the task's. The scheduler's caps count the task's.
func (l *launch) rank() string {
	if r := l.resolving(); r != nil {
		return r.rank
	}
	return l.t.Rank
}

// resolverRank is the rank a resolver session of t runs on: [parallel]
// resolver_rank, or the task's own (SPEC §6.11), and the model it resolves
// to. Igris never picks one.
func (e *Engine) resolverRank(t *plan.Task) (rank, model string, err error) {
	rank = e.cfg.Parallel.ResolverRank
	if rank == "" {
		rank = t.Rank
	}
	model, ok := e.cfg.Models[rank]
	if !ok {
		return "", "", fmt.Errorf("task %s: the resolver's rank %q is not a [models] key in igris.toml", t.ID, rank)
	}
	return rank, model, nil
}

// willResolve says whether a failure f of l's integration opens the
// resolver (SPEC §6.11): it is one a resolver can settle, none ran for the
// task in this run, and on_conflict = "resolve" or the owner asked for it.
func (e *Engine) willResolve(l *launch, f failure) bool {
	if !f.resolvable || e.resolved[l.t.ID] {
		return false
	}
	return e.cfg.Parallel.OnConflictOrDefault() == config.OnConflictResolve || l.integ.resolveAsked
}

// openResolver opens the resolver session for l's task after the failure
// f (SPEC §6.11): the task's own session is closed and its usage logged,
// then a fresh session opens in the worktree as it is: mid-rebase for a
// conflict, the integrated tree for a verify failure.
func (e *Engine) openResolver(ctx context.Context, l *launch, f failure) (ended, stopped bool, err error) {
	id, g := l.t.ID, l.integ
	g.resolveAsked = false
	rank, model, err := e.resolverRank(l.t)
	if err != nil {
		return false, false, err
	}
	e.resolved[id] = true
	e.waitsOver()
	e.dropPlan(l, "a resolver session takes over")
	// The task's done signal is spent: the resolver's own `igris done`
	// writes the next one.
	if err := e.dir.RemoveSignal(id); err != nil {
		return false, false, err
	}
	if l.sess != nil && !l.lost {
		if err := l.sess.Close(ctx); err != nil {
			e.warn(fmt.Sprintf("close the session of %s: %v; close its pane by hand", id, err))
		}
	}
	e.endUsage(ctx, l) // the attempt's session ended (SPEC §6.11)
	l.sess, l.lost, l.ep, l.stateErr, l.skip = nil, false, episode{}, "", nil
	g.resolver = &resolverRun{reason: f.reason, rank: rank, model: model, files: f.lines, profile: f.profile, taskModel: l.model}
	if f.reason == state.FailedVerify {
		g.resolver.files, g.resolver.lines = nil, f.lines
	}
	g.stage = integResolve
	l.model = model
	l.cur.Resolver = &state.Resolver{Reason: f.reason}
	l.cur.VerifyAttempts = 0
	return e.startResolver(ctx, l, startNew)
}

// startResolver opens a session of l's resolver: its first one, or the
// one that replaces it on a retry (fresh or continue).
func (e *Engine) startResolver(ctx context.Context, l *launch, how startKind) (ended, stopped bool, err error) {
	r := l.integ.resolver
	text := prompt.ResolveContinue(l.t.ID)
	if how != startContinue {
		if text, err = e.resolvePrompt(ctx, l, r); err != nil {
			return false, false, err
		}
	}
	st, err := e.prepareWith(l, how, text)
	if err != nil {
		return false, false, err
	}
	st.label = l.t.ID + " · resolver · " + r.rank
	detail := r.profile
	if r.reason == state.FailedConflict {
		detail = strings.Join(r.files, ", ")
	}
	// Not an attempt (SPEC §6.11): the task's attempt, the resolver's
	// rank, model and session.
	e.log(state.Event{Type: state.EventResolverOpened, Session: st.sessionID, Reason: r.reason, Detail: detail, Slot: l.cur.Slot})
	e.emit(Event{Kind: ResolverOpened, Reason: r.reason, Detail: r.cardText(), Output: r.lines})
	if open, err := e.beforeSession(ctx, l); err != nil || !open {
		return false, false, err
	}
	return false, false, e.openSession(ctx, l, st)
}

// resumeResolver sets l up as a task whose resolver session a stopped run
// left working (SPEC §6.11, §13): the task's launch is mid-integration, at
// the head of the integrator's line, and the reattached session is the
// resolver's. The task's done note is gone with the stopped run; the
// resolver's note stands in for it.
func (e *Engine) resumeResolver(l *launch, reason string) error {
	rank, model, err := e.resolverRank(l.t)
	if err != nil {
		return err
	}
	l.integ = &integration{stage: integResolve, resolver: &resolverRun{reason: reason, rank: rank, model: model, taskModel: l.model}}
	l.model = model
	e.resolved[l.t.ID] = true
	e.integrations = append([]*launch{l}, e.integrations...)
	return nil
}

// stepResolve is a poll of l's task while its resolver session works
// (SPEC §6.11): the owner's commands, the resolver's signal, its session.
// `igris done` is a resolution, `igris skip` a failed one (never a skip of
// the task, and no question for the owner).
func (e *Engine) stepResolve(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	t := l.t
	for _, c := range e.takeCommands(l) {
		switch c.Kind {
		case CmdDone:
			// The owner resolved it by hand: the resolution is checked
			// and verified like the resolver's (SPEC §6.11).
			e.endEpisode(&l.ep)
			return e.resolverDone(ctx, l, strings.TrimSpace(c.Text))
		case CmdSkip:
			if err := e.endResolverSession(ctx, l, "the owner skipped the task; then skipped"); err != nil {
				return false, false, err
			}
			e.unqueueIntegration(l)
			l.integ = nil
			return true, false, e.finish(ctx, l, plan.Skipped, c.Text)
		case CmdRetry:
			if c.Continue && (l.cur.Session == nil || !ValidSessionID(l.cur.ClaudeSession)) {
				e.reject(c, "there is no conversation of "+t.ID+"'s resolver to continue; retry fresh")
				continue
			}
			if _, _, err := e.retryResolver(ctx, l, c.Continue); err != nil {
				return false, false, err
			}
		case CmdPlan:
			e.decidePlan(l, c)
		default:
			e.reject(c, t.ID+"'s resolver session takes no "+c.Kind.String()+" now")
		}
	}
	switch sig := e.scanSignals(t, l.cur.StartedAt); {
	case sig == nil:
	case sig.Action == state.ActionDone:
		e.endEpisode(&l.ep)
		return e.resolverDone(ctx, l, sig.Note)
	case sig.Action == state.ActionSkip:
		e.endEpisode(&l.ep)
		return e.resolverFailed(ctx, l, "gave up: "+textsafe.Line(sig.Note), nil)
	}
	e.watchSession(ctx, l)
	return false, false, nil
}

// retryResolver replaces the resolver's session (SPEC §6.3, §15.3 `r`):
// the old one is closed and a new one continues the conversation or
// starts fresh. Still the one resolver of the task, and no attempt.
func (e *Engine) retryResolver(ctx context.Context, l *launch, cont bool) (ended, stopped bool, err error) {
	how, detail := startFresh, "fresh"
	if cont {
		how, detail = startContinue, "continue"
	}
	e.emit(Event{Kind: Retrying, Detail: detail})
	e.dropPlan(l, "the session is replaced")
	e.waitsOver()
	if l.sess != nil && !l.lost {
		if err := l.sess.Close(ctx); err != nil {
			e.warn(fmt.Sprintf("close the resolver session of %s: %v; close its pane by hand", l.t.ID, err))
		}
	}
	e.endUsage(ctx, l)
	l.sess, l.lost, l.ep, l.stateErr = nil, false, episode{}, ""
	return e.startResolver(ctx, l, how)
}

// endResolverSession closes the resolver's session once its work is over
// (resolved, gave up, or the owner acted), logs its usage line and, when
// the resolution failed, resolver_failed with why (SPEC §6.11). The task's
// model and state.json entry are the task's again.
func (e *Engine) endResolverSession(ctx context.Context, l *launch, failed string) error {
	g := l.integ
	r := g.resolver
	if failed != "" {
		e.log(state.Event{Type: state.EventResolverFailed, Rank: r.rank, Model: r.model, Detail: textsafe.Line(failed)})
	}
	e.waitsOver()
	if err := e.dir.RemoveSignal(l.t.ID); err != nil {
		return err
	}
	if l.sess != nil && !l.lost {
		if failed == "" {
			e.settle(ctx, l.sess, closeIdleWait) // let the agent finish its final message (SPEC §6.6)
		}
		if err := l.sess.Close(ctx); err != nil {
			e.warn(fmt.Sprintf("close the resolver session of %s: %v; close its pane by hand", l.t.ID, err))
		}
	}
	e.endUsage(ctx, l) // while the stage still says resolver: the line is marked
	l.sess, l.lost = nil, false
	l.model = r.taskModel
	l.cur.Resolver = nil
	g.stage = integQueued // the integrator's line was held for it
	return e.dir.SaveRun(e.run)
}

// resolverDone acts on the resolver's done with its note (SPEC §6.11): the
// session is closed, the rebase finished or the fix folded into the task's
// commit, the branch's shape checked, and the integrated tree verified,
// always, also for a task the owner marked done.
func (e *Engine) resolverDone(ctx context.Context, l *launch, note string) (ended, stopped bool, err error) {
	g := l.integ
	r := g.resolver
	if err := e.endResolverSession(ctx, l, ""); err != nil {
		return false, false, err
	}
	why, err := e.foldResolution(ctx, l, r)
	if err != nil {
		return false, false, err
	}
	if why != "" {
		return e.resolverFailed(ctx, l, why, nil)
	}
	e.log(state.Event{Type: state.EventResolverDone, Rank: r.rank, Model: r.model, Detail: note})
	if g.note == "" {
		g.note = note // a resumed run lost the task's own note
	}
	g.resolved = true
	what := "resolver resolved it"
	if note != "" {
		what += ": " + note
	}
	if !e.verifies(l.t) {
		e.emit(Event{Kind: ResolverDone, Detail: what})
		return e.fastForward(ctx, l)
	}
	e.emit(Event{Kind: ResolverDone, Detail: what + "; verifying the integrated tree"})
	l.check = &check{}
	e.checks = append(e.checks, l)
	g.stage = integVerify
	e.startChecks(ctx)
	return false, false, nil
}

// foldResolution finishes what the resolver left in l's worktree (SPEC
// §6.11) and checks the branch's shape. A conflict: with the rebase still
// stopped, markers left are a failure; else `git add -A`, then `rebase
// --continue` (or `--skip` with nothing staged: the work is on the base
// already); a session that continued the rebase itself is accepted. A
// verify failure: `git add -A` and `commit --amend --no-edit`, one commit
// per task. Then the worktree must be clean and the branch at most one
// commit ahead of the target. why says what failed, "" for a sound
// resolution; err is a git that couldn't run or a cancelled context.
func (e *Engine) foldResolution(ctx context.Context, l *launch, r *resolverRun) (why string, err error) {
	g := l.integ
	if g.target, g.branch, err = e.runTarget(ctx); err != nil {
		return "", err
	}
	dir := e.workDir(l)
	switch {
	case r.reason == state.FailedConflict && e.rebaseInProgress(ctx, dir):
		if files := e.markersLeft(ctx, dir); len(files) > 0 {
			return "markers left in " + strings.Join(files, ", "), nil
		}
		if _, err := e.git(ctx, l, "add", "-A"); err != nil {
			return "", err
		}
		step := "--continue"
		if !e.staged(ctx, dir) {
			step = "--skip"
		}
		if _, err := e.git(ctx, l, "-c", "core.editor=true", "rebase", step); err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			return "the rebase didn't " + strings.TrimPrefix(step, "--") + ": " + gitFirstLine(err), nil
		}
		if e.rebaseInProgress(ctx, dir) {
			return "the rebase stopped again", nil
		}
	case r.reason == state.FailedVerify:
		if _, err := e.git(ctx, l, "add", "-A"); err != nil {
			return "", err
		}
		if e.staged(ctx, dir) {
			if _, err := e.git(ctx, l, "commit", "--amend", "--no-edit"); err != nil {
				if ctx.Err() != nil {
					return "", err
				}
				return "the fix didn't fold into the task's commit: " + gitFirstLine(err), nil
			}
		}
	}
	status, err := e.git(ctx, l, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) != "" {
		return "the worktree has uncommitted changes", nil
	}
	ahead, err := e.aheadOf(ctx, l, g.target)
	if err != nil {
		return "", err
	}
	if ahead > 1 {
		return fmt.Sprintf("the branch is %d commits ahead of the run branch, not one", ahead), nil
	}
	return "", nil
}

// resolverFailed handles a failed resolution of l's task (SPEC §6.11): the
// resolver gave up, left the branch in a bad shape, or its resolution
// failed verify on the integrated tree. why says it. A stopped rebase is
// aborted now (with on_conflict = "resolve" the abort of §6.11 moves here).
// Then the one rerun, unless on_conflict = "ask" or it ran already; else
// Needs you (reason integration), with no Resolve: the resolver ran.
func (e *Engine) resolverFailed(ctx context.Context, l *launch, why string, lines []string) (ended, stopped bool, err error) {
	id, g := l.t.ID, l.integ
	r := g.resolver
	rerun := e.cfg.Parallel.OnConflictOrDefault() != config.OnConflictAsk && !e.reran[id]
	then := "then needs you"
	if rerun {
		then = "then rerun"
	}
	if g.stage == integResolve {
		if err := e.endResolverSession(ctx, l, why+"; "+then); err != nil {
			return false, false, err
		}
	} else {
		e.log(state.Event{Type: state.EventResolverFailed, Rank: r.rank, Model: r.model, Detail: textsafe.Line(why + "; " + then)})
	}
	e.unqueueIntegration(l)
	g.resolved = false
	if dir := e.workDir(l); r.reason == state.FailedConflict && e.rebaseInProgress(ctx, dir) {
		if _, err := e.gitIn(ctx, dir, "rebase", "--abort"); err != nil && ctx.Err() == nil {
			e.warn(fmt.Sprintf("abort the rebase of %s: %v; its worktree %s may be left mid-rebase", id, err, l.cur.Worktree.Path))
		}
	}
	what := "resolver failed: " + why
	if rerun {
		e.reran[id] = true
		e.emit(Event{Kind: ResolverFailed, Detail: what + "; igris runs the task again from the start", Output: lines})
		return false, false, e.rerun(ctx, l)
	}
	g.stage = integFailed
	e.emit(Event{Kind: ResolverFailed, Detail: what + "; needs you", Output: lines})
	failed := "didn't integrate: its " + what
	e.needsYouAsking(ctx, notifyNeedsInput, state.ReasonIntegration, id+" "+failed+"; rerun it fresh, mark it done, skip it or stop", wordsIntegration,
		&Event{Kind: Asked, Question: QuestionIntegration, Detail: id + " " + failed + ": rerun it fresh, mark it done (you integrated it by hand), skip it, or stop", Output: lines})
	return false, false, nil
}

// rebaseInProgress reports whether a rebase is stopped in dir: its state
// directory, rebase-merge (or rebase-apply), exists, which is how git
// itself tells. REBASE_HEAD is no sign: git leaves it behind when a
// rebase finishes (2.56), so a finished resolution read as stopped again.
func (e *Engine) rebaseInProgress(ctx context.Context, dir string) bool {
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		out, err := e.gitIn(ctx, dir, "rev-parse", "--git-path", name)
		p := strings.TrimSpace(out)
		if err != nil || p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p) // relative to dir in the main worktree
		}
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// markersLeft are the files in dir whose working tree still holds conflict
// markers, as `git diff --check` reports them (`a.go:12: leftover conflict
// marker`); at most maxConflictFiles. Whitespace errors, which --check
// reports too, don't count.
func (e *Engine) markersLeft(ctx context.Context, dir string) []string {
	out, _ := e.gitOut(ctx, dir, "diff", "--check")
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "leftover conflict marker") {
			continue
		}
		f, _, ok := strings.Cut(line, ":")
		if f = textsafe.Line(strings.TrimSpace(f)); ok && f != "" && !slices.Contains(files, f) {
			files = append(files, f)
		}
	}
	if len(files) > maxConflictFiles {
		files = files[:maxConflictFiles]
	}
	return files
}

// staged reports whether the index in dir differs from HEAD: `git diff
// --cached --quiet` exits 1 then.
func (e *Engine) staged(ctx context.Context, dir string) bool {
	_, code := e.gitOut(ctx, dir, "diff", "--cached", "--quiet")
	return code != 0
}

// gitOut runs git in dir and returns its output and exit code whatever
// the exit: for the git questions whose answer is the code (--quiet,
// --check). A git that can't run answers "" and -1.
func (e *Engine) gitOut(ctx context.Context, dir string, args ...string) (string, int) {
	res, err := e.runner.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: dir, Env: e.gitEnv(), Timeout: gitTimeout})
	if err != nil {
		return "", -1
	}
	return string(res.Stdout), res.ExitCode
}

// gitFirstLine is the first line git said on its error, cleaned; the
// error's own text when it said nothing.
func gitFirstLine(err error) string {
	var xe *runner.ExitError
	if errors.As(err, &xe) {
		if line := firstLine(string(xe.Stderr)); line != "" {
			return line
		}
	}
	return textsafe.Line(err.Error())
}

// Bounds of the resolver's prompt (SPEC §6.11): what the plan, git and
// verify contribute is cleaned and cut.
const (
	maxResolveOthers = 10       // the other side's tasks named with their text
	maxResolveText   = 2 << 10  // a task's text in it
	maxResolveTotal  = 32 << 10 // the other side in all
)

// resolvePrompt renders the resolver's first message (SPEC §6.11): the
// task's row, why it is here, the other side (the tasks the run branch
// took since this task's base, by this run's integrated lines), how to
// read both sides and the job.
func (e *Engine) resolvePrompt(ctx context.Context, l *launch, r *resolverRun) (string, error) {
	t := l.t
	v := prompt.ResolveVars{
		Vars:    prompt.VarsFor(t, prompt.Env{Model: r.model, PlanFile: e.planFile(l), TaskFile: e.taskFile(l), CommitPolicy: e.sessionPolicy()}),
		Reason:  r.reason,
		Files:   r.files,
		Profile: r.profile,
		Lines:   r.lines,
	}
	v.Rank = r.rank
	if r.reason == state.FailedVerify {
		_, v.Command = e.verifyProfile(t)
	}
	v.Others, v.More, v.Commits = e.otherSide(ctx, l)
	return prompt.RenderResolve(v)
}

// otherSide is what the run branch took since l's task's base (SPEC
// §6.11): the commits of base..target, mapped to tasks through this run's
// integrated lines, each task as its plan row; a commit of no task by its
// subject. At most maxResolveOthers tasks, more counted; nothing when git
// can't say (no ORIG_HEAD: the rebase never ran here).
func (e *Engine) otherSide(ctx context.Context, l *launch) (others []prompt.ResolveOther, more int, commits []string) {
	target, _, err := e.runTarget(ctx)
	if err != nil {
		return nil, 0, nil
	}
	base, err := e.git(ctx, l, "merge-base", target, "ORIG_HEAD")
	if err != nil {
		return nil, 0, nil
	}
	base = strings.TrimSpace(base)
	if !commitSHA.MatchString(base) {
		return nil, 0, nil
	}
	out, err := e.git(ctx, l, "log", "--format=%H%x09%s", base+".."+target)
	if err != nil {
		return nil, 0, nil
	}
	byCommit := map[string]string{}
	if events, err := e.dir.Events(); err == nil {
		for _, ev := range events {
			if ev.Run == e.runID && ev.Type == state.EventIntegrated && ev.Commit != "" && ev.Task != "" {
				byCommit[ev.Commit] = ev.Task
			}
		}
	}
	var p *plan.Plan
	seen := map[string]bool{}
	size := 0
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		sha, subject, _ := strings.Cut(line, "\t")
		if sha == "" {
			continue
		}
		id, ok := byCommit[sha]
		if !ok {
			if s := textsafe.Line(strings.TrimSpace(subject)); s != "" && len(commits) < maxResolveOthers {
				commits = append(commits, s)
			}
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if len(others) >= maxResolveOthers || size >= maxResolveTotal {
			more++
			continue
		}
		if p == nil {
			if p, err = e.loadPlan(); err != nil {
				return nil, 0, commits // the plan can't be read now: the IDs alone would mislead
			}
		}
		o := prompt.ResolveOther{ID: id}
		if task := p.Task(id); task != nil {
			o.Title, o.Text = textsafe.Line(task.Title), cutRunes(textsafe.Clean(task.Text), maxResolveText)
		}
		size += len(o.Title) + len(o.Text)
		others = append(others, o)
	}
	return others, more, commits
}

// cutRunes cuts s to at most n bytes on a rune boundary, with an ellipsis.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}
