package engine

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// EventKind classifies an Event.
type EventKind string

// Events of a run, in the order a UI typically sees them.
const (
	RunStarted        EventKind = "run_started"
	WorktreeReady     EventKind = "worktree_ready" // the run works in a git worktree (SPEC §6.8); Detail says where and on which branch
	WorktreeLeft      EventKind = "worktree_left"  // the run ends; Detail says where its work is and how to merge it
	PhaseStarted      EventKind = "phase_started"
	TaskStarted       EventKind = "task_started"       // marked in progress; Changes holds the cells written
	TaskResumed       EventKind = "task_resumed"       // a task in progress from an earlier run is picked up; Detail says how
	SessionOpened     EventKind = "session_opened"     // Session is set
	TaskDone          EventKind = "task_done"          // Detail is the done note; Changes holds the cells written
	PhaseDone         EventKind = "phase_done"         // every task of the phase is satisfied
	PhaseStuck        EventKind = "phase_stuck"        // Waiting lists the unfinished tasks
	NotRun            EventKind = "not_run"            // Waiting lists the slice's tasks left in the phase, deps unmet (SPEC §5.5)
	PauseOn           EventKind = "pause_on"           // pause-after-task was switched on
	PauseOff          EventKind = "pause_off"          // ... and off again; a held run continues
	Paused            EventKind = "paused"             // the run holds instead of launching Task
	ModeChanged       EventKind = "mode_changed"       // Detail is the run mode for the next sessions
	TaskModeChanged   EventKind = "task_mode_changed"  // Task's next session runs in mode Detail (an override)
	ConfigChanged     EventKind = "config_changed"     // igris.toml differs from the snapshot; needs the owner
	ConfigRestored    EventKind = "config_restored"    // igris.toml matches the snapshot again
	PlanChanged       EventKind = "plan_changed"       // rows changed that the run did not write; Detail lists them; the run holds (pause on)
	StaleSignal       EventKind = "stale_signal"       // a signal from before the task started was ignored
	StraySignal       EventKind = "stray_signal"       // a signal for another task (or an unreadable one) is kept, never applied
	NeedsYou          EventKind = "needs_you"          // the task waits on the owner; Detail says why, Reason the run log's reason
	NeedsYouClear     EventKind = "needs_you_clear"    // the agent is working again
	AgentWaiting      EventKind = "agent_waiting"      // the agent ended its turn but waits on background work Claude Code resumes it for
	AgentWaitingOver  EventKind = "agent_waiting_over" // ... and no longer does (it works, idles or the task ended)
	Nudged            EventKind = "nudged"             // igris reminded the idle agent of igris done once; Detail says so
	SessionLost       EventKind = "session_lost"       // the pane is gone or Claude Code exited without a signal
	TaskOverdue       EventKind = "task_overdue"       // the attempt runs longer than the task's Timeout; Detail says how long
	RankMismatch      EventKind = "rank_mismatch"      // the session runs on a model its rank doesn't resolve to (SPEC §6.9); Detail names both
	UsageRead         EventKind = "usage"              // the current attempt's token usage changed or can't be read (SPEC §6.9); Tokens holds it; not logged as a feed line
	QuotaRead         EventKind = "quota"              // the subscription quota the status line recorded changed (SPEC §6.3); Quota holds it; not logged as a feed line
	LimitHold         EventKind = "limit_hold"         // new starts are held for a usage limit (SPEC §6.9); Limit says which, Task the task that would start, Detail why; sent again when the window or its use changes
	LimitResume       EventKind = "limit_resume"       // ... and no longer are; Detail says why (reset, quota fell to 71%, started anyway)
	SessionLimited    EventKind = "session_limited"    // the task's session waits at a usage limit (SPEC §6.3); Limit says which and until when once the transcript's hit is read (nil before)
	SessionLimitOver  EventKind = "session_limit_over" // ... and works again, or igris told it to continue
	BudgetRead        EventKind = "budgets"            // what the run's budgets used changed (SPEC §6.9); Budgets holds them; not logged as a feed line
	BudgetExceeded    EventKind = "budget_exceeded"    // a budget is reached (SPEC §6.9); Budget says which, Detail what follows; the owner is asked (QuestionBudget)
	BudgetRaised      EventKind = "budget_raised"      // the owner raised a reached budget or let it go on; Budget says which, Detail how
	Asked             EventKind = "asked"              // Question waits for the owner's answer; Detail is the question
	PlanClosed        EventKind = "plan_closed"        // the plan shown for approval is answered or gone; Detail says how
	Retrying          EventKind = "retrying"           // the session is replaced; Detail is "continue" or "fresh"
	TaskSkipped       EventKind = "task_skipped"       // Detail is the reason; Changes holds the cells written
	TaskReset         EventKind = "task_reset"         // Task was put back to ready/blocked by `igris reset`; Detail is its change, Changes the cells written
	ResetDropped      EventKind = "reset_dropped"      // Task's reset request was declined or withdrawn; Detail says which
	YourTurn          EventKind = "your_turn"          // a user task waits for the owner; Detail is the full task text
	VerifyStarted     EventKind = "verify_started"     // Detail is the verify command, Verify its profile
	VerifyPassed      EventKind = "verify_passed"      // Detail is the verify command, Verify its profile
	VerifyFailed      EventKind = "verify_failed"      // Detail says why and which attempt
	VerifyLimit       EventKind = "verify_limit"       // verify_max_attempts failures in a row; needs the owner
	Committed         EventKind = "committed"          // Detail is the commit subject
	NotCommitted      EventKind = "not_committed"      // Detail says why: nothing to commit, or the owner declined
	Integrated        EventKind = "integrated"         // the task's commit is on the run branch (SPEC §6.11); Detail names the branch
	IntegrationFailed EventKind = "integration_failed" // the task didn't integrate (SPEC §6.11); Detail says why and what follows
	Overlap           EventKind = "overlap"            // two active tasks both change one file (SPEC §6.11); Detail names them and the file
	HookStarted       EventKind = "hook_started"       // a task hook starts (SPEC §6.7); Detail is its name, before_task or after_task
	HookFailed        EventKind = "hook_failed"        // a task hook failed (SPEC §6.7); Detail is the short reason, Output its last lines
	Warning           EventKind = "warning"            // something failed that doesn't stop the run
	RunFailed         EventKind = "run_error"          // Detail is the error; the run stops
	RunStopped        EventKind = "run_stopped"        // the run ended; Detail is the outcome or "error"
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
	Detail string
	// Reason is set on NeedsYou: the run log's reason (a state.Reason*).
	// For any but an idle, blocked or waiting episode, Detail reads
	// "<what>; <what to do>", and a card leads with what (SPEC §15.3).
	Reason  string
	Changes []plan.Change
	Waiting []plan.Waiting
	Session *backend.SessionRef
	// ClaudeSession is the Claude Code session UUID; set on SessionOpened so
	// a UI can offer `claude --resume <uuid>`.
	ClaudeSession string
	// Run is the run ID (SPEC §13), set on RunStarted; the events after
	// it belong to that run.
	Run string
	// Slice is set on RunStarted for a sliced run (SPEC §5.5): the IDs of
	// the tasks it may run, in plan order. Nil runs the phases whole.
	Slice []string
	// Question is set on Asked events.
	Question Question
	// Output is the last lines of a failed task hook's output, cleaned for
	// the terminal, on HookFailed; the conflicting files or the verify
	// output's last lines on IntegrationFailed and Asked with
	// QuestionIntegration, the files in the way with QuestionFilesInTheWay
	// (SPEC §6.11). It is shown, never logged or sent.
	Output []string
	// Plan is the plan text, cleaned for the terminal, and Then the mode
	// an approval switches the session to; set on Asked with
	// QuestionPlanApproval. The plan is shown, never logged or sent.
	Plan string
	Then string
	// Tokens is the attempt's usage, set on UsageRead (SPEC §6.9, §15.1).
	Tokens *Tokens
	// Quota is the subscription quota, set on QuotaRead (SPEC §6.3, §15.1).
	Quota *state.Quota
	// Slot is the task's slot in a parallel phase (SPEC §6.11), set on the
	// events of an active task that holds one; nil otherwise.
	Slot *int
	// Limit is the usage limit on LimitHold, LimitResume and
	// SessionLimited (SPEC §6.9): the window, its reset and, for a hold at
	// the threshold, the window's use; nil while the window isn't known.
	Limit *Limit
	// Budget is the budget on BudgetExceeded, BudgetRaised and Asked with
	// QuestionBudget; Budgets every budget of the run on BudgetRead (SPEC
	// §6.9).
	Budget  *BudgetUse
	Budgets []BudgetUse
}

// Limit is a usage limit a UI shows (SPEC §6.9, §15): a quota window at or
// above the threshold, or one a session hit. Its countdown is drawn from
// ResetsAt against the UI's clock.
type Limit struct {
	// Window is five_hour, seven_day or spend_limit.
	Window string `json:"window,omitempty"`
	// ResetsAt is when the window resets; UTC.
	ResetsAt time.Time `json:"resets_at,omitzero"`
	// Percent is the window's use that holds new starts; 0 after a hit.
	Percent float64 `json:"percent,omitempty"`
	// Task is the task whose session hit the window; "" for the quota.
	Task string `json:"task,omitempty"`
}

// Label is the limit's words for a header or a status line: "5h limit
// 96%", "7d limit" after a hit.
func (l Limit) Label() string {
	s := look.Window(l.Window) + " limit"
	if l.Percent > 0 {
		s += fmt.Sprintf(" %.0f%%", math.Round(l.Percent))
	}
	return s
}

// Episode keys one limit episode: the window and its reset (SPEC §10).
func (l Limit) Episode() string { return l.Window + "@" + l.ResetsAt.UTC().Format(time.RFC3339) }

// Tokens is what the current attempt's transcripts record so far, for the
// run card's counter. It never carries message content.
type Tokens struct {
	// In is input + cache read + cache write and Out the output tokens, the
	// main thread's and the subagents' together.
	In, Out int64
	// Models are the model ids the main thread ran on (usage.ValidModel).
	Models []string
	// Unavailable says igris can't read the transcript; In and Out are 0.
	Unavailable bool
	// USD is the price estimate of the tokens (SPEC §6.9), shown only when
	// Priced: every token was of a model with a price.
	USD    float64
	Priced bool
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
	// QuestionHookFailed: the before_task hook failed and no session was
	// opened (SPEC §6.7). CmdRetry (runs the hook again, then starts a
	// fresh session), CmdDone, CmdSkip or CmdStop.
	QuestionHookFailed Question = "hook_failed"
	// QuestionCommit: commit = "ask" and the task is verified; CmdAnswer
	// yes commits, no leaves the changes uncommitted.
	QuestionCommit Question = "commit"
	// QuestionConfirmReset: a reset signal asks to put Task back to
	// ready/blocked (SPEC §6.2). Sessions can write signals, so the owner
	// confirms it: CmdAnswer with Question QuestionConfirmReset and Task,
	// yes applies the reset, no drops it. It can be asked while another
	// question waits.
	QuestionConfirmReset Question = "confirm_reset"
	// QuestionPlanApproval: the session presents a plan for approval
	// (SPEC §6.3); Plan is its text, Then the mode an approval switches
	// to. CmdPlan answers it; PlanClosed says it is over, also when the
	// owner answered in the pane.
	QuestionPlanApproval Question = "plan_approval"
	// QuestionIntegration: the task didn't integrate and igris won't run
	// it again by itself (SPEC §6.11); its worktree is kept. CmdRetry
	// (fresh: run it again from the start), CmdDone (the owner integrated
	// it by hand), CmdSkip or CmdStop.
	QuestionIntegration Question = "integration"
	// QuestionFilesInTheWay: the task's rebased branch didn't fast-forward
	// because files where the run branch is checked out are in the way
	// (SPEC §6.11); Output names them. CmdRetry (try again: only the
	// fast-forward, no new session), CmdDone, CmdSkip or CmdStop.
	QuestionFilesInTheWay Question = "files_in_the_way"
	// QuestionBudget: a budget is reached (SPEC §6.9); Budget says which.
	// It is the run's, not a task's. CmdBudget answers it (a new value or
	// continue), or CmdStop.
	QuestionBudget Question = "budget"
)

// Kind names the question in a word or two, as the Needs-you lines say
// it (SPEC §15.1): `! M1-04 needs you · commit?`.
func (q Question) Kind() string {
	switch q {
	case QuestionCommit:
		return "commit?"
	case QuestionConfirmSkip:
		return "skip?"
	case QuestionSessionLost:
		return "session lost"
	case QuestionHookFailed:
		return "hook failed"
	case QuestionConfirmReset:
		return "reset?"
	case QuestionPlanApproval:
		return "a plan to approve"
	case QuestionIntegration, QuestionFilesInTheWay:
		return "integration"
	case QuestionBudget:
		return "budget reached"
	}
	return "your answer"
}

// emit stamps ev with the time and the current phase and task and hands it
// to the UI.
func (e *Engine) emit(ev Event) {
	e.trackTail()
	ev.At = e.clock.Now()
	if ev.Phase == "" {
		ev.Phase = e.phase
	}
	if ev.Task == "" && e.task != nil {
		ev = e.task.fill(ev)
		ev.Verify, _ = e.verifyProfile(e.task.t)
	}
	if l := e.launchOf(ev.Task); ev.Slot == nil && l != nil && l.cur != nil && l.cur.Slot != nil {
		slot := *l.cur.Slot
		ev.Slot = &slot // the card's head shows it (SPEC §15.1)
	}
	// Details quote notes, plan text and command output: nothing in them may
	// reach the owner's terminal as an escape sequence (SPEC §16).
	ev.Detail, ev.Title, ev.Verify = textsafe.Clean(ev.Detail), textsafe.Line(ev.Title), textsafe.Line(ev.Verify)
	ev.Plan, ev.Then = textsafe.Clean(ev.Plan), textsafe.Line(ev.Then)
	// So do the rank, model, mode and phase: igris.toml and the plan name
	// them, and a session can edit both (V10-05).
	ev.Rank, ev.Model, ev.Mode, ev.Phase = textsafe.Line(ev.Rank), textsafe.Line(ev.Model), textsafe.Line(ev.Mode), textsafe.Line(ev.Phase)
	if e.opts.Events != nil {
		e.opts.Events(ev)
	}
}

// trackTail records the sessions Tail and TailTask read: each active agent
// task's, while it is open, and the current one's: the first active task
// (state.json's current one), else the task being started. Called from
// the goroutine in Run, which owns e.task and e.active.
func (e *Engine) trackTail() {
	open := func(l *launch) backend.Session {
		if l != nil && l.sess != nil && !l.lost {
			return l.sess
		}
		return nil
	}
	cur := open(e.first())
	if cur == nil {
		cur = open(e.task)
	}
	var tasks map[string]backend.Session
	for _, l := range e.active {
		if s := open(l); s != nil {
			if tasks == nil {
				tasks = map[string]backend.Session{}
			}
			tasks[l.t.ID] = s
		}
	}
	if s := open(e.task); s != nil && tasks[e.task.t.ID] == nil {
		if tasks == nil {
			tasks = map[string]backend.Session{}
		}
		tasks[e.task.t.ID] = s // started, not yet active
	}
	e.tailMu.Lock()
	e.tailSess, e.tailTasks = cur, tasks
	e.tailMu.Unlock()
}

var _ backend.Tailer = (*Engine)(nil)

// Tail implements backend.Tailer for the TUI's live tail (SPEC §15.3): the
// last lines of the current task's session. It returns nothing when no
// session is open or the backend can't tail. It is safe to call from any
// goroutine; its output goes nowhere but the caller.
func (e *Engine) Tail(ctx context.Context, n int) ([]string, error) {
	return e.TailTask(ctx, "", n)
}

// TailTask is Tail for the session of active task id (SPEC §15.1: every
// card has its tail); "" is the current task's, the first active one. It returns nothing when
// that task has no open session.
func (e *Engine) TailTask(ctx context.Context, id string, n int) ([]string, error) {
	e.tailMu.Lock()
	s := e.tailSess
	if id != "" {
		s = e.tailTasks[id]
	}
	e.tailMu.Unlock()
	t, ok := s.(backend.Tailer)
	if !ok {
		return nil, nil
	}
	return t.Tail(ctx, n)
}

// warn reports a failure that doesn't stop the run.
func (e *Engine) warn(detail string) { e.emit(Event{Kind: Warning, Detail: detail}) }

// log appends to runs.jsonl (SPEC §13) with the run ID and, for an event
// without a task, the current task with its attempt. The log is a record,
// not state: a failed write is reported and the run goes on.
func (e *Engine) log(ev state.Event) {
	if l := e.task; l != nil {
		if ev.Task == "" {
			ev.Task, ev.Rank, ev.Model = l.t.ID, l.t.Rank, l.model
		}
		if ev.Task == l.t.ID {
			if ev.Model == "" {
				ev.Model = l.model
			}
			if ev.Attempt == 0 {
				ev.Attempt = l.attempt
			}
		}
	}
	e.logRun(ev)
}

// logRun appends ev with the run ID only: for run-wide events while a task
// is current.
func (e *Engine) logRun(ev state.Event) {
	ev.Run = e.runID
	if err := e.dir.Append(ev); err != nil {
		e.warn(err.Error())
	}
}

// maxLogTitle is how much of a task title the run log keeps.
const maxLogTitle = 80

// taskInfo is the phase, title and owner a task_started or task_resumed
// line carries: the title as notifications show it, cleaned and cut.
func taskInfo(ev state.Event, t *plan.Task) state.Event {
	ev.Phase, ev.Owner = phaseOf(t), string(t.Owner)
	title := []rune(textsafe.Line(notify.PlainTitle(t.Title)))
	if len(title) > maxLogTitle {
		title = title[:maxLogTitle]
	}
	ev.Title = strings.TrimSpace(string(title))
	return ev
}

// sinceMS is the time from start to now in milliseconds, 0 when start is
// unknown.
func (e *Engine) sinceMS(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	return e.clock.Now().Sub(start).Milliseconds()
}

// waitOn logs that the current task waits on the owner for reason
// (a state.Reason*); detail is the few words the notification says. The
// wait stays open on the task until it is cleared or the task ends. A
// wait for reason that is already open is not logged again: it is the
// same wait (a second skip request, the verify limit hit again).
func (e *Engine) waitOn(reason, detail string) {
	if l := e.task; l != nil {
		if slices.Contains(l.waits, reason) {
			return
		}
		l.waits = append(l.waits, reason)
	}
	e.log(state.Event{Type: state.EventNeedsYou, Reason: reason, Detail: detail})
}

// waitOver logs that the current task's wait for reason ended while the
// task goes on. It does nothing if no such wait is open.
func (e *Engine) waitOver(reason string) {
	l := e.task
	if l == nil {
		return
	}
	i := slices.Index(l.waits, reason)
	if i < 0 {
		return
	}
	l.waits = slices.Delete(l.waits, i, i+1)
	e.log(state.Event{Type: state.EventNeedsYouClear, Reason: reason})
}

// waitsOver ends every open wait of the current task, e.g. on a retry.
func (e *Engine) waitsOver() {
	if l := e.task; l != nil {
		for _, r := range slices.Clone(l.waits) {
			e.waitOver(r)
		}
	}
}

// Notification events (SPEC §10) the engine raises.
const (
	notifyTaskDone     = notify.TaskDone
	notifyNeedsInput   = notify.NeedsInput
	notifySessionLost  = notify.SessionLost
	notifyTaskOverdue  = notify.TaskOverdue
	notifyRankMismatch = notify.RankMismatch
	notifyVerifyLimit  = notify.VerifyFailedLimit
	notifyPhaseDone    = notify.PhaseDone
	notifyPhaseStuck   = notify.PhaseStuck
	notifyRunError     = notify.RunError
	notifyUsageLimit   = notify.UsageLimit
)

// toast sends a SPEC §10 notification through the router: to the backend
// toast and to the remote channels, whichever are set up and want the event.
// what says what happened in a few words; it must never carry file contents,
// diffs or command output. Delivery is best effort: a failing channel is
// reported as a warning and the run goes on. The call waits for the
// channels (at most one timeout plus one retry each, in parallel).
func (e *Engine) toast(ctx context.Context, event notify.Event, what string) {
	e.toastAsking(ctx, event, what, "")
}

// toastAsking is toast for a message that announces the pending question
// q of the current task ("" for none): ntfy gives it answer buttons (SPEC
// §10). It is sent after q's Asked event, so serve knows the question by
// the time it makes them.
func (e *Engine) toastAsking(ctx context.Context, event notify.Event, what string, q Question) {
	m := notify.Message{
		Event:    event,
		Project:  filepath.Base(e.dir.Root()),
		Phase:    e.phase,
		What:     what,
		RunID:    e.runID,
		At:       e.clock.Now(),
		Question: string(q),
	}
	if e.task != nil {
		m.TaskID, m.Title = e.task.t.ID, e.task.t.Title
	}
	if event == notifyNeedsInput && e.gathering {
		e.needs = append(e.needs, m) // sent with the poll's others (sendNeeds)
		return
	}
	e.notified(e.notifier.Notify(ctx, m))
}

// gather starts collecting the needs_input notifications of one poll over
// several active tasks (SPEC §10): sendNeeds sends them, as one message
// when they are for more than one task. With one active task there is
// nothing to group and each goes out at once.
func (e *Engine) gather() { e.gathering = len(e.active) > 1 }

// sendNeeds sends the needs_input notifications gathered so far and stops
// gathering. Those for one task go out as they are; those for several go
// out as one, "2 tasks need you: M1-03, M1-05", with no title and the
// first task's ID for templates. Every wait calls it, so a question asked
// in the middle of a poll is never held back by the gathering.
func (e *Engine) sendNeeds(ctx context.Context) {
	e.gathering = false
	needs := e.needs
	e.needs = nil
	var ids []string
	for _, m := range needs {
		if !slices.Contains(ids, m.TaskID) {
			ids = append(ids, m.TaskID)
		}
	}
	if len(ids) < 2 {
		for _, m := range needs {
			e.notifyAs(ctx, m)
		}
		return
	}
	last := needs[len(needs)-1]
	e.notifyAs(ctx, notify.Message{
		Event: notifyNeedsInput, Project: last.Project, Phase: last.Phase,
		TaskID: ids[0], Tasks: ids, What: notify.NeedYou(ids),
		RunID: last.RunID, At: last.At,
	})
}

// notifyAs sends m with its task in focus, so the run log files the
// delivery under that task: a poll's gathered messages go out after its
// steps, whatever task is in focus then.
func (e *Engine) notifyAs(ctx context.Context, m notify.Message) {
	was := e.task
	if l := e.launchOf(m.TaskID); l != nil {
		e.focus(l)
	}
	e.notified(e.notifier.Notify(ctx, m))
	e.task = was
}

// notified logs how each delivery went: sent, held for quiet hours or a
// digest; a failure is a warning.
func (e *Engine) notified(results []notify.Result) {
	for _, r := range results {
		what := string(r.Event)
		if r.Event == notify.Digest {
			what = fmt.Sprintf("digest of %d", r.Count)
		}
		switch {
		case r.Err != nil:
			e.warn("notification " + what + " not delivered to " + r.Channel + ": " + r.Err.Error())
		case r.Held:
			e.log(state.Event{Type: state.EventNotification, Detail: what + " held for " + r.Channel + " (quiet hours)"})
		case r.Event == notify.Digest:
			// A digest belongs to no task.
			e.logRun(state.Event{Type: state.EventNotification, Detail: what + " via " + r.Channel})
		default:
			e.log(state.Event{Type: state.EventNotification, Detail: what + " via " + r.Channel})
		}
	}
}

// flushHeld asks the notifier to send what quiet hours held once the
// window is over: at most once per poll interval, from every wait.
func (e *Engine) flushHeld(ctx context.Context) {
	h, ok := e.notifier.(Holder)
	if !ok || ctx.Err() != nil {
		return
	}
	now := e.clock.Now()
	if !e.lastFlush.IsZero() && now.Sub(e.lastFlush) < e.cfg.PollInterval.Std() {
		return
	}
	e.lastFlush = now
	e.notified(h.Flush(ctx))
}

// flushTasks sends the task_done messages collected for a digest: the
// phase is over.
func (e *Engine) flushTasks(ctx context.Context) {
	if h, ok := e.notifier.(Holder); ok {
		e.notified(h.FlushTasks(ctx))
	}
}

// flushAll sends everything the notifier still holds: the run stops.
func (e *Engine) flushAll(ctx context.Context) {
	if h, ok := e.notifier.(Holder); ok {
		e.notified(h.FlushAll(ctx))
	}
}
