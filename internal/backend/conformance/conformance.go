// Package conformance is the behavioral test suite every backend must pass
// (SPEC §11.1): open, prompt, state, focus and close a session; a session
// that went away; attach after an igris restart; a multi-line prompt with
// control characters; a side session (SPEC §6.10) and how attaching keeps
// it and task sessions apart; and, for a backend.Tailer, the tail. Each
// backend's tests run it against a World: the
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
	Args:          []string{"--model", "sonnet", "--session-id", UUID, "--append-system-prompt-file", "/work/demo/" + backend.RulesFile("T-01")},
	ClaudeSession: UUID,
}

// SideID and SideUUID are the side session the suite opens.
const (
	SideID   = "a3f2c1"
	SideUUID = "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f"
)

// SideSpec is the side session the suite opens: no task, its own rules
// file and IGRIS_SIDE in its environment.
var SideSpec = backend.SessionSpec{
	Side:          SideID,
	Dir:           "/work/demo",
	Label:         "side · sonnet",
	Args:          []string{"--model", "sonnet", "--session-id", SideUUID, "--append-system-prompt-file", "/work/demo/" + backend.SideRulesFile(SideID)},
	Env:           []string{"IGRIS_SIDE=" + SideID},
	ClaudeSession: SideUUID,
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
		if _, err := w.Backend().Attach(ctx, ref, Spec.TaskID); !errors.Is(err, backend.ErrSessionGone) {
			t.Errorf("Attach = %v, want ErrSessionGone", err)
		}
	})

	t.Run("attach after restart", func(t *testing.T) {
		w := newWorld(t)
		ref := open(t, w).Ref()
		s, err := w.Backend().Attach(ctx, ref, Spec.TaskID)
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

	// state.json is a session's to write: a ref naming a live pane of
	// another task is refused as foreign, never driven (V10-05).
	t.Run("attach refuses another task's pane", func(t *testing.T) {
		w := newWorld(t)
		ref := open(t, w).Ref()
		_, err := w.Backend().Attach(ctx, ref, "T-02")
		if !errors.Is(err, backend.ErrSessionGone) || !errors.Is(err, backend.ErrSessionForeign) {
			t.Errorf("Attach for T-02 = %v, want ErrSessionGone and ErrSessionForeign", err)
		}
	})

	// A side session opens like a task's and reattaches only as itself:
	// Attach for a task and AttachSide for another side session refuse its
	// pane (SPEC §6.10).
	t.Run("side session", func(t *testing.T) {
		w := newWorld(t)
		be := w.Backend()
		s, err := be.OpenSession(ctx, SideSpec)
		if err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		ref := s.Ref()
		if ref.Backend != be.Name() || ref.ClaudeSession != SideUUID || ref.PaneID == "" {
			t.Fatalf("ref %+v: want backend %q, Claude session %s and a pane", ref, be.Name(), SideUUID)
		}
		if err := s.Prompt(ctx, "Question: why?"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		be = w.Backend() // igris restarted
		if _, err := be.Attach(ctx, ref, Spec.TaskID); !errors.Is(err, backend.ErrSessionGone) || !errors.Is(err, backend.ErrSessionForeign) {
			t.Errorf("Attach as task %s = %v, want ErrSessionGone and ErrSessionForeign", Spec.TaskID, err)
		}
		if _, err := be.AttachSide(ctx, ref, "b4e3d2"); !errors.Is(err, backend.ErrSessionGone) || !errors.Is(err, backend.ErrSessionForeign) {
			t.Errorf("AttachSide as b4e3d2 = %v, want ErrSessionGone and ErrSessionForeign", err)
		}
		s, err = be.AttachSide(ctx, ref, SideID)
		if err != nil {
			t.Fatalf("AttachSide: %v", err)
		}
		if s.Ref() != ref {
			t.Errorf("ref %+v, want %+v", s.Ref(), ref)
		}
		if err := s.Prompt(ctx, "and then?"); err != nil {
			t.Fatalf("Prompt after AttachSide: %v", err)
		}
		if got := w.Prompts(ref); !slices.Equal(got, []string{"Question: why?", "and then?"}) {
			t.Errorf("prompts %q", got)
		}
		if err := s.Focus(ctx); err != nil {
			t.Errorf("Focus: %v", err)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := w.Backend().AttachSide(ctx, ref, SideID); !errors.Is(err, backend.ErrSessionGone) {
			t.Errorf("AttachSide after Close = %v, want ErrSessionGone", err)
		}
	})

	t.Run("attach side refuses a task's pane", func(t *testing.T) {
		w := newWorld(t)
		ref := open(t, w).Ref()
		_, err := w.Backend().AttachSide(ctx, ref, SideID)
		if !errors.Is(err, backend.ErrSessionGone) || !errors.Is(err, backend.ErrSessionForeign) {
			t.Errorf("AttachSide = %v, want ErrSessionGone and ErrSessionForeign", err)
		}
	})

	// A side ID that isn't one names no agent or file.
	t.Run("side IDs are shape-checked", func(t *testing.T) {
		w := newWorld(t)
		be := w.Backend()
		bad := SideSpec
		bad.Side = "--help"
		if _, err := be.OpenSession(ctx, bad); err == nil {
			t.Error("OpenSession with side ID --help succeeded")
		}
		ref := open(t, w).Ref()
		for _, id := range []string{"", "--help", "A3F2C1", "a3f2c1x", "../x"} {
			if _, err := be.AttachSide(ctx, ref, id); err == nil {
				t.Errorf("AttachSide(%q) succeeded", id)
			}
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
			if _, err := be.Attach(ctx, ref, Spec.TaskID); err == nil {
				t.Errorf("Attach(%+v) succeeded", ref)
			}
			if _, err := be.AttachSide(ctx, ref, SideID); err == nil {
				t.Errorf("AttachSide(%+v) succeeded", ref)
			}
		}
	})
}
