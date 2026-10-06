package notify

import (
	"context"

	"github.com/drilonrecica/igris/internal/backend"
)

// Toast shows a message as a backend toast (a herdr notification).
type Toast struct {
	Backend backend.Backend
}

// Name implements Channel.
func (Toast) Name() string { return "backend" }

// Send implements Channel: the "request" sound when igris needs the owner,
// "done" for completions (SPEC §10).
func (t Toast) Send(ctx context.Context, m Message) error {
	sound := backend.SoundRequest
	if m.Event == PhaseDone || m.Event == TaskDone {
		sound = backend.SoundDone
	}
	return t.Backend.Notify(ctx, backend.Notification{Title: m.Subject(), Body: m.Body(), Sound: sound})
}
