package engine

import (
	"context"
	"path/filepath"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
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
	StaleSignal     EventKind = "stale_signal"      // a signal from before the task started was ignored
	StraySignal     EventKind = "stray_signal"      // a signal for another task (or an unreadable one) is kept, never applied
	NeedsYou        EventKind = "needs_you"         // the task waits on the owner; Detail says why
	NeedsYouClear   EventKind = "needs_you_clear"   // the agent is working again
	SessionLost     EventKind = "session_lost"      // the pane is gone or Claude Code exited without a signal
	Asked           EventKind = "asked"             // Question waits for the owner's answer; Detail is the question
	Retrying        EventKind = "retrying"          // the session is replaced; Detail is "continue" or "fresh"
	TaskSkipped     EventKind = "task_skipped"      // Detail is the reason; Changes holds the cells written
	YourTurn        EventKind = "your_turn"         // a user task waits for the owner; Detail is the full task text
	VerifyStarted   EventKind = "verify_started"    // Detail is the verify command
	VerifyPassed    EventKind = "verify_passed"     // Detail is the verify command
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
	// Detail is a short human-readable addition; see the kinds.
	Detail  string
	Changes []plan.Change
	Waiting []plan.Waiting
	Session *backend.SessionRef
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
	}
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

// Notification events (SPEC §10) the engine raises so far.
const (
	notifyNeedsInput  = "needs_input"
	notifySessionLost = "session_lost"
	notifyVerifyLimit = "verify_failed_limit"
	notifyPhaseDone   = "phase_done"
	notifyPhaseStuck  = "phase_stuck"
	notifyRunError    = "run_error"
)

// toast shows a backend notification for a SPEC §10 event, if the backend
// channel is enabled. what says what happened in a few words; it must never
// carry file contents, diffs or command output. Delivery is best effort.
//
// This is the engine's only notification path until the router (M5) takes
// over.
func (e *Engine) toast(ctx context.Context, event, what string) {
	if !e.cfg.Notify.Backend.Enabled {
		return
	}
	sound := backend.SoundRequest
	if event == notifyPhaseDone {
		sound = backend.SoundDone
	}
	body := "phase " + e.phase
	if e.task != nil {
		body += " · " + e.task.t.ID + " " + e.task.t.Title
	}
	body += ": " + what
	n := backend.Notification{Title: "igris · " + filepath.Base(e.dir.Root()), Body: body, Sound: sound}
	if err := e.be.Notify(ctx, n); err != nil {
		e.warn("notification " + event + " not shown: " + err.Error())
		return
	}
	e.log(state.Event{Type: state.EventNotification, Detail: event})
}
