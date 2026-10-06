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

// launch is the task the run is working on, with what was resolved for it.
type launch struct {
	t     *plan.Task
	model string
	mode  string
}

func (l *launch) fill(ev Event) Event {
	ev.Task, ev.Title, ev.Rank, ev.Model, ev.Mode = l.t.ID, l.t.Title, l.t.Rank, l.model, l.mode
	return ev
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
	switch {
	case !t.Owner.IsAgent():
		return false, fmt.Errorf("task %s is a user task; running those is %w", t.ID, ErrUnsupported)
	case t.Status == plan.InProgress:
		return false, fmt.Errorf("task %s is already in progress; resuming a task is %w", t.ID, ErrUnsupported)
	}

	// Resolve everything that can fail before anything is written, so a
	// config problem never leaves a task in progress without a session.
	var ok bool
	if l.model, ok = e.cfg.Models[t.Rank]; !ok {
		return false, fmt.Errorf("task %s: unknown model rank %q; add it to [models] in igris.toml", t.ID, t.Rank)
	}
	if l.mode, err = e.modeFor(t); err != nil {
		return false, err
	}
	text, err := prompt.Render(prompt.VarsFor(t, prompt.Env{
		Model:        l.model,
		PlanFile:     e.cfg.Plan,
		CommitPolicy: e.cfg.Run.Commit,
	}), e.templatePath())
	if err != nil {
		return false, err
	}
	sessionID, err := NewSessionID()
	if err != nil {
		return false, err
	}
	rules, err := e.dir.WriteRules(t.ID, prompt.Rules())
	if err != nil {
		return false, err
	}
	args, err := ClaudeArgs(ClaudeParams{
		Model:     l.model,
		SessionID: sessionID,
		Mode:      l.mode,
		RulesFile: rules,
		ExtraArgs: e.cfg.Claude.ExtraArgs,
	})
	if err != nil {
		return false, err
	}

	cur := &state.Current{TaskID: t.ID, Mode: l.mode, ClaudeSession: sessionID, StartedAt: e.clock.Now()}
	e.run.Current = cur
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
	e.log(state.Event{Type: state.EventTaskStarted, Detail: "mode " + l.mode})
	e.emit(Event{Kind: TaskStarted, Changes: changes})

	sess, err := e.be.OpenSession(ctx, backend.SessionSpec{
		TaskID: t.ID,
		Dir:    e.dir.Root(),
		Label:  t.ID + " · " + t.Rank,
		Args:   args,
	})
	if err != nil {
		return false, fmt.Errorf("start the session for %s: %w", t.ID, err)
	}
	ref := sess.Ref()
	cur.Session = &ref
	if err := e.dir.SaveRun(e.run); err != nil {
		return false, err
	}
	opened := ref
	e.emit(Event{Kind: SessionOpened, Session: &opened})

	// The prompt is submitted, never passed as an argument (SPEC §6, §11.2).
	if err := sess.Prompt(ctx, text); err != nil {
		return false, fmt.Errorf("send the task prompt to %s: %w", t.ID, err)
	}

	sig, err := e.watch(ctx, t, cur.StartedAt)
	if err != nil {
		return false, err
	}
	if sig == nil {
		return true, nil
	}
	return false, e.accept(ctx, t, sess, sig)
}

// templatePath is the owner's prompt template; "" means the built-in one.
func (e *Engine) templatePath() string {
	if e.cfg.Run.PromptTemplate == "" {
		return ""
	}
	return inRoot(e.dir.Root(), e.cfg.Run.PromptTemplate)
}

// watch waits for the task's done signal and returns it; a nil signal means
// the run was stopped. Igris advances on a signal only, never on what the
// session looks like (SPEC §6.3).
//
// A signal written before the task started (an early `igris done`) is not
// this session's work: it is kept and reported once, never applied
// (SPEC §6.2). A skip signal is a request the owner has to confirm, so it is
// left in place too.
func (e *Engine) watch(ctx context.Context, t *plan.Task, started time.Time) (*state.Signal, error) {
	staleReported := false
	unreadable := "" // the last signal read error reported
	for {
		e.checkConfig(ctx) // before the signal, so a config edit is seen before the task is accepted
		if e.stopping(ctx) {
			return nil, nil
		}
		sig, err := e.dir.ReadSignal(t.ID)
		switch {
		case err != nil:
			if msg := err.Error(); msg != unreadable {
				unreadable = msg
				e.warn(msg)
			}
		case sig == nil:
		case sig.At.Before(started):
			if !staleReported {
				staleReported = true
				e.emit(Event{Kind: StaleSignal, Detail: fmt.Sprintf("ignoring a %s signal for %s written before the task started; run `igris %s %s` again if it is meant", sig.Action, t.ID, sig.Action, t.ID)})
			}
		case state.Classify(*sig, t.ID, t.Owner) == state.Apply:
			return sig, nil
		}
		e.wait(ctx, e.cfg.PollInterval.Std())
	}
}

// accept finishes a task whose done signal arrived: mark it done, unblock
// its dependents, log it, drop the signal and close the session.
func (e *Engine) accept(ctx context.Context, t *plan.Task, sess backend.Session, sig *state.Signal) error {
	changes, err := e.writer.Update(ctx, func(p *plan.Plan) ([]plan.Change, error) {
		return p.Sync(t.ID, plan.Done)
	})
	if err != nil {
		return err
	}
	e.log(state.Event{Type: state.EventTaskDone, Detail: sig.Note})
	if err := e.dir.RemoveSignal(t.ID); err != nil {
		return err
	}
	if err := sess.Close(ctx); err != nil {
		e.warn(fmt.Sprintf("close the session of %s: %v; close its pane by hand", t.ID, err))
	}
	e.run.Current = nil
	if err := e.dir.SaveRun(e.run); err != nil {
		return err
	}
	e.emit(Event{Kind: TaskDone, Detail: sig.Note, Changes: changes})
	e.task = nil
	return nil
}
