package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"unicode/utf8"
)

// slackLimit is igris's cap on a Slack message's text, in characters.
const slackLimit = 4000

// Slack posts to a Slack incoming webhook. The webhook URL is a secret: it
// is never put into an error.
type Slack struct {
	WebhookURL string
	HTTP       *http.Client // nil means a default client
	// Template, when set, makes the text; it is escaped like the default
	// (SPEC §10).
	Template *template.Template
}

// Name implements Channel.
func (*Slack) Name() string { return "slack" }

// Send implements Channel with one short `text` message.
func (s *Slack) Send(ctx context.Context, m Message) error {
	text := slackText(m)
	if s := templated(s.Template, m, func(s string) string { return s }); s != "" {
		text = slackEscape(s, slackLimit)
	}
	body, err := json.Marshal(struct {
		Text string `json:"text"`
	}{text})
	if err != nil {
		return fmt.Errorf("encode slack message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return invalidURL("notify.slack.webhook_url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "igris")
	resp, err := defaultHTTP(s.HTTP).Do(req)
	if err != nil {
		return fmt.Errorf("slack: %w", transportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack: %w", statusError(resp))
	}
	return nil
}

// slackText is "*igris · project* · <body> (event)" with Slack's control
// characters escaped, so a plan title can't mention <!channel> or make a
// link, cut to slackLimit characters without splitting an escape. A digest
// goes without the "(event)".
func slackText(m Message) string {
	s := "*" + m.Subject() + "*"
	if b := m.Body(); b != "" {
		s += " · " + b
	}
	if m.Event != Digest {
		s += " (" + string(m.Event) + ")"
	}
	return slackEscape(s, slackLimit)
}

var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// slackEscape escapes &, < and > in s and cuts the result to at most n
// characters, ending in "…" when it was longer. The cut falls between
// characters of s, never inside an escape.
func slackEscape(s string, n int) string {
	if e := slackEscaper.Replace(s); utf8.RuneCountInString(e) <= n {
		return e
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		e := slackEscaper.Replace(string(r))
		k := utf8.RuneCountInString(e)
		if used+k > n-1 {
			break
		}
		b.WriteString(e)
		used += k
	}
	return b.String() + "…"
}
