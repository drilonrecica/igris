package engine

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
)

// TestTailFollowsTheCurrentSession: Tail reads the open session of the
// current agent task and nothing between tasks or after the run (SPEC
// §15.3). It is called from another goroutine while the run goes on, as
// the TUI does; -race checks the hand-over.
func TestTailFollowsTheCurrentSession(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.be.SetTail("A-1", "A-1 working", "tail-secret-marker", "")
	h.be.SetTail("A-2", "A-2 working")
	tails := map[string][]string{}
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	h.onEvent = func(ev Event) {
		switch ev.Kind {
		case RunStarted:
			wg.Add(1)
			go func() { // the TUI's goroutine
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_, _ = h.eng.Tail(ctx, 3)
					}
				}
			}()
		case SessionOpened, TaskStarted:
			got, err := h.eng.Tail(ctx, 3)
			if err != nil {
				t.Errorf("Tail at %s of %s: %v", ev.Kind, ev.Task, err)
			}
			tails[string(ev.Kind)+" "+ev.Task] = got
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	close(stop)
	wg.Wait()
	for key, want := range map[string][]string{
		"task_started A-1":   nil, // no session yet
		"session_opened A-1": {"A-1 working", "tail-secret-marker"},
		"task_started A-2":   nil, // A-1's session is closed
		"session_opened A-2": {"A-2 working"},
	} {
		if got := tails[key]; !slices.Equal(got, want) {
			t.Errorf("Tail at %s = %q, want %q", key, got, want)
		}
	}
	if got, err := h.eng.Tail(ctx, 3); err != nil || got != nil {
		t.Errorf("Tail after the run = %q, %v; want nothing", got, err)
	}
	// The tail is display only: never logged.
	if strings.Contains(h.read(".igris/runs.jsonl"), "tail-secret-marker") || strings.Contains(h.read(".igris/state.json"), "tail-secret-marker") {
		t.Error("the tail reached .igris/")
	}
}

// TestTailOfALostSession: once the session is gone the tail is empty, not
// an error from the dead pane.
func TestTailOfALostSession(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.be.SetTail("A-1", "A-1 working")
	h.be.Script("A-1", backend.Working, backend.Exited)
	var got []string
	var err error
	asked := false
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionSessionLost && !asked {
			asked = true
			got, err = h.eng.Tail(context.Background(), 3)
			h.eng.Send(Command{Kind: CmdStop})
		}
	}
	if _, runErr := h.run(); runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
	if !asked || err != nil || got != nil {
		t.Errorf("Tail of a lost session = %q, %v (asked %v); want nothing", got, err, asked)
	}
}
