// Package conformance is the behavioral test suite every backend must pass
// (SPEC §11.1): open, prompt, state, focus and close a session; a session
// that went away; attach after an igris restart; a multi-line prompt with
// control characters; and, for a backend.Tailer, the tail. Each backend's tests run it against a World: the
// fake directly, herdr and tmux against simulations built from their
// recorded fixtures. It keeps "a new backend fits the interface without
// engine changes" honest.
package conformance

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
)

// World is a backend's surroundings for one test: a multiplexer with no
// sessions yet, in which Claude Code starts and settles at once.
type World interface {
	// Backend returns a backend in this world. Calling it again stands for
	// an igris restart: a new instance, the same sessions.
	Backend() backend.Backend
	// Kill makes the session's pane go away, as if the owner closed it.
	Kill(ref backend.SessionRef)
	// Prompts returns the prompts that reached the session's agent, in
	// order.
	Prompts(ref backend.SessionRef) []string
}

// UUID is the Claude session UUID of the sessions the suite opens.
const UUID = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"

// Spec is the session the suite opens.
var Spec = backend.SessionSpec{
	TaskID:        "T-01",
	Dir:           "/work/demo",
	Label:         "T-01 · sonnet",
	Args:          []string{"--model", "sonnet", "--session-id", UUID},
	ClaudeSession: UUID,
}

// Run runs the suite; newWorld returns a fresh world per subtest.
func Run(t *testing.T, newWorld func(t *testing.T) World) {
	ctx := context.Background()
	open := func(t *testing.T, w World) backend.Session {
		t.Helper()
		be := w.Backend()
		s, err := be.OpenSession(ctx, Spec)
		if err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		ref := s.Ref()
		if ref.Backend != be.Name() || ref.ClaudeSession != UUID || ref.PaneID == "" {
			t.Fatalf("ref %+v: want backend %q, Claude session %s and a pane", ref, be.Name(), UUID)
		}
		return s
	}

	t.Run("open prompt state focus close", func(t *testing.T) {
		w := newWorld(t)
		s := open(t, w)
		if err := s.Prompt(ctx, "# Task T-01\nDo the thing."); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := w.Prompts(s.Ref()); !slices.Equal(got, []string{"# Task T-01\nDo the thing."}) {
			t.Errorf("prompts %q", got)
		}
		if st, err := s.State(ctx); err != nil || st == backend.Exited {
			t.Errorf("State of a live session = %s, %v", st, err)
		}
		if err := s.Focus(ctx); err != nil {
			t.Errorf("Focus: %v", err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := s.Close(ctx); err != nil {
			t.Errorf("Close again: %v", err)
		}
		if st, err := s.State(ctx); err != nil || st != backend.Exited {
			t.Errorf("State after Close = %s, %v; want exited", st, err)
		}
	})

	// Prompts are typed into a terminal: escape sequences and control
	// characters other than newlines never reach it (SPEC §16).
	t.Run("prompt is cleaned", func(t *testing.T) {
		w := newWorld(t)
		s := open(t, w)
		if err := s.Prompt(ctx, "line one\x1b[31m red\x1b[0m\nline\x07 two"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		got := w.Prompts(s.Ref())
		if len(got) != 1 || got[0] != "line one red\nline two" {
			t.Errorf("prompts %q, want the text without control characters", got)
		}
	})

	t.Run("gone session", func(t *testing.T) {
		w := newWorld(t)
		s := open(t, w)
		ref := s.Ref()
		w.Kill(ref)
		if st, err := s.State(ctx); err != nil || st != backend.Exited {
			t.Errorf("State = %s, %v; want exited and no error", st, err)
		}
		if err := s.Prompt(ctx, "x"); !errors.Is(err, backend.ErrSessionGone) {
			t.Errorf("Prompt = %v, want ErrSessionGone", err)
		}
		if err := s.Focus(ctx); !errors.Is(err, backend.ErrSessionGone) {
			t.Errorf("Focus = %v, want ErrSessionGone", err)
		}
		if err := s.Close(ctx); err != nil {
			t.Errorf("Close of a gone session = %v, want nil", err)
		}
		if _, err := w.Backend().Attach(ctx, ref); !errors.Is(err, backend.ErrSessionGone) {
			t.Errorf("Attach = %v, want ErrSessionGone", err)
		}
	})

	t.Run("attach after restart", func(t *testing.T) {
		w := newWorld(t)
		ref := open(t, w).Ref()
		s, err := w.Backend().Attach(ctx, ref)
		if err != nil {
			t.Fatalf("Attach: %v", err)
		}
		if s.Ref() != ref {
			t.Errorf("ref %+v, want %+v", s.Ref(), ref)
		}
		if st, err := s.State(ctx); err != nil || st == backend.Exited {
			t.Errorf("State = %s, %v", st, err)
		}
		if err := s.Prompt(ctx, "again"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got := w.Prompts(ref); len(got) != 1 || got[0] != "again" {
			t.Errorf("prompts %q", got)
		}
	})

	// The live tail (SPEC §15.3) is optional: only a Tailer is held to it.
	t.Run("tail", func(t *testing.T) {
		w := newWorld(t)
		s := open(t, w)
		tl, ok := s.(backend.Tailer)
		if !ok {
			t.Skipf("%s sessions don't implement backend.Tailer", s.Ref().Backend)
		}
		if err := s.Prompt(ctx, "# Task T-01\nDo the thing."); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		got, err := tl.Tail(ctx, 5)
		if err != nil || len(got) > 5 {
			t.Errorf("Tail(5) = %q, %v; want at most 5 lines and no error", got, err)
		}
		if len(got) > 0 && strings.TrimSpace(got[len(got)-1]) == "" {
			t.Errorf("Tail(5) = %q ends in a blank line", got)
		}
		w.Kill(s.Ref())
		if _, err := tl.Tail(ctx, 5); !errors.Is(err, backend.ErrSessionGone) {
			t.Errorf("Tail of a gone session = %v, want ErrSessionGone", err)
		}
	})

	// A ref of another backend, or one state.json mangled, is refused.
	t.Run("attach refuses foreign refs", func(t *testing.T) {
		w := newWorld(t)
		be := w.Backend()
		for _, ref := range []backend.SessionRef{
			{Backend: "other", TabID: "1", PaneID: "1", Agent: "x"},
			{Backend: be.Name(), PaneID: "--help", TabID: "-t", Agent: "-x"},
			{Backend: be.Name(), PaneID: "fake-1", TabID: "@1", Agent: "t-01", ClaudeSession: "--model"},
		} {
			if _, err := be.Attach(ctx, ref); err == nil {
				t.Errorf("Attach(%+v) succeeded", ref)
			}
		}
	})
}
