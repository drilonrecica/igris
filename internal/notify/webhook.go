package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"text/template"
	"time"
)

// webhookLimit is the cap on a webhook payload's text, in characters.
const webhookLimit = 4000

// maxWebhookMessages is the most held messages a digest lists; the rest
// are counted in truncated.
const maxWebhookMessages = 50

// Webhook posts a JSON payload to any URL (SPEC §10). The URL and the
// secret are secrets: neither is ever put into an error.
type Webhook struct {
	URL string
	// Secret, when set, signs the body: X-Igris-Signature is
	// "sha256=" + the lowercase hex HMAC-SHA256 of the raw body.
	Secret string
	HTTP   *http.Client // nil means a default client
	// Template, when set, makes the payload's text (SPEC §10).
	Template *template.Template
}

// Name implements Channel.
func (*Webhook) Name() string { return "webhook" }

// webhookFields are the keys of every payload, version 1. Every key is
// always present ("" when unknown), so receivers need no optional handling.
type webhookFields struct {
	V       int    `json:"v"`
	Event   string `json:"event"`
	Project string `json:"project"`
	Phase   string `json:"phase"`
	Task    string `json:"task"`
	Title   string `json:"title"`
	What    string `json:"what"`
	Run     string `json:"run"`
	At      string `json:"at"` // RFC 3339 UTC
	Urgent  bool   `json:"urgent"`
}

// webhookPayload is the body: the fields, the text and, for a digest, the
// held messages it stands for (at most maxWebhookMessages, the oldest) and
// how many more it left out.
type webhookPayload struct {
	webhookFields
	Text      string          `json:"text"`
	Messages  []webhookFields `json:"messages,omitempty"`
	Truncated *int            `json:"truncated,omitempty"`
}

func webhookFieldsOf(m Message) webhookFields {
	f := webhookFields{
		V:       1,
		Event:   string(m.Event),
		Project: m.Project,
		Phase:   m.Phase,
		Task:    m.TaskID,
		Title:   PlainTitle(m.Title),
		What:    m.What,
		Run:     m.RunID,
		Urgent:  m.Event.Urgent(),
	}
	if !m.At.IsZero() {
		f.At = m.At.UTC().Format(time.RFC3339)
	}
	return f
}

// webhookBody encodes m as the version 1 payload with text as its text. A
// digest has empty task, title and what, and lists its messages.
func webhookBody(m Message, text string) ([]byte, error) {
	p := webhookPayload{webhookFields: webhookFieldsOf(m), Text: text}
	if m.Event == Digest {
		p.Task, p.Title, p.What = "", "", ""
		held := m.Held[:min(len(m.Held), maxWebhookMessages)]
		p.Messages = make([]webhookFields, len(held))
		for i, h := range held {
			p.Messages[i] = webhookFieldsOf(h)
		}
		more := len(m.Held) - len(held)
		p.Truncated = &more
	}
	return json.Marshal(p)
}

// text is the payload's text: the template's, else the message body.
func (w *Webhook) text(m Message) string {
	if s := templated(w.Template, m, cutTo(webhookLimit)); s != "" {
		return s
	}
	return cutRunes(m.Body(), webhookLimit)
}

// webhookSignature is the X-Igris-Signature value for body.
func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Send implements Channel.
func (w *Webhook) Send(ctx context.Context, m Message) error {
	body, err := webhookBody(m, w.text(m))
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return invalidURL("notify.webhook.url")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "igris")
	req.Header.Set("X-Igris-Event", string(m.Event))
	if w.Secret != "" {
		req.Header.Set("X-Igris-Signature", webhookSignature(w.Secret, body))
	}
	resp, err := defaultHTTP(w.HTTP).Do(req)
	if err != nil {
		return fmt.Errorf("webhook: %w", transportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("webhook: %w", statusError(resp))
	}
	return nil
}
