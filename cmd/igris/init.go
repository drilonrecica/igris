package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/backend/herdr"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/state"
)

const (
	claudeSettingsPath = ".claude/settings.local.json"
	integrationHint    = "recommended: run `herdr integration install claude` so herdr reports Claude's state accurately"
)

// execInit sets up the current directory as an igris project. It is safe to
// run again: it only adds what is missing and never overwrites.
func execInit(_ *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris init: %v\n", err)
		return exitFail
	}
	steps := []func(root string) (string, error){initConfig, initState, initGitignore, initClaudeSettings}
	for _, step := range steps {
		msg, err := step(root)
		if err != nil {
			fmt.Fprintf(stderr, "igris init: %v\n", err)
			return exitFail
		}
		fmt.Fprintln(stdout, msg)
	}
	fmt.Fprintln(stdout, herdrHint())
	return exitOK
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
	path := filepath.Join(root, claudeSettingsPath)
	settings := map[string]any{}
	data, err := os.ReadFile(path) //nolint:gosec // the owner's own Claude settings
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", fmt.Errorf("read %s: %w", path, err)
	case len(bytes.TrimSpace(data)) > 0:
		if err := json.Unmarshal(data, &settings); err != nil || settings == nil {
			return "", fmt.Errorf("%s is not a JSON object (%v); fix or remove it and run `igris init` again", claudeSettingsPath, err)
		}
	}

	perms, _ := settings["permissions"].(map[string]any)
	if _, present := settings["permissions"]; present && perms == nil {
		return "", fmt.Errorf("%s: \"permissions\" is not an object; fix it and run `igris init` again", claudeSettingsPath)
	}
	if perms == nil {
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	if _, present := perms["allow"]; present && allow == nil {
		return "", fmt.Errorf("%s: \"permissions.allow\" is not a list; fix it and run `igris init` again", claudeSettingsPath)
	}
	added := 0
	for _, rule := range checks.DoneAllowRules {
		if !containsAny(allow, rule) {
			allow = append(allow, rule)
			added++
		}
	}
	if added == 0 {
		return "kept " + claudeSettingsPath + " (`igris done` already allowed)", nil
	}
	perms["allow"] = allow
	settings["permissions"] = perms
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", claudeSettingsPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return "allowed `igris done` in " + claudeSettingsPath, nil
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
func herdrHint() string {
	if ariseGetenv(herdr.WorkspaceEnv) != "" {
		b := herdr.NewFromEnv(commandRunner(), ariseGetenv)
		if b.IntegrationHint(context.Background()) == "" {
			return "herdr's Claude Code integration is installed"
		}
	}
	return integrationHint
}
