package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestWebhookSend(t *testing.T) {
	at := time.Date(2026, 10, 8, 11, 15, 0, 0, time.FixedZone("CEST", 2*3600))
	tests := []struct {
		name, secret string
		m            Message
		want         map[string]any
	}{
		{
			name:   "signed task event",
			secret: "s3cret",
			m:      Message{Event: NeedsInput, Project: "demo", Phase: "M5", TaskID: "M5-02", Title: "`ntfy` channel.", What: "needs you", RunID: "20261008-091500-3fa2", At: at},
			want: map[string]any{
				"v": 1.0, "event": "needs_input", "project": "demo", "phase": "M5", "task": "M5-02", "title": "ntfy channel",
				"what": "needs you", "run": "20261008-091500-3fa2", "at": "2026-10-08T09:15:00Z", "urgent": true,
				"text": "phase M5 · M5-02 ntfy channel: needs you",
			},
		},
		{
			name: "unsigned, unknowns empty",
			m:    Message{Event: PhaseDone, Project: "demo"},
			want: map[string]any{
				"v": 1.0, "event": "phase_done", "project": "demo", "phase": "", "task": "", "title": "",
				"what": "", "run": "", "at": "", "urgent": false, "text": "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, got := serve(t, 200)
			w := &Webhook{URL: srv.URL + "/hook?x=1", Secret: tt.secret}
			if err := w.Send(context.Background(), tt.m); err != nil {
				t.Fatal(err)
			}
			if got.method != "POST" || got.path != "/hook" {
				t.Errorf("%s %s", got.method, got.path)
			}
			for h, want := range map[string]string{"Content-Type": "application/json", "User-Agent": "igris", "X-Igris-Event": string(tt.m.Event)} {
				if v := got.header.Get(h); v != want {
					t.Errorf("%s = %q, want %q", h, v, want)
				}
			}
			var p map[string]any
			if err := json.Unmarshal([]byte(got.body), &p); err != nil {
				t.Fatalf("body %q: %v", got.body, err)
			}
			if len(p) != len(tt.want) {
				t.Errorf("payload has %d keys, want %d: %s", len(p), len(tt.want), got.body)
			}
			for k, want := range tt.want {
				if p[k] != want {
					t.Errorf("%s = %#v, want %#v", k, p[k], want)
				}
			}
			sig := got.header.Get("X-Igris-Signature")
			if tt.secret == "" {
				if sig != "" {
					t.Errorf("unsigned webhook sent X-Igris-Signature %q", sig)
				}
				return
			}
			// The receiver's check: HMAC-SHA256 of the raw body with the
			// shared secret, lowercase hex.
			mac := hmac.New(sha256.New, []byte(tt.secret))
			mac.Write([]byte(got.body))
			want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			if !hmac.Equal([]byte(sig), []byte(want)) {
				t.Errorf("X-Igris-Signature = %q, want %q", sig, want)
			}
		})
	}
}

func TestWebhookCutsText(t *testing.T) {
	m := Message{Event: TaskDone, Project: "p", TaskID: "X-1", Title: strings.Repeat("é", 5000)}
	b, err := webhookBody(m, (&Webhook{}).text(m))
	if err != nil {
		t.Fatal(err)
	}
	var p webhookPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(p.Text)); n != webhookLimit || !strings.HasSuffix(p.Text, "…") {
		t.Errorf("text has %d characters, want %d ending in …", n, webhookLimit)
	}
}

// Neither the URL nor the secret may reach an error, as written or
// URL-escaped, whatever goes wrong; the router's redaction is the safety
// net behind the channel's own care.
func TestWebhookErrorsHideSecrets(t *testing.T) {
	const secret = "sig/SECRET key" //nolint:gosec // a made-up secret the errors must hide
	srv, _ := serve(t, 500)
	hook := srv.URL + "/in/TOKEN%20PART?k=QUERYSECRET"
	leaks := func(err error) bool {
		s := err.Error()
		for _, v := range []string{hook, srv.URL, "TOKEN", "QUERYSECRET", secret, "SECRET", url.QueryEscape(secret), url.PathEscape(secret)} {
			if strings.Contains(s, v) {
				return true
			}
		}
		return false
	}
	err := (&Webhook{URL: hook, Secret: secret}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || !strings.Contains(err.Error(), "500") || leaks(err) {
		t.Errorf("status error = %v, want the status and no secret", err)
	}
	srv.Close()
	err = (&Webhook{URL: hook, Secret: secret}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || leaks(err) {
		t.Errorf("connection error leaks: %v", err)
	}
	err = (&Webhook{URL: "http://bad host/TOKEN", Secret: secret}).Send(context.Background(), Message{})
	if err == nil || leaks(err) {
		t.Errorf("invalid URL error leaks: %v", err)
	}

	// Through the router, with a channel that puts both into its error.
	r := New(Options{
		Sleep:    noSleep,
		Secrets:  []string{hook, secret},
		Channels: []Entry{{Channel: failing{msg: "post " + hook + " " + url.QueryEscape(secret) + " " + url.PathEscape(hook)}}},
	})
	res := r.Notify(context.Background(), Message{Event: NeedsInput})
	if len(res) != 1 || res[0].Err == nil || strings.Contains(res[0].Err.Error(), "QUERYSECRET") || strings.Contains(res[0].Err.Error(), "SECRET") {
		t.Errorf("router result leaks: %v", res)
	}
}

// failing is a channel whose error text is msg.
type failing struct{ msg string }

func (failing) Name() string { return "failing" }

func (f failing) Send(context.Context, Message) error { return errorString(f.msg) }

type errorString string

func (e errorString) Error() string { return string(e) }

// A digest lists at most maxWebhookMessages held messages and says how
// many it left out.
func TestWebhookDigestCapsMessages(t *testing.T) {
	for _, tt := range []struct{ held, list, truncated int }{{3, 3, 0}, {maxWebhookMessages, maxWebhookMessages, 0}, {maxWebhookMessages + 10, maxWebhookMessages, 10}} {
		d := Message{Event: Digest, Project: "p", What: "quiet hours: held"}
		for i := range tt.held {
			d.Held = append(d.Held, Message{Event: TaskDone, Project: "p", TaskID: fmt.Sprintf("M1-%02d", i)})
		}
		b, err := webhookBody(d, d.What)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		msgs, _ := p["messages"].([]any)
		if len(msgs) != tt.list || p["truncated"] != float64(tt.truncated) {
			t.Errorf("%d held: %d messages, truncated %v; want %d and %d", tt.held, len(msgs), p["truncated"], tt.list, tt.truncated)
		}
	}
	// Not on other events.
	b, _ := webhookBody(Message{Event: TaskDone}, "")
	if strings.Contains(string(b), "truncated") || strings.Contains(string(b), "messages") {
		t.Errorf("a plain payload has digest keys: %s", b)
	}
}
