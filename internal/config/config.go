// Package config loads and validates igris.toml (SPEC §12).
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/textsafe"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/drilonrecica/igris/internal/plan"
)

// ErrNotFound is returned (wrapped) by Load when the file does not exist.
var ErrNotFound = errors.New("config file not found")

// ErrRemovedKey is wrapped by the error for a key a release removed, which
// `igris config migrate` deletes (SPEC §12).
var ErrRemovedKey = errors.New("removed")

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

// Short is d as people write it in igris.toml: 30m, 1h, 1h30m, 45s, where
// time.Duration says 30m0s.
func (d Duration) Short() string {
	s := d.Std().String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Config is the parsed igris.toml. Secret fields hold the text as written
// (possibly an "env:VAR" reference); use Resolve to obtain the real values.
type Config struct {
	Plan            string   `toml:"plan"`
	Backend         string   `toml:"backend"`
	DefaultMode     string   `toml:"default_mode"`
	NeedsInputAfter Duration `toml:"needs_input_after"`
	// WaitingLimit and Nudge are nil when not set (WaitingLimitOrDefault,
	// NudgeOn), so a config without them keeps its hash (SPEC §6.3).
	WaitingLimit *Duration         `toml:"waiting_limit,omitempty" json:",omitempty"`
	Nudge        *bool             `toml:"nudge,omitempty" json:",omitempty"`
	PollInterval Duration          `toml:"poll_interval"`
	Models       map[string]string `toml:"models"`
	Columns      map[string]string `toml:"columns"`
	Claude       Claude            `toml:"claude"`
	Run          Run               `toml:"run"`
	// Verify maps verify profile names to shell commands (SPEC §6.4);
	// run.verify is the profile "default". omitempty keeps the hash of a
	// config without profiles what it was before they existed.
	Verify map[string]string `toml:"verify,omitempty" json:",omitempty"`
	// Phases holds per-phase settings by phase ID (case-insensitive).
	Phases map[string]PhaseConfig `toml:"phases,omitempty" json:",omitempty"`
	// Hooks are the task hooks (SPEC §6.7). Left out when empty (IsZero),
	// so a config without them hashes as it did before they existed.
	Hooks Hooks `toml:"hooks,omitempty" json:",omitzero"`
	// PlanMode configures plan approval from igris (SPEC §6.3, §7.1); left
	// out of the hash and the written file while unset (since 1.1).
	PlanMode PlanMode `toml:"plan_mode,omitempty" json:",omitzero"`
	// Usage configures token usage and the rank audit (SPEC §6.9); left
	// out of the hash and the written file while unset (since 1.1).
	Usage Usage `toml:"usage,omitempty" json:",omitzero"`
	// Side configures the side-session dialog's defaults (SPEC §6.10); left
	// out of the hash and the written file while unset (since 1.2).
	Side   Side   `toml:"side,omitempty" json:",omitzero"`
	Adapt  Adapt  `toml:"adapt"`
	TUI    TUI    `toml:"tui"`
	Notify Notify `toml:"notify"`
}

// Claude configures how Claude Code is launched. The backends start claude
// from PATH; the claude.command key that never changed that is gone (v0.7).
type Claude struct {
	ExtraArgs []string `toml:"extra_args"`
}

// MarshalJSON encodes c for the config hash as it was while Claude had a
// Command, always "claude" then: the hash is part of the contract (SPEC
// §13, §19), so every config keeps the hash it had.
func (c Claude) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Command   string
		ExtraArgs []string
	}{"claude", c.ExtraArgs})
}

// Run configures verification and commits.
type Run struct {
	Verify            string   `toml:"verify"`
	VerifyTimeout     Duration `toml:"verify_timeout"`
	VerifyMaxAttempts int      `toml:"verify_max_attempts"`
	Commit            string   `toml:"commit"`
	CommitMessage     string   `toml:"commit_message"`
	PromptTemplate    string   `toml:"prompt_template"`
	// Env is the environment allowlist of verify and task hooks (SPEC
	// §6.4): variable names, or prefixes ending in "*". PATH and HOME are
	// always passed. nil passes igris's whole environment. Either way the
	// secrets' env: variables are never passed. Left out of the config hash
	// while unset.
	Env []string `toml:"env,omitempty" json:",omitempty"`
	// Worktree runs the sessions, verify and commits in a git worktree on
	// a branch of their own (SPEC §6.8); nil is off. Left out of the
	// config hash while unset.
	Worktree *bool `toml:"worktree,omitempty" json:",omitempty"`
}

// WorktreeOn reports whether runs use a worktree (SPEC §6.8); off unless
// worktree = true.
func (r Run) WorktreeOn() bool { return r.Worktree != nil && *r.Worktree }

// PhaseConfig is a [phases.<id>] table.
type PhaseConfig struct {
	// Verify is the verify profile of the phase's tasks, or "none"; "" is
	// not set.
	Verify string `toml:"verify,omitempty" json:",omitempty"`
}

// DefaultWaitingLimit is how long a session may wait on background work
// before it needs the owner, when waiting_limit is not set (SPEC §6.3).
const DefaultWaitingLimit = 30 * time.Minute

// WaitingLimitOrDefault is waiting_limit, or DefaultWaitingLimit when unset.
func (c *Config) WaitingLimitOrDefault() time.Duration {
	if c.WaitingLimit == nil {
		return DefaultWaitingLimit
	}
	return c.WaitingLimit.Std()
}

// NudgeOn reports whether igris reminds an idle agent of `igris done` once
// before it needs the owner (SPEC §6.3); on unless nudge = false.
func (c *Config) NudgeOn() bool { return c.Nudge == nil || *c.Nudge }

// DefaultHookTimeout bounds a task hook when hooks.timeout is not set.
const DefaultHookTimeout = 2 * time.Minute

// Hooks is the [hooks] table: commands igris runs around every agent
// session, as argv lists, never through a shell (SPEC §6.7). An empty list
// means no hook.
type Hooks struct {
	BeforeTask []string `toml:"before_task,omitempty" json:",omitempty"`
	AfterTask  []string `toml:"after_task,omitempty" json:",omitempty"`
	// Timeout is nil when not set: see TimeoutOrDefault.
	Timeout *Duration `toml:"timeout,omitempty" json:",omitempty"`
}

// IsZero reports that nothing in [hooks] is set (an empty list is not a
// setting), so the table stays out of the config hash and the written file.
func (h Hooks) IsZero() bool {
	return len(h.BeforeTask) == 0 && len(h.AfterTask) == 0 && h.Timeout == nil
}

// TimeoutOrDefault is hooks.timeout, or DefaultHookTimeout when unset.
func (h Hooks) TimeoutOrDefault() time.Duration {
	if h.Timeout == nil {
		return DefaultHookTimeout
	}
	return h.Timeout.Std()
}

// Plan approval defaults (SPEC §12, V11-P1).
const (
	DefaultPlanThen       = "auto"
	DefaultApproveTimeout = 30 * time.Minute
	// minApproveTimeout and maxApproveTimeout bound approve_timeout when
	// it isn't "0".
	minApproveTimeout = time.Minute
	maxApproveTimeout = 24 * time.Hour
)

// validPlanThen are the modes a plain plan task may switch to once igris
// approves its plan; never yolo (V11-P1).
var validPlanThen = []string{"default", "accept", "auto"}

// PlanMode is the [plan_mode] table (SPEC §12): plan approval from igris.
// Both keys are nil or empty when not set.
type PlanMode struct {
	// Then is the mode a plain `plan` task switches to when igris approves
	// its plan; "" is DefaultPlanThen.
	Then string `toml:"then,omitempty" json:",omitempty"`
	// ApproveTimeout is how long the plan hook waits for igris; 0 turns
	// approval from igris off. nil is DefaultApproveTimeout.
	ApproveTimeout *Duration `toml:"approve_timeout,omitempty" json:",omitempty"`
}

// ThenOrDefault is plan_mode.then, lower-case, or DefaultPlanThen.
func (p PlanMode) ThenOrDefault() string {
	if t := strings.ToLower(strings.TrimSpace(p.Then)); t != "" {
		return t
	}
	return DefaultPlanThen
}

// ApproveTimeoutOrDefault is plan_mode.approve_timeout, or
// DefaultApproveTimeout when unset; 0 means no approval from igris.
func (p PlanMode) ApproveTimeoutOrDefault() time.Duration {
	if p.ApproveTimeout == nil {
		return DefaultApproveTimeout
	}
	return p.ApproveTimeout.Std()
}

// Usage is the [usage] table (SPEC §12): token usage and the rank audit
// read from the sessions' transcripts (SPEC §6.9).
type Usage struct {
	// Enabled is nil when not set: see On.
	Enabled *bool `toml:"enabled,omitempty" json:",omitempty"`
	// Statusline makes igris's status line the one in task panes, to record
	// the subscription quota (SPEC §6.3). It replaces the owner's own.
	Statusline bool `toml:"statusline,omitempty" json:",omitempty"`
}

// On reports whether igris reads its sessions' transcripts for usage and
// the rank audit; on unless usage.enabled = false.
func (u Usage) On() bool { return u.Enabled == nil || *u.Enabled }

// Side session defaults (SPEC §12, V12-P1).
const (
	DefaultSideModel = "sonnet"
	DefaultSideMode  = "default"
)

// SideModes are the modes a side session may run in: never yolo nor a
// plan:<mode> value (V12-P1).
var SideModes = []string{"default", "accept", "auto", "plan"}

// Side is the [side] table (SPEC §12): what the side-session dialog
// preselects (SPEC §6.10). Both keys are empty when not set.
type Side struct {
	// Model is the rank preselected, a [models] key; "" is
	// DefaultSideModel.
	Model string `toml:"model,omitempty" json:",omitempty"`
	// Mode is the mode preselected, one of SideModes; "" is
	// DefaultSideMode.
	Mode string `toml:"mode,omitempty" json:",omitempty"`
}

// ModelOrDefault is side.model, trimmed, or DefaultSideModel.
func (s Side) ModelOrDefault() string {
	if m := strings.TrimSpace(s.Model); m != "" {
		return m
	}
	return DefaultSideModel
}

// ModeOrDefault is side.mode, lower-case, or DefaultSideMode.
func (s Side) ModeOrDefault() string {
	if m := strings.ToLower(strings.TrimSpace(s.Mode)); m != "" {
		return m
	}
	return DefaultSideMode
}

// VerifyDefault is the profile run.verify defines.
const VerifyDefault = "default"

// profilePattern is what a verify profile name looks like.
var profilePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// VerifyProfiles returns every verify profile: [verify] plus "default" for
// run.verify when it is set.
func (c *Config) VerifyProfiles() map[string]string {
	out := make(map[string]string, len(c.Verify)+1)
	for k, v := range c.Verify {
		out[k] = v
	}
	if c.Run.Verify != "" {
		out[VerifyDefault] = c.Run.Verify
	}
	return out
}

// Rules are the config values a plan is validated against: the model
// ranks and the verify profile names, with root as the directory Context
// paths are checked against (SPEC §3.2).
func (c *Config) Rules(root string) plan.Rules {
	return plan.Rules{Models: c.Models, Verify: sortedKeys(c.VerifyProfiles()), Root: root}
}

// PlanOptions are the options the plan is parsed with: the [columns]
// aliases, and root as the directory included files must stay inside
// (SPEC §3.7), the same root as for Rules.
func (c *Config) PlanOptions(root string) plan.Options {
	return plan.Options{Columns: c.Columns, Root: root}
}

// PhaseVerify returns the [phases.<id>] verify setting of phase id
// (case-insensitive), lower-case; "" if there is none.
func (c *Config) PhaseVerify(id string) string {
	for k, ph := range c.Phases {
		if strings.EqualFold(k, id) {
			return strings.ToLower(strings.TrimSpace(ph.Verify))
		}
	}
	return ""
}

// VerifyChoice is the verify setting that applies to a task, as written:
// its Verify cell, else [phases.<id>] verify, else "default". It may be
// "none" or a profile that isn't defined; VerifyFor resolves it.
func (c *Config) VerifyChoice(cell, phase string) string {
	if cell != "" {
		return cell
	}
	if v := c.PhaseVerify(phase); v != "" {
		return v
	}
	return VerifyDefault
}

// VerifyFor resolves the verify profile of a task (SPEC §6.4) from its
// Verify cell (plan.Task.Verify) and its phase: the cell, then
// [phases.<id>] verify, then "default". "none" at either of the first two
// levels, or nothing set at all, means no verification: profile and
// command are then "".
func (c *Config) VerifyFor(cell, phase string) (profile, command string) {
	profile = c.VerifyChoice(cell, phase)
	if profile == plan.VerifyNone {
		return "", ""
	}
	command, ok := c.VerifyProfiles()[profile]
	if !ok {
		// Unknown here: validation rejects it before a task runs.
		return "", ""
	}
	return profile, command
}

// Adapt configures `igris adapt`.
type Adapt struct {
	Model string `toml:"model"`
}

// TUI configures the terminal UI (SPEC §15).
type TUI struct {
	// Mouse turns on click/tap, wheel and hover support (SPEC §15.5). Off keeps the
	// terminal's own text selection without shift+drag.
	Mouse bool `toml:"mouse"`
	// Theme picks the colors for a dark or a light terminal; "auto" goes by
	// the terminal's background (SPEC §15.4).
	Theme string `toml:"theme"`
	// RankColors maps a rank to its color in the TUI: "#rrggbb" or an ANSI
	// color number 0-255. Ranks not listed keep the built-in colors.
	RankColors map[string]string `toml:"rank_colors"`
	// Tail shows the last lines of the current session in the task card,
	// refreshed every poll_interval (SPEC §15.3). Off makes no tail calls.
	// It is left out of the hash while on (MarshalJSON).
	Tail bool `toml:"tail"`
}

// MarshalJSON encodes t for the config hash with Tail only when it is off:
// on is the default, so a v0.4 config, which has no tail key, keeps the
// hash it had (like the other v0.5 keys while unset).
func (t TUI) MarshalJSON() ([]byte, error) {
	type plain TUI // without this method
	v := struct {
		plain
		Tail *bool `json:",omitempty"`
	}{plain: plain(t)}
	if !t.Tail {
		v.Tail = &t.Tail
	}
	return json.Marshal(v)
}

// Notify groups the notification channels and the settings that hold
// messages back (SPEC §10).
type Notify struct {
	// Quiet is the quiet-hours window, "HH:MM-HH:MM" local time; "" is
	// none. Quiet, BreakThrough and TaskDoneDigest are left out of the hash
	// while unset or default, so a v0.4 config keeps its hash.
	Quiet string `toml:"quiet,omitempty" json:",omitempty"`
	// BreakThrough lists the events never held in quiet hours. Write
	// leaves the default list out, like a channel's events.
	BreakThrough   BreakThrough   `toml:"break_through" json:",omitzero"`
	TaskDoneDigest TaskDoneDigest `toml:"task_done_digest,omitzero" json:",omitzero"`

	Backend NotifyBackend `toml:"backend"`
	Ntfy    Ntfy          `toml:"ntfy"`
	Discord Discord       `toml:"discord"`
	// The channels added in v0.5 are left out of the hash and the written
	// file while they are not set up (IsZero), so a v0.4 config keeps its
	// hash.
	Webhook Webhook `toml:"webhook,omitempty" json:",omitzero"`
	Slack   Slack   `toml:"slack,omitempty" json:",omitzero"`
	Gotify  Gotify  `toml:"gotify,omitempty" json:",omitzero"`
}

// NotifyBackend is the backend toast channel.
type NotifyBackend struct {
	Enabled bool `toml:"enabled"`
}

// Ntfy is the ntfy channel.
type Ntfy struct {
	Server string `toml:"server"`
	Topic  string `toml:"topic"`
	Token  string `toml:"token"`
	// Events is left out of a written file when it is the default list
	// (Write), so default events added later reach the config. An empty
	// list is written: it turns the channel's events off.
	Events []string `toml:"events"`
	// Template replaces the message text (SPEC §10); "" is the default
	// text. Left out of the hash when unset, on every channel.
	Template string `toml:"template,omitempty" json:",omitempty"`
}

// Discord is the Discord webhook channel.
type Discord struct {
	WebhookURL string   `toml:"webhook_url"`
	Events     []string `toml:"events"`                               // see Ntfy.Events
	Template   string   `toml:"template,omitempty" json:",omitempty"` // see Ntfy.Template
}

// Webhook is the generic webhook channel (SPEC §10).
type Webhook struct {
	URL      string   `toml:"url"`
	Secret   string   `toml:"secret"`
	Events   []string `toml:"events"`                               // see Ntfy.Events
	Template string   `toml:"template,omitempty" json:",omitempty"` // see Ntfy.Template
}

// IsZero reports that the channel is not set up: no url and no secret,
// whatever its events (they do nothing without a url).
func (w Webhook) IsZero() bool { return w.URL == "" && w.Secret == "" }

// Slack is the Slack incoming-webhook channel (SPEC §10).
type Slack struct {
	WebhookURL string   `toml:"webhook_url"`
	Events     []string `toml:"events"`                               // see Ntfy.Events
	Template   string   `toml:"template,omitempty" json:",omitempty"` // see Ntfy.Template
}

// IsZero reports that the channel is not set up (no webhook_url).
func (s Slack) IsZero() bool { return s.WebhookURL == "" }

// Gotify is the Gotify channel (SPEC §10).
type Gotify struct {
	Server   string   `toml:"server"`
	Token    string   `toml:"token"`
	Events   []string `toml:"events"`                               // see Ntfy.Events
	Template string   `toml:"template,omitempty" json:",omitempty"` // see Ntfy.Template
}

// IsZero reports that the channel is not set up: no server and no token.
func (g Gotify) IsZero() bool { return g.Server == "" && g.Token == "" }

// Secrets are the resolved values of the env:VAR references. They are never
// part of Config, so they cannot leak through the config hash or snapshot.
type Secrets struct {
	NtfyToken      string
	DiscordWebhook string
	WebhookURL     string
	WebhookSecret  string
	SlackWebhook   string
	GotifyToken    string
}

// String redacts every secret.
func (Secrets) String() string { return "config.Secrets{redacted}" }

// GoString redacts every secret.
func (Secrets) GoString() string { return "config.Secrets{redacted}" }

var (
	validModes   = []string{"default", "accept", "auto", "plan", "plan:default", "plan:accept", "plan:auto", "plan:yolo", "yolo"}
	validCommit  = []string{"ask", "auto", "never"}
	validAdapt   = []string{"sonnet", "opus"}
	validThemes  = []string{"auto", "dark", "light"}
	validEvents  = []string{"needs_input", "session_lost", "task_overdue", "rank_mismatch", "verify_failed_limit", "task_done", "phase_done", "phase_stuck", "run_error"}
	forbiddenArg = []string{
		"--model", "--fallback-model", "--permission-mode",
		"--dangerously-skip-permissions", "--allow-dangerously-skip-permissions",
		"--session-id", "--resume", "--continue",
		"--append-system-prompt", "--append-system-prompt-file",
		"--settings",
		// V10-05: what a task may use and where it runs are igris's to
		// set too: tools and their prompts, directories, system prompts,
		// plugins, MCP servers, agents, the IDE, and the session's shape.
		"--allowedTools", "--allowed-tools", "--disallowedTools", "--disallowed-tools", "--tools",
		"--add-dir", "--permission-prompt-tool", "--bare", "--setting-sources",
		"--plugin-dir", "--plugin-url", "--mcp-config", "--strict-mcp-config",
		"--agent", "--agents", "--system-prompt", "--system-prompt-snapshot", "--ide",
		"--worktree", "--fork-session", "--no-session-persistence",
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
	// Short flags, alone or clustered ("-c", "-rd"): -c is --continue, -r
	// is --resume and -w is --worktree.
	if len(name) > 1 && name[0] == '-' && name[1] != '-' {
		return strings.ContainsAny(name[1:], "crw")
	}
	return false
}

func defaultEvents() []string {
	return []string{"needs_input", "session_lost", "task_overdue", "rank_mismatch", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"}
}

// hashEvents is the default events list as the config hash encodes it: the
// list of igris 1.0. A later default event (rank_mismatch, 1.1) must not
// change the hash of a config that doesn't set events (SPEC §19); a config
// that lists 1.0's defaults itself hashes the same.
func hashEvents() []string {
	return []string{"needs_input", "session_lost", "task_overdue", "phase_done", "phase_stuck", "run_error", "verify_failed_limit"}
}

// BackendAuto picks herdr inside a herdr pane, else tmux inside tmux
// (SPEC §11.3).
const BackendAuto = "auto"

// validBackends are the values of backend.
var validBackends = []string{BackendAuto, "herdr", "tmux"}

// Default returns the configuration used when igris.toml is empty or absent.
func Default() *Config {
	return &Config{
		Plan:            "tasks.md",
		Backend:         BackendAuto,
		DefaultMode:     "auto",
		NeedsInputAfter: Duration(30 * time.Second),
		PollInterval:    Duration(2 * time.Second),
		Models:          map[string]string{"sonnet": "sonnet", "opus": "opus", "fable": "fable", "haiku": "haiku"},
		Columns:         map[string]string{},
		Claude:          Claude{ExtraArgs: []string{}},
		Run: Run{
			VerifyTimeout:     Duration(15 * time.Minute),
			VerifyMaxAttempts: 3,
			Commit:            "ask",
			CommitMessage:     "{{.ID}}: {{.Title}}",
		},
		Adapt: Adapt{Model: "sonnet"},
		TUI:   TUI{Mouse: true, Theme: "auto", RankColors: map[string]string{}, Tail: true},
		Notify: Notify{
			BreakThrough: defaultBreakThrough(),
			Backend:      NotifyBackend{Enabled: true},
			Ntfy:         Ntfy{Server: "https://ntfy.sh", Events: defaultEvents()},
			Discord:      Discord{Events: defaultEvents()},
			Webhook:      Webhook{Events: defaultEvents()},
			Slack:        Slack{Events: defaultEvents()},
			Gotify:       Gotify{Events: defaultEvents()},
		},
	}
}

// Load reads and validates the config file at path. A missing file wraps
// ErrNotFound.
func Load(path string) (*Config, error) {
	cfg, _, err := LoadKeys(path)
	return cfg, err
}

// LoadKeys is Load that also returns the keys the file sets (see Keys), so
// the effective config can tell the owner's values from the defaults.
func LoadKeys(path string) (*Config, Keys, error) {
	// Read as a regular file of bounded size: the file is read every poll
	// and a session can replace it (SPEC §16).
	data, err := fsafe.ReadFile(path, MaxSize, true)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil, fmt.Errorf("%s: %w (run `igris init` to create it)", path, ErrNotFound)
	case errors.Is(err, fsafe.ErrNotRegular), errors.Is(err, fsafe.ErrTooLarge):
		return nil, nil, fmt.Errorf("read %w; put the config file back", err)
	case err != nil:
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parse(data, path)
}

// MaxSize is the largest igris.toml igris reads (SPEC §16).
const MaxSize = 1 << 20

// Parse decodes data over the defaults, rejects unknown keys and validates.
// name is only used in error messages.
func Parse(data []byte, name string) (*Config, error) {
	cfg, _, err := parse(data, name)
	return cfg, err
}

func parse(data []byte, name string) (*Config, Keys, error) {
	if err := misplacedRunKeys(data); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	cfg := Default()
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w; fix that line and run the command again", name, err)
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		for _, k := range undec {
			if d, ok := removed(k); ok {
				return nil, nil, fmt.Errorf("%s: %s was %w in %s (%s); run `igris config migrate` to delete it", name, d.name(), ErrRemovedKey, d.removed, d.why)
			}
		}
		keys := make([]string, len(undec))
		for i, k := range undec {
			keys[i] = fmt.Sprintf("%q", k.String())
		}
		return nil, nil, fmt.Errorf("%s: unknown key(s) %s; check spelling against SPEC §12", name, strings.Join(keys, ", "))
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	}
	keys := Keys{}
	for _, k := range md.Keys() {
		keys[keyName(k...)] = true
	}
	return cfg, keys, nil
}

// runVerifyKeys are the [run] settings that are easy to write under
// [verify] by mistake, where they would read as profiles.
var runVerifyKeys = []string{"verify_timeout", "verify_max_attempts"}

// misplacedRunKeys reports a [run] setting written under [verify]. It
// looks before the typed decode, which would reject a number there with
// a less helpful message; a file that doesn't parse is left to it.
func misplacedRunKeys(data []byte) error {
	var raw struct {
		Verify map[string]any `toml:"verify"`
	}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil
	}
	var errs []error
	for _, k := range runVerifyKeys {
		if _, ok := raw.Verify[k]; ok {
			errs = append(errs, fmt.Errorf("verify.%s is a [run] setting, not a verify profile; move it under [run]", k))
		}
	}
	return errors.Join(errs...)
}

// Keys are the keys an igris.toml sets, tables and values alike, by their
// parts ("run", "verify"); a map such as [models] has one per entry. A
// key missing from it has its default value.
type Keys map[string]bool

// Set says the file sets the key with these parts.
func (k Keys) Set(parts ...string) bool { return k[keyName(parts...)] }

// keyName joins a key's parts with NUL, so a quoted key holding a dot
// ("a.b") doesn't read as two parts.
func keyName(parts ...string) string { return strings.Join(parts, "\x00") }

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
	oneOf("backend", c.Backend, validBackends)
	oneOf("default_mode", c.DefaultMode, validModes)
	positive("needs_input_after", c.NeedsInputAfter)
	if c.WaitingLimit != nil {
		positive("waiting_limit", *c.WaitingLimit)
	}
	positive("poll_interval", c.PollInterval)
	positive("run.verify_timeout", c.Run.VerifyTimeout)
	if c.Run.VerifyMaxAttempts < 1 {
		add("run.verify_max_attempts must be at least 1")
	}
	if c.Run.Env != nil && len(c.Run.Env) == 0 {
		// A written config leaves an empty list out, which would pass
		// everything: the opposite of what it asks for.
		add("run.env is empty; list the variables verify and task hooks need besides PATH and HOME (e.g. [\"PATH\"] for those two only), or remove it to pass igris's environment")
	}
	secrets := c.SecretEnvNames()
	for _, name := range secrets {
		// The commands igris runs never get a secret's variable, and they
		// can't do without these (V06-07).
		if name == "PATH" || name == "HOME" {
			add("a notification secret refers to env:%s; igris keeps a secret's variable from every command it runs, and they need %s: use a variable of its own", name, name)
		}
	}
	for _, name := range c.Run.Env {
		switch {
		case !envName.MatchString(name):
			add("run.env: %q is not a variable name; use names like \"GOPATH\" or a prefix like \"LC_*\"", name)
		case slices.Contains(secrets, name):
			add("run.env lists %s, which a notification secret refers to; igris never passes it to commands, so remove it from run.env", name)
		}
	}
	oneOf("run.commit", c.Run.Commit, validCommit)
	oneOf("adapt.model", c.Adapt.Model, validAdapt)
	oneOf("tui.theme", c.TUI.Theme, validThemes)
	for _, rank := range sortedKeys(c.TUI.RankColors) {
		if v := c.TUI.RankColors[rank]; !validColor(v) {
			add("tui.rank_colors.%s = %q is not a color; use \"#rrggbb\" or an ANSI color number \"0\" to \"255\"", rank, v)
		}
	}
	for _, alias := range sortedKeys(c.Models) {
		// A rank and its model become a claude argument and text on the
		// terminal; TOML accepts "\u001b" (V10-05). The error names the
		// key cleaned, never the sequence.
		switch {
		case strings.ContainsFunc(alias, unicode.IsControl):
			add("models.%s is a rank with a control character; fix the key in igris.toml", textsafe.Line(alias))
		case strings.ContainsFunc(c.Models[alias], unicode.IsControl):
			add("models.%s contains a control character; fix the value in igris.toml", alias)
		case strings.TrimSpace(c.Models[alias]) == "":
			add("models.%s must not be empty; give the claude --model value for this rank", alias)
		}
	}

	c.validateVerify(add)
	c.validateHooks(add)
	if c.PlanMode.Then != "" {
		oneOf("plan_mode.then", strings.ToLower(strings.TrimSpace(c.PlanMode.Then)), validPlanThen)
	}
	if t := c.PlanMode.ApproveTimeout; t != nil && *t != 0 && (t.Std() < minApproveTimeout || t.Std() > maxApproveTimeout) {
		add("plan_mode.approve_timeout = %q is out of range; use 1m to 24h, or \"0\" to approve plans in the pane only", t.Short())
	}

	c.validateSide(add)

	for i, arg := range c.Claude.ExtraArgs {
		switch {
		case ForbiddenExtraArg(arg):
			add("claude.extra_args contains %q, which igris sets itself or which could change the model or permission mode (SPEC §7.4); remove it", arg)
		case strings.ContainsFunc(arg, unicode.IsControl):
			add("claude.extra_args[%d] contains a control character; remove it (SPEC §7.4)", i)
		}
	}

	for _, ch := range []struct {
		key    string
		events []string
	}{
		{"notify.ntfy.events", c.Notify.Ntfy.Events},
		{"notify.discord.events", c.Notify.Discord.Events},
		{"notify.webhook.events", c.Notify.Webhook.Events},
		{"notify.slack.events", c.Notify.Slack.Events},
		{"notify.gotify.events", c.Notify.Gotify.Events},
	} {
		for _, ev := range ch.events {
			if !contains(validEvents, ev) {
				add("%s contains unknown event %q; use any of: %s", ch.key, ev, strings.Join(validEvents, ", "))
			}
		}
	}

	c.validateChannels(add)
	c.validateNotify(add)

	return errors.Join(errs...)
}

// validateChannels checks the v0.5 channels' settings (SPEC §12). A URL
// that is a secret is never quoted in an error.
func (c *Config) validateChannels(add func(string, ...any)) {
	w := c.Notify.Webhook
	if w.Secret != "" && w.URL == "" {
		add("notify.webhook.secret is set but notify.webhook.url is not; set the url or remove the secret")
	}
	if w.URL != "" && !isEnvRef(w.URL) && !httpURL(w.URL) {
		add("%s", badURL("notify.webhook.url"))
	}
	g := c.Notify.Gotify
	switch {
	case g.Server != "" && g.Token == "":
		add("notify.gotify.server is set but notify.gotify.token is not; set the application token (e.g. \"env:GOTIFY_TOKEN\") or remove the server")
	case g.Token != "" && g.Server == "":
		add("notify.gotify.token is set but notify.gotify.server is not; set the server (e.g. \"https://gotify.example.org\") or remove the token")
	}
	if g.Server != "" && !httpURL(g.Server) {
		add("notify.gotify.server is not an http or https URL with a host; use e.g. \"https://gotify.example.org\"")
	}
	// A server is shown on Settings and in doctor and is part of the
	// config hash: a password in it would be too (V10-05).
	for _, srv := range []struct{ key, val string }{{"notify.ntfy.server", c.Notify.Ntfy.Server}, {"notify.gotify.server", g.Server}} {
		if hasUserinfo(srv.val) {
			add("%s has a user or password in it; use token, not user:pass@ in the URL", srv.key)
		}
	}
	for _, h := range c.chatHooks() {
		if h.val != "" && !isEnvRef(h.val) && !httpsURL(h.val) {
			add("%s", badHTTPS(h.key))
		}
	}
}

// chatHooks are the Discord and Slack webhook URLs by config key.
func (c *Config) chatHooks() []struct{ key, val string } {
	return []struct{ key, val string }{
		{"notify.discord.webhook_url", c.Notify.Discord.WebhookURL},
		{"notify.slack.webhook_url", c.Notify.Slack.WebhookURL},
	}
}

// badHTTPS is the error text for a Discord or Slack webhook URL that isn't
// https with a host. Both services only take https, and the URL is the
// secret, so plain http would send it in the clear.
func badHTTPS(key string) string {
	return key + " is not an https URL with a host (the value is a secret, so it isn't shown); copy the webhook URL again into igris.toml or the environment variable"
}

// httpsURL reports whether s is an absolute https URL with a host.
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

// plainHTTP reports whether s is an http:// URL.
func plainHTTP(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "http"
}

// badURL is the error text for a secret URL that isn't http(s) with a host.
func badURL(key string) string {
	return key + " is not an http or https URL with a host (the value is a secret, so it isn't shown); fix it in igris.toml or in the environment variable"
}

// hasUserinfo reports whether s is a URL with a user or password part.
func hasUserinfo(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.User != nil
}

// httpURL reports whether s is an absolute http or https URL with a host.
func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// envName is a run.env entry: a variable name, or a prefix ending in "*".
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\*?$`)

func isEnvRef(s string) bool { return strings.HasPrefix(s, "env:") }

// validateVerify checks [verify] and [phases.<id>] (SPEC §12).
func (c *Config) validateVerify(add func(string, ...any)) {
	for _, name := range sortedKeys(c.Verify) {
		switch {
		case name == plan.VerifyNone:
			add("verify.none: the profile name none is reserved (a Verify cell of none turns verification off); rename the profile")
		case name == VerifyDefault && c.Run.Verify != "":
			add("run.verify and verify.default are the same profile; keep one of them")
		case !profilePattern.MatchString(name):
			add("verify.%s: profile names may contain only a-z, 0-9, '_' and '-'; rename it", name)
		}
		if strings.TrimSpace(c.Verify[name]) == "" {
			add("verify.%s must not be empty; give the shell command of this profile", name)
		}
	}
	profiles := c.VerifyProfiles()
	var names []string // the profiles a phase may name, for the hint
	for _, name := range sortedKeys(profiles) {
		if name != plan.VerifyNone && profilePattern.MatchString(name) && strings.TrimSpace(profiles[name]) != "" {
			names = append(names, name)
		}
	}
	names = append(names, plan.VerifyNone)
	seen := map[string]string{}
	for _, id := range sortedKeys(c.Phases) {
		if first, dup := seen[strings.ToLower(id)]; dup {
			add("phases.%s and phases.%s name the same phase (phase IDs match case-insensitively); keep one of them", first, id)
			continue
		}
		seen[strings.ToLower(id)] = id
		v := strings.ToLower(strings.TrimSpace(c.Phases[id].Verify))
		if _, ok := profiles[v]; v != "" && v != plan.VerifyNone && !ok {
			add("phases.%s.verify = %q is not a verify profile; define it under [verify] or use one of: %s", id, c.Phases[id].Verify, strings.Join(names, ", "))
		}
	}
}

// validateSide checks [side] (SPEC §12): a set model is a [models] rank,
// a set mode never skips permissions.
func (c *Config) validateSide(add func(string, ...any)) {
	if m := strings.TrimSpace(c.Side.Model); m != "" {
		if _, ok := c.Models[m]; !ok {
			add("side.model = %q is not a [models] rank; use one of: %s", textsafe.Line(m), strings.Join(sortedKeys(c.Models), ", "))
		}
	}
	if c.Side.Mode == "" {
		return
	}
	switch mode := c.Side.ModeOrDefault(); {
	case mode == "yolo" || strings.HasPrefix(mode, "plan:"):
		add("side.mode = %q: side sessions never skip permissions; use one of: %s", textsafe.Line(c.Side.Mode), strings.Join(SideModes, ", "))
	case !contains(SideModes, mode):
		add("side.mode = %q is invalid; use one of: %s", textsafe.Line(c.Side.Mode), strings.Join(SideModes, ", "))
	}
}

// validateHooks checks [hooks] (SPEC §12).
func (c *Config) validateHooks(add func(string, ...any)) {
	for _, h := range []struct {
		key  string
		argv []string
	}{{"hooks.before_task", c.Hooks.BeforeTask}, {"hooks.after_task", c.Hooks.AfterTask}} {
		if len(h.argv) > 0 && strings.TrimSpace(h.argv[0]) == "" {
			add("%s: the first element is the program and must not be empty; give the command as an argv list, e.g. [\"./scripts/prepare\", \"--quiet\"]", h.key)
		}
		for i, a := range h.argv {
			if strings.ContainsFunc(a, unicode.IsControl) {
				add("%s[%d] contains a control character; remove it (hooks are argv lists, one argument per element)", h.key, i)
			}
		}
	}
	if c.Hooks.Timeout != nil && *c.Hooks.Timeout <= 0 {
		add("hooks.timeout must be greater than zero (e.g. \"2m\")")
	}
}

// Warnings reports settings that are accepted but have no effect: they never
// fail a command (SPEC §12).
func (c *Config) Warnings() []string {
	out := c.Deprecated()
	// Plain http is allowed (a server on the LAN), but a secret then
	// crosses the network unencrypted (SPEC §16). An env: URL is not known
	// here.
	n := c.Notify
	if n.Ntfy.Topic != "" && n.Ntfy.Token != "" && plainHTTP(n.Ntfy.Server) {
		out = append(out, "notify.ntfy.server uses http://, so the token goes unencrypted; use an https:// server")
	}
	if n.Gotify.Token != "" && plainHTTP(n.Gotify.Server) {
		out = append(out, "notify.gotify.server uses http://, so the token goes unencrypted; use an https:// server")
	}
	if !isEnvRef(n.Webhook.URL) && plainHTTP(n.Webhook.URL) {
		out = append(out, webhookHTTP)
	}
	return out
}

// Deprecated lists the deprecated settings c has (Warnings starts with
// them); `igris config migrate` removes them. There are none today: the
// last one, claude.command, was removed in v0.7 (ErrRemovedKey).
func (c *Config) Deprecated() []string { return nil }

const webhookHTTP = "notify.webhook.url uses http://, so the payload (and its signature) go unencrypted; use an https:// URL"

// WarningsEnv is Warnings plus what only the environment shows: a webhook
// url given as an env: reference whose value is plain http (V06-07). The
// value itself is never in the text. getenv nil is Warnings.
func (c *Config) WarningsEnv(getenv func(string) string) []string {
	out := c.Warnings()
	if getenv == nil {
		return out
	}
	if name, ok := strings.CutPrefix(c.Notify.Webhook.URL, "env:"); ok && plainHTTP(getenv(name)) {
		out = append(out, webhookHTTP+" (it comes from "+name+")")
	}
	return out
}

// secretField is a notification secret: its key, its value as written and
// where Resolve puts the resolved value.
type secretField struct {
	key, val string
	dst      *string
}

// secretFields lists every secret field of the config, the one table
// Resolve and SecretEnvNames read. s receives the resolved values.
func (c *Config) secretFields(s *Secrets) []secretField {
	return []secretField{
		{"notify.ntfy.token", c.Notify.Ntfy.Token, &s.NtfyToken},
		{"notify.discord.webhook_url", c.Notify.Discord.WebhookURL, &s.DiscordWebhook},
		{"notify.webhook.url", c.Notify.Webhook.URL, &s.WebhookURL},
		{"notify.webhook.secret", c.Notify.Webhook.Secret, &s.WebhookSecret},
		{"notify.slack.webhook_url", c.Notify.Slack.WebhookURL, &s.SlackWebhook},
		{"notify.gotify.token", c.Notify.Gotify.Token, &s.GotifyToken},
	}
}

// SecretEnvNames returns the environment variables the secret fields refer
// to through env:, sorted and without duplicates. igris never passes them
// to the commands it runs (SPEC §16).
func (c *Config) SecretEnvNames() []string {
	var names []string
	for _, f := range c.secretFields(&Secrets{}) {
		if name, ok := strings.CutPrefix(f.val, "env:"); ok && name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
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
	for _, f := range c.secretFields(&s) {
		*f.dst = resolve(f.key, f.val)
	}
	// Validate checks a URL written in igris.toml; one from the environment
	// is checked here, once it is known.
	if isEnvRef(c.Notify.Webhook.URL) && s.WebhookURL != "" && !httpURL(s.WebhookURL) {
		errs = append(errs, errors.New(badURL("notify.webhook.url")))
	}
	resolved := map[string]string{"notify.discord.webhook_url": s.DiscordWebhook, "notify.slack.webhook_url": s.SlackWebhook}
	for _, h := range c.chatHooks() {
		if v := resolved[h.key]; isEnvRef(h.val) && v != "" && !httpsURL(v) {
			errs = append(errs, errors.New(badHTTPS(h.key)))
		}
	}
	return s, errors.Join(errs...)
}

// Write stores c as TOML at path (0600). Loading the file gives a config
// with the same Hash. A channel's events list equal to the default is left
// out, so the file follows the default when a later version adds an event.
func Write(path string, c *Config) error {
	out := *c
	if out.Notify.BreakThrough.IsZero() {
		out.Notify.BreakThrough = nil
	}
	if slices.Equal(out.Notify.Ntfy.Events, defaultEvents()) {
		out.Notify.Ntfy.Events = nil
	}
	if slices.Equal(out.Notify.Discord.Events, defaultEvents()) {
		out.Notify.Discord.Events = nil
	}
	// A channel that isn't set up is left out, events and all (IsZero).
	if out.Notify.Webhook.IsZero() {
		out.Notify.Webhook = Webhook{}
	} else if slices.Equal(out.Notify.Webhook.Events, defaultEvents()) {
		out.Notify.Webhook.Events = nil
	}
	if out.Notify.Slack.IsZero() {
		out.Notify.Slack = Slack{}
	} else if slices.Equal(out.Notify.Slack.Events, defaultEvents()) {
		out.Notify.Slack.Events = nil
	}
	if out.Notify.Gotify.IsZero() {
		out.Notify.Gotify = Gotify{}
	} else if slices.Equal(out.Notify.Gotify.Events, defaultEvents()) {
		out.Notify.Gotify.Events = nil
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(&out); err != nil {
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
	h := *c
	for _, ev := range []*[]string{&h.Notify.Ntfy.Events, &h.Notify.Discord.Events, &h.Notify.Webhook.Events, &h.Notify.Slack.Events, &h.Notify.Gotify.Events} {
		if slices.Equal(*ev, defaultEvents()) {
			*ev = hashEvents() // replaced, never written through: c keeps its lists
		}
	}
	b, err := json.Marshal(&h) // map keys are sorted, so the encoding is canonical
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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
