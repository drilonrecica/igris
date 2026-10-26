package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"text/template"
)

// ntfyLimit is ntfy's cap on a message body, in bytes.
const ntfyLimit = 4096

// ntfyMaxActions is how many buttons ntfy shows on one message.
const ntfyMaxActions = 3

// Action is an ntfy action button (https://docs.ntfy.sh/publish/#action-buttons):
// an http one POSTs to URL from the phone, a view one opens URL.
type Action struct {
	View  bool // a view action; else http with POST
	Label string
	URL   string
}

// Actions makes the buttons of a message (SPEC §10): the answers of the
// pending question it announces (Message.Question), and Open for a
// message about a task; none when it returns nil. It is called on every
// attempt to send the message.
type Actions func(m Message) []Action

// ntfyAction is an action in ntfy's JSON form.
type ntfyAction struct {
	Action string `json:"action"`
	Label  string `json:"label"`
	URL    string `json:"url"`
	Method string `json:"method,omitempty"`
	Clear  bool   `json:"clear"`
}

// Ntfy publishes to an ntfy topic (https://docs.ntfy.sh/publish/).
type Ntfy struct {
	Server string // base URL, e.g. https://ntfy.sh
	Topic  string
	Token  string       // optional bearer token
	HTTP   *http.Client // nil means a default client
	// Template, when set, makes the body (SPEC §10).
	Template *template.Template
	// Actions, when set, makes the buttons of a message (SPEC §10): the
	// answers of a pending question, Open; never on a digest.
	Actions Actions
}

// Name implements Channel.
func (*Ntfy) Name() string { return "ntfy" }

// Send implements Channel: the body is the message body (or the
// template's text), the title its subject, and urgent events get priority
// high.
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	body := cutBytes(m.Body(), ntfyLimit)
	if s := templated(n.Template, m, func(s string) string { return cutBytes(s, ntfyLimit) }); s != "" {
		body = s
	}
	u, err := url.JoinPath(n.Server, url.PathEscape(n.Topic))
	if err != nil {
		return invalidURL("notify.ntfy.server")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		return invalidURL("notify.ntfy.server")
	}
	req.Header.Set("Title", mime.BEncoding.Encode("UTF-8", m.Subject()))
	req.Header.Set("Priority", ntfyPriority(m.Event))
	req.Header.Set("User-Agent", "igris")
	if n.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.Token)
	}
	if h := n.actionsHeader(m); h != "" {
		req.Header.Set("Actions", h)
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
	if ev.NeedsYou() {
		return "high"
	}
	return "default"
}

// actionsHeader is the Actions header of m's buttons, in ntfy's JSON form
// (its simple form would need quoting for labels like "Leave uncommitted"); ""
// when m gets none. Actions decides which: answers for a pending
// question, Open for a message about a task (SPEC §10); a digest gets
// none.
func (n *Ntfy) actionsHeader(m Message) string {
	if n.Actions == nil || m.Event == Digest {
		return ""
	}
	acts := n.Actions(m)
	if len(acts) > ntfyMaxActions {
		acts = acts[:ntfyMaxActions]
	}
	if len(acts) == 0 {
		return ""
	}
	out := make([]ntfyAction, len(acts))
	for i, a := range acts {
		if a.View {
			out[i] = ntfyAction{Action: "view", Label: a.Label, URL: a.URL}
			continue
		}
		// clear: an answered question's notification goes away.
		out[i] = ntfyAction{Action: "http", Label: a.Label, URL: a.URL, Method: http.MethodPost, Clear: true}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}
