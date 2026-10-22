package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

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
	// verifyTailBytes bounds the excerpt in bytes too: herdr gets the
	// prompt as one argument, at most 128 KiB on Linux, and one line can be
	// any length.
	verifyTailBytes = 16 << 10
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
	profile, script, c := e.verifyCmd(l)
	e.emit(Event{Kind: VerifyStarted, Detail: script})
	began := e.clock.Now()
	res, err := e.runner.Run(ctx, c)
	return e.verified(ctx, l, profile, script, res, err, e.sinceMS(began))
}

// verifyCmd is the verify command of l's task: its profile's script, run
// with `sh -c` where the task works, with the environment of SPEC §6.4 and
// IGRIS_SLOT in a parallel phase (§6.11).
func (e *Engine) verifyCmd(l *launch) (profile, script string, c runner.Cmd) {
	profile, script = e.verifyProfile(l.t)
	c = runner.Shell(script, e.workDir(l), e.cfg.Run.VerifyTimeout.Std())
	c.Combined = true
	c.Env = append(e.commandEnv(), slotEnv(l)...)
	return profile, script, c
}

// verified acts on the result of l's verify, which ran the script of
// profile for took milliseconds: res and err are what the runner
// returned. It reports whether it passed (see verify).
func (e *Engine) verified(ctx context.Context, l *launch, profile, script string, res runner.Result, err error, took int64) (bool, error) {
	t := l.t
	timeout := e.cfg.Run.VerifyTimeout.Std()
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

// check is the verify of a task in a parallel phase (SPEC §6.11). It
// runs beside the poll loop, so the other tasks' sessions are watched
// meanwhile; step picks up its result. With [parallel] serial_verify only
// the first check of the queue runs, the others waiting in the order
// their done signals came in.
type check struct {
	v       verdict // the done it verifies
	started bool
	profile string
	script  string
	began   time.Time
	cancel  context.CancelFunc
	done    chan struct{} // closed once res and err are set
	res     runner.Result
	err     error
}

// queueVerify verifies the done v of l's task beside the poll loop
// (check): the task waits for its turn, if serial_verify makes it wait.
func (e *Engine) queueVerify(ctx context.Context, l *launch, v verdict) {
	l.check = &check{v: v}
	e.checks = append(e.checks, l)
	e.startChecks(ctx)
}

// startChecks starts the queued checks that may run: every one, or only
// the first with serial_verify.
func (e *Engine) startChecks(ctx context.Context) {
	serial := e.cfg.Parallel.SerialVerifyOn()
	for i, l := range e.checks {
		if serial && i > 0 {
			return
		}
		if !l.check.started {
			e.startCheck(ctx, l)
		}
	}
}

// startCheck starts l's check: the command runs on a goroutine of its
// own, which wakes the poll loop when it ends.
func (e *Engine) startCheck(ctx context.Context, l *launch) {
	k := l.check
	was := e.task
	e.focus(l)
	var c runner.Cmd
	k.profile, k.script, c = e.verifyCmd(l)
	e.emit(Event{Kind: VerifyStarted, Detail: k.script})
	e.task = was
	k.started, k.began, k.done = true, e.clock.Now(), make(chan struct{})
	var run context.Context
	run, k.cancel = context.WithCancel(ctx)
	go func() {
		defer close(k.done)
		k.res, k.err = e.runner.Run(run, c)
		select {
		case e.wake <- struct{}{}:
		default: // a wake-up is already pending
		}
	}()
}

// stepCheck is a poll of l's task while its check is queued or runs:
// nothing happens until it ends; then the result is acted on as verify
// does, and a passed check accepts the task. The next queued check
// starts first.
func (e *Engine) stepCheck(ctx context.Context, l *launch) (ended, stopped bool, err error) {
	k := l.check
	if !k.started {
		return false, false, nil
	}
	select {
	case <-k.done:
	default:
		return false, false, nil
	}
	e.dropCheck(l)
	e.startChecks(ctx)
	if k.err != nil && ctx.Err() != nil {
		return false, false, k.err
	}
	passed, err := e.verified(ctx, l, k.profile, k.script, k.res, k.err, e.sinceMS(k.began))
	if err != nil || !passed {
		return false, false, err // the task stays in progress
	}
	return e.accept(ctx, l, k.v)
}

// dropCheck takes l's check off the queue, stopping its command if it
// still runs (the task was reset, or the run ends).
func (e *Engine) dropCheck(l *launch) {
	k := l.check
	if k == nil {
		return
	}
	l.check = nil
	e.checks = slices.DeleteFunc(e.checks, func(o *launch) bool { return o == l })
	if k.started {
		k.cancel()
		<-k.done
	}
}

// tail returns the last n lines of out, and at most verifyTailBytes of
// them.
func tail(out string, n int) string {
	out = strings.TrimRight(out, "\n")
	if strings.TrimSpace(out) == "" {
		return "(no output)"
	}
	lines := strings.Split(out, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	s := strings.Join(lines, "\n")
	const cut = "… (cut)\n"
	if len(s) > verifyTailBytes {
		s = s[len(s)-(verifyTailBytes-len(cut)):]
		for len(s) > 0 && !utf8.RuneStart(s[0]) {
			s = s[1:] // don't start inside a character
		}
		s = cut + s
	}
	return s
}

// times is "once", "2 times", …
func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}
