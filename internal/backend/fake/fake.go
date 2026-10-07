// Package fake is an in-process backend for tests and --dry-run (SPEC §11.1).
// Sessions follow scripted agent-state sequences, and every prompt,
// notification, focus and close is recorded. It is safe for concurrent use.
package fake

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Name is the backend name reported by Name and stored in session refs.
const Name = "fake"

var (
	_ backend.Backend      = (*Backend)(nil)
	_ backend.Session      = (*Session)(nil)
	_ backend.PromptHolder = (*Session)(nil)
)

// AutoSignalFunc is called after a session's first prompt (the task prompt),
// e.g. to write the task's done signal the way a real session would.
type AutoSignalFunc func(ctx context.Context, taskID string) error

// Backend is the fake backend. The zero value is not usable; call New.
type Backend struct {
	mu         sync.Mutex
	available  error
	openErr    error
	autoSignal AutoSignalFunc
	scripts    map[string][][]backend.AgentState // per task: one sequence per future session
	sessions   map[string]*Session               // by PaneID
	startup    map[string]bool                   // task IDs whose next session holds its first prompt
	next       int

	opened        []backend.SessionSpec
	prompts       map[string][]string // by task ID
	notifications []backend.Notification
	focused       []backend.SessionRef
	closed        []backend.SessionRef
}

// New returns a fake backend that is available and whose sessions are idle
// unless scripted otherwise.
func New() *Backend {
	return &Backend{
		scripts:  map[string][][]backend.AgentState{},
		startup:  map[string]bool{},
		sessions: map[string]*Session{},
		prompts:  map[string][]string{},
	}
}

// StartupPrompt makes the next session opened for taskID sit at a startup
// prompt (like Claude Code's folder-trust question): its first prompt is
// held until a State call reports Idle or Done, then delivered, and that
// State reports Working. Like herdr, a reattached session forgets a held
// prompt; the engine hands it back with HoldPrompt.
func (b *Backend) StartupPrompt(taskID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.startup[taskID] = true
}

// Script queues the agent states for the next session opened for taskID.
// Each State call returns the next state; the last one repeats. Calling
// Script again for the same task queues a sequence for the session after
// that (e.g. a retry). Sessions without a script are always idle. A
// scripted Exited makes the session gone, like a real pane closing.
func (b *Backend) Script(taskID string, states ...backend.AgentState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.scripts[taskID] = append(b.scripts[taskID], slices.Clone(states))
}

// SetAutoSignal sets the function called after each session's first prompt.
func (b *Backend) SetAutoSignal(fn AutoSignalFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.autoSignal = fn
}

// SetAvailable sets the error Available returns; nil makes it available.
func (b *Backend) SetAvailable(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.available = err
}

// FailOpen makes the next OpenSession call fail with err.
func (b *Backend) FailOpen(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.openErr = err
}

// Kill makes the session with ref gone, as if its pane was closed by hand
// or Claude Code exited.
func (b *Backend) Kill(ref backend.SessionRef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.sessions[ref.PaneID]; s != nil {
		s.gone = true
	}
}

// Opened returns the spec of every opened session, in order.
func (b *Backend) Opened() []backend.SessionSpec {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]backend.SessionSpec, len(b.opened))
	for i, s := range b.opened {
		s.Args = slices.Clone(s.Args)
		s.Env = slices.Clone(s.Env)
		out[i] = s
	}
	return out
}

// Prompts returns the prompts sent to taskID's sessions, in order.
func (b *Backend) Prompts(taskID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.prompts[taskID])
}

// Notifications returns every notification shown, in order.
func (b *Backend) Notifications() []backend.Notification {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.notifications)
}

// Focused returns the ref of every focused session, in order.
func (b *Backend) Focused() []backend.SessionRef {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.focused)
}

// Closed returns the ref of every closed session, in order.
func (b *Backend) Closed() []backend.SessionRef {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.closed)
}

// Name returns "fake".
func (b *Backend) Name() string { return Name }

// Available returns the error set with SetAvailable.
func (b *Backend) Available(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.available
}

// OpenSession records spec and returns a new session following the next
// script queued for spec.TaskID.
func (b *Backend) OpenSession(ctx context.Context, spec backend.SessionSpec) (backend.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.openErr; err != nil {
		b.openErr = nil
		return nil, fmt.Errorf("open session for %s: %w", spec.TaskID, err)
	}

	states := []backend.AgentState{backend.Idle}
	if q := b.scripts[spec.TaskID]; len(q) > 0 {
		if len(q[0]) > 0 {
			states = q[0]
		}
		b.scripts[spec.TaskID] = q[1:]
	}
	b.next++
	s := &Session{
		b:       b,
		taskID:  spec.TaskID,
		ref:     backend.SessionRef{Backend: Name, PaneID: fmt.Sprintf("fake-%d", b.next), Agent: spec.TaskID, ClaudeSession: spec.ClaudeSession},
		states:  states,
		holding: b.startup[spec.TaskID],
	}
	delete(b.startup, spec.TaskID)
	b.sessions[s.ref.PaneID] = s
	spec.Args = slices.Clone(spec.Args)
	spec.Env = slices.Clone(spec.Env)
	b.opened = append(b.opened, spec)
	return s, nil
}

// Attach returns the live session with ref, or an error wrapping
// backend.ErrSessionGone.
func (b *Backend) Attach(ctx context.Context, ref backend.SessionRef) (backend.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Backend != Name {
		return nil, fmt.Errorf("attach %s session %s: not a %s session", ref.Backend, ref.PaneID, Name)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sessions[ref.PaneID]
	if s == nil || s.gone {
		return nil, fmt.Errorf("attach session %s: %w", ref.PaneID, backend.ErrSessionGone)
	}
	s.pending, s.hasPending = "", false // a restarted igris doesn't know it
	return s, nil
}

// Notify records n.
func (b *Backend) Notify(ctx context.Context, n backend.Notification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notifications = append(b.notifications, n)
	return nil
}

// Session is a fake session. Its fields are guarded by the backend's mutex.
type Session struct {
	b        *Backend
	taskID   string
	ref      backend.SessionRef
	states   []backend.AgentState // next state first; the last one repeats
	prompted bool
	gone     bool

	holding    bool // at a startup prompt: the first prompt is held
	pending    string
	hasPending bool
}

// PromptPending implements backend.PromptHolder.
func (s *Session) PromptPending() bool {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return s.hasPending
}

// HoldPrompt implements backend.PromptHolder.
func (s *Session) HoldPrompt(text string) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.pending, s.hasPending = text, true
}

// Ref returns the session's ref.
func (s *Session) Ref() backend.SessionRef { return s.ref }

// Prompt records text. The first prompt triggers the auto-signal function.
func (s *Session) Prompt(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.b.mu.Lock()
	if s.gone {
		s.b.mu.Unlock()
		return s.goneErr("prompt")
	}
	if s.holding && !s.prompted {
		s.pending, s.hasPending = text, true
		s.b.mu.Unlock()
		return nil
	}
	return s.deliver(ctx, text)
}

// deliver records text, cleaned as the real backends clean what they type
// into a pane; the first prompt triggers the auto-signal function. It is
// called with the backend's lock held and releases it.
func (s *Session) deliver(ctx context.Context, text string) error {
	s.b.prompts[s.taskID] = append(s.b.prompts[s.taskID], textsafe.Clean(text))
	first := !s.prompted
	s.prompted = true
	auto := s.b.autoSignal
	s.b.mu.Unlock()

	// Called without the lock so the function may use the backend.
	if first && auto != nil {
		if err := auto(ctx, s.taskID); err != nil {
			return fmt.Errorf("auto-signal %s: %w", s.taskID, err)
		}
	}
	return nil
}

// State returns the next scripted state, or Exited once the session is gone.
func (s *Session) State(ctx context.Context) (backend.AgentState, error) {
	if err := ctx.Err(); err != nil {
		return backend.Unknown, err
	}
	s.b.mu.Lock()
	if s.gone {
		s.b.mu.Unlock()
		return backend.Exited, nil
	}
	st := s.states[0]
	if len(s.states) > 1 {
		s.states = s.states[1:]
	}
	if st == backend.Exited {
		s.gone = true
	}
	if s.hasPending && (st == backend.Idle || st == backend.Done) {
		text := s.pending
		s.pending, s.hasPending, s.holding = "", false, false
		if err := s.deliver(ctx, text); err != nil { // releases the lock
			return backend.Unknown, err
		}
		return backend.Working, nil
	}
	s.b.mu.Unlock()
	return st, nil
}

// Focus records the focus.
func (s *Session) Focus(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if s.gone {
		return s.goneErr("focus")
	}
	s.b.focused = append(s.b.focused, s.ref)
	return nil
}

// Close makes the session gone and records it. Closing a gone session is a
// no-op.
func (s *Session) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if s.gone {
		return nil
	}
	s.gone = true
	s.b.closed = append(s.b.closed, s.ref)
	return nil
}

func (s *Session) goneErr(op string) error {
	return fmt.Errorf("%s session %s (%s): %w", op, s.ref.PaneID, s.taskID, backend.ErrSessionGone)
}
