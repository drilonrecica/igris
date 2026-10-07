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
}

func (l *launch) fill(ev Event) Event {
	ev.Task, ev.Title, ev.Rank, ev.Model, ev.Mode = l.t.ID, l.t.Title, l.t.Rank, l.model, l.mode
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
}

// runTask runs one agent task from launch to acceptance (SPEC §6). stopped
// reports that the owner stopped the run while the task was in progress.
//
// The order of the side effects is what makes a crash recoverable: the
// intent goes to state.json before the plan says "in progress", the session
// ref is stored as soon as the session exists, and state.json keeps pointing
// at the task until its session is closed.
func (e *Engine) runTask(ctx context.Context, t *plan.Task) (stopped bool, err error) {
	l := &launch{t: t}
	e.task = l
	if t.Status == plan.InProgress {
		return e.adopt(ctx, l)
	}
	if !t.Owner.IsAgent() {
		return e.runUserTask(ctx, l)
	}

	// Resolve everything that can fail before anything is written, so a
	// config problem never leaves a task in progress without a session.
	var ok bool
	if l.model, ok = e.cfg.Models[t.Rank]; !ok {
		return false, fmt.Errorf("task %s: unknown model rank %q; add it to [models] in igris.toml", t.ID, t.Rank)
	}
	l.cur = &state.Current{TaskID: t.ID, StartedAt: e.clock.Now()}
	start, err := e.prepareSession(l, startNew)
	if err != nil {
		return false, err
	}
	l.mode, l.cur.Mode, l.cur.ClaudeSession = start.mode, start.mode, start.sessionID
	if started, err := e.markInProgress(ctx, l, "mode "+l.mode); err != nil || !started {
		return false, err
	}
	if err := e.openSession(ctx, l, start); err != nil {
		return false, err
	}
	return e.drive(ctx, l)
}

// markInProgress records l in state.json, then marks its task in progress.
// started is false if the task's row changed since it was selected; nothing
// is recorded then and the loop selects again.
func (e *Engine) markInProgress(ctx context.Context, l *launch, detail string) (started bool, err error) {
	t := l.t
	e.run.Current = l.cur
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
		e.run.Current = nil
		if saveErr := e.dir.SaveRun(e.run); saveErr != nil {
			return false, saveErr
		}
		if errors.Is(err, errReselect) {
			e.task = nil
			return false, nil
		}
		return false, err
	}
	e.plans.wrote(changes)
	e.log(state.Event{Type: state.EventTaskStarted, Detail: detail})
	e.emit(Event{Kind: TaskStarted, Changes: changes})
	return true, nil
}

// prepareSession resolves the mode, session ID, arguments and first prompt
// of a session for l's task. It writes only the rules file.
func (e *Engine) prepareSession(l *launch, how startKind) (sessionStart, error) {
	t := l.t
	st := sessionStart{how: how}
	var err error
	if st.mode, err = e.modeFor(t); err != nil {
		return st, err
	}
	if how == startContinue {
		st.sessionID = l.cur.ClaudeSession
		st.text = prompt.Continue(t.ID)
	} else {
		if st.sessionID, err = NewSessionID(); err != nil {
			return st, err
		}
		st.text, err = prompt.Render(prompt.VarsFor(t, prompt.Env{
			Model:        l.model,
			PlanFile:     e.cfg.Plan,
			CommitPolicy: e.cfg.Run.Commit,
			Resumed:      how == startFresh,
		}), e.templatePath())
		if err != nil {
			return st, err
		}
	}
	rules, err := e.dir.WriteRules(t.ID, prompt.Rules())
	if err != nil {
		return st, err
	}
	hooks, err := WriteHooks(e.dir, t.ID, e.opts.IgrisPath)
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
	sess, err := e.be.OpenSession(ctx, backend.SessionSpec{
		TaskID: t.ID,
		Dir:    e.dir.Root(),
		Label:  t.ID + " · " + t.Rank,
		Args:   st.args,
		// Keys the session's hook state (SPEC §6.3).
		ClaudeSession: st.sessionID,
	})
	if err != nil {
		return fmt.Errorf("start the session for %s: %w; check that `claude` starts in %s (installed, on PATH, logged in), then run `igris arise` again", t.ID, err, e.be.Name())
	}
	l.sess = sess
	ref := sess.Ref()
	l.cur.Session = &ref
	if err := e.dir.SaveRun(e.run); err != nil {
		return err
	}
	opened := ref
	e.emit(Event{Kind: SessionOpened, Session: &opened, ClaudeSession: st.sessionID})

	// The prompt is submitted, never passed as an argument (SPEC §6, §11.2).
	switch err := sess.Prompt(ctx, st.text); {
	case errors.Is(err, backend.ErrSessionGone):
		e.lose(ctx, l)
	case err != nil:
		return fmt.Errorf("send the task prompt to %s: %w; its session is still open in %s: run `igris arise` to retry", t.ID, err, e.be.Name())
	}
	if h, ok := sess.(backend.PromptHolder); ok && h.PromptPending() {
		// Held at a startup prompt: if igris stops before it goes out, the
		// next run hands it back to the reattached session.
		l.cur.PendingPrompt = st.text
		return e.dir.SaveRun(e.run)
	}
	return nil
}

// promptDelivered forgets the recorded first prompt once l's session has
// delivered it.
func (e *Engine) promptDelivered(l *launch) {
	if l.cur.PendingPrompt == "" {
		return
	}
	if h, ok := l.sess.(backend.PromptHolder); ok && h.PromptPending() {
		return
	}
	l.cur.PendingPrompt = ""
	if err := e.dir.SaveRun(e.run); err != nil {
		e.warn(err.Error())
	}
}

// drive watches l's session until the task is finished or the run stops,
// verifying every done signal (SPEC §6.4) and committing before the task is
// marked done (SPEC §6 steps 8–9).
func (e *Engine) drive(ctx context.Context, l *launch) (stopped bool, err error) {
	for {
		v, err := e.watch(ctx, l)
		if err != nil {
			return false, err
		}
		switch v.kind {
		case verdictDone:
			// A session's done signal is verified; the owner's word is final.
			if v.sig != nil && e.cfg.Run.Verify != "" {
				passed, err := e.verify(ctx, l)
				if err != nil {
					return false, err
				}
				if !passed {
					continue // the task stays in progress
				}
			}
			if stopped, err := e.commit(ctx, l, v.note); err != nil || stopped {
				return stopped, err
			}
			return false, e.finish(ctx, l, plan.Done, v.note)
		case verdictSkip:
			return false, e.finish(ctx, l, plan.Skipped, v.note)
		}
		return true, nil
	}
}

// retry replaces l's session (SPEC §6.3, §15.3 `r`): the old one is closed
// and a new one continues the conversation or starts fresh.
func (e *Engine) retry(ctx context.Context, l *launch, cont bool) error {
	how, detail := startFresh, "fresh"
	if cont {
		how, detail = startContinue, "continue"
	}
	e.emit(Event{Kind: Retrying, Detail: detail})
	l.cur.VerifyAttempts = 0 // a new session gets the full verify budget
	if l.sess != nil {
		if err := l.sess.Close(ctx); err != nil {
			e.warn(fmt.Sprintf("close the session of %s: %v; close its pane by hand", l.t.ID, err))
		}
	}
	st, err := e.prepareSession(l, how)
	if err != nil {
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
// drops its signal and closes its session.
func (e *Engine) finish(ctx context.Context, l *launch, to plan.Status, note string) error {
	t := l.t
	changes, err := e.writer.Update(ctx, func(p *plan.Plan) ([]plan.Change, error) {
		return p.Sync(t.ID, to)
	})
	if err != nil {
		return err
	}
	e.plans.wrote(changes)
	logType, kind := state.EventTaskDone, TaskDone
	if to == plan.Skipped {
		logType, kind = state.EventTaskSkipped, TaskSkipped
	}
	e.log(state.Event{Type: logType, Detail: note})
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
	if to == plan.Done && t.Owner.IsAgent() {
		// After the session is closed, so a slow channel doesn't keep it open.
		e.toast(ctx, notifyTaskDone, "done")
	}
	e.run.Current = nil
	if err := e.dir.SaveRun(e.run); err != nil {
		return err
	}
	e.emit(Event{Kind: kind, Detail: note, Changes: changes})
	e.task = nil
	return nil
}

// settle waits up to max for sess to stop working: idle, done, blocked or
// gone. A state it can't read counts as not settled.
func (e *Engine) settle(ctx context.Context, sess backend.Session, max time.Duration) {
	deadline := e.clock.Now().Add(max)
	for {
		st, err := sess.State(ctx)
		if ctx.Err() != nil || (err == nil && (st.Settled() || st == backend.Exited)) {
			return
		}
		if !e.clock.Now().Before(deadline) {
			return
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}
