package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Fewer interruptions (SPEC §6.5, decisions.md V18-P3): Commit all, the
// commit question's third answer, makes commit auto for the rest of the
// run; commit = "batch" commits each task at once and asks once, when
// the run ends, whether to keep the run's commits.

// CommitAllText is the text of a CmdAnswer that answers the commit
// question with Commit all (SPEC §6.5, §13: `answer` with text `all`).
const CommitAllText = "all"

// maxBatchSubjects bounds the subjects the batch question lists.
const maxBatchSubjects = 20

// movedOutside is why the batch question offers no Undo.
const movedOutside = "HEAD moved outside igris"

// commitPolicy is the commit policy in force: the one Commit all set for
// the rest of the run, else igris.toml's.
func (e *Engine) commitPolicy() string {
	if e.run != nil && e.run.CommitOverride != "" {
		return e.run.CommitOverride
	}
	return e.cfg.Run.Commit
}

// sessionPolicy is the commit policy the task prompt names: for the
// session batch reads as auto, since igris commits after verification.
func (e *Engine) sessionPolicy() string {
	if p := e.commitPolicy(); p != CommitBatch {
		return p
	}
	return CommitAuto
}

// isCommitAll reports whether c answers the commit question with Commit
// all.
func isCommitAll(c Command) bool {
	return c.Kind == CmdAnswer && c.Yes && strings.EqualFold(strings.TrimSpace(c.Text), CommitAllText)
}

// commitAll applies the owner's Commit all on task id's commit question:
// commit is auto for the rest of this run (state.json's commit_override,
// the run log's commit_policy, a feed line; igris.toml is untouched), and
// in a parallel phase the commit questions already pending are answered
// Commit too.
func (e *Engine) commitAll(id string) {
	for _, l := range e.active {
		if l.t.ID != id && l.integ != nil && l.integ.stage == integAsk {
			e.pending = append(e.pending, Command{Kind: CmdAnswer, Yes: true, Task: l.t.ID})
		}
	}
	if e.run.CommitOverride == state.CommitAll {
		return
	}
	e.run.CommitOverride = state.CommitAll
	if err := e.dir.SaveRun(e.run); err != nil {
		e.warn(err.Error())
	}
	e.logRun(state.Event{Type: state.EventCommitPolicy, Detail: "auto: the owner's answer to " + id + "'s commit question"})
	e.emitRun(Event{Kind: CommitPolicy, Detail: "Commit all: every task is committed without asking for the rest of this run"})
}

// markBatchBase records the run branch's head before the run's first
// commit under commit = "batch" (SPEC §6.5): the base the batch
// question's Undo resets to. A resumed run keeps the base it had.
func (e *Engine) markBatchBase(ctx context.Context) {
	if e.commitPolicy() != CommitBatch || e.run.BatchBase != "" {
		return
	}
	out, err := e.gitIn(ctx, e.work, "rev-parse", "--verify", "-q", "HEAD")
	sha := strings.TrimSpace(out)
	if err != nil || !commitSHA.MatchString(sha) {
		e.reportOnce("batch-base", Warning, "the run branch has no commit to come back to, so the end of the run won't ask about its commits")
		return
	}
	e.run.BatchBase = sha
	if err := e.dir.SaveRun(e.run); err != nil {
		e.warn(err.Error())
	}
}

// batch is what a batch run committed, as the batch question shows it.
type batch struct {
	base     string
	n        int      // the run's commits
	subjects []string // their subjects, oldest first
	// why Undo is not offered: "" while base..HEAD is a straight line of
	// exactly the commits the run log records.
	why string
}

// batchOf reads the commits since base on the run branch against the
// commits the run log records (committed, integrated).
func (e *Engine) batchOf(ctx context.Context, base string) batch {
	b := batch{base: base}
	recorded := map[string]bool{}
	ours := 0 // this run's, for when git can't say
	if events, err := e.dir.Events(); err == nil {
		for _, ev := range events {
			if (ev.Type == state.EventCommitted || ev.Type == state.EventIntegrated) && ev.Commit != "" {
				recorded[ev.Commit] = true
				if ev.Run == e.runID && ev.Type == state.EventCommitted {
					ours++
				}
			}
		}
	}
	if _, err := e.gitIn(ctx, e.work, "merge-base", "--is-ancestor", base, "HEAD"); err != nil {
		b.n, b.why = ours, movedOutside
		return b
	}
	out, err := e.gitIn(ctx, e.work, "log", "--format=%H %P%x09%s", base+"..HEAD")
	if err != nil {
		b.n, b.why = ours, movedOutside
		return b
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	slices.Reverse(lines) // oldest first
	for _, line := range lines {
		ids, subject, _ := strings.Cut(line, "\t")
		f := strings.Fields(ids)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 || !recorded[f[0]] {
			b.why = movedOutside // a merge, or a commit igris didn't make
			continue
		}
		b.n++
		b.subjects = append(b.subjects, textsafe.Line(strings.TrimSpace(subject)))
	}
	return b
}

// question is the batch question's text, and the subjects it lists.
func (b batch) question() (string, []string) {
	q := "Keep the " + look.Count(b.n, "commit", "commits") + " of this run?"
	if b.why != "" {
		q += " " + b.why + ", so Undo is not offered; to undo them yourself: git reset --soft " + b.base
	}
	subjects := b.subjects
	if len(subjects) > maxBatchSubjects {
		more := fmt.Sprintf("… and %d more", len(subjects)-maxBatchSubjects)
		subjects = append(slices.Clone(subjects[:maxBatchSubjects]), more)
	}
	return q, subjects
}

// BatchTitle splits the batch question's text into its title, `Keep the
// 9 commits of this run?`, and what follows it (why Undo is not offered),
// for the UIs (SPEC §15.5).
func BatchTitle(detail string) (title, rest string) {
	title, rest, ok := strings.Cut(detail, "? ")
	if !ok {
		return detail, ""
	}
	return title + "?", rest
}

// Unattended says no one is left to answer: --no-tui's stdin ended. The
// batch question then keeps the run's commits, as leaving without an
// answer does (SPEC §6.5). It may be called from any goroutine.
func (e *Engine) Unattended() {
	e.mu.Lock()
	e.unattended = true
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) isUnattended() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.unattended
}

// askBatch puts a batch run's commits to the owner once the run ended,
// whatever its outcome (SPEC §6.5): Keep (the default) or Undo, `git
// reset --soft` to the base, which leaves every change in the tree and
// the index. The run waits for the answer. Quitting, Ctrl-C, a stop or
// the end of --no-tui's stdin leave it unanswered: the commits are kept,
// and so is the base, so a resumed run asks about them with its own. An
// answer clears the base.
func (e *Engine) askBatch(ctx context.Context, outcome string) {
	base := e.run.BatchBase
	if base == "" || ctx.Err() != nil {
		return
	}
	settled := false
	defer func() {
		if !settled {
			return
		}
		e.run.BatchBase = ""
		if err := e.dir.SaveRun(e.run); err != nil {
			e.warn(err.Error())
		}
	}()
	b := e.batchOf(ctx, base)
	if b.n == 0 && b.why == "" {
		settled = true // nothing committed since the base
		return
	}
	was := e.task
	e.task = nil // the run's question, not a task's
	defer func() { e.task = was }()
	q, subjects := b.question()
	what := "run " + outcome + " · keep the " + look.Count(b.n, "commit", "commits") + "?"
	e.logRun(state.Event{Type: state.EventNeedsYou, Reason: state.ReasonCommit, Detail: what})
	e.batchAsked = true
	e.emitRun(Event{Kind: Asked, Question: QuestionCommitBatch, Detail: q, Output: subjects, Undo: b.why == ""})
	e.toastAsking(ctx, notifyNeedsInput, what, QuestionCommitBatch)
	keep, answered := e.batchAnswer(ctx, b)
	e.logRun(state.Event{Type: state.EventNeedsYouClear, Reason: state.ReasonCommit})
	settled = answered
	if answered && !keep {
		e.undoBatch(ctx, base)
	}
}

// batchAnswer waits for the answer to the batch question: keep is true
// for Keep, false for Undo; answered is false when the owner left
// without one.
func (e *Engine) batchAnswer(ctx context.Context, b batch) (keep, answered bool) {
	e.stop = false // a stop that ended the run doesn't answer it
	for {
		e.drain()
		if e.stop || ctx.Err() != nil || e.isUnattended() {
			return true, false
		}
		e.resetAnswers = nil // the run is over: no reset is applied now
		for _, c := range e.pending {
			switch {
			case c.Kind != CmdAnswer || (c.Question != "" && c.Question != QuestionCommitBatch):
				e.reject(c, "the run has ended; answer whether to keep its commits")
			case !c.Yes && b.why != "":
				e.reject(c, b.why+"; undo them yourself: git reset --soft "+b.base)
			default:
				e.pending = nil
				return c.Yes, true
			}
		}
		e.pending = nil
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}

// undoBatch undoes the run's commits: `git reset --soft` to base, never
// --hard, once base..HEAD is still exactly the run's commits.
func (e *Engine) undoBatch(ctx context.Context, base string) {
	b := e.batchOf(ctx, base)
	if b.why != "" {
		e.warn(b.why + " while the question waited; nothing was undone; to undo the run's commits yourself: git reset --soft " + base)
		return
	}
	if _, err := e.gitIn(ctx, e.work, "reset", "--soft", base); err != nil {
		e.warn(fmt.Sprintf("undo the run's commits: %v; they are kept; to undo them yourself: git reset --soft %s", err, base))
		return
	}
	detail := look.Count(b.n, "commit", "commits") + " undone; their changes are in the tree"
	e.logRun(state.Event{Type: state.EventCommitsUndone, Commit: base, Detail: detail})
	e.emitRun(Event{Kind: CommitsUndone, Detail: detail})
}
