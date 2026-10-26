package engine

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Commit policies (SPEC §6.5).
const (
	CommitAsk   = "ask"
	CommitAuto  = "auto"
	CommitNever = "never"
	// CommitBatch commits as auto does and asks once, when the run ends,
	// whether to keep the run's commits (since 1.8).
	CommitBatch = "batch"
)

// gitTimeout bounds each git call.
const gitTimeout = 2 * time.Minute

// CommitVars are the variables of the commit_message template.
type CommitVars struct {
	ID, Title, Phase, Rank, Model string
	Note                          string // the done note; also the message body
	// Plan is the plan igris approved for the task, capped at 8 KiB, ""
	// without one (SPEC §6.5); also the body, after the note. Since 1.1.
	Plan string
}

// parseCommitMessage parses the commit_message template once, so a broken
// template stops the run before anything starts.
func parseCommitMessage(text string) (*template.Template, error) {
	tmpl, err := template.New("commit_message").Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("run.commit_message in igris.toml: %w; fix the template in igris.toml", err)
	}
	return tmpl, nil
}

// commit applies the commit policy after l's task was verified (SPEC §6.5):
// it commits every change in the tree with the rendered message, asking the
// owner first under "ask". Nothing to commit is not an error; a git failure
// is. stopped reports that the run stopped while the owner was asked;
// reset that the owner confirmed a reset of the task meanwhile, which was
// applied instead of the commit (SPEC §6.2).
func (e *Engine) commit(ctx context.Context, l *launch, note string) (stopped, reset bool, err error) {
	t := l.t
	if e.commitPolicy() == CommitNever {
		return false, false, nil
	}
	status, err := e.git(ctx, l, "status", "--porcelain")
	if err != nil {
		return false, false, err
	}
	if strings.TrimSpace(status) == "" {
		e.emit(Event{Kind: NotCommitted, Detail: "nothing to commit"})
		return false, false, nil
	}
	msg, approved, err := e.commitMessage(l, note)
	if err != nil {
		return false, false, err
	}

	if e.commitPolicy() == CommitAsk {
		e.waitOn(state.ReasonCommit, commitWhat)
		answer, stopped := e.ask(ctx, QuestionCommit, fmt.Sprintf("commit the changes of %s as %q?", t.ID, msg), commitWhat)
		if stopped {
			return true, false, nil
		}
		e.waitOver(state.ReasonCommit)
		// A reset requested while the question waited comes right after it.
		if reset, stopped, err := e.settleResets(ctx, t.ID); err != nil || stopped || reset {
			return stopped, reset, err
		}
		if !answer.Yes {
			e.emit(Event{Kind: NotCommitted, Detail: "the owner declined"})
			return false, false, nil
		}
		if isCommitAll(answer) {
			e.commitAll(t.ID)
		}
	}

	return false, false, e.commitChanges(ctx, l, msg, note, approved)
}

// commitWhat is what the commit question's notification says (SPEC §10).
const commitWhat = "commit question"

// commitMessage renders the commit subject for l's task from
// commit_message (SPEC §6.5) and returns it with the approved plan the
// body carries ("" without one).
func (e *Engine) commitMessage(l *launch, note string) (msg, approved string, err error) {
	t := l.t
	var subject bytes.Buffer
	approved = e.approvedPlan(l)
	vars := CommitVars{ID: t.ID, Title: t.Title, Phase: e.phase, Rank: t.Rank, Model: l.model, Note: note, Plan: approved}
	if err := e.commitMsg.Execute(&subject, vars); err != nil {
		return "", "", fmt.Errorf("render run.commit_message for %s: %w; fix the template in igris.toml", t.ID, err)
	}
	msg = strings.TrimSpace(subject.String())
	if msg == "" {
		return "", "", fmt.Errorf("run.commit_message renders to an empty message for %s; fix it in igris.toml", t.ID)
	}
	return msg, approved, nil
}

// commitChanges stages and commits every change where l's task works
// with the subject msg, the note and the approved plan as the body (SPEC
// §6.5), and logs the commit. A git failure is an error.
func (e *Engine) commitChanges(ctx context.Context, l *launch, msg, note, approved string) error {
	t := l.t
	e.markBatchBase(ctx)
	if _, err := e.git(ctx, l, "add", "-A"); err != nil {
		return err
	}
	args := []string{"commit", "-m", msg}
	if note = strings.TrimSpace(note); note != "" {
		args = append(args, "-m", note)
	}
	if approved != "" {
		args = append(args, "-m", "Approved plan:\n\n"+approved)
	}
	if _, err := e.git(ctx, l, args...); err != nil {
		return fmt.Errorf("%w; fix what git reports (e.g. a failing hook), commit the changes of %s by hand or set commit = \"never\" in [run], then run `igris arise` again", err, t.ID)
	}
	e.log(state.Event{Type: state.EventCommitted, Commit: e.head(ctx, l), Detail: msg})
	e.emit(Event{Kind: Committed, Detail: msg})
	return nil
}

// head is the commit SHA HEAD points at where l works, for the run log; "" with a
// warning if git can't say.
func (e *Engine) head(ctx context.Context, l *launch) string {
	out, err := e.git(ctx, l, "rev-parse", "HEAD")
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

// commitSHA is a full SHA-1 or SHA-256 commit hash.
var commitSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// git runs git where l's task works (the project root, the run's worktree
// or the task's own, SPEC §6.8, §6.11) and returns its output; a non-zero
// exit is an error.
func (e *Engine) git(ctx context.Context, l *launch, args ...string) (string, error) {
	out, err := e.gitIn(ctx, e.workDir(l), args...)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

// ask raises a yes/no question, notifies it (needs_input, a few words
// in what) and waits for the owner's CmdAnswer, which it returns. Other
// task commands are rejected until it is answered; reset requests are
// asked about meanwhile, never applied. stopped reports that the run
// stopped instead.
func (e *Engine) ask(ctx context.Context, q Question, text, what string) (answer Command, stopped bool) {
	e.emit(Event{Kind: Asked, Question: q, Detail: text})
	e.toastAsking(ctx, notifyNeedsInput, what, q)
	for {
		e.checkConfig(ctx)
		if e.stopping(ctx) {
			return Command{}, true
		}
		for _, c := range e.takeCommands(e.task) {
			if c.Kind == CmdAnswer {
				return c, false
			}
			e.reject(c, "answer the question first: "+text)
		}
		if _, err := e.resets(ctx, false); err != nil {
			e.warn(err.Error())
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}
