package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/state"
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

// TestTaskDoneNotification: a channel that asks for task_done hears about
// every finished agent task, then the phase.
func TestTaskDoneNotification(t *testing.T) {
	discord, discordSrv := newRecorder(t, http.StatusNoContent)
	h := newHarness(t, chainPlan, "")
	cfg := config.Default()
	cfg.Notify.Discord.Events = []string{"task_done", "phase_done"}
	secrets := config.Secrets{DiscordWebhook: discordSrv.URL + "/api/webhooks/1/x"}
	router := notify.FromConfig(cfg.Notify, secrets, h.be, func(o *notify.Options) { o.Sleep = noSleep })

	res, err := h.run(func(o *Options) { o.Notifier = router })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	want := []string{"A-1 One: done (task_done)", "A-2 Two: done (task_done)", "A-3 Three: done (task_done)", "phase A: complete (phase_done)"}
	if discord.count() != len(want) {
		t.Fatalf("discord got %d messages, want %d: %q", discord.count(), len(want), discord.bodies)
	}
	for i, w := range want {
		if !strings.Contains(discord.bodies[i], w) {
			t.Errorf("message %d = %s, want it to contain %q", i, discord.bodies[i], w)
		}
	}
	if got := h.toasts(); len(got) != 1 {
		t.Errorf("backend toasts = %q, want only phase_done (task_done is not a default event)", got)
	}
}

// TestWebhookCarriesRunAndTime: the webhook payload names the run's ID from
// the run log and the event's time by the engine's clock, and is signed.
func TestWebhookCarriesRunAndTime(t *testing.T) {
	wh, whSrv := newRecorder(t, http.StatusOK)
	h := newHarness(t, chainPlan, "")
	cfg := config.Default()
	cfg.Notify.Webhook.URL = "env:WH"
	secrets := config.Secrets{WebhookURL: whSrv.URL + "/in", WebhookSecret: "whsecret"}
	router := notify.FromConfig(cfg.Notify, secrets, nil, func(o *notify.Options) { o.Sleep = noSleep })

	res, err := h.run(func(o *Options) { o.Notifier = router })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if wh.count() != 1 {
		t.Fatalf("webhook got %q, want one phase_done payload", wh.bodies)
	}
	var p struct{ Event, Run, At string }
	if err := json.Unmarshal([]byte(wh.bodies[0]), &p); err != nil {
		t.Fatal(err)
	}
	var run string
	for _, ev := range h.logEvents() {
		if ev.Type == state.EventRunStarted {
			run = ev.Run
		}
	}
	if p.Event != "phase_done" || run == "" || p.Run != run {
		t.Errorf("payload event %q run %q, want phase_done in run %q", p.Event, p.Run, run)
	}
	if at, err := time.Parse(time.RFC3339, p.At); err != nil || at.Location() != time.UTC || at.Before(h.clock.Now().Add(-time.Hour)) {
		t.Errorf("at = %q (%v), want an RFC 3339 UTC time from the run's clock", p.At, err)
	}
	if !strings.HasPrefix(wh.heads[0].Get("X-Igris-Signature"), "sha256=") || wh.heads[0].Get("X-Igris-Event") != "phase_done" {
		t.Errorf("headers %v", wh.heads[0])
	}
}
