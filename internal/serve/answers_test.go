package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// Answers says whether a serve answers on the project's socket, and the
// client that asks leaves without a word, which serve tolerates.
func TestAnswers(t *testing.T) {
	ctx := context.Background()
	s := New(Options{Version: "1.4.0", Mode: "plan"})
	root := listenTemp(t, s)
	if !Answers(ctx, root) {
		t.Error("Answers = false with a serve listening")
	}
	if Answers(ctx, t.TempDir()) {
		t.Error("Answers = true with no socket")
	}
	// The server is still fine for a real client.
	c, k, _ := attachTo(t, s)
	defer c.Close()
	k.waitFor(t, "the snapshot", func(s *sink) bool { return s.count("snapshot") == 1 })
}

// A run that ends with an error ends the client with a RunError carrying
// the run's error, from the live events and from a snapshot alike.
func TestClientRunError(t *testing.T) {
	at := time.Date(2026, 10, 22, 9, 41, 0, 0, time.UTC)
	events := []engine.Event{
		{At: at, Kind: engine.RunStarted, Run: "r1", Phase: "M0", Detail: "phase M0"},
		{At: at, Kind: engine.RunFailed, Phase: "M0", Detail: "backend herdr is not available: no socket"},
		{At: at, Kind: engine.RunStopped, Phase: "M0", Detail: "error"},
	}
	t.Run("live", func(t *testing.T) {
		s := New(Options{Version: "1.4.0", Mode: "plan"})
		_, k, _ := attachTo(t, s)
		for _, ev := range events {
			s.Publish(ev)
		}
		k.waitFor(t, "the end", func(s *sink) bool { return s.count("end") == 1 })
		var re *RunError
		if !errors.As(k.err, &re) || re.Detail != events[1].Detail || k.err.Error() != events[1].Detail {
			t.Errorf("end = %v, want a RunError with the run's error", k.err)
		}
	})
	t.Run("snapshot", func(t *testing.T) {
		s := New(Options{Version: "1.4.0", Mode: "plan"})
		for _, ev := range events {
			s.Publish(ev)
		}
		_, k, _ := attachTo(t, s)
		k.waitFor(t, "the end", func(s *sink) bool { return s.count("end") == 1 })
		var re *RunError
		if !errors.As(k.err, &re) || re.Detail != events[1].Detail {
			t.Errorf("end = %v, want a RunError with the run's error from the feed", k.err)
		}
	})
	if got := (&RunError{}).Error(); got != "the run ended with an error; .igris/serve.log may say more" {
		t.Errorf("empty RunError = %q", got)
	}
	if err := endError("stopped", "x"); err != nil {
		t.Errorf("endError(stopped) = %v", err)
	}
}
