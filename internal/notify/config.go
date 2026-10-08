package notify

import (
	"text/template"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/config"
)

// FromConfig builds the router for the [notify] settings. A channel is left
// out when it is off or not set up: the backend toast when disabled, ntfy
// without a topic, Discord, Slack and the webhook without a URL, Gotify
// without a server and a token, and any channel whose events list is
// empty. Quiet hours, break_through, task_done_digest and the channel
// templates come from c (pass Immediate to turn the holding off). secrets holds the resolved env: references; every value is
// scrubbed from the errors the router reports. tune adjusts the router's
// Options last (tests use it to avoid real sleeps).
func FromConfig(c config.Notify, s config.Secrets, be backend.Backend, tune ...func(*Options)) *Router {
	var entries []Entry
	if c.Backend.Enabled && be != nil {
		// The toast has no events setting: it gets the default events, the
		// ones that need the owner or end a phase (SPEC §10), not task_done.
		// Quiet hours never hold it either.
		entries = append(entries, Entry{Channel: Toast{Backend: be}, Events: DefaultEvents, NeverHeld: true})
	}
	if c.Ntfy.Topic != "" && len(c.Ntfy.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Ntfy{Server: c.Ntfy.Server, Topic: c.Ntfy.Topic, Token: s.NtfyToken, Template: tmpl("ntfy", c.Ntfy.Template)},
			Events:  toEvents(c.Ntfy.Events),
		})
	}
	if s.DiscordWebhook != "" && len(c.Discord.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Discord{WebhookURL: s.DiscordWebhook, Template: tmpl("discord", c.Discord.Template)},
			Events:  toEvents(c.Discord.Events),
		})
	}
	if s.WebhookURL != "" && len(c.Webhook.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Webhook{URL: s.WebhookURL, Secret: s.WebhookSecret, Template: tmpl("webhook", c.Webhook.Template)},
			Events:  toEvents(c.Webhook.Events),
		})
	}
	if s.SlackWebhook != "" && len(c.Slack.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Slack{WebhookURL: s.SlackWebhook, Template: tmpl("slack", c.Slack.Template)},
			Events:  toEvents(c.Slack.Events),
		})
	}
	if c.Gotify.Server != "" && s.GotifyToken != "" && len(c.Gotify.Events) > 0 {
		entries = append(entries, Entry{
			Channel: &Gotify{Server: c.Gotify.Server, Token: s.GotifyToken, Template: tmpl("gotify", c.Gotify.Template)},
			Events:  toEvents(c.Gotify.Events),
		})
	}
	o := Options{
		Channels:       entries,
		Secrets:        []string{s.NtfyToken, s.DiscordWebhook, s.WebhookURL, s.WebhookSecret, s.SlackWebhook, s.GotifyToken},
		BreakThrough:   toEvents(c.BreakThroughEvents()),
		TaskDoneDigest: c.TaskDoneDigest,
	}
	if w, ok := c.QuietHours(); ok {
		o.Quiet = &w
	}
	for _, f := range tune {
		f(&o)
	}
	return New(o)
}

// tmpl is a channel's parsed template; nil when it has none. The config
// was validated when it loaded, so a template that fails here is left out
// (the channel sends its default text) rather than failing the run.
func tmpl(channel, text string) *template.Template {
	if text == "" {
		return nil
	}
	t, err := config.ParseMessageTemplate("notify."+channel+".template", text)
	if err != nil {
		return nil
	}
	return t
}

func toEvents(names []string) []Event {
	out := make([]Event, len(names))
	for i, n := range names {
		out[i] = Event(n)
	}
	return out
}
