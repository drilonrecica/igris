package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

[tui.rank_colors]
opus = "#B48CFF"
fable = "220"

[notify.backend]
enabled = false

[notify.ntfy]
server = "https://ntfy.example"
topic = "igris"
token = "env:NTFY_TOKEN"
events = ["needs_input", "task_done"]

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
		got.Models["opus"] != "opus" || !got.TUI.Mouse || got.TUI.Theme != "auto" || len(got.TUI.RankColors) != 0 || !got.Notify.Backend.Enabled || got.Notify.Ntfy.Server != "https://ntfy.sh" {
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
	if c.TUI.Mouse {
		t.Errorf("tui.mouse = true, want false")
	}
	if c.TUI.Theme != "light" || c.TUI.RankColors["opus"] != "#B48CFF" || c.TUI.RankColors["fable"] != "220" {
		t.Errorf("tui: %+v", c.TUI)
	}
	if c.Adapt.Model != "opus" || c.Notify.Backend.Enabled {
		t.Errorf("adapt/notify.backend: %+v %+v", c.Adapt, c.Notify.Backend)
	}
	if got := strings.Join(c.Notify.Ntfy.Events, ","); got != "needs_input,task_done" {
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

func TestSecretsAreRedacted(t *testing.T) {
	s := Secrets{NtfyToken: "supersecret", DiscordWebhook: "https://hook/secret"}
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
	if got := c.Rules().Verify; strings.Join(got, ",") != "default,fast" {
		t.Errorf("Rules().Verify = %q", got)
	}
	if got := Default().Rules().Verify; len(got) != 0 {
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
	for _, src := range []string{"", "plan = \"x.md\"\n[run]\nverify = \"make test\"\ncommit = \"never\"\n[columns]\n\"Depends on\" = \"Deps\"\n[notify.ntfy]\ntoken = \"env:T\"\n[tui]\ntheme = \"dark\"\n[tui.rank_colors]\nopus = \"5\"\n", "[verify]\nfast = \"go test ./...\"\n[phases.M1]\nverify = \"fast\"\n[phases.\"V1.2\"]\nverify = \"none\"\n"} {
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
		{"[claude]\ncommand = \"/opt/claude\"\n", `claude.command = "/opt/claude" is ignored: igris always starts claude from PATH`},
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
