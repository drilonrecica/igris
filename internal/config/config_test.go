package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const specSample = `
plan = "imp-docs/tasks.md"
backend = "herdr"
default_mode = "accept"
needs_input_after = "45s"
poll_interval = "1s"

[models]
sonnet = "sonnet"
opus = "opus"
fable = "fable"
haiku = "haiku"

[columns]
"Depends on" = "Deps"
"Agent" = "Model"

[claude]
command = "claude"
extra_args = ["--verbose"]

[run]
verify = "make fmt lint test"
verify_timeout = "20m"
verify_max_attempts = 5
commit = "auto"
commit_message = "{{.ID}}: {{.Title}}"
prompt_template = "prompt.tmpl"

[adapt]
model = "opus"

[tui]
mouse = false
theme = "light"
tail = false

[tui.rank_colors]
opus = "#B48CFF"
fable = "220"

[notify.backend]
enabled = false

[notify.ntfy]
server = "https://ntfy.example"
topic = "igris"
token = "env:NTFY_TOKEN"
events = ["needs_input", "task_overdue", "task_done"]

[notify.discord]
webhook_url = "env:IGRIS_DISCORD_WEBHOOK"
events = ["run_error"]
`

func TestParseEmptyGivesDefaults(t *testing.T) {
	got, err := Parse(nil, "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	if got.Hash() != want.Hash() {
		t.Fatalf("empty config differs from defaults:\n got %+v\nwant %+v", got, want)
	}
	if got.Plan != "tasks.md" || got.NeedsInputAfter.Std() != 30*time.Second ||
		got.Run.VerifyTimeout.Std() != 15*time.Minute || got.Run.VerifyMaxAttempts != 3 ||
		got.Models["opus"] != "opus" || !got.TUI.Mouse || got.TUI.Theme != "auto" || !got.TUI.Tail || len(got.TUI.RankColors) != 0 || !got.Notify.Backend.Enabled || got.Notify.Ntfy.Server != "https://ntfy.sh" {
		t.Fatalf("unexpected defaults: %+v", got)
	}
}

func TestParseFullSample(t *testing.T) {
	c, err := Parse([]byte(specSample), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Plan != "imp-docs/tasks.md" || c.DefaultMode != "accept" ||
		c.NeedsInputAfter.Std() != 45*time.Second || c.PollInterval.Std() != time.Second {
		t.Errorf("top-level: %+v", c)
	}
	if c.Columns["Depends on"] != "Deps" || c.Columns["Agent"] != "Model" {
		t.Errorf("columns: %v", c.Columns)
	}
	if c.Run.Verify != "make fmt lint test" || c.Run.VerifyTimeout.Std() != 20*time.Minute ||
		c.Run.VerifyMaxAttempts != 5 || c.Run.Commit != "auto" || c.Run.PromptTemplate != "prompt.tmpl" {
		t.Errorf("run: %+v", c.Run)
	}
	if c.TUI.Mouse || c.TUI.Tail {
		t.Errorf("tui.mouse/tail = %v/%v, want false", c.TUI.Mouse, c.TUI.Tail)
	}
	if c.TUI.Theme != "light" || c.TUI.RankColors["opus"] != "#B48CFF" || c.TUI.RankColors["fable"] != "220" {
		t.Errorf("tui: %+v", c.TUI)
	}
	if c.Adapt.Model != "opus" || c.Notify.Backend.Enabled {
		t.Errorf("adapt/notify.backend: %+v %+v", c.Adapt, c.Notify.Backend)
	}
	if got := strings.Join(c.Notify.Ntfy.Events, ","); got != "needs_input,task_overdue,task_done" {
		t.Errorf("ntfy events = %s", got)
	}
	if got := strings.Join(c.Claude.ExtraArgs, ","); got != "--verbose" {
		t.Errorf("extra_args = %s", got)
	}
}

// An igris.toml saved by a Windows editor (UTF-8 BOM, CRLF) loads like any
// other: the BOM must not swallow the first key.
func TestParseWindowsEditorFile(t *testing.T) {
	c, err := Parse([]byte("\xef\xbb\xbfplan = \"plan.md\"\r\nneeds_input_after = \"20s\"\r\n[run]\r\ncommit = \"never\"\r\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Plan != "plan.md" || c.NeedsInputAfter.Std() != 20*time.Second || c.Run.Commit != "never" {
		t.Errorf("plan = %q, needs_input_after = %v, commit = %q", c.Plan, c.NeedsInputAfter.Std(), c.Run.Commit)
	}
}

func TestParseModelsMergeOverDefaults(t *testing.T) {
	c, err := Parse([]byte("[models]\nopus = \"claude-opus-5-5\"\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Models["opus"] != "claude-opus-5-5" || c.Models["sonnet"] != "sonnet" {
		t.Errorf("models = %v", c.Models)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string // substrings that must all appear
	}{
		{"unknown top-level", `polll_interval = "1s"`, []string{"unknown key", "polll_interval"}},
		{"unknown nested", "[run]\nverfy = \"x\"", []string{"run.verfy"}},
		{"several unknown", "a = 1\n[claude]\nb = 2", []string{`"a"`, `"claude.b"`}},
		{"unknown table", "[nope]\nx = 1", []string{"nope"}},
		{"unknown tui key", "[tui]\nmice = false", []string{"tui.mice"}},
		{"tui mouse not bool", "[tui]\nmouse = \"no\"", []string{"parse igris.toml"}},
		{"bad theme", "[tui]\ntheme = \"neon\"", []string{`tui.theme = "neon"`, "auto, dark, light"}},
		{"bad backend", `backend = "zellij"`, []string{`backend = "zellij"`, "auto, herdr, tmux"}},
		{"rank color by name", "[tui.rank_colors]\nopus = \"purple\"", []string{`tui.rank_colors.opus = "purple"`, "#rrggbb"}},
		{"rank color short hex", "[tui.rank_colors]\nopus = \"#fff\"", []string{"tui.rank_colors.opus"}},
		{"rank color bad hex", "[tui.rank_colors]\nopus = \"#12345g\"", []string{"tui.rank_colors.opus"}},
		{"rank color number too large", "[tui.rank_colors]\nopus = \"256\"", []string{"tui.rank_colors.opus"}},
		{"rank color negative", "[tui.rank_colors]\nopus = \"-1\"", []string{"tui.rank_colors.opus"}},
		{"rank color empty", "[tui.rank_colors]\nopus = \"\"", []string{"tui.rank_colors.opus"}},
		{"rank color not string", "[tui.rank_colors]\nopus = 5", []string{"parse igris.toml"}},
		{"syntax", "plan = ", []string{"parse igris.toml", "fix that line and run the command again"}},
		{"bad duration", `poll_interval = "soon"`, []string{"invalid duration", "soon"}},
		{"duration not string", `poll_interval = 5`, []string{"parse igris.toml"}},
		{"zero duration", `needs_input_after = "0s"`, []string{"needs_input_after must be greater than zero"}},
		{"negative duration", "[run]\nverify_timeout = \"-1m\"", []string{"run.verify_timeout must be greater than zero"}},
		{"bad mode", `default_mode = "bypass"`, []string{`default_mode = "bypass"`, "default, accept, auto, plan, yolo"}},
		{"bad commit", "[run]\ncommit = \"sometimes\"", []string{"run.commit"}},
		{"bad adapt model", "[adapt]\nmodel = \"haiku\"", []string{"adapt.model", "sonnet, opus"}},
		{"zero attempts", "[run]\nverify_max_attempts = 0", []string{"verify_max_attempts"}},
		{"empty plan", `plan = ""`, []string{"plan must not be empty"}},
		{"empty model value", "[models]\nopus = \"\"", []string{"models.opus"}},
		{"bad event", "[notify.ntfy]\nevents = [\"nope\"]", []string{"notify.ntfy.events", `"nope"`}},
		{"profile named none", "[verify]\nnone = \"true\"", []string{"verify.none", "reserved"}},
		{"default twice", "[run]\nverify = \"a\"\n[verify]\ndefault = \"b\"", []string{"run.verify and verify.default are the same profile"}},
		{"bad profile name", "[verify]\nFast = \"true\"", []string{"verify.Fast", "a-z, 0-9"}},
		{"empty profile", "[verify]\nfast = \" \"", []string{"verify.fast must not be empty"}},
		{"profile not a string", "[verify]\nfast = 1", []string{"parse igris.toml"}},
		{"unknown phase profile", "[verify]\nfast = \"true\"\n[phases.M1]\nverify = \"fsat\"", []string{`phases.M1.verify = "fsat" is not a verify profile`, "fast, none"}},
		{"phase default without run.verify", "[phases.M1]\nverify = \"default\"", []string{"phases.M1.verify", "use one of: none"}},
		{"unknown phase key", "[phases.M1]\nmodel = \"opus\"", []string{"phases.M1.model"}},
		{"phase twice", "[phases.m1]\nverify = \"none\"\n[phases.M1]\nverify = \"none\"", []string{"phases.M1 and phases.m1 name the same phase"}},
		{"run key under [verify]", "[verify]\nverify_timeout = \"10m\"", []string{"verify.verify_timeout is a [run] setting, not a verify profile; move it under [run]"}},
		{"run number under [verify]", "[verify]\nfast = \"x\"\nverify_max_attempts = 5", []string{"verify.verify_max_attempts is a [run] setting, not a verify profile; move it under [run]"}},
		{"hook first element empty", "[hooks]\nbefore_task = [\"\", \"x\"]", []string{"hooks.before_task: the first element is the program"}},
		{"hook first element blank", "[hooks]\nafter_task = [\" \"]", []string{"hooks.after_task: the first element"}},
		{"hook control character", "[hooks]\nbefore_task = [\"./x\", \"a\\nb\"]", []string{"hooks.before_task[1] contains a control character"}},
		{"hook NUL", "[hooks]\nafter_task = [\"./x\\u0000\"]", []string{"hooks.after_task[0] contains a control character"}},
		{"hook not a list", "[hooks]\nbefore_task = \"make prep\"", []string{"parse igris.toml"}},
		{"hook zero timeout", "[hooks]\ntimeout = \"0s\"", []string{"hooks.timeout must be greater than zero"}},
		{"hook negative timeout", "[hooks]\ntimeout = \"-1m\"", []string{"hooks.timeout must be greater than zero"}},
		{"unknown hooks key", "[hooks]\nbefore = [\"x\"]", []string{"hooks.before"}},
		{"webhook secret without url", "[notify.webhook]\nsecret = \"s\"", []string{"notify.webhook.secret is set but notify.webhook.url is not"}},
		{"webhook url not http", "[notify.webhook]\nurl = \"ftp://h/x\"", []string{"notify.webhook.url is not an http or https URL"}},
		{"webhook url without host", "[notify.webhook]\nurl = \"https:///x\"", []string{"notify.webhook.url is not an http or https URL"}},
		{"webhook url relative", "[notify.webhook]\nurl = \"hooks/x\"", []string{"notify.webhook.url is not an http or https URL"}},
		{"webhook bad event", "[notify.webhook]\nurl = \"https://h/x\"\nevents = [\"nope\"]", []string{"notify.webhook.events", `"nope"`}},
		{"template unknown field", "[notify.slack]\ntemplate = \"{{.Foo}}\"", []string{"notify.slack.template: template: notify.slack.template:1:2: executing", "can't evaluate field Foo", "use Event, Project, Phase, TaskID, Title, What, RunID or At"}},
		{"template syntax", "[notify.ntfy]\ntemplate = \"{{.Event\"", []string{"notify.ntfy.template: template:", "fix the template"}},
		{"template unknown function", "[notify.discord]\ntemplate = \"{{env .Event}}\"", []string{"notify.discord.template", `function "env" not defined`}},
		{"template bad method call", "[notify.gotify]\ntemplate = \"{{.At.Nope}}\"", []string{"notify.gotify.template", "Nope"}},
		{"template too long", "[notify.webhook]\ntemplate = \"" + strings.Repeat("x", 1001) + "\"", []string{"notify.webhook.template is 1001 characters long; keep it to 1000"}},
		{"template runaway output", "[notify.webhook]\ntemplate = \"{{range 100000}}{{$.Event}}{{end}}\"", []string{"notify.webhook.template", "more than 64 KiB"}},
		{"quiet not a window", "[notify]\nquiet = \"22:00\"", []string{`notify.quiet = "22:00" is invalid`, "HH:MM-HH:MM"}},
		{"quiet bad hour", "[notify]\nquiet = \"24:00-07:00\"", []string{`notify.quiet = "24:00-07:00" is invalid`}},
		{"quiet bad minute", "[notify]\nquiet = \"22:60-07:00\"", []string{"notify.quiet"}},
		{"quiet one digit", "[notify]\nquiet = \"9:00-17:00\"", []string{"notify.quiet"}},
		{"quiet signed", "[notify]\nquiet = \"+1:00-07:00\"", []string{"notify.quiet"}},
		{"quiet empty window", "[notify]\nquiet = \"07:00-07:00\"", []string{"starts and ends at the same time"}},
		{"break_through bad event", "[notify]\nbreak_through = [\"needs_input\", \"nope\"]", []string{"notify.break_through", `"nope"`}},
		{"digest negative", "[notify]\ntask_done_digest = -2", []string{"notify.task_done_digest = -2 is invalid", `"phase"`}},
		{"digest word", "[notify]\ntask_done_digest = \"task\"", []string{`notify.task_done_digest = "task" is invalid`}},
		{"digest float", "[notify]\ntask_done_digest = 2.5", []string{"notify.task_done_digest = 2.5 is invalid"}},
		{"unknown notify key", "[notify]\nquiet_hours = \"22:00-07:00\"", []string{"notify.quiet_hours"}},
		{"slack bad event", "[notify.slack]\nwebhook_url = \"https://h/x\"\nevents = [\"nope\"]", []string{"notify.slack.events", `"nope"`}},
		{"unknown slack key", "[notify.slack]\nurl = \"https://h/x\"", []string{"notify.slack.url"}},
		{"gotify server only", "[notify.gotify]\nserver = \"https://g.example\"", []string{"notify.gotify.server is set but notify.gotify.token is not"}},
		{"gotify token only", "[notify.gotify]\ntoken = \"env:GT\"", []string{"notify.gotify.token is set but notify.gotify.server is not"}},
		{"gotify server not http", "[notify.gotify]\nserver = \"gotify.example\"\ntoken = \"t\"", []string{"notify.gotify.server is not an http or https URL"}},
		{"gotify bad event", "[notify.gotify]\nserver = \"https://g\"\ntoken = \"t\"\nevents = [\"nope\"]", []string{"notify.gotify.events", `"nope"`}},
		{"multiple problems reported together", "default_mode = \"x\"\n[run]\ncommit = \"y\"", []string{"default_mode", "run.commit"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.toml), "igris.toml")
			if err == nil {
				t.Fatal("expected error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q missing %q", err, w)
				}
			}
		})
	}
}

func TestForbiddenExtraArgs(t *testing.T) {
	forbidden := []string{
		"--model", "--fallback-model", "--permission-mode", "--dangerously-skip-permissions",
		"--allow-dangerously-skip-permissions", "--session-id", "--resume", "--continue",
		"--append-system-prompt", "--append-system-prompt-file",
	}
	for _, f := range forbidden {
		for _, arg := range []string{f, f + "=value"} {
			t.Run(arg, func(t *testing.T) {
				_, err := Parse([]byte(`[claude]`+"\n"+`extra_args = ["--verbose", "`+arg+`"]`), "igris.toml")
				if err == nil || !strings.Contains(err.Error(), "claude.extra_args") || !strings.Contains(err.Error(), arg) {
					t.Fatalf("err = %v", err)
				}
			})
		}
	}
	for _, ok := range []string{"--verbose", "--add-dir", "--model-x", "--debug=api"} {
		if _, err := Parse([]byte(`[claude]`+"\n"+`extra_args = ["`+ok+`"]`), "igris.toml"); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
}

func TestResolve(t *testing.T) {
	env := map[string]string{"NTFY_TOKEN": "tok", "IGRIS_DISCORD_WEBHOOK": "https://discord.test/hook"}
	getenv := func(k string) string { return env[k] }

	c, err := Parse([]byte(specSample), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Resolve(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if s.NtfyToken != "tok" || s.DiscordWebhook != "https://discord.test/hook" {
		t.Errorf("secrets = %+v", s)
	}
	if c.Notify.Ntfy.Token != "env:NTFY_TOKEN" {
		t.Errorf("config must keep the reference, got %q", c.Notify.Ntfy.Token)
	}

	// Unset variable is an error naming the variable, not its value.
	_, err = c.Resolve(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "NTFY_TOKEN") || !strings.Contains(err.Error(), "IGRIS_DISCORD_WEBHOOK") {
		t.Errorf("err = %v", err)
	}

	// Literal values and empty secrets pass through.
	lit := Default()
	lit.Notify.Ntfy.Token = "literal"
	s, err = lit.Resolve(getenv)
	if err != nil || s.NtfyToken != "literal" || s.DiscordWebhook != "" {
		t.Errorf("literal: %+v, %v", s, err)
	}
}

// The webhook URL and secret are secrets: errors about them never show the
// value, and an env: URL is checked once it is resolved.
func TestWebhookSecrets(t *testing.T) {
	c, err := Parse([]byte("[notify.webhook]\nurl = \"env:WH_URL\"\nsecret = \"env:WH_SECRET\"\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"WH_URL": "https://hooks.test/in/TOKEN", "WH_SECRET": "SIGSECRET"}
	s, err := c.Resolve(func(k string) string { return env[k] })
	if err != nil || s.WebhookURL != "https://hooks.test/in/TOKEN" || s.WebhookSecret != "SIGSECRET" {
		t.Errorf("resolved %v: url %q secret %q", err, s.WebhookURL, s.WebhookSecret)
	}
	if _, err := c.Resolve(func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "WH_URL") || !strings.Contains(err.Error(), "WH_SECRET") {
		t.Errorf("unset variables: %v", err)
	}
	env["WH_URL"] = "ftp://hooks.test/TOKEN"
	_, err = c.Resolve(func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "notify.webhook.url is not an http or https URL") || strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("bad env URL: %v", err)
	}
	_, err = Parse([]byte("[notify.webhook]\nurl = \"ftp://hooks.test/TOKEN\"\n"), "igris.toml")
	if err == nil || strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("bad literal URL error shows it: %v", err)
	}
	lit, err := Parse([]byte("[notify.webhook]\nurl = \"https://h/x\"\nsecret = \"lit\"\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if s, err := lit.Resolve(func(string) string { return "" }); err != nil || s.WebhookURL != "https://h/x" || s.WebhookSecret != "lit" {
		t.Errorf("literal: %v %q %q", err, s.WebhookURL, s.WebhookSecret)
	}
}

func TestSlackGotifySecrets(t *testing.T) {
	c, err := Parse([]byte("[notify.slack]\nwebhook_url = \"env:SLACK\"\n[notify.gotify]\nserver = \"https://gotify.test\"\ntoken = \"env:GT\"\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SLACK": "https://hooks.slack.test/T/B/x", "GT": "AppTok"}
	s, err := c.Resolve(func(k string) string { return env[k] })
	if err != nil || s.SlackWebhook != "https://hooks.slack.test/T/B/x" || s.GotifyToken != "AppTok" {
		t.Errorf("resolved %v: slack %q gotify %q", err, s.SlackWebhook, s.GotifyToken)
	}
	if _, err := c.Resolve(func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "SLACK") || !strings.Contains(err.Error(), "GT") {
		t.Errorf("unset variables: %v", err)
	}
	if c.Notify.Gotify.Server != "https://gotify.test" || c.Notify.Gotify.Token != "env:GT" {
		t.Errorf("config must keep the reference: %+v", c.Notify.Gotify)
	}
}

// A v0.4 config has none of the v0.5 channels: an unset channel changes
// neither the hash nor the written file.
func TestHashWithoutNewChannelsIsUnchanged(t *testing.T) {
	b, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"Webhook"`, `"Slack"`, `"Gotify"`} {
		if strings.Contains(string(b), key) {
			t.Errorf("config JSON has %s: %s", key, b)
		}
	}
	path := filepath.Join(t.TempDir(), "igris.toml")
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || (strings.Contains(string(data), "webhook]") || strings.Contains(string(data), "slack") || strings.Contains(string(data), "gotify")) { //nolint:gosec // the test's own temp file
		t.Errorf("written default config has a new channel (%v):\n%s", err, data)
	}
	for _, src := range []string{
		"[notify.webhook]\n", "[notify.webhook]\nevents = []\n", "[notify.webhook]\nevents = [\"task_done\"]\n",
		"[notify.slack]\n", "[notify.slack]\nevents = [\"task_done\"]\n", "[notify.gotify]\nevents = []\n",
	} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() != Default().Hash() {
			t.Errorf("%q changed the hash", src)
		}
	}
	for _, src := range []string{
		"[notify.webhook]\nurl = \"https://h/x\"\n", "[notify.webhook]\nurl = \"env:U\"\nsecret = \"env:S\"\n",
		"[notify.slack]\nwebhook_url = \"env:S\"\n", "[notify.gotify]\nserver = \"https://g\"\ntoken = \"env:T\"\n",
	} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() == Default().Hash() {
			t.Errorf("%q left the hash unchanged", src)
		}
	}
}

func TestSecretsAreRedacted(t *testing.T) {
	s := Secrets{NtfyToken: "supersecret", DiscordWebhook: "https://hook/secret", WebhookURL: "https://wh/secret", WebhookSecret: "secretsig", SlackWebhook: "https://slack/secret", GotifyToken: "gotifysecret"}
	for _, out := range []string{
		fmt.Sprint(s), fmt.Sprintf("%v", s), fmt.Sprintf("%+v", s), fmt.Sprintf("%#v", s),
	} {
		if strings.Contains(out, "secret") {
			t.Errorf("leaked: %s", out)
		}
	}
}

func TestHash(t *testing.T) {
	a, _ := Parse([]byte(specSample), "igris.toml")
	b, _ := Parse([]byte(specSample), "igris.toml")
	if a.Hash() != b.Hash() || len(a.Hash()) != 64 {
		t.Fatalf("hash not stable: %s vs %s", a.Hash(), b.Hash())
	}

	mutations := map[string]func(*Config){
		"verify":  func(c *Config) { c.Run.Verify = "true" },
		"model":   func(c *Config) { c.Models["opus"] = "x" },
		"args":    func(c *Config) { c.Claude.ExtraArgs = append(c.Claude.ExtraArgs, "--debug") },
		"timeout": func(c *Config) { c.Run.VerifyTimeout++ },
		"token":   func(c *Config) { c.Notify.Ntfy.Token = "env:OTHER" },
		"column":  func(c *Config) { c.Columns["X"] = "Y" },
		"mouse":   func(c *Config) { c.TUI.Mouse = !c.TUI.Mouse },
		"theme":   func(c *Config) { c.TUI.Theme = "dark" },
		"tail":    func(c *Config) { c.TUI.Tail = !c.TUI.Tail },
		"rank":    func(c *Config) { c.TUI.RankColors["opus"] = "5" },
	}
	for name, mutate := range mutations {
		c, _ := Parse([]byte(specSample), "igris.toml")
		mutate(c)
		if c.Hash() == a.Hash() {
			t.Errorf("hash unchanged after changing %s", name)
		}
	}

	// Resolving secrets must not influence the hash.
	before := a.Hash()
	if _, err := a.Resolve(func(string) string { return "v" }); err != nil {
		t.Fatal(err)
	}
	if a.Hash() != before {
		t.Error("hash changed after Resolve")
	}
}

func TestHashWithoutProfilesIsUnchanged(t *testing.T) {
	// Configs from before [verify] and [phases] keep their hash, so a v0.3
	// run resumes without a config-change prompt.
	b, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"Phases"`, `"Verify":null`, `"Verify":{`} {
		if strings.Contains(string(b), key) {
			t.Errorf("config JSON has %s: %s", key, b)
		}
	}
	c, _ := Parse([]byte("[verify]\nfast = \"true\"\n"), "igris.toml")
	if c.Hash() == Default().Hash() {
		t.Error("hash unchanged by a verify profile")
	}
}

func TestHooks(t *testing.T) {
	c, err := Parse([]byte("[hooks]\nbefore_task = [\"./scripts/dev-db\", \"up\"]\nafter_task = [\"notify-send\", \"done\"]\ntimeout = \"30s\"\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Hooks.BeforeTask, " "); got != "./scripts/dev-db up" {
		t.Errorf("before_task = %q", got)
	}
	if got := strings.Join(c.Hooks.AfterTask, " "); got != "notify-send done" {
		t.Errorf("after_task = %q", got)
	}
	if got := c.Hooks.TimeoutOrDefault(); got != 30*time.Second {
		t.Errorf("timeout = %s", got)
	}
	if got := Default().Hooks.TimeoutOrDefault(); got != 2*time.Minute {
		t.Errorf("default timeout = %s, want 2m", got)
	}
}

func TestHashWithoutHooksIsUnchanged(t *testing.T) {
	// A v0.3 config has no [hooks]; an empty table or empty lists are no
	// hooks either, so none of them may change the hash.
	b, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"Hooks"`) {
		t.Errorf("config JSON has Hooks: %s", b)
	}
	// igris init writes Default(): no [hooks] table in it.
	path := filepath.Join(t.TempDir(), "igris.toml")
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || strings.Contains(string(data), "hooks") { //nolint:gosec // the test's own temp file
		t.Errorf("written default config has hooks (%v):\n%s", err, data)
	}
	for _, src := range []string{"[hooks]\n", "[hooks]\nbefore_task = []\nafter_task = []\n"} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() != Default().Hash() {
			t.Errorf("%q changed the hash", src)
		}
	}
	for _, src := range []string{"[hooks]\nbefore_task = [\"x\"]\n", "[hooks]\nafter_task = [\"x\"]\n", "[hooks]\ntimeout = \"2m\"\n"} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() == Default().Hash() {
			t.Errorf("%q left the hash unchanged", src)
		}
	}
}

func TestVerifyFor(t *testing.T) {
	const toml = "[run]\nverify = \"make all\"\n[verify]\nfast = \"make fast\"\n[phases.M1]\nverify = \"Fast\"\n[phases.m2]\nverify = \"none\"\n"
	c, err := Parse([]byte(toml), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	noDefault, err := Parse([]byte("[verify]\nfast = \"make fast\"\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		cfg           *Config
		cell, phase   string
		profile, want string
	}{
		{"cell wins", c, "default", "M1", "default", "make all"},
		{"phase default, case-insensitive", c, "", "m1", "fast", "make fast"},
		{"phase none", c, "", "M2", "", ""},
		{"cell over phase none", c, "fast", "M2", "fast", "make fast"},
		{"cell none", c, "none", "M0", "", ""},
		{"project default", c, "", "M0", "default", "make all"},
		{"no default: nothing", noDefault, "", "M0", "", ""},
		{"unknown", noDefault, "slow", "M0", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, cmd := tt.cfg.VerifyFor(tt.cell, tt.phase)
			if profile != tt.profile || cmd != tt.want {
				t.Errorf("VerifyFor(%q, %q) = %q, %q; want %q, %q", tt.cell, tt.phase, profile, cmd, tt.profile, tt.want)
			}
		})
	}
	if got := c.Rules("").Verify; strings.Join(got, ",") != "default,fast" {
		t.Errorf("Rules().Verify = %q", got)
	}
	if got := Default().Rules("").Verify; len(got) != 0 {
		t.Errorf("default Rules().Verify = %q", got)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	_, err := Load(filepath.Join(dir, "igris.toml"))
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "igris init") {
		t.Fatalf("missing file: %v", err)
	}

	path := filepath.Join(dir, "igris.toml")
	if err := os.WriteFile(path, []byte(`plan = "p.md"`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || c.Plan != "p.md" {
		t.Fatalf("Load: %v, %+v", err, c)
	}

	if err := os.WriteFile(path, []byte(`bogus = true`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("error should name the file: %v", err)
	}
}

func TestLoadKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "igris.toml")
	text := "plan = \"p.md\"\n[run]\nverify = \"make\"\n[columns]\n\"a.b\" = \"Deps\"\n[notify.ntfy]\ntoken = \"env:T\"\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, keys, err := LoadKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range [][]string{{"plan"}, {"run"}, {"run", "verify"}, {"columns", "a.b"}, {"notify", "ntfy", "token"}} {
		if !keys.Set(k...) {
			t.Errorf("%q not set", k)
		}
	}
	for _, k := range [][]string{{"backend"}, {"run", "commit"}, {"columns", "a", "b"}, {"notify", "discord"}, {"models", "opus"}} {
		if keys.Set(k...) {
			t.Errorf("%q set", k)
		}
	}
	if err := os.WriteFile(path, []byte("bogus = 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, keys, err := LoadKeys(path); err == nil || keys != nil {
		t.Errorf("invalid file: %v, %v", keys, err)
	}
}

func TestForbiddenExtraArg(t *testing.T) {
	for arg, want := range map[string]bool{
		"--model": true, "--model=haiku": true, "--resume": true, "--append-system-prompt-file=x": true,
		"--settings": true, "--settings=/tmp/s.json": true,
		"-c": true, "-r": true, "-rc": true, "-dc": true, "-c=x": true, // short forms of --continue and --resume
		"--verbose": false, "--add-dir": false, "-d": false, "-n": false, "-": false, "--": false, "": false,
	} {
		if got := ForbiddenExtraArg(arg); got != want {
			t.Errorf("ForbiddenExtraArg(%q) = %v, want %v", arg, got, want)
		}
	}
}

func TestWriteRoundTrips(t *testing.T) {
	for _, src := range []string{"", "plan = \"x.md\"\n[run]\nverify = \"make test\"\ncommit = \"never\"\n[columns]\n\"Depends on\" = \"Deps\"\n[notify.ntfy]\ntoken = \"env:T\"\n[tui]\ntheme = \"dark\"\n[tui.rank_colors]\nopus = \"5\"\n", "[verify]\nfast = \"go test ./...\"\n[phases.M1]\nverify = \"fast\"\n[phases.\"V1.2\"]\nverify = \"none\"\n", "[hooks]\nbefore_task = [\"./prep\", \"a b\"]\nafter_task = [\"post\"]\ntimeout = \"45s\"\n", "[notify.webhook]\nurl = \"env:WH\"\nsecret = \"env:WS\"\nevents = [\"task_done\", \"needs_input\"]\n", "[notify.webhook]\nurl = \"https://h/x\"\nevents = []\n", "[notify.slack]\nwebhook_url = \"env:S\"\nevents = [\"task_done\"]\n[notify.gotify]\nserver = \"https://g\"\ntoken = \"env:T\"\n"} {
		want, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "igris.toml")
		if err := Write(path, want); err != nil {
			t.Fatalf("Write: %v", err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load after Write: %v", err)
		}
		if got.Hash() != want.Hash() {
			t.Errorf("round trip of %q changed the config:\n%+v\n%+v", src, got, want)
		}
	}
}

// claude.command is deprecated: any value but "claude" loads with a
// warning, never an error.
func TestWarnings(t *testing.T) {
	tests := []struct {
		toml string
		want string // "" = no warning
	}{
		{"", ""},
		{"[claude]\ncommand = \"claude\"\n", ""},
		{"[claude]\ncommand = \"\"\n", `claude.command = "" is ignored`},
		{"[claude]\ncommand = \"/opt/claude\"\n", `claude.command = "/opt/claude" is ignored: igris always starts claude from PATH; remove it from igris.toml (the key is deprecated and goes away before 1.0)`},
	}
	for _, tt := range tests {
		cfg, err := Parse([]byte(tt.toml), "igris.toml")
		if err != nil {
			t.Fatalf("%q: %v", tt.toml, err)
		}
		got := cfg.Warnings()
		switch {
		case tt.want == "" && len(got) != 0:
			t.Errorf("%q: warnings %q, want none", tt.toml, got)
		case tt.want != "" && (len(got) != 1 || !strings.Contains(got[0], tt.want)):
			t.Errorf("%q: warnings %q, want %q", tt.toml, got, tt.want)
		}
	}
}

// The "use one of" list names only usable profiles, and none once.
func TestPhaseVerifyChoices(t *testing.T) {
	_, err := Parse([]byte("[verify]\nnone = \"x\"\nBad = \"y\"\nempty = \" \"\nfast = \"z\"\n[phases.M1]\nverify = \"q\"\n"), "igris.toml")
	want := `phases.M1.verify = "q" is not a verify profile; define it under [verify] or use one of: fast, none`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("error %v, want %q", err, want)
	}
}

// Write leaves out a channel's events when they are the default list, so later default events reach the config (decision V04-P1).
func TestWriteOmitsDefaultEvents(t *testing.T) {
	custom := Default()
	custom.Notify.Discord.Events = []string{"needs_input"}
	reordered := Default()
	slices.Reverse(reordered.Notify.Ntfy.Events)
	silent := Default()
	silent.Notify.Ntfy.Events = []string{}
	webhook := Default()
	webhook.Notify.Webhook.URL = "env:WH"
	slack := Default()
	slack.Notify.Slack.WebhookURL = "env:S"
	gotify := Default()
	gotify.Notify.Gotify.Server, gotify.Notify.Gotify.Token = "https://g", "env:T"
	gotify.Notify.Gotify.Events = []string{"needs_input"}
	unset := Default()
	unset.Notify.Webhook.Events = []string{"task_done"} // no url: not set up
	for _, tt := range []struct {
		name string
		cfg  *Config
		want int // event lists written
	}{{"defaults", Default(), 0}, {"reordered", reordered, 1}, {"custom", custom, 1}, {"no events", silent, 1}, {"webhook", webhook, 0}, {"slack", slack, 0}, {"gotify", gotify, 1}, {"webhook not set up", unset, 0}} {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.cfg.Hash()
			path := filepath.Join(t.TempDir(), "igris.toml")
			if err := Write(path, tt.cfg); err != nil {
				t.Fatal(err)
			}
			if tt.cfg.Hash() != before {
				t.Errorf("Write changed the caller's config: %+v", tt.cfg.Notify)
			}
			data, err := os.ReadFile(path) //nolint:gosec // a temp file
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(data), "events"); got != tt.want {
				t.Errorf("%d events lines, want %d:\n%s", got, tt.want, data)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Hash() != tt.cfg.Hash() {
				t.Errorf("round trip changed the config")
			}
		})
	}
}

func TestNotifyHoldSettings(t *testing.T) {
	c, err := Parse([]byte("[notify]\nquiet = \"22:00-07:00\"\nbreak_through = []\ntask_done_digest = \"phase\"\n[notify.slack]\nwebhook_url = \"env:S\"\ntemplate = \"{{.TaskID}} {{.At.Format \\\"15:04\\\"}}\"\n"), "igris.toml")
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := c.Notify.QuietHours(); !ok || w != (QuietWindow{Start: 22 * 60, End: 7 * 60}) || w.String() != "22:00–07:00" {
		t.Errorf("quiet = %+v, %v", w, ok)
	}
	if got := c.Notify.BreakThroughEvents(); got == nil || len(got) != 0 {
		t.Errorf("break_through = %#v, want an empty list (hold everything)", got)
	}
	if c.Notify.TaskDoneDigest != DigestPhase || !c.Notify.TaskDoneDigest.On() || c.Notify.TaskDoneDigest.Every() != 0 {
		t.Errorf("task_done_digest = %v", c.Notify.TaskDoneDigest)
	}
	for src, want := range map[string]TaskDoneDigest{"0": 0, "1": 0, "2": 2, "25": 25} {
		c, err := Parse([]byte("[notify]\ntask_done_digest = "+src+"\n"), "igris.toml")
		if err != nil || c.Notify.TaskDoneDigest != want || c.Notify.TaskDoneDigest.Every() != int(want) || c.Notify.TaskDoneDigest.On() != (want != 0) {
			t.Errorf("task_done_digest = %s: %v, %v", src, c.Notify.TaskDoneDigest, err)
		}
	}
	if d := Default().Notify; d.Quiet != "" || strings.Join(d.BreakThroughEvents(), ",") != "needs_input,session_lost,task_overdue" || d.TaskDoneDigest != 0 {
		t.Errorf("defaults %+v", d)
	}
	if _, ok := Default().Notify.QuietHours(); ok {
		t.Error("quiet hours on by default")
	}
}

func TestQuietWindowContains(t *testing.T) {
	at := func(hh, mm int) time.Time { return time.Date(2026, 3, 29, hh, mm, 0, 0, time.UTC) }
	tests := []struct {
		quiet string
		in    []time.Time
		out   []time.Time
	}{
		{"22:00-07:00", []time.Time{at(22, 0), at(23, 59), at(0, 0), at(6, 59)}, []time.Time{at(7, 0), at(12, 0), at(21, 59)}},
		{"01:30-02:00", []time.Time{at(1, 30), at(1, 59)}, []time.Time{at(1, 29), at(2, 0), at(23, 0)}},
		{"00:00-23:59", []time.Time{at(0, 0), at(23, 58)}, []time.Time{at(23, 59)}},
	}
	for _, tt := range tests {
		w, ok, err := ParseQuiet(tt.quiet)
		if err != nil || !ok {
			t.Fatalf("%s: %v", tt.quiet, err)
		}
		for _, x := range tt.in {
			if !w.Contains(x) {
				t.Errorf("%s does not contain %s", tt.quiet, x.Format("15:04"))
			}
		}
		for _, x := range tt.out {
			if w.Contains(x) {
				t.Errorf("%s contains %s", tt.quiet, x.Format("15:04"))
			}
		}
	}
	// The wall clock of the time's own location counts.
	w, _, _ := ParseQuiet("22:00-07:00")
	if berlin := time.FixedZone("CET", 3600); !w.Contains(time.Date(2026, 1, 1, 21, 30, 0, 0, time.UTC).In(berlin)) {
		t.Error("22:30 CET is not in 22:00-07:00")
	}
}

// A v0.4 config has no quiet hours, digest or templates: leaving them unset
// or at their defaults changes neither the hash nor the written file.
func TestHashWithoutHoldSettingsIsUnchanged(t *testing.T) {
	b, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"Quiet"`, `"BreakThrough"`, `"TaskDoneDigest"`, `"Template"`} {
		if strings.Contains(string(b), key) {
			t.Errorf("config JSON has %s: %s", key, b)
		}
	}
	path := filepath.Join(t.TempDir(), "igris.toml")
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || strings.Contains(string(data), "quiet") || strings.Contains(string(data), "break_through") || strings.Contains(string(data), "digest") || strings.Contains(string(data), " template =") { //nolint:gosec // the test's own temp file
		t.Errorf("written default config has a v0.5 notify key (%v):\n%s", err, data)
	}
	for _, src := range []string{
		"[notify]\n", "[notify]\nquiet = \"\"\n", "[notify]\ntask_done_digest = 0\n", "[notify]\ntask_done_digest = 1\n",
		"[notify]\nbreak_through = [\"needs_input\", \"session_lost\", \"task_overdue\"]\n",
		"[notify.ntfy]\ntemplate = \"\"\n[notify.discord]\ntemplate = \"\"\n",
		"[notify.webhook]\ntemplate = \"{{.Event}}\"\n", // no url: not set up
	} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() != Default().Hash() {
			t.Errorf("%q changed the hash", src)
		}
	}
	for _, src := range []string{
		"[notify]\nquiet = \"22:00-07:00\"\n", "[notify]\nbreak_through = []\n", "[notify]\nbreak_through = [\"needs_input\"]\n",
		"[notify]\nbreak_through = [\"session_lost\", \"needs_input\", \"task_overdue\"]\n",
		"[notify]\ntask_done_digest = 2\n", "[notify]\ntask_done_digest = \"phase\"\n",
		"[notify.ntfy]\ntemplate = \"{{.Event}}\"\n", "[notify.discord]\ntemplate = \"{{.Event}}\"\n",
		"[notify.slack]\nwebhook_url = \"env:S\"\ntemplate = \"{{.Event}}\"\n",
	} {
		c, err := Parse([]byte(src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		if c.Hash() == Default().Hash() {
			t.Errorf("%q left the hash unchanged", src)
		}
	}
}

func TestWriteRoundTripsHoldSettings(t *testing.T) {
	for _, tt := range []struct{ src, want, absent string }{
		{"[notify]\nquiet = \"22:00-07:00\"\ntask_done_digest = \"phase\"\nbreak_through = []\n", "task_done_digest = \"phase\"", ""},
		{"[notify]\ntask_done_digest = 5\nbreak_through = [\"needs_input\"]\n", "task_done_digest = 5", ""},
		{"[notify]\nbreak_through = [\"needs_input\", \"session_lost\", \"task_overdue\"]\n", "", "break_through"},
		{"[notify.ntfy]\ntopic = \"t\"\ntemplate = \"{{.TaskID}}: {{.What}}\"\n[notify.gotify]\nserver = \"https://g\"\ntoken = \"env:T\"\ntemplate = \"{{.Event}}\"\n", "template = \"{{.TaskID}}: {{.What}}\"", ""},
	} {
		want, err := Parse([]byte(tt.src), "igris.toml")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "igris.toml")
		if err := Write(path, want); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path) //nolint:gosec // a temp file
		if !strings.Contains(string(data), tt.want) || (tt.absent != "" && strings.Contains(string(data), tt.absent)) {
			t.Errorf("written:\n%s\nwant %q, not %q", data, tt.want, tt.absent)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load after Write: %v\n%s", err, data)
		}
		if got.Hash() != want.Hash() {
			t.Errorf("round trip of %q changed the config:\n%s", tt.src, data)
		}
	}
}
