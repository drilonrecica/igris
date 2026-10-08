package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// slackLimit is igris's cap on a Slack message's text, in characters.
const slackLimit = 4000

// Slack posts to a Slack incoming webhook. The webhook URL is a secret: it
// is never put into an error.
type Slack struct {
	WebhookURL string
	HTTP       *http.Client // nil means a default client
}

// Name implements Channel.
func (*Slack) Name() string { return "slack" }

// Send implements Channel with one short `text` message.
func (s *Slack) Send(ctx context.Context, m Message) error {
	body, err := json.Marshal(struct {
		Text string `json:"text"`
	}{slackText(m)})
	if err != nil {
		return fmt.Errorf("encode slack message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack webhook_url is not a valid URL: %w", transportError(err))
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
// link, cut to slackLimit characters without splitting an escape.
func slackText(m Message) string {
	s := "*" + m.Subject() + "*"
	if b := m.Body(); b != "" {
		s += " · " + b
	}
	s += " (" + string(m.Event) + ")"
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
