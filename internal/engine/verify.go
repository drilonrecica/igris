package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
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

// verifyProfile resolves t's verify profile and its command from the
// config snapshot (SPEC §6.4): the Verify cell, then [phases.<id>] verify,
// then "default". Both are "" when the task has no verification.
func (e *Engine) verifyProfile(t *plan.Task) (profile, command string) {
	phase := ""
	if t.Phase != nil {
		phase = t.Phase.ID
	}
	return e.cfg.VerifyFor(t.Verify, phase)
}

// verifyOff reports whether t's Verify cell or its phase turns
// verification off with "none" (SPEC §6.4), as the dry run shows it.
func (e *Engine) verifyOff(t *plan.Task) bool {
	phase := ""
	if t.Phase != nil {
		phase = t.Phase.ID
	}
	return e.cfg.VerifyChoice(t.Verify, phase) == plan.VerifyNone
}

// verifies reports whether a done signal for t is verified: it has a
// verify profile and this is no dry run, which only shows the profile.
func (e *Engine) verifies(t *plan.Task) bool {
	_, command := e.verifyProfile(t)
	return command != "" && !e.opts.noVerify
}

// verify runs the verify command of l's task's profile (SPEC §6.4) and
// reports whether it passed. On failure the signal is
// deleted and, below verify_max_attempts, the output tail is sent into the
// session so the agent can fix it; at the limit the owner is called instead.
// Only a command that can't be run at all is an error.
func (e *Engine) verify(ctx context.Context, l *launch) (bool, error) {
	t := l.t
	profile, script := e.verifyProfile(t)
	timeout := e.cfg.Run.VerifyTimeout.Std()
	e.emit(Event{Kind: VerifyStarted, Detail: script})
	c := runner.Shell(script, e.dir.Root(), timeout)
	c.Combined = true
	began := e.clock.Now()
	res, err := e.runner.Run(ctx, c)
	took := e.sinceMS(began)

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
		e.log(state.Event{Type: state.EventVerifyPassed, Profile: profile, DurationMS: took, Detail: "profile " + profile})
		e.emit(Event{Kind: VerifyPassed, Detail: script})
		return true, nil
	}

	l.cur.VerifyAttempts++
	n, max := l.cur.VerifyAttempts, e.cfg.Run.VerifyMaxAttempts
	if err := e.dir.SaveRun(e.run); err != nil {
		return false, err
	}
	// The log keeps the result, never the output (SPEC §13).
	e.log(state.Event{Type: state.EventVerifyFailed, Profile: profile, DurationMS: took, Detail: fmt.Sprintf("profile %s: attempt %d of %d: %s", profile, n, max, why)})
	if err := e.dir.RemoveSignal(t.ID); err != nil {
		return false, err
	}
	e.emit(Event{Kind: VerifyFailed, Detail: fmt.Sprintf("%s (`%s`) failed (%s), attempt %d of %d", profile, script, why, n, max)})

	if n >= max {
		e.emit(Event{Kind: VerifyLimit, Detail: fmt.Sprintf("verify failed %s in a row; igris stops sending failures to the session", times(n))})
		e.needsYou(ctx, notifyVerifyLimit, state.ReasonVerifyLimit, fmt.Sprintf("verification failed %s; fix it, retry or skip the task", times(n)), "")
		return false, nil
	}
	if l.lost {
		// Nobody to send the failure to: the owner decides.
		e.lose(ctx, l)
		return false, nil
	}
	e.settle(ctx, l.sess, verifyIdleWait)
	msg := fmt.Sprintf("igris verification `%s` (`%s`) failed (%s). Last lines of its output:\n\n```\n%s\n```\n\nFix the problem, then run `igris done %s` again.\n",
		profile, script, why, tail(textsafe.Clean(string(res.Stdout)), verifyTailLines), t.ID)
	switch err := l.sess.Prompt(ctx, msg); {
	case errors.Is(err, backend.ErrSessionGone):
		e.lose(ctx, l)
	case err != nil:
		if ctx.Err() != nil {
			return false, err
		}
		e.warn(fmt.Sprintf("send the verify failure to %s: %v", t.ID, err))
		e.needsYou(ctx, notifyNeedsInput, state.ReasonVerifyNotSent, "the verify failure could not be sent to the session; tell it yourself", "verify failure not sent to the session")
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

// times is "once", "2 times", …
func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}
