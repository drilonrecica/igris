package notify

import (
	"slices"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
)

func channelNames(r *Router) []string {
	var out []string
	for _, e := range r.o.Channels {
		out = append(out, e.Channel.Name())
	}
	return out
}

func TestFromConfig(t *testing.T) {
	be := fake.New()
	full := func() config.Notify {
		c := config.Default().Notify
		c.Ntfy.Topic = "t"
		return c
	}
	tests := []struct {
		name    string
		tweak   func(*config.Notify)
		secrets config.Secrets
		be      bool
		want    []string
	}{
		{"toast and ntfy", func(*config.Notify) {}, config.Secrets{}, true, []string{"backend", "ntfy"}},
		{"toast off", func(c *config.Notify) { c.Backend.Enabled = false }, config.Secrets{}, true, []string{"ntfy"}},
		{"no backend", func(*config.Notify) {}, config.Secrets{}, false, []string{"ntfy"}},
		{"discord needs a webhook", func(*config.Notify) {}, config.Secrets{DiscordWebhook: "https://h/x"}, true, []string{"backend", "ntfy", "discord"}},
		{"empty events turn a channel off", func(c *config.Notify) { c.Ntfy.Events = []string{} }, config.Secrets{}, true, []string{"backend"}},
		{"ntfy without topic", func(c *config.Notify) { c.Ntfy.Topic = "" }, config.Secrets{}, true, []string{"backend"}},
		{"webhook needs a url", func(c *config.Notify) { c.Webhook.URL = "env:U" }, config.Secrets{WebhookURL: "https://h/w"}, true, []string{"backend", "ntfy", "webhook"}},
		{"webhook unresolved", func(c *config.Notify) { c.Webhook.URL = "env:U" }, config.Secrets{WebhookSecret: "s"}, true, []string{"backend", "ntfy"}},
		{"webhook without events", func(c *config.Notify) { c.Webhook.Events = []string{} }, config.Secrets{WebhookURL: "https://h/w"}, true, []string{"backend", "ntfy"}},
	}
	for _, tt := range tests {
		c := full()
		tt.tweak(&c)
		var b backend.Backend // stays a nil interface when tt.be is false
		if tt.be {
			b = be
		}
		r := FromConfig(c, tt.secrets, b)
		if got := channelNames(r); !slices.Equal(got, tt.want) {
			t.Errorf("%s: channels = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestFromConfigPassesSecrets(t *testing.T) {
	c := config.Default().Notify
	c.Ntfy.Topic = "t"
	r := FromConfig(c, config.Secrets{NtfyToken: "tk", DiscordWebhook: "https://h/x", WebhookURL: "https://h/w", WebhookSecret: "ws"}, nil)
	if got := r.o.Channels[0].Channel.(*Ntfy).Token; got != "tk" {
		t.Errorf("ntfy token = %q", got)
	}
	if got := r.o.Channels[1].Channel.(*Discord).WebhookURL; got != "https://h/x" {
		t.Errorf("discord webhook = %q", got)
	}
	if w := r.o.Channels[2].Channel.(*Webhook); w.URL != "https://h/w" || w.Secret != "ws" {
		t.Errorf("webhook = %q, %q", w.URL, w.Secret)
	}
	if !slices.Equal(r.o.Secrets, []string{"tk", "https://h/x", "https://h/w", "ws"}) {
		t.Errorf("redaction list = %v", r.o.Secrets)
	}
	if got := r.o.Channels[1].Events; len(got) != len(DefaultEvents) || slices.Contains(got, TaskDone) {
		t.Errorf("discord events = %v, want the defaults", got)
	}
}
