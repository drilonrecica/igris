package fake

import (
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/conformance"
)

// fakeWorld is the fake backend as a conformance world. Its sessions live
// in the backend itself, so a "restart" keeps the same instance.
type fakeWorld struct{ b *Backend }

func (w fakeWorld) Backend() backend.Backend                { return w.b }
func (w fakeWorld) Kill(ref backend.SessionRef)             { w.b.Kill(ref) }
func (w fakeWorld) Prompts(ref backend.SessionRef) []string { return w.b.SessionPrompts(ref) }

func TestConformance(t *testing.T) {
	conformance.Run(t, func(*testing.T) conformance.World { return fakeWorld{New()} })
}
