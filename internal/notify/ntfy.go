package notify

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"text/template"
)

// ntfyLimit is ntfy's cap on a message body, in bytes.
const ntfyLimit = 4096

// Ntfy publishes to an ntfy topic (https://docs.ntfy.sh/publish/).
type Ntfy struct {
	Server string // base URL, e.g. https://ntfy.sh
	Topic  string
	Token  string       // optional bearer token
	HTTP   *http.Client // nil means a default client
	// Template, when set, makes the body (SPEC §10).
	Template *template.Template
}

// Name implements Channel.
func (*Ntfy) Name() string { return "ntfy" }

// Send implements Channel: the body is the message body (or the
// template's text), the title its subject, and urgent events get priority
// high.
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	body := m.Body()
	if s := templated(n.Template, m, func(s string) string { return cutBytes(s, ntfyLimit) }); s != "" {
		body = s
	}
	u, err := url.JoinPath(n.Server, url.PathEscape(n.Topic))
	if err != nil {
		return fmt.Errorf("ntfy server %q is not a URL: %w", n.Server, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build ntfy request: %w", transportError(err))
	}
	req.Header.Set("Title", mime.BEncoding.Encode("UTF-8", m.Subject()))
	req.Header.Set("Priority", ntfyPriority(m.Event))
	req.Header.Set("User-Agent", "igris")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	resp, err := defaultHTTP(n.HTTP).Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", transportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy: %w", statusError(resp))
	}
	return nil
}

func ntfyPriority(ev Event) string {
	if ev.Urgent() {
		return "high"
	}
	return "default"
}
