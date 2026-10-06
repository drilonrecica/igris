package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
)

func TestToastSounds(t *testing.T) {
	tests := []struct {
		ev   Event
		want backend.Sound
	}{
		{NeedsInput, backend.SoundRequest},
		{SessionLost, backend.SoundRequest},
		{VerifyFailedLimit, backend.SoundRequest},
		{PhaseStuck, backend.SoundRequest},
		{RunError, backend.SoundRequest},
		{PhaseDone, backend.SoundDone},
		{TaskDone, backend.SoundDone},
	}
	for _, tt := range tests {
		be := fake.New()
		err := Toast{Backend: be}.Send(context.Background(), Message{Event: tt.ev, Project: "demo", Phase: "M5", TaskID: "M5-04", Title: "Toasts", What: "w"})
		if err != nil {
			t.Fatal(err)
		}
		got := be.Notifications()
		if len(got) != 1 || got[0].Sound != tt.want || got[0].Title != "igris · demo" || got[0].Body != "phase M5 · M5-04 Toasts: w" {
			t.Errorf("%s: toast = %+v, want sound %s", tt.ev, got, tt.want)
		}
	}
}

type failingBackend struct{ backend.Backend }

func (failingBackend) Notify(context.Context, backend.Notification) error {
	return errors.New("no herdr")
}

func TestToastReportsBackendFailure(t *testing.T) {
	err := Toast{Backend: failingBackend{}}.Send(context.Background(), Message{Event: NeedsInput})
	if err == nil {
		t.Error("a failing backend must surface as an error so the router can retry and report it")
	}
}
