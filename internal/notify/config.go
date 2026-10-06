package notify

import (
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
)

// FromConfig builds the router for the [notify] settings. A channel is left
// out when it is off or not set up: the backend toast when disabled, ntfy
// without a topic, Discord without a webhook URL, and any channel whose
// events list is empty. secrets holds the resolved env: references; every
// value is scrubbed from the errors the router reports.
func FromConfig(c config.Notify, s config.Secrets, be backend.Backend) *Router {
	var entries []Entry
	if c.Backend.Enabled && be != nil {
		entries = append(entries, Entry{Channel: Toast{Backend: be}, Events: AllEvents})
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
	return New(Options{Channels: entries, Secrets: []string{s.NtfyToken, s.DiscordWebhook}})
}

func toEvents(names []string) []Event {
	out := make([]Event, len(names))
	for i, n := range names {
		out[i] = Event(n)
	}
	return out
}
