package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// hookTailLines is how much of a failed hook's output the feed shows
// (SPEC §6.7).
const hookTailLines = 20

// Task hook names, as in [hooks] and in the messages.
const (
	hookBefore = "before_task"
	hookAfter  = "after_task"
)

// runsHooks reports whether task hooks run in this run: never in a dry
// run, which only says they would (SPEC §6.7).
func (e *Engine) runsHooks() bool { return !e.opts.noHooks }

// runHook runs the task hook name with argv from the config snapshot for
// l's task (SPEC §6.7): as argv, never through a shell, in the project
// root, with igris's environment plus the IGRIS_* variables. result is
// IGRIS_RESULT, set for after_task only. It returns the short reason of a
// failure ("" when the hook passed or there is none); the output tail is
// shown in the feed only. err is set only when ctx ended.
func (e *Engine) runHook(ctx context.Context, l *launch, name string, argv []string, result plan.Status) (reason string, err error) {
	if len(argv) == 0 || !e.runsHooks() {
		return "", nil
	}
	t := l.t
	phase := ""
	if t.Phase != nil {
		phase = t.Phase.ID
	}
	env := append(os.Environ(),
		"IGRIS_TASK_ID="+t.ID,
		"IGRIS_PHASE="+phase,
		"IGRIS_RANK="+t.Rank,
		"IGRIS_MODEL="+l.model,
	)
	if name == hookAfter {
		env = append(env, "IGRIS_RESULT="+result.String())
	}
	timeout := e.cfg.Hooks.TimeoutOrDefault()
	e.emit(Event{Kind: HookStarted, Detail: name})
	res, runErr := e.runner.Run(ctx, runner.Cmd{
		Name:     argv[0],
		Args:     argv[1:],
		Dir:      e.dir.Root(),
		Env:      env,
		Timeout:  timeout,
		Combined: true,
	})
	if runErr != nil && ctx.Err() != nil {
		return "", runErr
	}
	why := hookFailure(res, runErr, timeout)
	if why == "" {
		return "", nil
	}
	reason = textsafe.Line(name + " hook failed: " + why)
	// The run log gets the reason, never the output (SPEC §6.7).
	e.log(state.Event{Type: state.EventError, Detail: reason})
	e.emit(Event{Kind: HookFailed, Detail: reason, Output: hookOutput(res.Stdout)})
	e.toast(ctx, notifyRunError, reason)
	return reason, nil
}

// hookFailure says in fixed words why a hook failed ("" if it passed). The
// runner's error is never quoted: it names the argv, and an argv from
// igris.toml may carry a token (SPEC §6.7).
func hookFailure(res runner.Result, err error, timeout time.Duration) string {
	if err == nil {
		if res.ExitCode == 0 {
			return ""
		}
		return fmt.Sprintf("exit status %d", res.ExitCode)
	}
	if sig, ok := runner.KilledBy(err); ok {
		return "killed by " + sig
	}
	switch {
	case errors.Is(err, runner.ErrTimeout):
		return "timed out after " + timeout.String()
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return "not found"
	}
	return "can't start it"
}

// hookOutput is the last hookTailLines lines of a hook's output, cleaned
// for the terminal; nil when it printed nothing.
func hookOutput(out []byte) []string {
	text := strings.TrimRight(textsafe.Clean(string(out)), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > hookTailLines {
		lines = lines[len(lines)-hookTailLines:]
	}
	for i, line := range lines {
		lines[i] = textsafe.Line(line)
	}
	return lines
}

// beforeSession runs the before_task hook ahead of a session igris opens
// for l (SPEC §6.7). opened is false when the hook failed: no session is
// opened, the task stays in progress and the owner chooses how to go on.
//
// A stop sent while the hook ran opens no session either: the caller's next
// poll stops the run, leaving the task in progress without a session, as
// a crash there would.
func (e *Engine) beforeSession(ctx context.Context, l *launch) (opened bool, err error) {
	reason, err := e.runHook(ctx, l, hookBefore, e.cfg.Hooks.BeforeTask, plan.InProgress)
	if err != nil {
		return false, err
	}
	if reason == "" {
		if len(e.cfg.Hooks.BeforeTask) > 0 && e.stopping(ctx) {
			l.sess, l.lost = nil, true
			return false, nil
		}
		return true, nil
	}
	l.sess, l.lost = nil, true
	// run_error is the notification (runHook); Needs you is the UI's.
	e.emit(Event{Kind: NeedsYou, Detail: reason + "; no session was opened"})
	e.emit(Event{Kind: Asked, Question: QuestionHookFailed, Detail: "the " + hookBefore + " hook of " + l.t.ID + " failed: retry (runs the hook again), mark the task done, skip it, or stop"})
	return false, nil
}
