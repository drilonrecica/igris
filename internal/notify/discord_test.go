package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDiscordSend(t *testing.T) {
	srv, got := serve(t, 204)
	d := &Discord{WebhookURL: srv.URL + "/api/webhooks/1/tok"}
	m := Message{Event: PhaseDone, Project: "demo", Phase: "M5", What: "complete"}
	if err := d.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/api/webhooks/1/tok" {
		t.Errorf("%s %s", got.method, got.path)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var p struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal([]byte(got.body), &p); err != nil {
		t.Fatalf("body %q: %v", got.body, err)
	}
	if want := "**igris · demo** · phase M5: complete (phase_done)"; p.Content != want {
		t.Errorf("content = %q, want %q", p.Content, want)
	}
	if p.AllowedMentions.Parse == nil || len(p.AllowedMentions.Parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want an empty list (the key must be present)", p.AllowedMentions.Parse)
	}
	if !strings.Contains(got.body, `"parse":[]`) {
		t.Errorf("body %q lacks \"parse\":[]", got.body)
	}
}

func TestDiscordTruncates(t *testing.T) {
	m := Message{Event: NeedsInput, Project: "p", TaskID: "X-1", Title: strings.Repeat("é", 3000)}
	c := discordContent(m)
	if n := utf8.RuneCountInString(c); n != discordLimit {
		t.Errorf("%d characters, want %d", n, discordLimit)
	}
	if !utf8.ValidString(c) || !strings.HasSuffix(c, "…") {
		t.Errorf("bad truncation: valid=%v suffix=%q", utf8.ValidString(c), c[len(c)-4:])
	}
}

func TestDiscordErrorsHideTheWebhook(t *testing.T) {
	srv, _ := serve(t, 429)
	hook := srv.URL + "/api/webhooks/1/SECRETTOKEN"
	err := (&Discord{WebhookURL: hook}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want the status", err)
	}
	srv.Close()
	err = (&Discord{WebhookURL: hook}).Send(context.Background(), Message{Event: NeedsInput})
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("connection error leaks the webhook: %v", err)
	}
	err = (&Discord{WebhookURL: "http://bad host/SECRETTOKEN"}).Send(context.Background(), Message{})
	if err == nil || strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Errorf("invalid URL error leaks the webhook: %v", err)
	}
}

// A plan title can't make a masked link ([text](url)) in Discord, from the
// default text or a template.
func TestDiscordNeutralisesMaskedLinks(t *testing.T) {
	m := Message{Event: TaskDone, Project: "p", TaskID: "X-1", Title: `see [docs](https://evil.test) \[x](y)`, What: "done"}
	for _, tt := range []struct {
		name string
		d    *Discord
	}{{"default", &Discord{}}, {"template", &Discord{Template: mustTemplate(t, "{{.Title}} [{{.TaskID}}](https://x.test)")}}} {
		srv, got := serve(t, 204)
		tt.d.WebhookURL = srv.URL
		if err := tt.d.Send(context.Background(), m); err != nil {
			t.Fatal(err)
		}
		var p struct{ Content string }
		if err := json.Unmarshal([]byte(got.body), &p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p.Content, " [docs](") || strings.Contains(p.Content, " [X-1](") || !strings.Contains(p.Content, `\[docs\](https://evil.test)`) {
			t.Errorf("%s: content %q keeps a link", tt.name, p.Content)
		}
		// A backslash in the title can't undo the escape.
		if strings.Contains(p.Content, `\[x](y)`) {
			t.Errorf("%s: content %q", tt.name, p.Content)
		}
	}
	// The cut still lands on the limit.
	long := Message{Event: NeedsInput, Project: "p", TaskID: "X-1", Title: strings.Repeat("[", 3000)}
	if n := utf8.RuneCountInString(discordContent(long)); n != discordLimit {
		t.Errorf("%d characters, want %d", n, discordLimit)
	}
}
