// Package config loads and validates igris.toml (SPEC §12).
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ErrNotFound is returned (wrapped) by Load when the file does not exist.
var ErrNotFound = errors.New("config file not found")

// Duration is a time.Duration written in Go syntax ("30s", "15m") in TOML.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("invalid duration %q (use Go syntax such as \"30s\" or \"15m\")", b)
	}
	*d = Duration(v)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Config is the parsed igris.toml. Secret fields hold the text as written
// (possibly an "env:VAR" reference); use Resolve to obtain the real values.
type Config struct {
	Plan            string            `toml:"plan"`
	Backend         string            `toml:"backend"`
	DefaultMode     string            `toml:"default_mode"`
	NeedsInputAfter Duration          `toml:"needs_input_after"`
	PollInterval    Duration          `toml:"poll_interval"`
	Models          map[string]string `toml:"models"`
	Columns         map[string]string `toml:"columns"`
	Claude          Claude            `toml:"claude"`
	Run             Run               `toml:"run"`
	Adapt           Adapt             `toml:"adapt"`
	TUI             TUI               `toml:"tui"`
	Notify          Notify            `toml:"notify"`
}

// Claude configures how Claude Code is launched.
type Claude struct {
	Command   string   `toml:"command"`
	ExtraArgs []string `toml:"extra_args"`
}

// Run configures verification and commits.
type Run struct {
	Verify            string   `toml:"verify"`
	VerifyTimeout     Duration `toml:"verify_timeout"`
	VerifyMaxAttempts int      `toml:"verify_max_attempts"`
	Commit            string   `toml:"commit"`
	CommitMessage     string   `toml:"commit_message"`
	PromptTemplate    string   `toml:"prompt_template"`
}

// Adapt configures `igris adapt`.
type Adapt struct {
	Model string `toml:"model"`
}

// TUI configures the terminal UI (SPEC §15).
type TUI struct {
	// Mouse turns on click/tap and wheel support (SPEC §15.5). Off keeps the
	// terminal's own text selection without shift+drag.
	Mouse bool `toml:"mouse"`
	// Theme picks the colors for a dark or a light terminal; "auto" goes by
	// the terminal's background (SPEC §15.4).
	Theme string `toml:"theme"`
	// RankColors maps a rank to its color in the TUI: "#rrggbb" or an ANSI
	// color number 0-255. Ranks not listed keep the built-in colors.
	RankColors map[string]string `toml:"rank_colors"`
}

// Notify groups the notification channels.
type Notify struct {
	Backend NotifyBackend `toml:"backend"`
	Ntfy    Ntfy          `toml:"ntfy"`
	Discord Discord       `toml:"discord"`
}

// NotifyBackend is the backend toast channel.
type NotifyBackend struct {
	Enabled bool `toml:"enabled"`
}

// Ntfy is the ntfy channel.
type Ntfy struct {
	Server string   `toml:"server"`
	Topic  string   `toml:"topic"`
	Token  string   `toml:"token"`
	Events []string `toml:"events"`
}

// Discord is the Discord webhook channel.
type Discord struct {
	WebhookURL string   `toml:"webhook_url"`
	Events     []string `toml:"events"`
}

// Secrets are the resolved values of the env:VAR references. They are never
// part of Config, so they cannot leak through the config hash or snapshot.
type Secrets struct {
	NtfyToken      string
	DiscordWebhook string
}

// String redacts every secret.
func (Secrets) String() string { return "config.Secrets{redacted}" }

// GoString redacts every secret.
func (Secrets) GoString() string { return "config.Secrets{redacted}" }

var (
	validModes   = []string{"default", "accept", "auto", "plan", "yolo"}
	validCommit  = []string{"ask", "auto", "never"}
	validAdapt   = []string{"sonnet", "opus"}
	validThemes  = []string{"auto", "dark", "light"}
	validEvents  = []string{"needs_input", "session_lost", "verify_failed_limit", "task_done", "phase_done", "phase_stuck", "run_error"}
	forbiddenArg = []string{
		"--model", "--fallback-model", "--permission-mode",
		"--dangerously-skip-permissions", "--allow-dangerously-skip-permissions",
		"--session-id", "--resume", "--continue",
		"--append-system-prompt", "--append-system-prompt-file",
		"--settings",
	}
)

// ForbiddenExtraArg reports whether arg (a flag, optionally as --flag=value)
// is one claude.extra_args must not contain (SPEC §7.4): igris sets it
// itself, or it could change the model or the permission mode.
func ForbiddenExtraArg(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	if contains(forbiddenArg, name) {
		return true
	}
	// Short flags, alone or clustered ("-c", "-rd"): -c is --continue and -r
	// is --resume.
	if len(name) > 1 && name[0] == '-' && name[1] != '-' {
		return strings.ContainsAny(name[1:], "cr")
	}
	return false
}

func defaultEvents() []string {
	return []string{"needs_input", "session_lost", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"}
}

// Default returns the configuration used when igris.toml is empty or absent.
func Default() *Config {
	return &Config{
		Plan:            "tasks.md",
		Backend:         "herdr",
		DefaultMode:     "default",
		NeedsInputAfter: Duration(30 * time.Second),
		PollInterval:    Duration(2 * time.Second),
		Models:          map[string]string{"sonnet": "sonnet", "opus": "opus", "fable": "fable", "haiku": "haiku"},
		Columns:         map[string]string{},
		Claude:          Claude{Command: "claude", ExtraArgs: []string{}},
		Run: Run{
			VerifyTimeout:     Duration(15 * time.Minute),
			VerifyMaxAttempts: 3,
			Commit:            "ask",
			CommitMessage:     "{{.ID}}: {{.Title}}",
		},
		Adapt: Adapt{Model: "sonnet"},
		TUI:   TUI{Mouse: true, Theme: "auto", RankColors: map[string]string{}},
		Notify: Notify{
			Backend: NotifyBackend{Enabled: true},
			Ntfy:    Ntfy{Server: "https://ntfy.sh", Events: defaultEvents()},
			Discord: Discord{Events: defaultEvents()},
		},
	}
}

// Load reads and validates the config file at path. A missing file wraps
// ErrNotFound.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the owner's own config file
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: %w (run `igris init` to create it)", path, ErrNotFound)
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data, path)
}

// Parse decodes data over the defaults, rejects unknown keys and validates.
// name is only used in error messages.
func Parse(data []byte, name string) (*Config, error) {
	cfg := Default()
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w; fix that line and run the command again", name, err)
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		keys := make([]string, len(undec))
		for i, k := range undec {
			keys[i] = fmt.Sprintf("%q", k.String())
		}
		return nil, fmt.Errorf("%s: unknown key(s) %s; check spelling against SPEC §12", name, strings.Join(keys, ", "))
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return cfg, nil
}

// Validate checks every value and reports all problems at once.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	oneOf := func(key, val string, allowed []string) {
		if !contains(allowed, val) {
			add("%s = %q is invalid; use one of: %s", key, val, strings.Join(allowed, ", "))
		}
	}
	positive := func(key string, d Duration) {
		if d <= 0 {
			add("%s must be greater than zero (e.g. \"30s\")", key)
		}
	}

	if strings.TrimSpace(c.Plan) == "" {
		add("plan must not be empty; set it to the path of your plan file")
	}
	if strings.TrimSpace(c.Backend) == "" {
		add("backend must not be empty; use \"herdr\"")
	}
	oneOf("default_mode", c.DefaultMode, validModes)
	positive("needs_input_after", c.NeedsInputAfter)
	positive("poll_interval", c.PollInterval)
	positive("run.verify_timeout", c.Run.VerifyTimeout)
	if c.Run.VerifyMaxAttempts < 1 {
		add("run.verify_max_attempts must be at least 1")
	}
	oneOf("run.commit", c.Run.Commit, validCommit)
	oneOf("adapt.model", c.Adapt.Model, validAdapt)
	oneOf("tui.theme", c.TUI.Theme, validThemes)
	for _, rank := range sortedKeys(c.TUI.RankColors) {
		if v := c.TUI.RankColors[rank]; !validColor(v) {
			add("tui.rank_colors.%s = %q is not a color; use \"#rrggbb\" or an ANSI color number \"0\" to \"255\"", rank, v)
		}
	}
	if strings.TrimSpace(c.Claude.Command) == "" {
		add("claude.command must not be empty; use \"claude\"")
	}

	for _, alias := range sortedKeys(c.Models) {
		if strings.TrimSpace(c.Models[alias]) == "" {
			add("models.%s must not be empty; give the claude --model value for this rank", alias)
		}
	}

	for _, arg := range c.Claude.ExtraArgs {
		if ForbiddenExtraArg(arg) {
			add("claude.extra_args contains %q, which igris sets itself or which could change the model or permission mode (SPEC §7.4); remove it", arg)
		}
	}

	for _, ch := range []struct {
		key    string
		events []string
	}{{"notify.ntfy.events", c.Notify.Ntfy.Events}, {"notify.discord.events", c.Notify.Discord.Events}} {
		for _, ev := range ch.events {
			if !contains(validEvents, ev) {
				add("%s contains unknown event %q; use any of: %s", ch.key, ev, strings.Join(validEvents, ", "))
			}
		}
	}

	return errors.Join(errs...)
}

// Resolve expands env:VAR references in the secret fields. A reference to an
// unset or empty variable is an error; other values pass through as written.
func (c *Config) Resolve(getenv func(string) string) (Secrets, error) {
	var s Secrets
	var errs []error
	resolve := func(key, val string) string {
		name, ok := strings.CutPrefix(val, "env:")
		if !ok {
			return val
		}
		v := getenv(name)
		if v == "" {
			errs = append(errs, fmt.Errorf("%s refers to environment variable %s, which is not set; export it or put the value in igris.toml", key, name))
		}
		return v
	}
	s.NtfyToken = resolve("notify.ntfy.token", c.Notify.Ntfy.Token)
	s.DiscordWebhook = resolve("notify.discord.webhook_url", c.Notify.Discord.WebhookURL)
	return s, errors.Join(errs...)
}

// Write stores c as TOML at path (0600). Loading the file gives a config
// with the same Hash.
func Write(path string, c *Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Hash returns a stable digest of the configuration as written (env:
// references unresolved), recorded in the run snapshot (SPEC §13).
func (c *Config) Hash() string {
	b, err := json.Marshal(c) // map keys are sorted, so the encoding is canonical
	if err != nil {
		// Config contains only strings, numbers, bools, slices and maps.
		panic(fmt.Sprintf("config: hash: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// validColor reports whether s is "#rrggbb" or an ANSI color number.
func validColor(s string) bool {
	if hex, ok := strings.CutPrefix(s, "#"); ok {
		if len(hex) != 6 {
			return false
		}
		_, err := strconv.ParseUint(hex, 16, 32)
		return err == nil
	}
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n <= 255
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
