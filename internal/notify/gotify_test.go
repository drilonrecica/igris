package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestGotifySend(t *testing.T) {
	tests := []struct {
		ev   Event
		prio int
	}{
		{NeedsInput, 8}, {SessionLost, 8}, {TaskOverdue, 8},
		{VerifyFailedLimit, 5}, {PhaseStuck, 5}, {RunError, 5}, {PhaseDone, 5},
		{TaskDone, 3},
	}
	for _, tt := range tests {
		t.Run(string(tt.ev), func(t *testing.T) {
			srv, got := serve(t, 200)
			g := &Gotify{Server: srv.URL + "/gotify/", Token: "AppTok3n"}
			m := Message{Event: tt.ev, Project: "demo", Phase: "M5", TaskID: "M5-02", Title: "**Gotify**", What: "news"}
			if err := g.Send(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			if got.method != "POST" || got.path != "/gotify/message" {
				t.Errorf("%s %s, want POST /gotify/message", got.method, got.path)
			}
			if got.header.Get("X-Gotify-Key") != "AppTok3n" || got.header.Get("Content-Type") != "application/json" {
				t.Errorf("headers %v", got.header)
			}
			var p map[string]any
			if err := json.Unmarshal([]byte(got.body), &p); err != nil {
				t.Fatalf("body %q: %v", got.body, err)
			}
			want := map[string]any{"title": "igris · demo", "message": "phase M5 · M5-02 Gotify: news", "priority": float64(tt.prio)}
			if len(p) != len(want) {
				t.Errorf("payload %v, want %v", p, want)
			}
			for k, v := range want {
				if p[k] != v {
					t.Errorf("%s = %#v, want %#v", k, p[k], v)
				}
			}
		})
	}
}

func TestGotifyCutsMessage(t *testing.T) {
	srv, got := serve(t, 200)
	m := Message{Event: TaskDone, Project: "p", TaskID: "X-1", Title: strings.Repeat("é", 5000)}
	if err := (&Gotify{Server: srv.URL, Token: "t"}).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var p gotifyPayload
	if err := json.Unmarshal([]byte(got.body), &p); err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(p.Message); n != gotifyLimit || !strings.HasSuffix(p.Message, "…") {
		t.Errorf("message has %d characters, want %d ending in …", n, gotifyLimit)
	}
}

// The token travels in a header only; no error shows it.
func TestGotifyErrorsHideTheToken(t *testing.T) {
	const token = "SECRETTOKEN"
	srv, got := serve(t, 401)
	err := (&Gotify{Server: srv.URL, Token: token}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), token) {
		t.Errorf("status error = %v", err)
	}
	if strings.Contains(got.path, token) {
		t.Errorf("token in the URL: %s", got.path)
	}
	srv.Close()
	if err := (&Gotify{Server: srv.URL, Token: token}).Send(context.Background(), Message{Event: NeedsInput}); err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("connection error: %v", err)
	}
	if err := (&Gotify{Server: "http://bad host", Token: token}).Send(context.Background(), Message{}); err == nil || strings.Contains(err.Error(), token) {
		t.Errorf("invalid server error: %v", err)
	}
}
