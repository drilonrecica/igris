package notify

import (
	"context"
	"encoding/json"
	"mime"
	"strings"
	"testing"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/config"
)

func mustTemplate(t *testing.T, text string) *template.Template {
	t.Helper()
	tm, err := config.ParseMessageTemplate("notify.test.template", text)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

var tmplMsg = Message{
	Event: TaskDone, Project: "demo", Phase: "M1", TaskID: "M1-03", Title: "**Config** `loader`.",
	What: "done", RunID: "20261008-091500-3fa2", At: time.Date(2026, 10, 8, 9, 15, 0, 0, time.Local),
}

// Every variable reaches the template; the text is what each channel sends
// in place of its default.
func TestTemplateReplacesText(t *testing.T) {
	const text = "{{.Event}}|{{.Project}}|{{.Phase}}|{{.TaskID}}|{{.Title}}|{{.What}}|{{.RunID}}|{{.At.Format \"15:04\"}}"
	const want = "task_done|demo|M1|M1-03|Config loader|done|20261008-091500-3fa2|09:15"
	tm := mustTemplate(t, text)
	ctx := context.Background()

	srv, got := serve(t, 200)
	if err := (&Ntfy{Server: srv.URL, Topic: "t", Template: tm}).Send(ctx, tmplMsg); err != nil {
		t.Fatal(err)
	}
	if title, _ := new(mime.WordDecoder).DecodeHeader(got.header.Get("Title")); got.body != want || title != "igris · demo" {
		t.Errorf("ntfy body %q (title %q), want %q", got.body, got.header.Get("Title"), want)
	}

	field := func(body, key string) string {
		var p map[string]any
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		s, _ := p[key].(string)
		return s
	}
	srv, got = serve(t, 204)
	if err := (&Discord{WebhookURL: srv.URL, Template: tm}).Send(ctx, tmplMsg); err != nil {
		t.Fatal(err)
	}
	if c := field(got.body, "content"); c != want {
		t.Errorf("discord content %q", c)
	}
	srv, got = serve(t, 200)
	if err := (&Gotify{Server: srv.URL, Token: "x", Template: tm}).Send(ctx, tmplMsg); err != nil {
		t.Fatal(err)
	}
	if m, ti := field(got.body, "message"), field(got.body, "title"); m != want || ti != "igris · demo" {
		t.Errorf("gotify message %q title %q", m, ti)
	}
	srv, got = serve(t, 200)
	if err := (&Webhook{URL: srv.URL, Template: tm}).Send(ctx, tmplMsg); err != nil {
		t.Fatal(err)
	}
	if tx, w := field(got.body, "text"), field(got.body, "what"); tx != want || w != "done" {
		t.Errorf("webhook text %q what %q", tx, w)
	}
}

// Slack escapes the template's output like its default text.
func TestSlackTemplateIsEscaped(t *testing.T) {
	srv, got := serve(t, 200)
	m := tmplMsg
	m.Title = "<!channel> & <https://evil.test|x>"
	s := &Slack{WebhookURL: srv.URL, Template: mustTemplate(t, "<{{.Title}}>")}
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	var p struct{ Text string }
	if err := json.Unmarshal([]byte(got.body), &p); err != nil {
		t.Fatal(err)
	}
	if want := "&lt;&lt;!channel&gt; &amp; &lt;https://evil.test|x&gt;&gt;"; p.Text != want {
		t.Errorf("text %q, want %q", p.Text, want)
	}
}

func TestTemplatedCleansCutsAndFallsBack(t *testing.T) {
	long := strings.Repeat("é", 900) // 1800 bytes, 900 characters per {{.What}}
	tests := []struct {
		name, text, what string
		cut              func(string) string
		want             string
	}{
		{"no template", "", "w", cutTo(10), ""},
		{"empty output falls back", "{{if false}}x{{end}}  ", "w", cutTo(10), ""},
		{"blank output falls back", "{{.What}}", " \n ", cutTo(10), ""},
		{"control characters out, newlines kept", "a\x1b[2J{{.What}}\nb", "\x07w\r", cutTo(100), "aw\nb"},
		{"cut to characters", "{{.What}}", "abcdefghijkl", cutTo(5), "abcd…"},
		{"cut to bytes on a character boundary", "{{.What}}{{.What}}{{.What}}", long, func(s string) string { return cutBytes(s, ntfyLimit) }, ""},
	}
	for _, tt := range tests {
		var tm *template.Template
		if tt.text != "" {
			tm = mustTemplate(t, tt.text)
		}
		m := tmplMsg
		m.What = tt.what
		got := templated(tm, m, tt.cut)
		if tt.name == "cut to bytes on a character boundary" {
			if len(got) > ntfyLimit || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
				t.Errorf("%s: %d bytes, valid %v", tt.name, len(got), utf8.ValidString(got))
			}
			continue
		}
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
	// Digests have a fixed format: no template.
	if got := templated(mustTemplate(t, "x"), Message{Event: Digest}, cutTo(10)); got != "" {
		t.Errorf("digest templated to %q", got)
	}
}

// Each channel cuts template output to its own limit.
func TestTemplateChannelCaps(t *testing.T) {
	tm := mustTemplate(t, "{{.What}}{{.What}}{{.What}}{{.What}}{{.What}}")
	m := tmplMsg
	m.What = strings.Repeat("x", 1000) // 5000 characters out
	ctx := context.Background()
	field := func(body, key string) string {
		var p map[string]any
		_ = json.Unmarshal([]byte(body), &p)
		s, _ := p[key].(string)
		return s
	}
	for _, tt := range []struct {
		name string
		send func(url string) error
		get  func(c *captured) string
		max  int
	}{
		{"ntfy", func(u string) error { return (&Ntfy{Server: u, Topic: "t", Template: tm}).Send(ctx, m) }, func(c *captured) string { return c.body }, ntfyLimit},
		{"discord", func(u string) error { return (&Discord{WebhookURL: u, Template: tm}).Send(ctx, m) }, func(c *captured) string { return field(c.body, "content") }, discordLimit},
		{"slack", func(u string) error { return (&Slack{WebhookURL: u, Template: tm}).Send(ctx, m) }, func(c *captured) string { return field(c.body, "text") }, slackLimit},
		{"gotify", func(u string) error { return (&Gotify{Server: u, Token: "k", Template: tm}).Send(ctx, m) }, func(c *captured) string { return field(c.body, "message") }, gotifyLimit},
		{"webhook", func(u string) error { return (&Webhook{URL: u, Template: tm}).Send(ctx, m) }, func(c *captured) string { return field(c.body, "text") }, webhookLimit},
	} {
		srv, got := serve(t, 200)
		if err := tt.send(srv.URL); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		// x is one byte, so characters and bytes agree but for the "…".
		if s := tt.get(got); utf8.RuneCountInString(s) != tt.max && len(s) != tt.max || !strings.HasSuffix(s, "…") {
			t.Errorf("%s: %d characters (%d bytes), want the cap %d ending in …", tt.name, utf8.RuneCountInString(s), len(s), tt.max)
		}
	}
}

func TestDigestOnChannels(t *testing.T) {
	ctx := context.Background()
	held := []Message{
		{Event: TaskDone, Project: "demo", Phase: "M1", TaskID: "M1-01", Title: "One", What: "done", RunID: "r", At: time.Date(2026, 10, 8, 22, 5, 0, 0, time.UTC)},
		{Event: PhaseDone, Project: "demo", Phase: "M1", What: "complete", RunID: "r"},
	}
	d := Message{Event: Digest, Project: "demo", What: "quiet hours 22:00–07:00: 2 held\n22:05 task_done: …", RunID: "r", At: time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC), Held: held}
	tm := mustTemplate(t, "custom")

	srv, got := serve(t, 200)
	if err := (&Webhook{URL: srv.URL, Template: tm}).Send(ctx, d); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Event, Task, Title, What, Run, At, Text string
		Messages                                []map[string]any
	}
	if err := json.Unmarshal([]byte(got.body), &p); err != nil {
		t.Fatal(err)
	}
	if p.Event != "digest" || got.header.Get("X-Igris-Event") != "digest" || p.Task != "" || p.What != "" || p.Text != d.What || p.Run != "r" || p.At != "2026-10-09T07:00:00Z" {
		t.Errorf("digest payload %+v", p)
	}
	if len(p.Messages) != 2 || p.Messages[0]["task"] != "M1-01" || p.Messages[0]["at"] != "2026-10-08T22:05:00Z" || p.Messages[1]["event"] != "phase_done" {
		t.Errorf("messages %v", p.Messages)
	}
	for _, msg := range p.Messages {
		if _, ok := msg["text"]; ok {
			t.Errorf("a held message has text: %v", msg)
		}
		if _, ok := msg["messages"]; ok {
			t.Errorf("a held message has messages: %v", msg)
		}
	}

	srv, got = serve(t, 204)
	if err := (&Discord{WebhookURL: srv.URL, Template: tm}).Send(ctx, d); err != nil {
		t.Fatal(err)
	}
	if want := "**igris · demo** · " + d.What; !strings.Contains(got.body, jsonText(want)) {
		t.Errorf("discord digest %s, want content %q", got.body, want)
	}
	srv, got = serve(t, 200)
	if err := (&Slack{WebhookURL: srv.URL}).Send(ctx, d); err != nil {
		t.Fatal(err)
	}
	if want := "*igris · demo* · " + d.What; !strings.Contains(got.body, jsonText(want)) {
		t.Errorf("slack digest %s, want text %q", got.body, want)
	}

	for _, tt := range []struct {
		held []Message
		want int
	}{{held, 5}, {held[:1], 3}, {[]Message{{Event: NeedsInput}}, 5}} {
		d.Held = tt.held
		if got := gotifyPriority(d); got != tt.want {
			t.Errorf("gotify priority of a digest of %v = %d, want %d", tt.held, got, tt.want)
		}
	}
}

func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return strings.Trim(string(b), `"`)
}
