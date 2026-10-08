package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// discordLimit is Discord's cap on a message's content, in characters.
const discordLimit = 2000

// Discord posts to a Discord webhook. The webhook URL is a secret: it is
// never put into an error.
type Discord struct {
	WebhookURL string
	HTTP       *http.Client // nil means a default client
}

// Name implements Channel.
func (*Discord) Name() string { return "discord" }

type discordPayload struct {
	Content string `json:"content"`
	// AllowedMentions with an empty Parse list stops task titles from
	// pinging @everyone or roles.
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

// Send implements Channel with one short plain `content` message.
func (d *Discord) Send(ctx context.Context, m Message) error {
	p := discordPayload{Content: discordContent(m)}
	p.AllowedMentions.Parse = []string{}
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encode discord message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("discord webhook_url is not a valid URL: %w", transportError(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "igris")
	resp, err := defaultHTTP(d.HTTP).Do(req)
	if err != nil {
		return fmt.Errorf("discord: %w", transportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("discord: %w", statusError(resp))
	}
	return nil
}

// discordContent is "**igris · project** · <body> (event)", cut to Discord's
// limit on a character boundary.
func discordContent(m Message) string {
	s := "**" + m.Subject() + "**"
	if b := m.Body(); b != "" {
		s += " · " + b
	}
	s += " (" + string(m.Event) + ")"
	return cutRunes(s, discordLimit)
}
