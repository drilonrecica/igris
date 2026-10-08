package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// Commit policies (SPEC §6.5).
const (
	CommitAsk   = "ask"
	CommitAuto  = "auto"
	CommitNever = "never"
)

// gitTimeout bounds each git call.
const gitTimeout = 2 * time.Minute

// CommitVars are the variables of the commit_message template.
type CommitVars struct {
	ID, Title, Phase, Rank, Model string
	Note                          string // the done note; also the message body
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
	if e.cfg.Run.Commit == CommitNever {
		return false, false, nil
	}
	status, err := e.git(ctx, "status", "--porcelain")
	if err != nil {
		return false, false, err
	}
	if strings.TrimSpace(status) == "" {
		e.emit(Event{Kind: NotCommitted, Detail: "nothing to commit"})
		return false, false, nil
	}
	var subject bytes.Buffer
	vars := CommitVars{ID: t.ID, Title: t.Title, Phase: e.phase, Rank: t.Rank, Model: l.model, Note: note}
	if err := e.commitMsg.Execute(&subject, vars); err != nil {
		return false, false, fmt.Errorf("render run.commit_message for %s: %w; fix the template in igris.toml", t.ID, err)
	}
	msg := strings.TrimSpace(subject.String())
	if msg == "" {
		return false, false, fmt.Errorf("run.commit_message renders to an empty message for %s; fix it in igris.toml", t.ID)
	}

	if e.cfg.Run.Commit == CommitAsk {
		yes, stopped := e.ask(ctx, QuestionCommit, fmt.Sprintf("commit the changes of %s as %q?", t.ID, msg))
		if stopped {
			return true, false, nil
		}
		// A reset requested while the question waited comes right after it.
		if reset, stopped, err := e.settleResets(ctx, t.ID); err != nil || stopped || reset {
			return stopped, reset, err
		}
		if !yes {
			e.emit(Event{Kind: NotCommitted, Detail: "the owner declined"})
			return false, false, nil
		}
	}

	if _, err := e.git(ctx, "add", "-A"); err != nil {
		return false, false, err
	}
	args := []string{"commit", "-m", msg}
	if note = strings.TrimSpace(note); note != "" {
		args = append(args, "-m", note)
	}
	if _, err := e.git(ctx, args...); err != nil {
		return false, false, fmt.Errorf("%w; fix what git reports (e.g. a failing hook), commit the changes of %s by hand or set commit = \"never\" in [run], then run `igris arise` again", err, t.ID)
	}
	e.log(state.Event{Type: state.EventCommitted, Detail: msg})
	e.emit(Event{Kind: Committed, Detail: msg})
	return false, false, nil
}

// git runs git in the project root and returns its output; a non-zero exit
// is an error.
func (e *Engine) git(ctx context.Context, args ...string) (string, error) {
	res, err := e.runner.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: e.dir.Root(), Timeout: gitTimeout})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return string(res.Stdout), nil
}

// ask raises a yes/no question and waits for the owner's CmdAnswer. Other
// task commands are rejected until it is answered; reset requests are
// asked about meanwhile, never applied. stopped reports that the run
// stopped instead.
func (e *Engine) ask(ctx context.Context, q Question, text string) (yes, stopped bool) {
	e.emit(Event{Kind: Asked, Question: q, Detail: text})
	for {
		e.checkConfig(ctx)
		if e.stopping(ctx) {
			return false, true
		}
		for _, c := range e.takeCommands() {
			if c.Kind == CmdAnswer {
				return c.Yes, false
			}
			e.reject(c, "answer the question first: "+text)
		}
		if _, err := e.resets(ctx, false); err != nil {
			e.warn(err.Error())
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}
