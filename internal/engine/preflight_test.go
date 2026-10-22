package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/drilonrecica/igris/internal/state"
)

// Preflight answers as Run would refuse to start, and takes nothing:
// no lock, no state, no session (SPEC §14: arise asks before serve).
func TestPreflight(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, driftPlan, "")
	opts := func() Options {
		return Options{Config: h.cfg, Backend: h.be, State: h.dir, Clock: h.clock, Runner: h.cmds, Phase: "A", Events: func(Event) {}}
	}
	preflight := func(o Options) error {
		t.Helper()
		eng, err := New(o)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return eng.Preflight(ctx)
	}
	var drift *DriftError
	if err := preflight(opts()); !errors.As(err, &drift) {
		t.Fatalf("Preflight = %v, want a *DriftError", err)
	}
	untouched := func() {
		t.Helper()
		if got := h.read("tasks.md"); got != driftPlan {
			t.Errorf("plan written:\n%s", got)
		}
		if got := h.opened(); got != "" {
			t.Errorf("sessions opened for %q", got)
		}
		if _, err := h.dir.LoadRun(); !errors.Is(err, state.ErrNoRun) {
			t.Errorf("state.json written: %v", err)
		}
		h.assertUnlocked()
	}
	untouched()

	// A lock held elsewhere is the answer, unless the caller holds it.
	lock, err := h.dir.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	var locked *state.LockedError
	if err := preflight(opts()); !errors.As(err, &locked) {
		t.Errorf("Preflight with the lock held = %v, want a *LockedError", err)
	}
	held := opts()
	held.LockHeld = true
	if err := preflight(held); !errors.As(err, &drift) {
		t.Errorf("Preflight as the lock holder = %v, want the drift", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}

	confirmed := opts()
	confirmed.ConfirmedDrift = true
	if err := preflight(confirmed); err != nil {
		t.Errorf("Preflight with the drift confirmed = %v", err)
	}
	untouched()
}
