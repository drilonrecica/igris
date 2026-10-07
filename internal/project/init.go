package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/examples"
	"github.com/drilonrecica/igris/internal/backend/herdr"
	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

const (
	// ClaudeSettingsPath holds the `igris done` allow rules init writes.
	ClaudeSettingsPath = ".claude/settings.local.json"
	integrationHint    = "runs in herdr or tmux; igris reads Claude's state from Claude Code hooks (in herdr, `herdr integration install claude` adds herdr's own)"
)

// initStep is one step of Init: the file it touches and what it does.
type initStep struct {
	id, path string
	run      func(root string) (string, error)
}

func initSteps(example bool) []initStep {
	steps := []initStep{
		{report.StepConfig, state.ConfigFile, initConfig},
		{report.StepState, state.DirName + "/", initState},
		{report.StepGitignore, ".gitignore", initGitignore},
		{report.StepClaudeSettings, ClaudeSettingsPath, initClaudeSettings},
	}
	if example {
		steps = append(steps, initStep{report.StepExamplePlan, "", initExamplePlan})
	}
	return steps
}

// InitFiles are the files and directories `igris init` touches (with
// example, the example plan too, at the path igris.toml gives).
func InitFiles(example bool) []string {
	var out []string
	for _, s := range initSteps(example) {
		if s.path != "" {
			out = append(out, s.path)
		}
	}
	return out
}

// Init sets dir up as an igris project (SPEC §14 `igris init`): it only adds
// what is missing and never overwrites. The steps' results come in order,
// the herdr hint last. On an error the steps finished before it are
// returned with it.
func Init(ctx context.Context, dir string, example bool, env Env) ([]report.Step, error) {
	var out []report.Step
	for _, s := range initSteps(example) {
		msg, err := s.run(dir)
		if err != nil {
			return out, err
		}
		path := s.path
		if s.id == report.StepExamplePlan {
			path = examplePlanPath(dir)
		}
		out = append(out, report.Step{ID: s.id, Path: path, Message: msg})
	}
	out = append(out, report.Step{ID: report.StepHerdrHint, Message: herdrHint(ctx, env)})
	return out, nil
}

// examplePlanPath is the plan path igris.toml in root gives, for the
// example plan step's result.
func examplePlanPath(root string) string {
	if cfg, err := config.Load(filepath.Join(root, state.ConfigFile)); err == nil {
		return cfg.Plan
	}
	return config.Default().Plan
}

// initConfig writes the default igris.toml unless one exists.
func initConfig(root string) (string, error) {
	path := filepath.Join(root, state.ConfigFile)
	if _, err := os.Stat(path); err == nil {
		return "kept " + state.ConfigFile + " (already exists)", nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("check %s: %w", path, err)
	}
	if err := config.Write(path, config.Default()); err != nil {
		return "", err
	}
	return "created " + state.ConfigFile, nil
}

// initExamplePlan writes the canonical example plan at the configured plan
// path unless a file is already there.
func initExamplePlan(root string) (string, error) {
	cfg, err := config.Load(filepath.Join(root, state.ConfigFile))
	if err != nil {
		return "", err
	}
	path := InRoot(root, cfg.Plan)
	kept := "kept " + cfg.Plan + " (already exists)"
	if _, err := os.Stat(path); err == nil {
		return kept, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("check %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	// O_EXCL: never overwrite, even if the file appeared since the check.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // a plan is meant to be readable
	if errors.Is(err, fs.ErrExist) {
		return kept, nil
	}
	if err != nil {
		return "", fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := f.Write(examples.Plan); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return "created " + cfg.Plan + " (example plan)", nil
}

// initState creates .igris/ with its subdirectories.
func initState(root string) (string, error) {
	if _, err := state.Open(root, state.Options{}); err != nil {
		return "", err
	}
	return "ready " + state.DirName + "/", nil
}

// initGitignore adds the .igris/ entry unless .gitignore already ignores it.
func initGitignore(root string) (string, error) {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path) //nolint:gosec // the owner's own .gitignore
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch strings.TrimSpace(line) {
		case ".igris", ".igris/", "/.igris", "/.igris/":
			return "kept .gitignore (already ignores " + state.DirName + "/)", nil
		}
	}
	entry := state.DirName + "/\n"
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		entry = "\n" + entry
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // .gitignore is meant to be readable
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.WriteString(entry); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return "added " + state.DirName + "/ to .gitignore", nil
}

// initClaudeSettings merges the `igris done` allow rules into
// .claude/settings.local.json, keeping everything else in the file.
func initClaudeSettings(root string) (string, error) {
	path := filepath.Join(root, ClaudeSettingsPath)
	settings := map[string]any{}
	data, err := os.ReadFile(path) //nolint:gosec // the owner's own Claude settings
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", fmt.Errorf("read %s: %w", path, err)
	case len(bytes.TrimSpace(data)) > 0:
		if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
			return "", fmt.Errorf("%s is not a JSON object (%v); fix or remove it and run `igris init` again", ClaudeSettingsPath, err)
		}
	}

	perms, _ := settings["permissions"].(map[string]any)
	if _, present := settings["permissions"]; present && perms == nil {
		return "", fmt.Errorf("%s: \"permissions\" is not an object; fix it and run `igris init` again", ClaudeSettingsPath)
	}
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	if _, present := perms["allow"]; present && allow == nil {
		return "", fmt.Errorf("%s: \"permissions.allow\" is not a list; fix it and run `igris init` again", ClaudeSettingsPath)
	}
	added := 0
	for _, rule := range checks.DoneAllowRules {
		if !containsAny(allow, rule) {
			allow = append(allow, rule)
			added++
		}
	}
	if added == 0 {
		return "kept " + ClaudeSettingsPath + " (`igris done` already allowed)", nil
	}
	perms["allow"] = allow
	settings["permissions"] = perms
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", ClaudeSettingsPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return "allowed `igris done` in " + ClaudeSettingsPath, nil
}

func containsAny(list []any, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// herdrHint asks herdr whether its Claude integration is installed when
// that is possible (inside a herdr pane) and otherwise recommends it.
func herdrHint(ctx context.Context, env Env) string {
	getenv := env.getenv()
	switch {
	case getenv(herdr.WorkspaceEnv) != "":
		b := herdr.NewFromEnv(env.runner(), getenv)
		if b.IntegrationHint(ctx) == "" {
			return "herdr's Claude Code integration is installed"
		}
	case getenv(tmux.Env) != "":
		return "inside tmux: sessions open in tmux windows; igris reads Claude's state from Claude Code hooks"
	}
	return integrationHint
}
