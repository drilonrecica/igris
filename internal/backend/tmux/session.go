package tmux

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// ErrAgentBlocked is wrapped by Prompt when the agent waits at a question
// or permission prompt: anything pasted now would answer it.
var ErrAgentBlocked = errors.New("the agent is waiting at a prompt in its window; answer it there")

const (
	// promptWait is how long Prompt waits for the agent to settle before it
	// pastes anyway.
	promptWait = 10 * time.Second
	promptPoll = 250 * time.Millisecond
	// promptSettle: a held prompt goes out once the agent has stayed idle
	// this long; Claude Code shows its input box a moment before it takes
	// input (as for herdr, SPEC §11.2).
	promptSettle = 2 * time.Second
	// pasteSettle lets Claude Code take the bracketed paste before Enter.
	pasteSettle = 150 * time.Millisecond
)

var _ backend.Session = (*Session)(nil)
var _ backend.PromptHolder = (*Session)(nil)
var _ backend.Tailer = (*Session)(nil)

// Session is one Claude Code session in a tmux window. It is safe for
// concurrent use.
type Session struct {
	c     *Client
	id    string // task ID (window ID after Attach), for messages
	ref   backend.SessionRef
	sleep func(ctx context.Context, d time.Duration) error
	hooks backend.HookStates

	mu sync.Mutex
	// startupBlocked: no hook has fired since the window opened, so Claude
	// Code may sit at a startup question; the first prompt is held.
	startupBlocked bool
	pending        string
	hasPending     bool
}

// Ref returns the session's ref: window, pane and Claude session UUID.
func (s *Session) Ref() backend.SessionRef { return s.ref }

// hookState is the last state Claude Code's hooks recorded for this
// session, Unknown if there is none.
func (s *Session) hookState() backend.AgentState {
	if s.hooks == nil || s.ref.ClaudeSession == "" {
		return backend.Unknown
	}
	if st, ok := s.hooks(s.ref.ClaudeSession); ok {
		return st
	}
	return backend.Unknown
}

// alive checks the pane. It returns an error wrapping
// backend.ErrSessionGone when the pane is gone or dead.
func (s *Session) alive(ctx context.Context, op string) error {
	p, err := s.c.Pane(ctx, s.ref.PaneID)
	switch {
	case IsGone(err):
		return s.goneErr(op, err)
	case err != nil:
		return fmt.Errorf("%s %s: %w", op, s.id, err)
	case p.Dead:
		return s.goneErr(op, errors.New("claude code is no longer running in the pane"))
	}
	return nil
}

// Prompt pastes text into the session and submits it once the agent has
// settled. While Claude Code may sit at a startup question, text is held
// and State delivers it once the hooks say the agent is idle.
func (s *Session) Prompt(ctx context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.alive(ctx, "prompt"); err != nil {
		return err
	}
	if s.startupBlocked {
		if s.hookState() != backend.Idle {
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
	for range int(promptWait / promptPoll) {
		st := s.hookState()
		if st == backend.Blocked {
			return fmt.Errorf("prompt %s: %w", s.id, ErrAgentBlocked)
		}
		if st == backend.Exited {
			return s.goneErr("prompt", errors.New("claude code ended its session"))
		}
		if st.Settled() || st == backend.Unknown {
			break
		}
		if err := s.sleep(ctx, promptPoll); err != nil {
			return err
		}
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
// is idle, from State, as after a startup question.
func (s *Session) HoldPrompt(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending, s.hasPending, s.startupBlocked = text, true, true
}

// sendHeld sends a held prompt once the agent has stayed idle for
// promptSettle. It reports false, sending nothing, if the agent is no
// longer idle by then. The caller holds s.mu.
func (s *Session) sendHeld(ctx context.Context, text string) (bool, error) {
	if err := s.sleep(ctx, promptSettle); err != nil {
		return false, err
	}
	if s.hookState() != backend.Idle {
		return false, nil
	}
	return true, s.send(ctx, text)
}

// bufferUnsafe matches what may not appear in a paste buffer name.
var bufferUnsafe = regexp.MustCompile(`[^a-z0-9_-]`)

// bufferName is the paste buffer for this session's prompts.
func (s *Session) bufferName() string {
	return "igris-" + bufferUnsafe.ReplaceAllString(strings.ToLower(s.ref.PaneID+"-"+s.id), "_")
}

// send pastes text with bracketed paste and presses Enter. The text is
// cleaned of escape sequences and control characters other than newlines
// first: it is typed into a terminal (SPEC §16). The caller holds s.mu.
func (s *Session) send(ctx context.Context, text string) error {
	buf := s.bufferName()
	if err := s.c.LoadBuffer(ctx, buf, textsafe.Clean(text)); err != nil {
		return fmt.Errorf("prompt %s: %w", s.id, err)
	}
	if err := s.c.PasteBuffer(ctx, buf, s.ref.PaneID); err != nil {
		if IsGone(err) {
			return s.goneErr("prompt", err)
		}
		return fmt.Errorf("prompt %s: %w", s.id, err)
	}
	if err := s.sleep(ctx, pasteSettle); err != nil {
		return err
	}
	if err := s.c.SendEnter(ctx, s.ref.PaneID); err != nil {
		if IsGone(err) {
			return s.goneErr("prompt", err)
		}
		return fmt.Errorf("prompt %s: %w", s.id, err)
	}
	return nil
}

// State reports the agent state: Exited for a missing or dead pane or an
// ended session, else what the hooks recorded. Before the first hook it is
// Blocked while the first prompt is held (a startup question), else
// Unknown. A held prompt is delivered here once the agent is idle; State
// then reports Working.
func (s *Session) State(ctx context.Context) (backend.AgentState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.alive(ctx, "state of"); err != nil {
		if errors.Is(err, backend.ErrSessionGone) {
			return backend.Exited, nil
		}
		return backend.Unknown, err
	}
	st := s.hookState()
	switch {
	case st == backend.Exited:
		return backend.Exited, nil
	case st == backend.Unknown && s.startupBlocked:
		return backend.Blocked, nil
	}
	if s.hasPending && st == backend.Idle {
		switch sent, err := s.sendHeld(ctx, s.pending); {
		case errors.Is(err, backend.ErrSessionGone):
			return backend.Exited, nil
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

// Tail implements backend.Tailer with capture-pane. It doesn't take s.mu:
// it reads nothing State or Prompt change, and must not wait behind a
// held prompt's settle. A gone pane yields ErrSessionGone; a dead one
// still shows its last output.
func (s *Session) Tail(ctx context.Context, n int) ([]string, error) {
	out, err := s.c.CapturePane(ctx, s.ref.PaneID, n)
	switch {
	case IsGone(err):
		return nil, s.goneErr("tail", err)
	case err != nil:
		return nil, fmt.Errorf("tail %s: %w", s.id, err)
	}
	return backend.TailLines(out, n), nil
}

// Focus makes the session's window the current one.
func (s *Session) Focus(ctx context.Context) error {
	err := s.c.SelectWindow(ctx, s.ref.TabID)
	switch {
	case IsGone(err):
		return s.goneErr("focus", err)
	case err != nil:
		return fmt.Errorf("focus %s: %w", s.id, err)
	}
	return nil
}

// Close kills the session's window, ending Claude Code. A window that is
// already gone is not an error.
func (s *Session) Close(ctx context.Context) error {
	if err := s.c.KillWindow(ctx, s.ref.TabID); err != nil && !IsGone(err) {
		return fmt.Errorf("close %s: %w", s.id, err)
	}
	return nil
}

func (s *Session) goneErr(op string, err error) error {
	return fmt.Errorf("%s %s: %w: %w", op, s.id, backend.ErrSessionGone, err)
}
