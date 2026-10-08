package checks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Doctor-only check IDs: they run in `igris doctor` and the home screen's
// Doctor page, not in `check` or `arise`.
const (
	IDConfigValid = "config-valid" // igris.toml parses and every value is valid
	IDPlanValid   = "plan-valid"   // the plan file exists and has no validation problems
	IDAllowRules  = "allow-rules"  // `igris done` allowed, `igris skip` not, in .claude/settings.local.json
	IDStatePerms  = "state-perms"  // .igris/ is 0700 and its files 0600
	IDLock        = "lock"         // no stale or foreign run lock
	IDWSL         = "wsl"          // project not on the Windows filesystem under WSL
	IDNotify      = "notify"       // at least one notification channel is set up
)

const (
	claudeSettingsFile = ".claude/settings.local.json"
	// skipSample is a command line `igris skip` allow rules are matched against.
	skipSample = "igris skip x"
)

// DoneAllowRules let a session run `igris done` without a permission
// prompt (SPEC §6.2, §7.1); `igris init` writes them and doctor checks for
// them. `igris skip` is deliberately not among them.
var DoneAllowRules = []string{"Bash(igris done:*)", "Bash(igris done *)"}

// DoctorOptions are the inputs of Doctor. Of Options, Root, Config,
// NoConfig, ParentConfig and Plan are ignored: Doctor finds them from Dir.
type DoctorOptions struct {
	Options
	// Dir is the working directory; the project root is found from it as
	// for `arise`, else Dir itself.
	Dir string
}

// Doctor runs the health check of SPEC §14: the shared checks plus the
// doctor-only ones, in the order the spec lists them. It is strictly
// read-only and never fails: what it can't tell is a result.
func Doctor(ctx context.Context, o DoctorOptions) []Result {
	root := o.projectRoot()
	cfg, noConfig, cfgResults := loadConfig(root)
	base := o.Options
	base.Root, base.Config, base.NoConfig, base.Plan, base.ParentConfig = root, cfg, noConfig, nil, ""
	if noConfig {
		base.ParentConfig = ParentConfig(root)
		cfgResults = []Result{project(base)}
	}
	pick := func(ids ...string) []Result {
		b := base
		b.IDs = ids
		return Run(ctx, b)
	}

	var out []Result
	out = append(out, pick(IDClaude, IDHerdr, IDTmux, IDAPIKey, IDBackend, IDHerdrIntegration, IDGit)...)
	out = append(out, cfgResults...)
	if cfgResults[0].Level != Fail {
		out = append(out, pick(IDConfig)...)
	}
	p, planResults := doctorPlan(root, cfg, noConfig)
	out = append(out, planResults...)
	if p != nil {
		base.Plan = p
		out = append(out, pick(IDPlanHints, IDDrift)...)
	}
	out = append(out, allowRules(root))
	out = append(out, statePerms(root))
	out = append(out, lock(root))
	out = append(out, wsl(root))
	out = append(out, notifyChannels(cfg, base.Getenv))

	for i := range out {
		r := &out[i]
		// Doctor is mostly run from a plain shell, where "not inside herdr
		// or tmux" is a fact to know, not a failure of the machine.
		if r.ID == IDBackend && r.Level == Fail {
			r.Level = Warn
		}
		if rel, err := filepath.Rel(root, r.File); r.File != "" && filepath.IsAbs(r.File) && err == nil && !strings.HasPrefix(rel, "..") {
			r.File = rel
		}
		if r.Level != OK && r.Next == "" {
			r.Next = defaultNext[r.ID]
		}
		r.Message, r.Next, r.File = textsafe.Line(r.Message), textsafe.Line(r.Next), textsafe.Line(r.File)
	}
	return out
}

// defaultNext is the command for a problem whose check doesn't name one.
var defaultNext = map[string]string{
	IDClaude:  "claude --version",
	IDHerdr:   "herdr --version",
	IDTmux:    "tmux -V",
	IDBackend: "run igris inside a herdr pane or a tmux session",
	IDNotify:  "igris notify test",
}

// projectRoot is the project root found from o.Dir (the nearest directory
// holding igris.toml or .igris/), else o.Dir.
func (o DoctorOptions) projectRoot() string {
	dir := o.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if r, err := state.FindRoot(dir); err == nil {
		return r
	}
	return dir
}

// loadConfig reads root/igris.toml. The first result is always the
// IDConfigValid one (a Fail first when the file is invalid, one result per
// problem); cfg is the defaults when the file is missing or invalid.
func loadConfig(root string) (cfg *config.Config, noConfig bool, rs []Result) {
	path := filepath.Join(root, state.ConfigFile)
	cfg, err := config.Load(path)
	switch {
	case err == nil:
		return cfg, false, []Result{{ID: IDConfigValid, Level: OK, Message: state.ConfigFile + " is valid", File: state.ConfigFile}}
	case errors.Is(err, config.ErrNotFound):
		return config.Default(), true, []Result{{ID: IDConfigValid, Level: OK, Message: "no " + state.ConfigFile + " here; using the defaults", Next: "igris init"}}
	}
	for _, line := range strings.Split(err.Error(), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), state.ConfigFile+":"))
		if line == "" {
			continue
		}
		rs = append(rs, Result{ID: IDConfigValid, Level: Fail, Message: line, File: state.ConfigFile, Next: "edit " + state.ConfigFile + ", then igris doctor"})
	}
	return config.Default(), false, rs
}

// doctorPlan loads and validates the plan. It returns the plan only when
// it is valid, for the hint and drift checks.
func doctorPlan(root string, cfg *config.Config, noConfig bool) (*plan.Plan, []Result) {
	path := cfg.Plan
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	p, err := plan.Load(path, plan.Options{Columns: cfg.Columns})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Without igris.toml there may simply be no project here.
			level, next := Fail, "igris adapt"
			if noConfig {
				level, next = Warn, "igris init --example"
			}
			return nil, []Result{{ID: IDPlanValid, Level: level, Message: fmt.Sprintf("no plan at %s", cfg.Plan), Next: next}}
		}
		return nil, []Result{{ID: IDPlanValid, Level: Fail, Message: err.Error(), Next: "igris adapt"}}
	}
	issues := p.Validate(cfg.Rules(root))
	if len(issues) == 0 {
		return p, []Result{{ID: IDPlanValid, Level: OK, Message: fmt.Sprintf("%s is valid (%d phases, %d tasks)", cfg.Plan, len(p.Phases), len(p.Tasks)), File: p.Path}}
	}
	out := make([]Result, len(issues))
	for i, is := range issues {
		out[i] = Result{ID: IDPlanValid, Level: Fail, Message: is.Msg, File: is.File, Line: is.Line, Next: "igris adapt"}
	}
	return nil, out
}

// allowRules checks .claude/settings.local.json: the `igris done` rules
// `igris init` writes must be there, and no rule may allow `igris skip`,
// which would bypass the skip confirmation (SPEC §6.2, §7.1).
func allowRules(root string) Result {
	r := func(l Level, msg, next string) Result {
		return Result{ID: IDAllowRules, Level: l, Message: msg, Next: next}
	}
	data, err := os.ReadFile(filepath.Join(root, claudeSettingsFile)) //nolint:gosec // the owner's own Claude settings
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r(Warn, claudeSettingsFile+" not found: every session's `igris done` would ask for permission", "igris init")
	case err != nil:
		return r(Warn, fmt.Sprintf("can't read %s: %v", claudeSettingsFile, err), "igris init")
	}
	var settings struct {
		Permissions struct {
			Allow []any `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return r(Warn, fmt.Sprintf("%s is not valid JSON of the expected shape (%v)", claudeSettingsFile, err), "igris init")
	}
	var allowed []string
	for _, v := range settings.Permissions.Allow {
		if s, ok := v.(string); ok {
			allowed = append(allowed, s)
		}
	}
	for _, rule := range allowed {
		if allowsSkip(rule) {
			return r(Warn, fmt.Sprintf("%s allows `igris skip` (rule %s): a session could skip tasks without your confirmation", claudeSettingsFile, rule),
				fmt.Sprintf("remove %q from permissions.allow in %s", rule, claudeSettingsFile))
		}
	}
	var missing []string
	for _, rule := range DoneAllowRules {
		found := false
		for _, a := range allowed {
			found = found || a == rule
		}
		if !found {
			missing = append(missing, rule)
		}
	}
	if len(missing) > 0 {
		return r(Warn, fmt.Sprintf("%s lacks %s: a session's `igris done` would ask for permission", claudeSettingsFile, strings.Join(missing, ", ")), "igris init")
	}
	return r(OK, "`igris done` is allowed and `igris skip` is not", "")
}

// allowsSkip says whether a Claude Code Bash allow rule matches
// `igris skip`: an exact rule, or a prefix rule (`:*` or a trailing `*`)
// whose prefix `igris skip x` starts with.
func allowsSkip(rule string) bool {
	inner, ok := strings.CutPrefix(rule, "Bash(")
	if !ok {
		return false
	}
	inner, ok = strings.CutSuffix(inner, ")")
	if !ok {
		return false
	}
	inner = strings.TrimSpace(inner)
	if prefix, ok := strings.CutSuffix(inner, ":*"); ok {
		return strings.HasPrefix(skipSample, strings.TrimSpace(prefix)+" ") || strings.TrimSpace(prefix) == "igris skip"
	}
	if prefix, ok := strings.CutSuffix(inner, "*"); ok {
		return strings.HasPrefix(skipSample, prefix)
	}
	return inner == "igris skip"
}

// statePerms checks that .igris/ is 0700 and everything in it 0600 (SPEC
// §13, §16). A missing .igris/ is fine: nothing was created yet.
func statePerms(root string) Result {
	dir := filepath.Join(root, state.DirName)
	_, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Result{ID: IDStatePerms, Level: OK, Message: state.DirName + "/ doesn't exist yet; the first run creates it with the right modes"}
	case err != nil:
		return Result{ID: IDStatePerms, Level: Warn, Message: fmt.Sprintf("can't read %s/: %v", state.DirName, err), Next: "chmod 700 " + state.DirName}
	}
	var bad []string
	rootBad := false
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if fi.Mode().Perm() != want {
			rel, _ := filepath.Rel(root, path)
			bad = append(bad, fmt.Sprintf("%s is %04o, want %04o", rel, fi.Mode().Perm(), want))
			rootBad = rootBad || path == dir
		}
		return nil
	})
	if len(bad) == 0 {
		return Result{ID: IDStatePerms, Level: OK, Message: state.DirName + "/ is 0700 and its files 0600"}
	}
	next := "chmod -R go-rwx " + state.DirName
	if len(bad) == 1 && rootBad {
		next = "chmod 700 " + state.DirName
	}
	msg := bad[0]
	if len(bad) > 1 {
		msg = fmt.Sprintf("%s (and %d more)", bad[0], len(bad)-1)
	}
	return Result{ID: IDStatePerms, Level: Warn, Message: msg, Next: next}
}

// lock reports a stale, unreadable or foreign run lock (SPEC §13). A lock
// held by a live igris on this host is fine: that is a run in progress.
func lock(root string) Result {
	st, err := state.PeekLock(root)
	switch {
	case err != nil:
		return Result{ID: IDLock, Level: Warn, Message: fmt.Sprintf("can't read the run lock: %v", err), Next: "igris arise --force-unlock"}
	case !st.Held:
		return Result{ID: IDLock, Level: OK, Message: "no run lock"}
	case st.Unreadable:
		return Result{ID: IDLock, Level: Warn, Message: "the run lock can't be read: " + st.Reason, Next: "igris arise --force-unlock"}
	case st.Stale:
		return Result{ID: IDLock, Level: Warn, Message: fmt.Sprintf("the run lock is stale (%s): %s", st.Info, st.Reason), Next: "igris arise --force-unlock"}
	case st.Remote:
		return Result{ID: IDLock, Level: Warn, Message: fmt.Sprintf("the run lock is held from another host (%s); igris can't tell whether it is alive", st.Info), Next: "igris arise --force-unlock"}
	}
	return Result{ID: IDLock, Level: OK, Message: fmt.Sprintf("a run is in progress (%s)", st.Info)}
}

// wsl warns about a project on the Windows filesystem under WSL, where
// file watching, permissions and line endings misbehave (docs/check-wsl.md).
func wsl(root string) Result {
	if root == "/mnt" || strings.HasPrefix(root, "/mnt/") {
		return Result{ID: IDWSL, Level: Warn, Next: "see docs/check-wsl.md",
			Message: "the project is under /mnt/ (the Windows filesystem under WSL); a project under your Linux home directory is more reliable"}
	}
	return Result{ID: IDWSL, Level: OK, Message: "the project is not under /mnt/"}
}

// notifyChannels checks that some notification channel is set up (SPEC
// §10). It never sends and never prints a secret.
func notifyChannels(cfg *config.Config, getenv func(string) string) Result {
	if getenv == nil {
		getenv = os.Getenv
	}
	secrets, err := cfg.Resolve(getenv)
	if err != nil {
		return Result{ID: IDNotify, Level: Warn, Message: err.Error(), Next: "edit " + state.ConfigFile + " [notify]"}
	}
	var on []string
	if cfg.Notify.Backend.Enabled {
		on = append(on, "backend toast")
	}
	if cfg.Notify.Ntfy.Topic != "" && len(cfg.Notify.Ntfy.Events) > 0 {
		on = append(on, "ntfy")
	}
	if secrets.DiscordWebhook != "" && len(cfg.Notify.Discord.Events) > 0 {
		on = append(on, "Discord")
	}
	if secrets.WebhookURL != "" && len(cfg.Notify.Webhook.Events) > 0 {
		on = append(on, "webhook")
	}
	if len(on) == 0 {
		return Result{ID: IDNotify, Level: Warn, Message: "no notification channel is set up: you won't hear when a task needs you", Next: "edit " + state.ConfigFile + " [notify]"}
	}
	return Result{ID: IDNotify, Level: OK, Message: "notification channels: " + strings.Join(on, ", ") + " (igris notify test sends a sample)"}
}
