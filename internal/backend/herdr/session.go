package herdr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
)

// ErrAgentBlocked is wrapped by Prompt when the agent waits at an approval
// or question prompt and can't take a message.
var ErrAgentBlocked = errors.New("the agent is waiting at a prompt in its pane; answer it there")

// promptWait is how long Prompt waits for the agent to settle before it
// submits anyway (Claude Code queues input typed while it works).
const promptWait = 10 * time.Second

// promptSettle is how long a prompt held at a startup prompt waits once
// the agent looks idle, before it is sent. Right after the folder-trust
// question closes, Claude Code draws its input box (herdr reports idle) a
// moment before it takes input: in gate M5-06, prompts sent within ~0.4 s
// were dropped although `agent prompt` succeeded, from 0.5 s on they landed
// (Claude Code 2.1.292, herdr 0.9.1). 2 s leaves a wide margin.
const promptSettle = 2 * time.Second

var (
	_ backend.Session = (*Session)(nil)
	_ backend.Tailer  = (*Session)(nil)
)

// Session is one Claude Code agent in a herdr tab. It is safe for
// concurrent use.
type Session struct {
	c     *Client
	id    string // task ID, for error messages
	ref   backend.SessionRef
	sleep func(ctx context.Context, d time.Duration) error // the backend's; tests replace it
	hooks backend.HookStates                               // the backend's; nil: no hook state

	mu sync.Mutex
	// startupBlocked is set while Claude Code may still sit at a startup
	// prompt (folder trust): `agent start` returned agent_not_ready and no
	// prompt has been delivered yet.
	startupBlocked bool
	// pending is the prompt held back during a startup prompt; State
	// delivers it once the agent is idle.
	pending    string
	hasPending bool
}

// Ref returns the session's ref: tab, pane and agent name.
func (s *Session) Ref() backend.SessionRef { return s.ref }

// Prompt submits text to the agent once it has settled. While the agent is
// blocked at a startup prompt, text is held and delivered by State as soon
// as the agent is idle, so the engine can watch it raise Needs you.
func (s *Session) Prompt(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.startupBlocked {
		p, err := s.c.PaneGet(ctx, s.ref.PaneID)
		switch {
		case gone(err):
			return s.goneErr("prompt", err)
		case err != nil:
			return fmt.Errorf("prompt %s: %w", s.id, err)
		case p.Agent == "":
			return s.goneErr("prompt", errors.New("claude code is no longer running in the pane"))
		case p.Status != StatusIdle && p.Status != StatusDone:
			s.pending, s.hasPending = text, true
			return nil
		}
		switch sent, err := s.sendHeld(ctx, text); {
		case err != nil:
			return err
		case !sent:
			s.pending, s.hasPending = text, true
			return nil
		}
		s.startupBlocked = false
		return nil
	}

	a, err := s.c.AgentWait(ctx, s.ref.Agent, nil, promptWait)
	switch {
	case gone(err):
		return s.goneErr("prompt", err)
	case IsCode(err, CodeTimeout):
		// Still working; submit anyway.
	case err != nil:
		return fmt.Errorf("prompt %s: %w", s.id, err)
	case a.Status == StatusBlocked:
		return fmt.Errorf("prompt %s: %w", s.id, ErrAgentBlocked)
	}
	return s.send(ctx, text)
}

// PromptPending implements backend.PromptHolder.
func (s *Session) PromptPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasPending
}

// HoldPrompt implements backend.PromptHolder: text goes out once the agent
// is idle, from State, as after a startup prompt.
func (s *Session) HoldPrompt(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending, s.hasPending, s.startupBlocked = text, true, true
}

// sendHeld sends a prompt held at a startup prompt once the agent has
// stayed idle for promptSettle (see there). It reports false, sending
// nothing, if the agent is no longer idle by then (another prompt came up).
// The caller holds s.mu.
func (s *Session) sendHeld(ctx context.Context, text string) (bool, error) {
	if err := s.sleep(ctx, promptSettle); err != nil {
		return false, err
	}
	p, err := s.c.PaneGet(ctx, s.ref.PaneID)
	switch {
	case gone(err):
		return false, s.goneErr("prompt", err)
	case err != nil:
		return false, fmt.Errorf("prompt %s: %w", s.id, err)
	case p.Agent == "":
		return false, s.goneErr("prompt", errors.New("claude code is no longer running in the pane"))
	case p.Status != StatusIdle && p.Status != StatusDone:
		return false, nil
	}
	return true, s.send(ctx, text)
}

// send submits text with `agent prompt`. The caller holds s.mu.
func (s *Session) send(ctx context.Context, text string) error {
	_, err := s.c.AgentPrompt(ctx, s.ref.Agent, text)
	switch {
	case gone(err):
		return s.goneErr("prompt", err)
	case IsCode(err, CodeAgentBlocked):
		return fmt.Errorf("prompt %s: %w: %w", s.id, ErrAgentBlocked, err)
	case err != nil:
		return fmt.Errorf("prompt %s: %w", s.id, err)
	}
	return nil
}

// State reports the agent state from `pane get`. A vanished pane, or a pane
// where Claude Code no longer runs, is Exited. A prompt held back during a
// startup prompt is delivered here once the agent is idle; State then
// reports Working.
func (s *Session) State(ctx context.Context) (backend.AgentState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.c.PaneGet(ctx, s.ref.PaneID)
	switch {
	case IsCode(err, CodePaneNotFound):
		return backend.Exited, nil
	case err != nil:
		return backend.Unknown, fmt.Errorf("state of %s: %w", s.id, err)
	case p.Agent == "":
		// The pane is back at its shell: Claude Code exited.
		return backend.Exited, nil
	}
	st := agentState(p.Status)
	if st == backend.Unknown {
		st = s.hookState()
	}

	if s.hasPending && (st == backend.Idle || st == backend.Done) {
		switch sent, err := s.sendHeld(ctx, s.pending); {
		case errors.Is(err, backend.ErrSessionGone):
			return backend.Exited, nil
		case errors.Is(err, ErrAgentBlocked):
			return backend.Blocked, nil // blocked again; try on a later poll
		case err != nil:
			return backend.Unknown, fmt.Errorf("deliver the first prompt to %s: %w", s.id, err)
		case !sent:
			return backend.Unknown, nil // busy again; try on a later poll
		}
		s.pending, s.hasPending, s.startupBlocked = "", false, false
		return backend.Working, nil
	}
	return st, nil
}

// hookState is the agent state Claude Code's hooks recorded for this
// session, used when herdr can't tell (no integration, SPEC §6.3); Unknown
// when there is none.
func (s *Session) hookState() backend.AgentState {
	if s.hooks == nil || s.ref.ClaudeSession == "" {
		return backend.Unknown
	}
	if st, ok := s.hooks(s.ref.ClaudeSession); ok {
		return st
	}
	return backend.Unknown
}

// agentState maps herdr's agent_status to a backend state.
func agentState(status string) backend.AgentState {
	switch status {
	case StatusIdle:
		return backend.Idle
	case StatusWorking:
		return backend.Working
	case StatusBlocked:
		return backend.Blocked
	case StatusDone:
		return backend.Done
	}
	return backend.Unknown
}

// Tail implements backend.Tailer with `agent read`: the transcript
// (recent-unwrapped), or the screen (visible) when the transcript is empty
// because a dialog is drawn on the alternate screen. It doesn't take s.mu:
// it reads nothing State or Prompt change, and must not wait behind a
// held prompt's settle.
func (s *Session) Tail(ctx context.Context, n int) ([]string, error) {
	var lines []string
	for _, source := range []string{SourceRecentUnwrapped, SourceVisible} {
		out, err := s.c.AgentRead(ctx, s.ref.Agent, source, n)
		switch {
		case gone(err):
			return nil, s.goneErr("tail", err)
		case err != nil:
			return nil, fmt.Errorf("tail %s: %w", s.id, err)
		}
		if lines = backend.LastLines(out, n); len(lines) > 0 {
			break
		}
	}
	return lines, nil
}

// Focus brings the session's tab to the front.
func (s *Session) Focus(ctx context.Context) error {
	_, err := s.c.TabFocus(ctx, s.ref.TabID)
	switch {
	case IsCode(err, CodeTabNotFound):
		return s.goneErr("focus", err)
	case err != nil:
		return fmt.Errorf("focus %s: %w", s.id, err)
	}
	return nil
}

// Close closes the session's tab, ending Claude Code. A tab that is already
// gone is not an error.
func (s *Session) Close(ctx context.Context) error {
	err := s.c.TabClose(ctx, s.ref.TabID)
	if err != nil && !IsCode(err, CodeTabNotFound) {
		return fmt.Errorf("close %s: %w", s.id, err)
	}
	return nil
}

// gone reports whether err says the agent or its pane no longer exists.
func gone(err error) bool {
	return IsCode(err, CodeAgentNotFound) || IsCode(err, CodePaneNotFound) || IsCode(err, CodeTabNotFound)
}

func (s *Session) goneErr(op string, cause error) error {
	return fmt.Errorf("%s session %s (%s): %w: %w", op, s.ref.Agent, s.id, backend.ErrSessionGone, cause)
}
