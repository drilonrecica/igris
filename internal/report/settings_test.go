package report

import (
	"os"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/config"
)

// find is the row for key in section sec.
func find(t *testing.T, secs []SettingsSection, sec, key string) Setting {
	t.Helper()
	for _, s := range secs {
		if s.Name != sec {
			continue
		}
		for _, r := range s.Rows {
			if r.Key == key {
				return r
			}
		}
	}
	t.Fatalf("no %s in [%s]", key, sec)
	return Setting{}
}

func TestSettingsHideSecrets(t *testing.T) {
	env := map[string]string{"NTFY_TOKEN": "tk_live_value", "HOOK": ""} //nolint:gosec // a made-up secret the page must hide
	getenv := func(k string) string { return env[k] }
	tests := []struct {
		token, webhook      string
		wantToken, wantHook string
	}{
		{"", "", "not set", "not set"},
		{"env:NTFY_TOKEN", "env:HOOK", "set (env:NTFY_TOKEN)", "env:HOOK — not set in the environment"},
		{"tk_written_in_file", "https://discord.com/api/webhooks/1/abc", "set (hidden)", "set (hidden)"},
		{"env:BAD\x1b[2J", "", "env:BAD — not set in the environment", "not set"},
	}
	for _, tt := range tests {
		cfg := config.Default()
		cfg.Notify.Ntfy.Token, cfg.Notify.Discord.WebhookURL = tt.token, tt.webhook
		cfg.Notify.Ntfy.Server = "https://me:pa55word@ntfy.example"
		secs := NewSettings(cfg, nil, getenv)
		tok, hook := find(t, secs, "notify.ntfy", "token"), find(t, secs, "notify.discord", "webhook_url")
		if tok.Value != tt.wantToken || hook.Value != tt.wantHook || !tok.Secret || !hook.Secret {
			t.Errorf("token %q, webhook %q: got %+v, %+v", tt.token, tt.webhook, tok, hook)
		}
		var all strings.Builder
		for _, s := range secs {
			for _, r := range s.Rows {
				all.WriteString(r.Key + "=" + r.Value + "\n")
			}
		}
		for _, secret := range []string{"tk_live_value", "tk_written_in_file", "webhooks/1/abc", "pa55word", "\x1b"} {
			if strings.Contains(all.String(), secret) {
				t.Errorf("settings show %q:\n%s", secret, all.String())
			}
		}
	}
}

// The v0.5 channels are shown only once set up, their secrets never.
func TestSettingsNewChannels(t *testing.T) {
	has := func(secs []SettingsSection, name string) bool {
		for _, s := range secs {
			if s.Name == name {
				return true
			}
		}
		return false
	}
	cfg := config.Default()
	if secs := NewSettings(cfg, nil, nil); has(secs, "notify.webhook") {
		t.Error("an unset webhook is shown")
	}
	cfg.Notify.Webhook.URL, cfg.Notify.Webhook.Secret = "https://hooks.example/in/TOKEN", "env:WH_SECRET"
	secs := NewSettings(cfg, nil, func(k string) string { return map[string]string{"WH_SECRET": "sig"}[k] })
	if u := find(t, secs, "notify.webhook", "url"); u.Value != "set (hidden)" || !u.Secret {
		t.Errorf("url row %+v", u)
	}
	if s := find(t, secs, "notify.webhook", "secret"); s.Value != "set (env:WH_SECRET)" || !s.Secret {
		t.Errorf("secret row %+v", s)
	}
	if has(secs, "notify.slack") || has(secs, "notify.gotify") {
		t.Error("unset Slack or Gotify is shown")
	}
	cfg.Notify.Slack.WebhookURL = "https://hooks.slack.example/T/B/TOKEN"
	cfg.Notify.Gotify.Server, cfg.Notify.Gotify.Token = "https://me:pa55@gotify.example", "AppTOKEN"
	secs = NewSettings(cfg, nil, nil)
	if r := find(t, secs, "notify.slack", "webhook_url"); r.Value != "set (hidden)" || !r.Secret {
		t.Errorf("slack row %+v", r)
	}
	if r := find(t, secs, "notify.gotify", "token"); r.Value != "set (hidden)" || !r.Secret {
		t.Errorf("gotify token row %+v", r)
	}
	if r := find(t, secs, "notify.gotify", "server"); strings.Contains(r.Value, "pa55") || !strings.Contains(r.Value, "gotify.example") {
		t.Errorf("gotify server row %+v", r)
	}
}

func TestSettingsMarkDefaults(t *testing.T) {
	cfg, keys, err := loadKeys(t, "[run]\nverify = \"make test\"\n[models]\nopus = \"opus\"\nx = \"claude-x\"\n[columns]\n\"Depends on\" = \"Deps\"\n")
	if err != nil {
		t.Fatal(err)
	}
	secs := NewSettings(cfg, keys, nil)
	for _, tt := range []struct {
		sec, key, value string
		def             bool
	}{
		{"", "plan", `"tasks.md"`, true},
		{"run", "verify", `"make test"`, false},
		{"run", "verify_timeout", `"15m0s"`, true},
		{"models", "opus", `"opus"`, false}, // set to the default value: still the owner's
		{"models", "sonnet", `"sonnet"`, true},
		{"models", "x", `"claude-x"`, false},
		{"columns", `"Depends on"`, `"Deps"`, false},
		{"tui", "mouse", "true", true},
		{"notify.ntfy", "events", `["needs_input", "session_lost", "task_overdue", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"]`, true},
	} {
		if r := find(t, secs, tt.sec, tt.key); r.Value != tt.value || r.Default != tt.def {
			t.Errorf("[%s] %s: %+v, want %s default %v", tt.sec, tt.key, r, tt.value, tt.def)
		}
	}
	// Without keys (no igris.toml, or an invalid one) everything is a default.
	for _, s := range NewSettings(config.Default(), nil, nil) {
		for _, r := range s.Rows {
			if !r.Default {
				t.Errorf("[%s] %s not marked default", s.Name, r.Key)
			}
		}
	}
}

// loadKeys writes text as igris.toml and loads it with its keys.
func loadKeys(t *testing.T, text string) (*config.Config, config.Keys, error) {
	t.Helper()
	path := t.TempDir() + "/igris.toml"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.LoadKeys(path)
}

func TestSettingsShowHooksOnlyWhenSet(t *testing.T) {
	for _, s := range NewSettings(config.Default(), nil, nil) {
		if s.Name == "hooks" {
			t.Fatalf("[hooks] shown without hooks: %+v", s)
		}
	}
	cfg, keys, err := loadKeys(t, "[hooks]\nbefore_task = [\"./prep\", \"up\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	secs := NewSettings(cfg, keys, nil)
	if r := find(t, secs, "hooks", "before_task"); r.Value != `["./prep", "up"]` || r.Default {
		t.Errorf("before_task = %+v", r)
	}
	if r := find(t, secs, "hooks", "after_task"); r.Value != "[]" || !r.Default {
		t.Errorf("after_task = %+v", r)
	}
	if r := find(t, secs, "hooks", "timeout"); r.Value != `"2m0s"` || !r.Default {
		t.Errorf("timeout = %+v", r)
	}
}

// [notify] shows quiet hours, break_through and the digest; a channel's
// template shows only when set.
func TestSettingsNotifyHolding(t *testing.T) {
	secs := NewSettings(config.Default(), nil, nil)
	for _, tt := range []struct{ key, want string }{
		{"quiet", `""`},
		{"break_through", `["needs_input", "session_lost", "task_overdue"]`},
		{"task_done_digest", "0"},
	} {
		if r := find(t, secs, "notify", tt.key); r.Value != tt.want || !r.Default {
			t.Errorf("%s row %+v, want %s (default)", tt.key, r, tt.want)
		}
	}
	for _, s := range secs {
		for _, r := range s.Rows {
			if r.Key == "template" {
				t.Errorf("[%s] shows an unset template", s.Name)
			}
		}
	}
	cfg, keys, err := loadKeys(t, "[notify]\nquiet = \"22:00-07:00\"\ntask_done_digest = \"phase\"\nbreak_through = []\n[notify.ntfy]\ntemplate = \"{{.TaskID}}: {{.What}}\"\n")
	if err != nil {
		t.Fatal(err)
	}
	secs = NewSettings(cfg, keys, nil)
	for _, tt := range []struct{ sec, key, want string }{
		{"notify", "quiet", `"22:00-07:00"`},
		{"notify", "break_through", "[]"},
		{"notify", "task_done_digest", `"phase"`},
		{"notify.ntfy", "template", `"{{.TaskID}}: {{.What}}"`},
	} {
		if r := find(t, secs, tt.sec, tt.key); r.Value != tt.want || r.Default {
			t.Errorf("[%s] %s row %+v, want %s (set)", tt.sec, tt.key, r, tt.want)
		}
	}
}
