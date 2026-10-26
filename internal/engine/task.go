package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
)

// errReselect is returned inside a plan update when the task's row changed
// since it was selected; the loop then selects again from the fresh plan.
var errReselect = errors.New("task changed since it was selected")

// closeIdleWait bounds how long igris waits for an accepted task's agent to
// finish its final message before closing the pane (SPEC §6.6).
const closeIdleWait = 30 * time.Second

// launch is the task the run is working on, with what was resolved for it.
type launch struct {
	t     *plan.Task
	model string
	mode  string
	cur   *state.Current  // the task's record in state.json
	sess  backend.Session // nil until a session is open
	lost  bool            // the session is gone; the owner decides what's next
	// attemptAt is when the current attempt's clock started (SPEC §6.3):
	// the session's first prompt, or the reattach on resume. overdue is
	// set once the attempt ran past the task's Timeout.
	attemptAt time.Time
	overdue   bool
	// nudged is set once igris reminded the attempt's idle agent of
	// `igris done` (SPEC §6.3): at most once per attempt.
	nudged bool
	// promptAt is when a session's first prompt (task or continue prompt)
	// was delivered, while igris still looks for signs that Claude Code
	// submitted it; zero once it did, after the one Enter (SPEC §6.3), or
	// for a reattached session.
	promptAt time.Time
	// attempt is the agent task's attempt in this run for the run log
	// (SPEC §13): 1 for the first session or the reattached one, +1 per
	// retry, and on from the last one when the task is started again in
	// the same run after a reset. since is when this run started or picked up the task; waits
	// are the needs-you reasons logged and not yet cleared.
	attempt int
	since   time.Time
	waits   []string
	// plan is the plan the session presents for approval in igris, if any
	// (SPEC §6.3).
	plan planShown
	// usage counts the tokens of the attempt's session (SPEC §6.9); nil
	// without a session, with [usage] off, or once logged.
	usage *attemptUsage
	// limit is the session's wait at a usage limit (SPEC §6.3); nil while
	// it isn't waiting at one.
	limit *sessionLimit
	// The state watchStep keeps between polls (SPEC §6.3): the idle
	// episode, the skip request waiting for the owner's answer and the
	// last State error reported.
	ep       episode
	skip     *state.Signal
	stateErr string
	// check is the task's verify while it is queued or runs beside the
	// poll loop, in a parallel phase (SPEC §6.11); nil otherwise.
	check *check
	// integ is the task's integration from its accepted done until it is
	// marked done (SPEC §6.11); nil otherwise.
	integ *integration
	// files are the paths the task's worktree changed at the last poll and
	// fresh the ones new then, for the overlap watcher (SPEC §6.11).
	files, fresh map[string]bool
}

// nextAttempt counts a new attempt of l's task in this run.
func (e *Engine) nextAttempt(l *launch) {
	e.attempts[l.t.ID]++
	l.attempt = e.attempts[l.t.ID]
	if l.cur != nil {
		l.cur.Attempt = l.attempt // saved with the next change (SPEC §13)
	}
}

// startAttempt starts a new attempt's Timeout clock at now.
func (l *launch) startAttempt(now time.Time) { l.attemptAt, l.overdue, l.nudged = now, false, false }

func (l *launch) fill(ev Event) Event {
	ev.Task, ev.Title, ev.Rank, ev.Model, ev.Mode = l.t.ID, l.t.Title, l.rank(), l.model, l.mode
	if ev.Phase == "" {
		ev.Phase = phaseOf(l.t)
	}
	if l.cur != nil && l.cur.Slot != nil {
		slot := *l.cur.Slot
		ev.Slot = &slot
	}
	return ev
}

// startKind says how a session for a task starts.
type startKind int

const (
	startNew      startKind = iota // first session of the task
	startFresh                     // a new conversation for a task worked on before (Resumed=true)
	startContinue                  // the previous conversation goes on (claude --resume)
)

// sessionStart is everything resolved for one session launch.
type sessionStart struct {
	how       startKind
	mode      string
	sessionID string
	args      []string
	text      string // the first prompt
	label     string // the pane's label; "" for the task's usual one
}

// startTask starts the task t (SPEC §6 steps 1–5): a user task is handed to
// the owner, an agent task gets its session. parallel says the phase runs
// its tasks at once: the task takes a slot (SPEC §6.11). started is false
// if the task's row changed since it was selected; the loop then selects
// again.
//
// The order of the side effects is what makes a crash recoverable: the
// intent goes to state.json before the plan says "in progress", the session
// ref is stored as soon as the session exists, and state.json keeps pointing
// at the task until its session is closed.
func (e *Engine) startTask(ctx context.Context, t *plan.Task, parallel bool) (started bool, err error) {
	l := &launch{t: t}
	e.focus(l)
	defer e.unfocus()
	if !t.Owner.IsAgent() {
		return e.startUserTask(ctx, l, parallel)
	}

	// Resolve everything that can fail before anything is written, so a
	// config problem never leaves a task in progress without a session.
	var ok bool
	if l.model, ok = e.cfg.Models[t.Rank]; !ok {
		return false, fmt.Errorf("task %s: unknown model rank %q; add it to [models] in igris.toml", t.ID, t.Rank)
	}
	l.cur = &state.Current{TaskID: t.ID, StartedAt: e.clock.Now()}
	if parallel {
		l.cur.Slot = e.freeSlot()
	}
	start, err := e.prepareSession(l, startNew)
	if err != nil {
		return false, err
	}
	l.mode, l.cur.Mode, l.cur.ClaudeSession = start.mode, start.mode, start.sessionID
	if started, err := e.markInProgress(ctx, l, "mode "+l.mode); err != nil || !started {
		return false, err
	}
	// The worktree and the hook come after the mark, so a failure or a
	// crash leaves the known "in progress, no session" state (SPEC §6.7,
	// §6.11).
	if err := e.ensureWorktree(ctx, l, startNew); err != nil {
		return false, err
	}
	if open, err := e.beforeSession(ctx, l); err != nil {
		return false, err
	} else if open {
		if err := e.openSession(ctx, l, start); err != nil {
			return false, err
		}
	}
	return true, nil
}

// markInProgress records l in state.json, then marks its task in progress.
// started is false if the task's row changed since it was selected; nothing
// is recorded then and the loop selects again.
func (e *Engine) markInProgress(ctx context.Context, l *launch, detail string) (started bool, err error) {
	t := l.t
	e.run.Activate(l.cur)
	if err := e.dir.SaveRun(e.run); err != nil {
		return false, err
	}
	if e.opts.beforeMark != nil {
		e.opts.beforeMark()
	}
	changes, err := e.writer.Update(ctx, func(p *plan.Plan) ([]plan.Change, error) {
		// The writer re-read the plan (SPEC §4): never overwrite a status
		// the owner or a session changed since the selection.
		if fresh := p.Task(t.ID); fresh == nil || fresh.Status != t.Status {
			return nil, errReselect
		}
		return p.Sync(t.ID, plan.InProgress)
	})
	if err != nil {
		// The writer failed before changing the file, so nothing started:
		// withdraw the intent.
		e.run.Deactivate(t.ID)
		if saveErr := e.dir.SaveRun(e.run); saveErr != nil {
			return false, saveErr
		}
		if errors.Is(err, errReselect) {
			return false, nil
		}
		return false, err
	}
	e.addLaunch(l)
	e.plans.wrote(changes)
	l.since = e.clock.Now()
	ev := taskInfo(state.Event{Type: state.EventTaskStarted, Detail: detail, Slot: l.cur.Slot}, t)
	if t.Owner.IsAgent() {
		ev.Session = l.cur.ClaudeSession
		e.nextAttempt(l)
		if l.cur.Slot != nil {
			ev.Branch = e.taskWorktree(t.ID).Branch // the worktree it gets (SPEC §6.11)
		}
	}
	e.log(ev)
	e.emit(Event{Kind: TaskStarted, Changes: changes})
	return true, nil
}

// prepareSession resolves the mode, session ID, arguments and first prompt
// of a session for l's task. It writes only the rules file.
func (e *Engine) prepareSession(l *launch, how startKind) (sessionStart, error) {
	text := prompt.Continue(l.t.ID)
	if how != startContinue {
		var err error
		if text, err = e.taskPrompt(l, how == startFresh); err != nil {
			return sessionStart{how: how}, err
		}
	}
	return e.prepareWith(l, how, text)
}

// prepareWith is prepareSession with text as the first prompt: the task
// prompt, the continue prompt or a resolver's (SPEC §6.11).
func (e *Engine) prepareWith(l *launch, how startKind, text string) (sessionStart, error) {
	t := l.t
	st := sessionStart{how: how, text: text}
	var err error
	if st.mode, err = e.modeFor(t); err != nil {
		return st, err
	}
	if how == startContinue {
		st.sessionID = l.cur.ClaudeSession
	} else if st.sessionID, err = NewSessionID(); err != nil {
		return st, err
	}
	rules, err := e.dir.WriteRules(t.ID, prompt.Rules())
	if err != nil {
		return st, err
	}
	var allow []string
	if e.inWorktree(l) {
		allow = e.worktreeAllow()
	}
	hooks, err := WriteHooks(e.dir, t.ID, e.opts.IgrisPath, allow, e.planApproval(st.mode), e.cfg.Usage.Statusline)
	if err != nil {
		return st, err
	}
	st.args, err = ClaudeArgs(ClaudeParams{
		Model:        l.model,
		SessionID:    st.sessionID,
		Mode:         st.mode,
		RulesFile:    rules,
		SettingsFile: hooks,
		ExtraArgs:    e.cfg.Claude.ExtraArgs,
		Resume:       how == startContinue,
	})
	return st, err
}

// taskPrompt renders the task prompt for l's task from the plan and the
// config snapshot; resumed marks a fresh session after a lost one.
func (e *Engine) taskPrompt(l *launch, resumed bool) (string, error) {
	return prompt.Render(prompt.VarsFor(l.t, prompt.Env{
		Model:        l.model,
		PlanFile:     e.planFile(l),
		TaskFile:     e.taskFile(l),
		CommitPolicy: e.sessionPolicy(),
		Resumed:      resumed,
		DepNotes:     e.depNotes(l.t),
	}), e.templatePath())
}

// firstPrompt is the prompt a reattached session that never got its task
// receives: the task prompt, built again rather than read back from
// state.json (SPEC §13, V10-05).
func (e *Engine) firstPrompt(l *launch) (string, error) { return e.taskPrompt(l, false) }

// openSession opens a session for l as prepared in st, records it in
// state.json and submits the first prompt. A session that is gone before
// the prompt arrives leaves l lost rather than failing the run (SPEC §11.1).
func (e *Engine) openSession(ctx context.Context, l *launch, st sessionStart) error {
	t := l.t
	l.mode, l.cur.Mode, l.cur.ClaudeSession, l.cur.Session, l.cur.PendingPrompt = st.mode, st.mode, st.sessionID, nil, ""
	l.sess, l.lost = nil, false
	if err := e.dir.SaveRun(e.run); err != nil {
		return err
	}
	if err := state.ClearAgentState(e.dir.Root(), st.sessionID); err != nil {
		return err
	}
	from := e.clock.Now() // a continued session's transcript holds the earlier attempts too
	label := st.label
	if label == "" {
		label = t.ID + " · " + t.Rank
	}
	sess, err := e.be.OpenSession(ctx, backend.SessionSpec{
		TaskID: t.ID,
		Dir:    e.workDir(l),
		Label:  label,
		Args:   st.args,
		Env:    slotEnv(l),
		// Keys the session's hook state (SPEC §6.3).
		ClaudeSession: st.sessionID,
	})
	if err != nil {
		return fmt.Errorf("start the session for %s: %w; check that `claude` starts in %s (installed, on PATH, logged in), then run `igris arise` again", t.ID, err, e.be.Name())
	}
	l.sess = sess
	e.startUsage(l, from)
	ref := sess.Ref()
	l.cur.Session = &ref
	if err := e.dir.SaveRun(e.run); err != nil {
		return err
	}
	opened := ref
	e.emit(Event{Kind: SessionOpened, Session: &opened, ClaudeSession: st.sessionID})

	// The prompt is submitted, never passed as an argument (SPEC §6, §11.2).
	// The attempt's clock starts when it is delivered (SPEC §6.3).
	l.attemptAt, l.overdue, l.promptAt = time.Time{}, false, time.Time{}
	switch err := sess.Prompt(ctx, st.text); {
	case errors.Is(err, backend.ErrSessionGone):
		e.lose(ctx, l)
	case err != nil:
		return fmt.Errorf("send the task prompt to %s: %w; its session is still open in %s: run `igris arise` to retry", t.ID, err, e.be.Name())
	}
	if h, ok := sess.(backend.PromptHolder); ok && h.PromptPending() {
		// Held at a startup prompt: if igris stops before it goes out, the
		// next run hands it back to the reattached session. The clock
		// starts once it goes out (promptDelivered).
		l.cur.PendingPrompt = st.text
		return e.dir.SaveRun(e.run)
	}
	l.startAttempt(e.clock.Now())
	if !l.lost {
		l.promptAt = l.attemptAt
	}
	return nil
}

// promptDelivered forgets the recorded first prompt once l's session has
// delivered it, and starts the attempt's clock then. It reports whether the
// prompt went out on this call.
func (e *Engine) promptDelivered(l *launch) bool {
	if l.cur.PendingPrompt == "" {
		return false
	}
	if h, ok := l.sess.(backend.PromptHolder); ok && h.PromptPending() {
		return false
	}
	l.cur.PendingPrompt = ""
	l.startAttempt(e.clock.Now())
	l.promptAt = l.attemptAt
	if err := e.dir.SaveRun(e.run); err != nil {
		e.warn(err.Error())
	}
	return true
}

// retry replaces l's session (SPEC §6.3, §15.3 `r`): the old one is closed
// and a new one continues the conversation or starts fresh.
func (e *Engine) retry(ctx context.Context, l *launch, cont bool) error {
	how, detail := startFresh, "fresh"
	if cont {
		how, detail = startContinue, "continue"
	}
	e.emit(Event{Kind: Retrying, Detail: detail})
	e.dropPlan(l, "the session is replaced")
	e.waitsOver()            // the owner acted; whatever the old session waited on is over
	l.cur.VerifyAttempts = 0 // a new session gets the full verify budget
	if l.sess != nil {
		if err := l.sess.Close(ctx); err != nil {
			e.warn(fmt.Sprintf("close the session of %s: %v; close its pane by hand", l.t.ID, err))
		}
	}
	e.endUsage(ctx, l) // the old attempt's, before the retry counts a new one
	st, err := e.prepareSession(l, how)
	if err != nil {
		return err
	}
	// Every session opened is an attempt, also one the hook keeps from
	// opening (SPEC §13).
	e.nextAttempt(l)
	retried := state.Event{Type: state.EventTaskRetried, Session: st.sessionID, Detail: detail, Slot: l.cur.Slot}
	if l.cur.Worktree != nil {
		retried.Branch = l.cur.Worktree.Branch
	}
	e.log(retried)
	if err := e.ensureWorktree(ctx, l, how); err != nil {
		return err
	}
	if open, err := e.beforeSession(ctx, l); err != nil || !open {
		return err
	}
	return e.openSession(ctx, l, st)
}

// templatePath is the owner's prompt template; "" means the built-in one.
func (e *Engine) templatePath() string {
	if e.cfg.Run.PromptTemplate == "" {
		return ""
	}
	return inRoot(e.dir.Root(), e.cfg.Run.PromptTemplate)
}

// finish marks a task done or skipped, unblocks its dependents, logs it,
// drops its signal, closes its session, runs the after_task hook and
// removes the task's worktree.
func (e *Engine) finish(ctx context.Context, l *launch, to plan.Status, note string) error {
	t := l.t
	changes, err := e.writer.Update(ctx, func(p *plan.Plan) ([]plan.Change, error) {
		return p.Sync(t.ID, to)
	})
	if err != nil {
		return err
	}
	e.plans.wrote(changes)
	e.dropPlan(l, "the task ended")
	logType, kind := state.EventTaskDone, TaskDone
	if to == plan.Skipped {
		logType, kind = state.EventTaskSkipped, TaskSkipped
	}
	e.log(state.Event{Type: logType, Detail: note, DurationMS: e.sinceMS(l.since)})
	if err := e.dir.RemoveSignal(t.ID); err != nil {
		return err
	}
	if l.sess != nil && !l.lost {
		if to == plan.Done {
			// Let the agent finish its final message (SPEC §6.6).
			e.settle(ctx, l.sess, closeIdleWait)
		}
		if err := l.sess.Close(ctx); err != nil {
			e.warn(fmt.Sprintf("close the session of %s: %v; close its pane by hand", t.ID, err))
		}
	}
	// After the close, so the agent's final message counts.
	e.endUsage(ctx, l)
	if to == plan.Done && t.Owner.IsAgent() {
		// After the session is closed, so a slow channel doesn't keep it open.
		e.toast(ctx, notifyTaskDone, "done")
	}
	e.run.Deactivate(t.ID)
	if err := e.dir.SaveRun(e.run); err != nil {
		return err
	}
	e.emit(Event{Kind: kind, Detail: note, Changes: changes})
	if t.Owner.IsAgent() {
		// Once, after the session is closed; a failure is reported and the
		// run goes on (SPEC §6.7). In the task's worktree, removed after it
		// (SPEC §6.11).
		if _, err := e.runHook(ctx, l, hookAfter, e.cfg.Hooks.AfterTask, to); err != nil {
			return err
		}
	}
	e.dropWorktree(ctx, l.cur)
	e.dropLaunch(t.ID)
	return nil
}

// settle waits up to max for sess to stop working: idle, done, blocked,
// waiting on background work (its turn ended too) or gone. A state it can't read counts as not settled.
func (e *Engine) settle(ctx context.Context, sess backend.Session, max time.Duration) {
	deadline := e.clock.Now().Add(max)
	for {
		st, err := sess.State(ctx)
		if ctx.Err() != nil || (err == nil && (st.Settled() || st == backend.Waiting || st == backend.Exited)) {
			return
		}
		if !e.clock.Now().Before(deadline) {
			return
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}
