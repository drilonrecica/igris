package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/notify"
)

// recorder is an httptest server that remembers every request body.
type recorder struct {
	mu     sync.Mutex
	status int
	bodies []string
	heads  []http.Header
}

func newRecorder(t *testing.T, status int) (*recorder, *httptest.Server) {
	t.Helper()
	r := &recorder{status: status}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies, r.heads = append(r.bodies, string(b)), append(r.heads, req.Header.Clone())
		r.mu.Unlock()
		w.WriteHeader(r.status)
	}))
	t.Cleanup(srv.Close)
	return r, srv
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func noSleep(context.Context, time.Duration) {}

// TestNotificationsReachEveryChannel runs a phase with a working Discord
// webhook and an ntfy that fails: the toast and Discord still get the
// event, the failure is a warning, and the run completes.
func TestNotificationsReachEveryChannel(t *testing.T) {
	const token, hookPath = "tk_secret", "/api/webhooks/1/HOOKSECRET"
	ntfy, ntfySrv := newRecorder(t, http.StatusInternalServerError)
	discord, discordSrv := newRecorder(t, http.StatusNoContent)

	h := newHarness(t, chainPlan, "")
	hook := discordSrv.URL + hookPath
	cfg := config.Default()
	cfg.Notify.Ntfy.Topic, cfg.Notify.Ntfy.Server = "igris", ntfySrv.URL
	secrets := config.Secrets{NtfyToken: token, DiscordWebhook: hook}
	// FromConfig is what arise uses; only its sleeping is swapped out here.
	router := notify.FromConfig(cfg.Notify, secrets, h.be, func(o *notify.Options) { o.Sleep = noSleep })

	res, err := h.run(func(o *Options) { o.Notifier = router })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}

	if got := h.toasts(); len(got) != 1 || !strings.HasPrefix(got[0], "done: ") {
		t.Errorf("backend toasts = %q, want the phase_done toast", got)
	}
	if discord.count() != 1 || !strings.Contains(discord.bodies[0], "phase_done") || !strings.Contains(discord.bodies[0], "phase A") {
		t.Errorf("discord got %q, want one phase_done message", discord.bodies)
	}
	if ntfy.count() != 2 {
		t.Errorf("ntfy got %d requests, want 2 (attempt + retry)", ntfy.count())
	}
	if got := ntfy.heads[0].Get("Authorization"); got != "Bearer "+token {
		t.Errorf("ntfy Authorization = %q", got)
	}

	var warned string
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, "ntfy") {
			warned = ev.Detail
		}
	}
	if warned == "" {
		t.Fatalf("no warning for the failed ntfy delivery in %s", h.kinds())
	}
	for _, leak := range []string{token, "HOOKSECRET", discordSrv.URL} {
		if strings.Contains(warned, leak) {
			t.Errorf("warning leaks %q: %s", leak, warned)
		}
	}
	// The run log records deliveries, not secrets.
	logged := h.read(".igris/runs.jsonl")
	for _, leak := range []string{token, "HOOKSECRET"} {
		if strings.Contains(logged, leak) {
			t.Errorf("runs.jsonl leaks %q", leak)
		}
	}
	if !strings.Contains(logged, "phase_done via backend") || !strings.Contains(logged, "phase_done via discord") {
		t.Errorf("runs.jsonl lacks the deliveries:\n%s", logged)
	}
}
