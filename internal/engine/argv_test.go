package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestResolveMode(t *testing.T) {
	tests := []struct {
		name                                           string
		override, taskMode, runMode, defaultMode, want string
		wantErr                                        bool
	}{
		{"override wins", "yolo", "plan", "accept", "auto", "yolo", false},
		{"task mode beats run mode", "", "plan", "accept", "auto", "plan", false},
		{"run mode beats config", "", "", "accept", "auto", "accept", false},
		{"config default_mode", "", "", "", "auto", "auto", false},
		{"fallback default", "", "", "", "", "default", false},
		{"whitespace and case ignored", " ", "PLAN ", "", "", "plan", false},
		{"unknown override", "wild", "plan", "", "", "", true},
		{"unknown task mode", "", "wild", "", "", "", true},
		{"unknown config mode", "", "", "", "wild", "", true},
		{"unknown lower precedence is not reached", "accept", "wild", "", "", "accept", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveMode(tt.override, tt.taskMode, tt.runMode, tt.defaultMode)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaudeArgs(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	base := ClaudeParams{Model: "opus", SessionID: id, Mode: ModeDefault, RulesFile: "/p/M0-01.rules.md"}

	tests := []struct {
		name   string
		mutate func(*ClaudeParams)
		want   []string
	}{
		{"default fresh", func(*ClaudeParams) {},
			[]string{"--model", "opus", "--session-id", id, "--append-system-prompt-file", "/p/M0-01.rules.md"}},
		{"accept", func(p *ClaudeParams) { p.Mode = ModeAccept },
			[]string{"--model", "opus", "--session-id", id, "--permission-mode", "acceptEdits", "--append-system-prompt-file", "/p/M0-01.rules.md"}},
		{"auto", func(p *ClaudeParams) { p.Mode = ModeAuto },
			[]string{"--model", "opus", "--session-id", id, "--permission-mode", "auto", "--append-system-prompt-file", "/p/M0-01.rules.md"}},
		{"plan", func(p *ClaudeParams) { p.Mode = ModePlan },
			[]string{"--model", "opus", "--session-id", id, "--permission-mode", "plan", "--append-system-prompt-file", "/p/M0-01.rules.md"}},
		{"yolo", func(p *ClaudeParams) { p.Mode = ModeYolo },
			[]string{"--model", "opus", "--session-id", id, "--dangerously-skip-permissions", "--append-system-prompt-file", "/p/M0-01.rules.md"}},
		{"resume keeps model and mode", func(p *ClaudeParams) { p.Resume = true; p.Mode = ModeAccept },
			[]string{"--model", "opus", "--resume", id, "--permission-mode", "acceptEdits", "--append-system-prompt-file", "/p/M0-01.rules.md"}},
		{"hooks settings before extra args", func(p *ClaudeParams) {
			p.SettingsFile = "/p/hooks/M0-01.settings.json"
			p.ExtraArgs = []string{"--verbose"}
		},
			[]string{"--model", "opus", "--session-id", id, "--append-system-prompt-file", "/p/M0-01.rules.md", "--settings", "/p/hooks/M0-01.settings.json", "--verbose"}},
		{"extra args last, order kept", func(p *ClaudeParams) { p.ExtraArgs = []string{"--verbose", "--add-dir", "../x"} },
			[]string{"--model", "opus", "--session-id", id, "--append-system-prompt-file", "/p/M0-01.rules.md", "--verbose", "--add-dir", "../x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base
			tt.mutate(&p)
			got, err := ClaudeArgs(p)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("args =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

func TestClaudeArgsNoPositionalPrompt(t *testing.T) {
	args, err := ClaudeArgs(ClaudeParams{Model: "sonnet", SessionID: "11111111-2222-4333-8444-555555555555", Mode: ModeYolo, RulesFile: "r.md"})
	if err != nil {
		t.Fatal(err)
	}
	// Every non-flag element must be the value of the flag before it.
	for i, a := range args {
		if !strings.HasPrefix(a, "-") && (i == 0 || !strings.HasPrefix(args[i-1], "-")) {
			t.Errorf("argument %d %q is a stray positional (the task prompt must not travel as argv)", i, a)
		}
	}
}

func TestClaudeArgsErrors(t *testing.T) {
	good := ClaudeParams{Model: "opus", SessionID: "11111111-2222-4333-8444-555555555555", Mode: ModeDefault, RulesFile: "r.md"}
	tests := []struct {
		name   string
		mutate func(*ClaudeParams)
		want   string
	}{
		{"no model", func(p *ClaudeParams) { p.Model = " " }, "model is empty"},
		{"no session", func(p *ClaudeParams) { p.SessionID = "" }, "session ID"},
		{"session ID smuggles a flag", func(p *ClaudeParams) { p.SessionID = "--dangerously-skip-permissions" }, "not a UUID"},
		{"session ID is not a UUID", func(p *ClaudeParams) { p.SessionID = "abc" }, "not a UUID"},
		{"no rules", func(p *ClaudeParams) { p.RulesFile = "" }, "rules file"},
		{"bad mode", func(p *ClaudeParams) { p.Mode = "wild" }, "unknown run mode"},
		{"forbidden extra", func(p *ClaudeParams) { p.ExtraArgs = []string{"--model=haiku"} }, "sets itself"},
		{"forbidden skip", func(p *ClaudeParams) { p.ExtraArgs = []string{"--dangerously-skip-permissions"} }, "sets itself"},
		{"forbidden fallback", func(p *ClaudeParams) { p.ExtraArgs = []string{"--fallback-model", "haiku"} }, "sets itself"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := good
			tt.mutate(&p)
			if _, err := ClaudeArgs(p); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNewSessionID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 50 {
		id, err := NewSessionID()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(id) {
			t.Fatalf("id %q is not a v4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

// The hooks name igris by its PATH link when that is the running binary,
// so they keep working after a package manager replaces the versioned file.
func TestIgrisPath(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "igris")
	if err := os.Symlink(self, link); err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", dir)
	if got := igrisPath(); got != link {
		t.Errorf("igrisPath() = %q, want the PATH link %q", got, link)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "igris"), []byte("#!/bin/sh\n"), 0o700); err != nil { //nolint:gosec // must be executable for LookPath
		t.Fatal(err)
	}
	t.Setenv("PATH", other)
	if got := igrisPath(); got != self {
		t.Errorf("igrisPath() = %q with another igris in PATH, want the running %q", got, self)
	}
}
