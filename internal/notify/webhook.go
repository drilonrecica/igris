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
	"time"
)

// webhookLimit is the cap on a webhook payload's text, in characters.
const webhookLimit = 4000

// Webhook posts a JSON payload to any URL (SPEC §10). The URL and the
// secret are secrets: neither is ever put into an error.
type Webhook struct {
	URL string
	// Secret, when set, signs the body: X-Igris-Signature is
	// "sha256=" + the lowercase hex HMAC-SHA256 of the raw body.
	Secret string
	HTTP   *http.Client // nil means a default client
}

// Name implements Channel.
func (*Webhook) Name() string { return "webhook" }

// webhookPayload is the body, version 1. Every key is always present ("" when
// unknown), so receivers need no optional handling.
type webhookPayload struct {
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
	Text    string `json:"text"`
}

// webhookBody encodes m as the version 1 payload.
func webhookBody(m Message) ([]byte, error) {
	p := webhookPayload{
		V:       1,
		Event:   string(m.Event),
		Project: m.Project,
		Phase:   m.Phase,
		Task:    m.TaskID,
		Title:   PlainTitle(m.Title),
		What:    m.What,
		Run:     m.RunID,
		Urgent:  m.Event.Urgent(),
		Text:    cutRunes(m.Body(), webhookLimit),
	}
	if !m.At.IsZero() {
		p.At = m.At.UTC().Format(time.RFC3339)
	}
	return json.Marshal(p)
}

// webhookSignature is the X-Igris-Signature value for body.
func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Send implements Channel.
func (w *Webhook) Send(ctx context.Context, m Message) error {
	body, err := webhookBody(m)
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook url is not a valid URL: %w", transportError(err))
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
