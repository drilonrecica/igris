package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"text/template"
)

// gotifyLimit is igris's cap on a Gotify message, in characters.
const gotifyLimit = 4000

// Gotify posts to a Gotify server (https://gotify.net/docs/pushmsg). The
// application token is a secret: it goes in a header, never in the URL or
// an error.
type Gotify struct {
	Server string // base URL, e.g. https://gotify.example.org
	Token  string
	HTTP   *http.Client // nil means a default client
	// Template, when set, makes the message (SPEC §10).
	Template *template.Template
}

// Name implements Channel.
func (*Gotify) Name() string { return "gotify" }

type gotifyPayload struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
}

// Send implements Channel: the title is the subject, the message the body
// (or the template's text), and the priority follows how urgent the event
// is.
func (g *Gotify) Send(ctx context.Context, m Message) error {
	text := cutRunes(m.Body(), gotifyLimit)
	if s := templated(g.Template, m, cutTo(gotifyLimit)); s != "" {
		text = s
	}
	body, err := json.Marshal(gotifyPayload{
		Title:    m.Subject(),
		Message:  text,
		Priority: gotifyPriority(m),
	})
	if err != nil {
		return fmt.Errorf("encode gotify message: %w", err)
	}
	u, err := url.JoinPath(g.Server, "message")
	if err != nil {
		return fmt.Errorf("gotify server is not a URL: %w", transportError(err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build gotify request: %w", transportError(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "igris")
	req.Header.Set("X-Gotify-Key", g.Token)
	resp, err := defaultHTTP(g.HTTP).Do(req)
	if err != nil {
		return fmt.Errorf("gotify: %w", transportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("gotify: %w", statusError(resp))
	}
	return nil
}

// gotifyPriority maps a message to Gotify's 0-10 scale (decision V05-P1):
// 8 when the owner is needed now, 3 for a finished task (and a digest of
// finished tasks only), 5 for the rest, other digests included.
func gotifyPriority(m Message) int {
	switch {
	case m.Event.Urgent():
		return 8
	case m.Event == TaskDone:
		return 3
	case m.Event == Digest && len(m.Held) > 0 && !slices.ContainsFunc(m.Held, func(h Message) bool { return h.Event != TaskDone }):
		return 3
	}
	return 5
}
