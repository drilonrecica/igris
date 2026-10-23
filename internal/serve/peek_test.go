package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// Peek reads the run's snapshot over its socket and leaves, as `igris
// status` does; with nothing served it says ErrNotServed.
func TestPeek(t *testing.T) {
	dir := openDir(t, t.TempDir())
	if _, err := Peek(context.Background(), dir.Root(), "igris test"); !errors.Is(err, ErrNotServed) {
		t.Fatalf("Peek with no run = %v, want ErrNotServed", err)
	}
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Version: "1.7.0"})
	go func() { _ = s.Serve(l) }()
	defer func() { _ = s.Close() }()
	s.Publish(engine.Event{At: time.Now(), Kind: engine.PauseOn})
	snap, err := Peek(context.Background(), dir.Root(), "igris test")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Type != TypeSnapshot || snap.Server != "igris 1.7.0" || !snap.Paused {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestQuestionKind(t *testing.T) {
	if got := QuestionKind(string(engine.QuestionCommit)); got != "commit?" {
		t.Errorf("QuestionKind(commit) = %q", got)
	}
}
