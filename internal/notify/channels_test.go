package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRouterDeliversToRealChannels runs ntfy and Discord against httptest
// servers through the router, retry included.
func TestRouterDeliversToRealChannels(t *testing.T) {
	ntfySrv, ntfyGot := serve(t, 200)
	var attempts atomic.Int32
	var discordBody string
	discordSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		discordBody = string(b)
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway) // the retry must get through
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(discordSrv.Close)

	hook := discordSrv.URL + "/api/webhooks/1/HOOKSECRET"
	r := New(Options{
		Sleep:   noSleep,
		Secrets: []string{hook, "tk_secret"},
		Channels: []Entry{
			{Channel: &Ntfy{Server: ntfySrv.URL, Topic: "igris", Token: "tk_secret"}},
			{Channel: &Discord{WebhookURL: hook}},
		},
	})
	res := r.Notify(context.Background(), Message{Event: SessionLost, Project: "demo", Phase: "M5", TaskID: "M5-05", Title: "Tests", What: "session lost"})
	for _, x := range res {
		if x.Err != nil {
			t.Errorf("%s: %v", x.Channel, x.Err)
		}
	}
	if ntfyGot.path != "/igris" || ntfyGot.header.Get("Priority") != "high" || ntfyGot.header.Get("Authorization") != "Bearer tk_secret" {
		t.Errorf("ntfy got %+v", ntfyGot)
	}
	if attempts.Load() != 2 || !strings.Contains(discordBody, "session_lost") {
		t.Errorf("discord: %d attempts, body %q", attempts.Load(), discordBody)
	}
}

// TestRouterGivesUpOnAHangingServer checks the per-attempt timeout against
// a real server that never answers, and that no secret reaches the result.
func TestRouterGivesUpOnAHangingServer(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	hook := srv.URL + "/api/webhooks/1/HOOKSECRET"
	r := New(Options{
		Sleep:    noSleep,
		Timeout:  20 * time.Millisecond,
		Secrets:  []string{hook},
		Channels: []Entry{{Channel: &Discord{WebhookURL: hook}}},
	})
	res := r.Notify(context.Background(), Message{Event: RunError})
	if res[0].Err == nil {
		t.Fatal("a hanging server must end in an error")
	}
	if strings.Contains(res[0].Err.Error(), "HOOKSECRET") {
		t.Errorf("error leaks the webhook: %v", res[0].Err)
	}
	if hits.Load() != 2 {
		t.Errorf("%d requests, want 2 (attempt + one retry)", hits.Load())
	}
}
