package notify

import (
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
)

// FromConfig builds the router for the [notify] settings. A channel is left
// out when it is off or not set up: the backend toast when disabled, ntfy
// without a topic, Discord, Slack and the webhook without a URL, Gotify
// without a server and a token, and any channel whose events list is
// empty. secrets holds the resolved env: references; every value is
// scrubbed from the errors the router reports. tune adjusts the router's
// Options last (tests use it to avoid real sleeps).
func FromConfig(c config.Notify, s config.Secrets, be backend.Backend, tune ...func(*Options)) *Router {
	var entries []Entry
	if c.Backend.Enabled && be != nil {
		// The toast has no events setting: it gets the default events, the
		// ones that need the owner or end a phase (SPEC §10), not task_done.
		entries = append(entries, Entry{Channel: Toast{Backend: be}, Events: DefaultEvents})
	}
	if c.Ntfy.Topic != "" && len(c.Ntfy.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Ntfy{Server: c.Ntfy.Server, Topic: c.Ntfy.Topic, Token: s.NtfyToken},
			Events:  toEvents(c.Ntfy.Events),
		})
	}
	if s.DiscordWebhook != "" && len(c.Discord.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Discord{WebhookURL: s.DiscordWebhook},
			Events:  toEvents(c.Discord.Events),
		})
	}
	if s.WebhookURL != "" && len(c.Webhook.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Webhook{URL: s.WebhookURL, Secret: s.WebhookSecret},
			Events:  toEvents(c.Webhook.Events),
		})
	}
	if s.SlackWebhook != "" && len(c.Slack.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Slack{WebhookURL: s.SlackWebhook},
			Events:  toEvents(c.Slack.Events),
		})
	}
	if c.Gotify.Server != "" && s.GotifyToken != "" && len(c.Gotify.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Gotify{Server: c.Gotify.Server, Token: s.GotifyToken},
			Events:  toEvents(c.Gotify.Events),
		})
	}
	o := Options{Channels: entries, Secrets: []string{s.NtfyToken, s.DiscordWebhook, s.WebhookURL, s.WebhookSecret, s.SlackWebhook, s.GotifyToken}}
	for _, f := range tune {
		f(&o)
	}
	return New(o)
}

func toEvents(names []string) []Event {
	out := make([]Event, len(names))
	for i, n := range names {
		out[i] = Event(n)
	}
	return out
}
