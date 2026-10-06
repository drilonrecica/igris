package notify

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type captured struct {
	method, path, body string
	header             http.Header
}

func serve(t *testing.T, status int) (*httptest.Server, *captured) {
	t.Helper()
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.method, c.path, c.body, c.header = r.Method, r.URL.EscapedPath(), string(b), r.Header.Clone()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "echo "+r.Header.Get("Authorization")) // #nosec G705 -- test server, not HTML
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestNtfySend(t *testing.T) {
	m := Message{Event: NeedsInput, Project: "demo", Phase: "M5", TaskID: "M5-02", Title: "ntfy", What: "your turn"}
	tests := []struct {
		name, token, topic, wantPath, wantPrio string
		ev                                     Event
	}{
		{"urgent with token", "tk_abc", "my topic", "/my%20topic", "high", NeedsInput},
		{"session lost is urgent", "", "t", "/t", "high", SessionLost},
		{"phase done is default", "", "t", "/t", "default", PhaseDone},
	}
	for _, tt := range tests {
		srv, got := serve(t, 200)
		m.Event = tt.ev
		n := &Ntfy{Server: srv.URL + "/", Topic: tt.topic, Token: tt.token}
		if err := n.Send(context.Background(), m); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got.method != "POST" || got.path != tt.wantPath {
			t.Errorf("%s: %s %s, want POST %s", tt.name, got.method, got.path, tt.wantPath)
		}
		if got.body != "phase M5 · M5-02 ntfy: your turn" {
			t.Errorf("%s: body = %q", tt.name, got.body)
		}
		title, err := new(mime.WordDecoder).DecodeHeader(got.header.Get("Title"))
		if err != nil || title != "igris · demo" {
			t.Errorf("%s: Title = %q (%v)", tt.name, got.header.Get("Title"), err)
		}
		if p := got.header.Get("Priority"); p != tt.wantPrio {
			t.Errorf("%s: Priority = %q, want %q", tt.name, p, tt.wantPrio)
		}
		auth := got.header.Get("Authorization")
		if (tt.token == "") != (auth == "") || (tt.token != "" && auth != "Bearer "+tt.token) {
			t.Errorf("%s: Authorization = %q", tt.name, auth)
		}
	}
}

func TestNtfyErrors(t *testing.T) {
	srv, _ := serve(t, 403)
	err := (&Ntfy{Server: srv.URL, Topic: "t", Token: "tk_secret"}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want the status", err)
	}
	if strings.Contains(err.Error(), "tk_secret") {
		t.Errorf("error leaks the token (server echo): %v", err)
	}

	srv.Close()
	err = (&Ntfy{Server: srv.URL, Topic: "topicsecret"}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("connection error = %v, want one without the URL", err)
	}
}
