package tmux

import (
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/conformance"
)

// simWorld is the simulated tmux server (sim_test.go) as a conformance
// world.
type simWorld struct{ s *sim }

func (w simWorld) Backend() backend.Backend    { return w.s.backend() }
func (w simWorld) Kill(ref backend.SessionRef) { w.s.kill(ref.PaneID) }
func (w simWorld) Prompts(ref backend.SessionRef) []string {
	if p := w.s.pane(ref.PaneID); p != nil {
		return p.pasted
	}
	return nil
}

func TestConformance(t *testing.T) {
	conformance.Run(t, func(*testing.T) conformance.World { return simWorld{newSim()} })
}
