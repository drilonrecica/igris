package fake

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
)

func open(t *testing.T, b *Backend, taskID string) backend.Session {
	t.Helper()
	s, err := b.OpenSession(context.Background(), backend.SessionSpec{TaskID: taskID, Dir: "/work", Label: taskID + " · opus", Args: []string{"--model", "opus"}})
	if err != nil {
		t.Fatalf("OpenSession(%s): %v", taskID, err)
	}
	return s
}

func states(t *testing.T, s backend.Session, n int) []backend.AgentState {
	t.Helper()
	var out []backend.AgentState
	for range n {
		st, err := s.State(context.Background())
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		out = append(out, st)
	}
	return out
}

func TestStateSequences(t *testing.T) {
	const (
		W = backend.Working
		I = backend.Idle
		B = backend.Blocked
		X = backend.Exited
	)
	tests := []struct {
		name   string
		script [][]backend.AgentState // queued sequences
		n      int
		want   []backend.AgentState
	}{
		{"unscripted is idle", nil, 3, []backend.AgentState{I, I, I}},
		{"sequence then last repeats", [][]backend.AgentState{{W, W, B}}, 5, []backend.AgentState{W, W, B, B, B}},
		{"empty script is idle", [][]backend.AgentState{{}}, 2, []backend.AgentState{I, I}},
		{"exited sticks", [][]backend.AgentState{{W, X, I}}, 4, []backend.AgentState{W, X, X, X}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			for _, seq := range tt.script {
				b.Script("T-1", seq...)
			}
			got := states(t, open(t, b, "T-1"), tt.n)
			if !slices.Equal(got, tt.want) {
				t.Errorf("states = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScriptPerSession(t *testing.T) {
	b := New()
	b.Script("T-1", backend.Working, backend.Exited)
	b.Script("T-1", backend.Done)
	b.Script("T-2", backend.Blocked)

	first := open(t, b, "T-1")
	if got := states(t, first, 3); !slices.Equal(got, []backend.AgentState{backend.Working, backend.Exited, backend.Exited}) {
		t.Errorf("first session states = %v", got)
	}
	if got := states(t, open(t, b, "T-2"), 1); got[0] != backend.Blocked {
		t.Errorf("T-2 state = %v, want blocked", got[0])
	}
	if got := states(t, open(t, b, "T-1"), 2); !slices.Equal(got, []backend.AgentState{backend.Done, backend.Done}) {
		t.Errorf("retry session states = %v", got)
	}
	if got := states(t, open(t, b, "T-1"), 1); got[0] != backend.Idle {
		t.Errorf("third session state = %v, want idle", got[0])
	}
}

func TestRecording(t *testing.T) {
	ctx := context.Background()
	b := New()
	s1 := open(t, b, "T-1")
	s2 := open(t, b, "T-2")
	if s1.Ref() == s2.Ref() {
		t.Fatalf("sessions share ref %+v", s1.Ref())
	}
	if s1.Ref().Backend != Name {
		t.Errorf("ref backend = %q, want %q", s1.Ref().Backend, Name)
	}
	mustNil(t, s1.Prompt(ctx, "task prompt\nline two"))
	mustNil(t, s2.Prompt(ctx, "other"))
	mustNil(t, s1.Prompt(ctx, "verify failed"))
	mustNil(t, s1.Focus(ctx))
	mustNil(t, b.Notify(ctx, backend.Notification{Title: "needs you", Sound: backend.SoundRequest}))
	mustNil(t, s1.Close(ctx))

	if got, want := b.Prompts("T-1"), []string{"task prompt\nline two", "verify failed"}; !slices.Equal(got, want) {
		t.Errorf("Prompts(T-1) = %q, want %q", got, want)
	}
	if got := b.Prompts("T-3"); len(got) != 0 {
		t.Errorf("Prompts(T-3) = %q, want none", got)
	}
	if got := b.Opened(); len(got) != 2 || got[0].TaskID != "T-1" || got[0].Label != "T-1 · opus" || !slices.Equal(got[0].Args, []string{"--model", "opus"}) {
		t.Errorf("Opened = %+v", got)
	}
	if got := b.Focused(); !slices.Equal(got, []backend.SessionRef{s1.Ref()}) {
		t.Errorf("Focused = %v", got)
	}
	if got := b.Closed(); !slices.Equal(got, []backend.SessionRef{s1.Ref()}) {
		t.Errorf("Closed = %v", got)
	}
	if got := b.Notifications(); len(got) != 1 || got[0].Title != "needs you" {
		t.Errorf("Notifications = %+v", got)
	}

	// Returned slices are copies.
	b.Opened()[0].Args[0] = "x"
	if b.Opened()[0].Args[0] != "--model" {
		t.Error("Opened returned the backend's own Args slice")
	}
}

func TestAutoSignal(t *testing.T) {
	ctx := context.Background()
	b := New()
	var got []string
	b.SetAutoSignal(func(_ context.Context, id string) error {
		got = append(got, id)
		return nil
	})
	s := open(t, b, "T-1")
	mustNil(t, s.Prompt(ctx, "task"))
	mustNil(t, s.Prompt(ctx, "follow-up"))
	mustNil(t, open(t, b, "T-2").Prompt(ctx, "task"))
	if want := []string{"T-1", "T-2"}; !slices.Equal(got, want) {
		t.Errorf("auto-signal calls = %v, want %v", got, want)
	}

	boom := errors.New("boom")
	b.SetAutoSignal(func(context.Context, string) error { return boom })
	if err := open(t, b, "T-3").Prompt(ctx, "task"); !errors.Is(err, boom) {
		t.Errorf("Prompt error = %v, want wrapping %v", err, boom)
	}
}

func TestGoneSessions(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		kill func(b *Backend, s backend.Session)
	}{
		{"killed", func(b *Backend, s backend.Session) { b.Kill(s.Ref()) }},
		{"scripted exit", func(_ *Backend, s backend.Session) { _, _ = s.State(ctx); _, _ = s.State(ctx) }},
		{"closed", func(_ *Backend, s backend.Session) { _ = s.Close(ctx) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			b.Script("T-1", backend.Working, backend.Exited, backend.Working)
			s := open(t, b, "T-1")
			if _, err := b.Attach(ctx, s.Ref()); err != nil {
				t.Fatalf("Attach live session: %v", err)
			}
			tt.kill(b, s)

			if st, err := s.State(ctx); err != nil || st != backend.Exited {
				t.Errorf("State = %v, %v; want exited, nil", st, err)
			}
			if err := s.Prompt(ctx, "x"); !errors.Is(err, backend.ErrSessionGone) {
				t.Errorf("Prompt error = %v, want ErrSessionGone", err)
			}
			if err := s.Focus(ctx); !errors.Is(err, backend.ErrSessionGone) {
				t.Errorf("Focus error = %v, want ErrSessionGone", err)
			}
			if err := s.Close(ctx); err != nil {
				t.Errorf("Close on gone session = %v, want nil", err)
			}
			if _, err := b.Attach(ctx, s.Ref()); !errors.Is(err, backend.ErrSessionGone) {
				t.Errorf("Attach error = %v, want ErrSessionGone", err)
			}
		})
	}
}

func TestAttach(t *testing.T) {
	ctx := context.Background()
	b := New()
	b.Script("T-1", backend.Working, backend.Idle)
	s := open(t, b, "T-1")
	_ = states(t, s, 1)

	got, err := b.Attach(ctx, s.Ref())
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if st := states(t, got, 1)[0]; st != backend.Idle {
		t.Errorf("attached session continues script: state = %v, want idle", st)
	}
	if _, err := b.Attach(ctx, backend.SessionRef{Backend: Name, PaneID: "fake-99"}); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Attach unknown ref error = %v, want ErrSessionGone", err)
	}
	if _, err := b.Attach(ctx, backend.SessionRef{Backend: "herdr", PaneID: s.Ref().PaneID}); err == nil || errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Attach foreign ref error = %v, want a non-gone error", err)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	b := New()
	mustNil(t, b.Available(ctx))
	down := errors.New("herdr down")
	b.SetAvailable(down)
	if err := b.Available(ctx); !errors.Is(err, down) {
		t.Errorf("Available = %v, want %v", err, down)
	}

	b.FailOpen(down)
	if _, err := b.OpenSession(ctx, backend.SessionSpec{TaskID: "T-1"}); !errors.Is(err, down) {
		t.Errorf("OpenSession = %v, want %v", err, down)
	}
	open(t, b, "T-1") // only the next open fails
	if n := len(b.Opened()); n != 1 {
		t.Errorf("Opened has %d sessions, want 1 (failed open not recorded)", n)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.OpenSession(cancelled, backend.SessionSpec{TaskID: "T-2"}); !errors.Is(err, context.Canceled) {
		t.Errorf("OpenSession with cancelled ctx = %v", err)
	}
}

func TestConcurrentUse(t *testing.T) {
	ctx := context.Background()
	b := New()
	b.SetAutoSignal(func(context.Context, string) error { return nil })
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("T-%d", i)
			s, err := b.OpenSession(ctx, backend.SessionSpec{TaskID: id})
			if err != nil {
				t.Error(err)
				return
			}
			for range 10 {
				_, _ = s.State(ctx)
				_ = s.Prompt(ctx, "p")
				_ = b.Notify(ctx, backend.Notification{Title: id})
			}
			_ = s.Close(ctx)
		}()
	}
	wg.Wait()
	if n := len(b.Closed()); n != 8 {
		t.Errorf("Closed = %d sessions, want 8", n)
	}
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestTail(t *testing.T) {
	ctx := context.Background()
	b := New()
	s := open(t, b, "T-01")
	tl := s.(backend.Tailer)
	if got, err := tl.Tail(ctx, 3); err != nil || len(got) != 0 {
		t.Errorf("Tail before SetTail = %q, %v", got, err)
	}
	b.SetTail("T-01", "one", "two", "three", "four", "", "")
	if got, err := tl.Tail(ctx, 3); err != nil || !slices.Equal(got, []string{"two", "three", "four"}) {
		t.Errorf("Tail = %q, %v", got, err)
	}
	b.Kill(s.Ref())
	if _, err := tl.Tail(ctx, 3); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Tail of a gone session = %v", err)
	}
}
