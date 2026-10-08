package notify

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSlackSend(t *testing.T) {
	srv, got := serve(t, 200)
	s := &Slack{WebhookURL: srv.URL + "/services/T0/B0/xyz"}
	m := Message{Event: NeedsInput, Project: "demo", Phase: "M5", TaskID: "M5-02", Title: "Ping <!channel> & <https://evil.test|click>", What: "needs you"}
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/services/T0/B0/xyz" || got.header.Get("Content-Type") != "application/json" {
		t.Errorf("%s %s %v", got.method, got.path, got.header)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(got.body), &p); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	want := "*igris · demo* · phase M5 · M5-02 Ping &lt;!channel&gt; &amp; &lt;https://evil.test|click&gt;: needs you (needs_input)"
	if len(p) != 1 || p["text"] != want {
		t.Errorf("payload = %v, want only text %q", p, want)
	}
}

func TestSlackEscapeCuts(t *testing.T) {
	tests := []struct {
		name, in string
		n        int
		want     string
	}{
		{"fits", "a<b", 10, "a&lt;b"},
		{"exactly", "a<b", 6, "a&lt;b"},
		{"no split escape", "ab<cd", 6, "ab…"},
		{"cut after escape", "a&bcdef", 8, "a&amp;b…"},
		{"characters, not bytes", "éééé", 3, "éé…"},
	}
	for _, tt := range tests {
		if got := slackEscape(tt.in, tt.n); got != tt.want {
			t.Errorf("%s: slackEscape(%q, %d) = %q, want %q", tt.name, tt.in, tt.n, got, tt.want)
		}
	}
	long := slackText(Message{Event: TaskDone, Project: "p", TaskID: "X-1", Title: strings.Repeat("<é", 3000)})
	if n := utf8.RuneCountInString(long); n > slackLimit || !strings.HasSuffix(long, "…") || strings.Contains(long, "<") {
		t.Errorf("long text: %d characters, suffix %q", n, long[len(long)-8:])
	}
}

func TestSlackErrorsHideTheWebhook(t *testing.T) {
	srv, _ := serve(t, 403)
	hook := srv.URL + "/services/T0/B0/SECRETTOKEN"
	leaks := func(err error) bool {
		for _, v := range []string{"SECRETTOKEN", srv.URL, url.QueryEscape(hook)} {
			if strings.Contains(err.Error(), v) {
				return true
			}
		}
		return false
	}
	err := (&Slack{WebhookURL: hook}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || !strings.Contains(err.Error(), "403") || leaks(err) {
		t.Errorf("status error = %v", err)
	}
	srv.Close()
	if err := (&Slack{WebhookURL: hook}).Send(context.Background(), Message{Event: NeedsInput}); err == nil || leaks(err) {
		t.Errorf("connection error leaks the webhook: %v", err)
	}
	if err := (&Slack{WebhookURL: "http://bad host/SECRETTOKEN"}).Send(context.Background(), Message{}); err == nil || leaks(err) {
		t.Errorf("invalid URL error leaks the webhook: %v", err)
	}
}
