package engine

import (
	"context"
	"path/filepath"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// EventKind classifies an Event.
type EventKind string

// Events of a run, in the order a UI typically sees them.
const (
	RunStarted      EventKind = "run_started"
	PhaseStarted    EventKind = "phase_started"
	TaskStarted     EventKind = "task_started"      // marked in progress; Changes holds the cells written
	TaskResumed     EventKind = "task_resumed"      // a task in progress from an earlier run is picked up; Detail says how
	SessionOpened   EventKind = "session_opened"    // Session is set
	TaskDone        EventKind = "task_done"         // Detail is the done note; Changes holds the cells written
	PhaseDone       EventKind = "phase_done"        // every task of the phase is satisfied
	PhaseStuck      EventKind = "phase_stuck"       // Waiting lists the unfinished tasks
	PauseOn         EventKind = "pause_on"          // pause-after-task was switched on
	PauseOff        EventKind = "pause_off"         // ... and off again; a held run continues
	Paused          EventKind = "paused"            // the run holds instead of launching Task
	ModeChanged     EventKind = "mode_changed"      // Detail is the run mode for the next sessions
	TaskModeChanged EventKind = "task_mode_changed" // Task's next session runs in mode Detail (an override)
	ConfigChanged   EventKind = "config_changed"    // igris.toml differs from the snapshot; needs the owner
	ConfigRestored  EventKind = "config_restored"   // igris.toml matches the snapshot again
	PlanChanged     EventKind = "plan_changed"      // rows changed that the run did not write; Detail lists them; the run holds (pause on)
	StaleSignal     EventKind = "stale_signal"      // a signal from before the task started was ignored
	StraySignal     EventKind = "stray_signal"      // a signal for another task (or an unreadable one) is kept, never applied
	NeedsYou        EventKind = "needs_you"         // the task waits on the owner; Detail says why
	NeedsYouClear   EventKind = "needs_you_clear"   // the agent is working again
	SessionLost     EventKind = "session_lost"      // the pane is gone or Claude Code exited without a signal
	Asked           EventKind = "asked"             // Question waits for the owner's answer; Detail is the question
	Retrying        EventKind = "retrying"          // the session is replaced; Detail is "continue" or "fresh"
	TaskSkipped     EventKind = "task_skipped"      // Detail is the reason; Changes holds the cells written
	YourTurn        EventKind = "your_turn"         // a user task waits for the owner; Detail is the full task text
	VerifyStarted   EventKind = "verify_started"    // Detail is the verify command, Verify its profile
	VerifyPassed    EventKind = "verify_passed"     // Detail is the verify command, Verify its profile
	VerifyFailed    EventKind = "verify_failed"     // Detail says why and which attempt
	VerifyLimit     EventKind = "verify_limit"      // verify_max_attempts failures in a row; needs the owner
	Committed       EventKind = "committed"         // Detail is the commit subject
	NotCommitted    EventKind = "not_committed"     // Detail says why: nothing to commit, or the owner declined
	Warning         EventKind = "warning"           // something failed that doesn't stop the run
	RunFailed       EventKind = "run_error"         // Detail is the error; the run stops
	RunStopped      EventKind = "run_stopped"       // the run ended; Detail is the outcome or "error"
)

// Event is one step of a run, delivered to Options.Events. UIs render it;
// the run log and notifications are written by the engine itself.
type Event struct {
	At    time.Time
	Kind  EventKind
	Phase string
	Task  string // task ID; empty for run and phase events
	Title string
	Rank  string
	Model string
	Mode  string
	// Verify is the task's verify profile (SPEC §6.4), "" for none.
	Verify string
	// Detail is a short human-readable addition; see the kinds.
	Detail  string
	Changes []plan.Change
	Waiting []plan.Waiting
	Session *backend.SessionRef
	// ClaudeSession is the Claude Code session UUID; set on SessionOpened so
	// a UI can offer `claude --resume <uuid>`.
	ClaudeSession string
	// Question is set on Asked events.
	Question Question
}

// Question identifies what an Asked event waits for. The owner answers with
// the commands listed for each.
type Question string

const (
	// QuestionConfirmSkip: the agent session asked to skip its task (Detail
	// is the reason). CmdAnswer yes applies the skip, no discards it.
	QuestionConfirmSkip Question = "confirm_skip"
	// QuestionSessionLost: the session is gone. CmdRetry (continue or
	// fresh), CmdDone, CmdSkip or CmdStop.
	QuestionSessionLost Question = "session_lost"
	// QuestionCommit: commit = "ask" and the task is verified; CmdAnswer
	// yes commits, no leaves the changes uncommitted.
	QuestionCommit Question = "commit"
)

// emit stamps ev with the time and the current phase and task and hands it
// to the UI.
func (e *Engine) emit(ev Event) {
	ev.At = e.clock.Now()
	if ev.Phase == "" {
		ev.Phase = e.phase
	}
	if ev.Task == "" && e.task != nil {
		ev = e.task.fill(ev)
		ev.Verify, _ = e.verifyProfile(e.task.t)
	}
	// Details quote notes, plan text and command output: nothing in them may
	// reach the owner's terminal as an escape sequence (SPEC §16).
	ev.Detail, ev.Title, ev.Verify = textsafe.Clean(ev.Detail), textsafe.Line(ev.Title), textsafe.Line(ev.Verify)
	if e.opts.Events != nil {
		e.opts.Events(ev)
	}
}

// warn reports a failure that doesn't stop the run.
func (e *Engine) warn(detail string) { e.emit(Event{Kind: Warning, Detail: detail}) }

// log appends to runs.jsonl. The log is a record, not state: a failed write
// is reported and the run goes on.
func (e *Engine) log(ev state.Event) {
	if ev.Task == "" && e.task != nil {
		ev.Task, ev.Rank, ev.Model = e.task.t.ID, e.task.t.Rank, e.task.model
	}
	if err := e.dir.Append(ev); err != nil {
		e.warn(err.Error())
	}
}

// Notification events (SPEC §10) the engine raises.
const (
	notifyTaskDone    = notify.TaskDone
	notifyNeedsInput  = notify.NeedsInput
	notifySessionLost = notify.SessionLost
	notifyVerifyLimit = notify.VerifyFailedLimit
	notifyPhaseDone   = notify.PhaseDone
	notifyPhaseStuck  = notify.PhaseStuck
	notifyRunError    = notify.RunError
)

// toast sends a SPEC §10 notification through the router: to the backend
// toast and to ntfy and Discord, whichever are set up and want the event.
// what says what happened in a few words; it must never carry file contents,
// diffs or command output. Delivery is best effort: a failing channel is
// reported as a warning and the run goes on. The call waits for the
// channels (at most one timeout plus one retry each, in parallel).
func (e *Engine) toast(ctx context.Context, event notify.Event, what string) {
	m := notify.Message{
		Event:   event,
		Project: filepath.Base(e.dir.Root()),
		Phase:   e.phase,
		What:    what,
	}
	if e.task != nil {
		m.TaskID, m.Title = e.task.t.ID, e.task.t.Title
	}
	for _, r := range e.notifier.Notify(ctx, m) {
		if r.Err != nil {
			e.warn("notification " + string(event) + " not delivered to " + r.Channel + ": " + r.Err.Error())
			continue
		}
		e.log(state.Event{Type: state.EventNotification, Detail: string(event) + " via " + r.Channel})
	}
}
