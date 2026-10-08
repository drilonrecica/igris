package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

// CommandKind says what the owner asked for.
type CommandKind int

// Owner commands.
const (
	// CmdPause toggles pause-after-task (SPEC §15.3): while it is on, the
	// current task finishes normally but no further task is launched; the
	// run stays alive and continues when it is toggled off.
	CmdPause CommandKind = iota + 1
	// CmdStop ends the run now. A running session is left open and the state
	// is kept, so a later `igris arise` can pick it up (SPEC §13).
	CmdStop
	// CmdDone marks the current task done; Text is the note. It is the
	// owner's decision, so verification is skipped.
	CmdDone
	// CmdSkip skips the current task; Text is the reason. For an agent task
	// the UI confirms before sending it; the engine applies it directly.
	CmdSkip
	// CmdRetry closes the current task's session and opens a new one: with
	// Continue the previous conversation goes on (`claude --resume`),
	// otherwise a fresh session starts with Resumed=true.
	CmdRetry
	// CmdAnswer answers the pending question (see Question); Yes is the
	// answer. With Question set to QuestionConfirmReset it answers a reset
	// request instead, the one for Task ("" = the oldest).
	CmdAnswer
	// CmdMode sets the run mode for the sessions launched from now on
	// (SPEC §7.2); Text is the mode. Skip-permissions mode needs Yes: the
	// owner typed its confirmation (SPEC §7.3), unless the run was started
	// with it confirmed.
	CmdMode
	// CmdTaskMode overrides the run mode of one task (SPEC §7.2): Task is
	// the task, Text the mode. It applies to the task's next session, like
	// CmdMode; skip-permissions mode needs Yes the same way.
	CmdTaskMode
)

func (k CommandKind) String() string {
	switch k {
	case CmdPause:
		return "pause"
	case CmdStop:
		return "stop"
	case CmdDone:
		return "done"
	case CmdSkip:
		return "skip"
	case CmdRetry:
		return "retry"
	case CmdAnswer:
		return "answer"
	case CmdMode:
		return "mode"
	case CmdTaskMode:
		return "task mode"
	}
	return "unknown"
}

// Command is an owner action sent to a running engine.
type Command struct {
	Kind     CommandKind
	Text     string // CmdDone: the note; CmdSkip: the reason; CmdMode, CmdTaskMode: the mode
	Continue bool   // CmdRetry: continue the conversation instead of starting fresh
	Yes      bool   // CmdAnswer: the answer; CmdMode, CmdTaskMode: skip permissions confirmed
	Task     string // CmdTaskMode: the task whose mode is overridden; CmdAnswer: the reset request answered
	// Question is the question a CmdAnswer answers: "" for the one the
	// current task waits on, or QuestionConfirmReset.
	Question Question
}

// Send queues c for the run. It may be called from any goroutine, including
// from the Events callback; it never blocks and never drops a command.
func (e *Engine) Send(c Command) {
	e.mu.Lock()
	e.queue = append(e.queue, c)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// drain applies the queued run-wide commands in order and keeps the
// task-scoped ones for the loop that is waiting on the current task.
func (e *Engine) drain() {
	e.mu.Lock()
	queue := e.queue
	e.queue = nil
	e.mu.Unlock()
	for _, c := range queue {
		switch c.Kind {
		case CmdPause:
			e.pause = !e.pause
			if e.pause {
				e.emit(Event{Kind: PauseOn})
			} else {
				if e.hold {
					e.hold = false // the owner looked at the changed plan
					e.logRun(state.Event{Type: state.EventNeedsYouClear, Reason: state.ReasonPlanChanged})
				}
				e.emit(Event{Kind: PauseOff})
			}
		case CmdStop:
			e.stop = true
		case CmdMode, CmdTaskMode:
			e.setMode(c)
		case CmdAnswer:
			if isResetAnswer(c) {
				// Run-wide: a reset request can be for any task (SPEC §6.2).
				e.resetAnswers = append(e.resetAnswers, c)
				continue
			}
			e.pending = append(e.pending, c)
		default:
			e.pending = append(e.pending, c)
		}
	}
}

// isResetAnswer reports whether c answers a reset request.
func isResetAnswer(c Command) bool {
	return c.Kind == CmdAnswer && c.Question == QuestionConfirmReset
}

// setMode applies a CmdMode or CmdTaskMode. A running session keeps its
// mode.
func (e *Engine) setMode(c Command) {
	m := strings.ToLower(strings.TrimSpace(c.Text))
	task := strings.TrimSpace(c.Task)
	switch {
	case c.Kind == CmdTaskMode && task == "":
		e.reject(c, "no task given")
		return
	case !ValidMode(m):
		e.reject(c, fmt.Sprintf("unknown run mode %q (want default|accept|auto|plan|yolo)", c.Text))
		return
	case m == ModeYolo && !c.Yes && !e.yoloConfirmed():
		e.reject(c, "skip-permissions mode needs the owner's typed confirmation")
		return
	case m == ModeYolo:
		e.yoloOK = true
	}
	if c.Kind == CmdTaskMode {
		e.overrides[task] = m
		e.emit(Event{Kind: TaskModeChanged, Task: task, Detail: m})
		return
	}
	e.runMode = m
	e.emit(Event{Kind: ModeChanged, Detail: m})
}

// takeCommands returns the task-scoped commands received so far.
func (e *Engine) takeCommands() []Command {
	cmds := e.pending
	e.pending = nil
	return cmds
}

// reject tells the owner that c can't be used right now.
func (e *Engine) reject(c Command, why string) {
	e.warn("ignored " + c.Kind.String() + ": " + why)
}

// stopping applies pending commands and reports whether the run should end
// now: the owner sent stop or ctx is done.
func (e *Engine) stopping(ctx context.Context) bool {
	e.drain()
	return e.stop || ctx.Err() != nil
}

// wait blocks for d, or less if a command arrives or ctx ends. Every loop
// that waits goes through here, so commands are never left sitting for a
// whole poll interval, and quiet-hours digests go out when the window ends
// even while the run waits on the owner.
func (e *Engine) wait(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-e.wake:
	case <-e.clock.After(d):
	}
	e.flushHeld(ctx)
}
