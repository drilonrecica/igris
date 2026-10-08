package report

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// SettingsSection is one TOML table of the effective config, as home's
// Settings page shows it (SPEC §15.6).
type SettingsSection struct {
	Name string // the table, "run" or "notify.ntfy"; "" for the top-level keys
	Rows []Setting
}

// Setting is one key of the effective config.
type Setting struct {
	Key string // as TOML writes it: quoted when it isn't a bare key
	// Value is TOML-shaped and cleaned; for a secret it says only whether
	// it is set and where from, never the value (SPEC §16).
	Value string
	// Default says igris.toml doesn't set the key, so the value is the
	// default.
	Default bool
	Secret  bool
}

// NewSettings lays out cfg, the effective config, in igris.toml's order
// (SPEC §12). keys are the keys the file sets (nil when there is no file or
// it is invalid: everything is then a default); getenv tells whether the
// variable an env: reference names is set.
func NewSettings(cfg *config.Config, keys config.Keys, getenv func(string) string) []SettingsSection {
	var out []SettingsSection
	sec := func(name string) *SettingsSection {
		out = append(out, SettingsSection{Name: name})
		return &out[len(out)-1]
	}
	// set adds key to s with v as its value; the section's name gives the
	// key's parts.
	set := func(s *SettingsSection, key, v string) {
		parts := append(splitSection(s.Name), key)
		s.Rows = append(s.Rows, Setting{Key: tomlKey(key), Value: v, Default: !keys.Set(parts...)})
	}
	secret := func(s *SettingsSection, key, v string) {
		set(s, key, secretValue(v, getenv))
		s.Rows[len(s.Rows)-1].Secret = true
	}
	table := func(name string, m map[string]string) {
		s := sec(name)
		for _, k := range sortedKeys(m) {
			set(s, k, tomlString(m[k]))
		}
	}

	top := sec("")
	set(top, "plan", tomlString(cfg.Plan))
	set(top, "backend", tomlString(cfg.Backend))
	set(top, "default_mode", tomlString(cfg.DefaultMode))
	set(top, "needs_input_after", tomlString(cfg.NeedsInputAfter.Std().String()))
	set(top, "poll_interval", tomlString(cfg.PollInterval.Std().String()))
	table("models", cfg.Models)
	table("columns", cfg.Columns)

	cl := sec("claude")
	set(cl, "command", tomlString(cfg.Claude.Command))
	set(cl, "extra_args", tomlArray(cfg.Claude.ExtraArgs))

	run := sec("run")
	set(run, "verify", tomlString(cfg.Run.Verify))
	set(run, "verify_timeout", tomlString(cfg.Run.VerifyTimeout.Std().String()))
	set(run, "verify_max_attempts", strconv.Itoa(cfg.Run.VerifyMaxAttempts))
	set(run, "commit", tomlString(cfg.Run.Commit))
	set(run, "commit_message", tomlString(cfg.Run.CommitMessage))
	set(run, "prompt_template", tomlString(cfg.Run.PromptTemplate))

	table("verify", cfg.Verify)
	for _, id := range sortedKeys(cfg.Phases) {
		// A phase ID may hold a dot, so the key's parts are given here
		// rather than split from the section's name.
		ph := sec("phases." + tomlKey(id))
		ph.Rows = append(ph.Rows, Setting{Key: "verify", Value: tomlString(cfg.Phases[id].Verify), Default: !keys.Set("phases", id, "verify")})
	}

	if !cfg.Hooks.IsZero() {
		// Shown only when set, like [verify] profiles: most configs have none.
		hk := sec("hooks")
		set(hk, "before_task", tomlArray(cfg.Hooks.BeforeTask))
		set(hk, "after_task", tomlArray(cfg.Hooks.AfterTask))
		set(hk, "timeout", tomlString(cfg.Hooks.TimeoutOrDefault().String()))
	}

	set(sec("adapt"), "model", tomlString(cfg.Adapt.Model))

	tui := sec("tui")
	set(tui, "mouse", strconv.FormatBool(cfg.TUI.Mouse))
	set(tui, "theme", tomlString(cfg.TUI.Theme))
	set(tui, "tail", strconv.FormatBool(cfg.TUI.Tail))
	table("tui.rank_colors", cfg.TUI.RankColors)

	no := sec("notify")
	set(no, "quiet", tomlString(cfg.Notify.Quiet))
	set(no, "break_through", tomlArray(cfg.Notify.BreakThroughEvents()))
	set(no, "task_done_digest", cfg.Notify.TaskDoneDigest.String())

	set(sec("notify.backend"), "enabled", strconv.FormatBool(cfg.Notify.Backend.Enabled))
	// A channel's template is shown only when set: most have none.
	template := func(s *SettingsSection, text string) {
		if text != "" {
			set(s, "template", tomlString(text))
		}
	}

	nt := sec("notify.ntfy")
	set(nt, "server", tomlString(redactURL(cfg.Notify.Ntfy.Server)))
	set(nt, "topic", tomlString(cfg.Notify.Ntfy.Topic))
	secret(nt, "token", cfg.Notify.Ntfy.Token)
	set(nt, "events", tomlArray(cfg.Notify.Ntfy.Events))
	template(nt, cfg.Notify.Ntfy.Template)

	dc := sec("notify.discord")
	secret(dc, "webhook_url", cfg.Notify.Discord.WebhookURL)
	set(dc, "events", tomlArray(cfg.Notify.Discord.Events))
	template(dc, cfg.Notify.Discord.Template)

	// The v0.5 channels are shown only when set up, like [hooks]: most
	// configs have none of them.
	if !cfg.Notify.Webhook.IsZero() {
		wh := sec("notify.webhook")
		secret(wh, "url", cfg.Notify.Webhook.URL)
		secret(wh, "secret", cfg.Notify.Webhook.Secret)
		set(wh, "events", tomlArray(cfg.Notify.Webhook.Events))
		template(wh, cfg.Notify.Webhook.Template)
	}
	if !cfg.Notify.Slack.IsZero() {
		sl := sec("notify.slack")
		secret(sl, "webhook_url", cfg.Notify.Slack.WebhookURL)
		set(sl, "events", tomlArray(cfg.Notify.Slack.Events))
		template(sl, cfg.Notify.Slack.Template)
	}
	if !cfg.Notify.Gotify.IsZero() {
		gt := sec("notify.gotify")
		set(gt, "server", tomlString(redactURL(cfg.Notify.Gotify.Server)))
		secret(gt, "token", cfg.Notify.Gotify.Token)
		set(gt, "events", tomlArray(cfg.Notify.Gotify.Events))
		template(gt, cfg.Notify.Gotify.Template)
	}
	return out
}

// secretValue says whether a secret is set and where from, never what it
// is: "set (env:NTFY_TOKEN)", "set (hidden)" for a value written in
// igris.toml, "not set".
func secretValue(v string, getenv func(string) string) string {
	name, isEnv := strings.CutPrefix(v, "env:")
	switch {
	case v == "":
		return "not set"
	case !isEnv:
		return "set (hidden)"
	case getenv != nil && getenv(name) != "":
		return "set (env:" + textsafe.Line(name) + ")"
	}
	return "env:" + textsafe.Line(name) + " — not set in the environment"
}

// redactURL hides a password written into a URL (https://user:pass@host).
func redactURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.User != nil {
		return u.Redacted()
	}
	return s
}

// tomlString is s as a TOML string, cleaned for the terminal.
func tomlString(s string) string { return textsafe.Line(strconv.Quote(s)) }

// tomlArray is a TOML array of strings on one line.
func tomlArray(list []string) string {
	q := make([]string, len(list))
	for i, s := range list {
		q[i] = tomlString(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// tomlKey is key as TOML writes it: bare when it can be, else quoted.
func tomlKey(key string) string {
	if key != "" && strings.Trim(key, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == "" {
		return key
	}
	return tomlString(key)
}

// splitSection is a section's key parts; none for the top level.
func splitSection(name string) []string {
	if name == "" {
		return nil
	}
	return strings.Split(name, ".")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
