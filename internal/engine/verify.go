package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

const (
	// verifyTailLines is how much of a failed verify's output goes back into
	// the session (SPEC §6.4).
	verifyTailLines = 60
	// verifyIdleWait bounds the wait for the agent to finish its turn before
	// a failure is sent (it ran `igris done` as its last action).
	verifyIdleWait = 30 * time.Second
)

// verify runs the verify command of the config snapshot for l's task
// (SPEC §6.4) and reports whether it passed. On failure the signal is
// deleted and, below verify_max_attempts, the output tail is sent into the
// session so the agent can fix it; at the limit the owner is called instead.
// Only a command that can't be run at all is an error.
func (e *Engine) verify(ctx context.Context, l *launch) (bool, error) {
	t, script := l.t, e.cfg.Run.Verify
	timeout := e.cfg.Run.VerifyTimeout.Std()
	e.emit(Event{Kind: VerifyStarted, Detail: script})
	c := runner.Shell(script, e.dir.Root(), timeout)
	c.Combined = true
	res, err := e.runner.Run(ctx, c)

	var why string
	switch {
	case errors.Is(err, runner.ErrTimeout):
		why = "timed out after " + timeout.String()
	case err != nil:
		if ctx.Err() != nil {
			return false, err
		}
		return false, fmt.Errorf("run the verify command for %s: %w", t.ID, err)
	case res.ExitCode != 0:
		why = fmt.Sprintf("exit status %d", res.ExitCode)
	default:
		l.cur.VerifyAttempts = 0
		if err := e.dir.SaveRun(e.run); err != nil {
			return false, err
		}
		e.log(state.Event{Type: state.EventVerifyPassed})
		e.emit(Event{Kind: VerifyPassed, Detail: script})
		return true, nil
	}

	l.cur.VerifyAttempts++
	n, max := l.cur.VerifyAttempts, e.cfg.Run.VerifyMaxAttempts
	if err := e.dir.SaveRun(e.run); err != nil {
		return false, err
	}
	// The log keeps the result, never the output (SPEC §13).
	e.log(state.Event{Type: state.EventVerifyFailed, Detail: fmt.Sprintf("attempt %d of %d: %s", n, max, why)})
	if err := e.dir.RemoveSignal(t.ID); err != nil {
		return false, err
	}
	e.emit(Event{Kind: VerifyFailed, Detail: fmt.Sprintf("`%s` failed (%s), attempt %d of %d", script, why, n, max)})

	if n >= max {
		e.emit(Event{Kind: VerifyLimit, Detail: fmt.Sprintf("verify failed %d times in a row; igris stops sending failures to the session", n)})
		e.needsYou(ctx, notifyVerifyLimit, fmt.Sprintf("verification failed %d times; fix it, retry or skip the task", n))
		return false, nil
	}
	if l.lost {
		// Nobody to send the failure to: the owner decides.
		e.lose(ctx, l)
		return false, nil
	}
	e.settle(ctx, l.sess, verifyIdleWait)
	msg := fmt.Sprintf("igris verification `%s` failed (%s). Last lines of its output:\n\n```\n%s\n```\n\nFix the problem, then run `igris done %s` again.\n",
		script, why, tail(textsafe.Clean(string(res.Stdout)), verifyTailLines), t.ID)
	switch err := l.sess.Prompt(ctx, msg); {
	case errors.Is(err, backend.ErrSessionGone):
		e.lose(ctx, l)
	case err != nil:
		if ctx.Err() != nil {
			return false, err
		}
		e.warn(fmt.Sprintf("send the verify failure to %s: %v", t.ID, err))
		e.needsYou(ctx, notifyNeedsInput, "the verify failure could not be sent to the session; tell it yourself")
	}
	return false, nil
}

// tail returns the last n lines of out.
func tail(out string, n int) string {
	out = strings.TrimRight(out, "\n")
	if strings.TrimSpace(out) == "" {
		return "(no output)"
	}
	lines := strings.Split(out, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
