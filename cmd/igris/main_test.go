package main

import (
	"bytes"
	"context"
	"os"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/side"
)

// TestMain keeps the tests off the real claude and herdr: no version check
// runs unless a test asks for one (see withVersions).
func TestMain(m *testing.M) {
	compatWarnings = func(context.Context, runner.Runner) []checks.Result { return nil }
	// Nor on the developer's ANTHROPIC_API_KEY: check would warn about it.
	ariseGetenv = func(k string) string {
		if k == checks.APIKeyVar {
			return ""
		}
		return os.Getenv(k)
	}
	interactiveTerm = func() bool { return false } // tests are not on a terminal
	// Run from a side session, done, skip and reset would refuse.
	_ = os.Unsetenv(side.Env)
	os.Exit(m.Run())
}

// Every command's help is asked for, so it goes to stdout with the
// command's synopsis, and exits 0 (SPEC §14).
func TestEveryCommandHelp(t *testing.T) {
	for _, c := range commands() {
		for _, args := range [][]string{{c.name, "-h"}, {"help", c.name}} {
			var out, errb bytes.Buffer
			if code := run(args, &out, &errb); code != exitOK {
				t.Errorf("%v: exit %d", args, code)
			}
			if !strings.HasPrefix(out.String(), "Usage:\n  igris "+c.name) || errb.Len() != 0 {
				t.Errorf("%v: stdout %q, stderr %q", args, out.String(), errb.String())
			}
		}
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no args prints usage", nil, exitUsage, "", "Usage:"},
		{"help", []string{"--help"}, exitOK, "Commands:", ""},
		{"help word", []string{"help"}, exitOK, "Commands:", ""},
		{"version", []string{"version"}, exitOK, "igris dev", ""},
		{"unknown command", []string{"bogus"}, exitUsage, "", "unknown command"},
		{"check unknown flag", []string{"check", "--nope"}, exitUsage, "", "flag provided but not defined"},
		{"check missing plan", []string{"check", "--plan", "/nonexistent/p.md"}, exitFail, "", "read plan"},
		{"check extra arg", []string{"check", "x"}, exitUsage, "", "unexpected argument"},
		{"phases extra arg", []string{"phases", "x"}, exitUsage, "", "unexpected argument"},
		{"status two phases", []string{"status", "M0", "M1"}, exitUsage, "", "unexpected argument"},
		{"arise flags", []string{"arise", "M0", "--through", "M1", "--mode", "plan", "--no-tui", "--dry-run", "--plan-is-not-a-flag"}, exitUsage, "", "flag provided but not defined"},
		{"arise force-unlock flag", []string{"arise", "--force-unlock", "--mode", "wild"}, exitUsage, "", "invalid --mode"},
		{"arise help lists force-unlock", []string{"arise", "-h"}, exitOK, "-force-unlock", ""},
		{"arise bad mode", []string{"arise", "--mode", "wild"}, exitUsage, "", "invalid --mode"},
		{"done missing id", []string{"done"}, exitUsage, "", "missing ID"},
		{"skip missing reason", []string{"skip", "M0-01"}, exitUsage, "", "skip needs a reason: igris skip ID --reason TEXT; see `igris skip -h`"},
		{"skip missing id", []string{"skip", "--reason", "x"}, exitUsage, "", "missing ID"},
		{"adapt extra arg", []string{"adapt", "x"}, exitUsage, "", "unexpected argument"},
		{"adapt help lists plan", []string{"adapt", "-h"}, exitOK, "-plan", ""},
		{"adapt bad model", []string{"adapt", "--model", "gpt"}, exitUsage, "", "invalid --model"},
		{"subcommand help", []string{"check", "-h"}, exitOK, "Usage:\n  igris check [--plan PATH]", ""},
		// The exit codes SPEC §14 lists for the command line itself.
		{"help flag", []string{"-h"}, exitOK, "Exit codes: 0 success", ""},
		{"help command", []string{"help", "check"}, exitOK, "Usage:\n  igris check [--plan PATH]", ""},
		{"help unknown command", []string{"help", "nope"}, exitUsage, "", "igris help: unknown command \"nope\""},
		{"help two commands", []string{"help", "check", "status"}, exitUsage, "", "at most one command"},
		{"version -v", []string{"-v"}, exitOK, "igris dev", ""},
		{"version --version", []string{"--version"}, exitOK, "igris dev", ""},
		{"version extra arg", []string{"version", "x"}, exitUsage, "", "takes no arguments"},
		{"init extra arg", []string{"init", "x"}, exitUsage, "", "unexpected argument"},
		{"done bad id", []string{"done", "M1 03"}, exitUsage, "", "invalid task ID \"M1 03\""},
		{"skip bad id", []string{"skip", "../evil", "--reason", "r"}, exitUsage, "", "invalid task ID"},
		{"skip blank reason", []string{"skip", "M0-01", "--reason", "   "}, exitUsage, "", "skip needs a reason"},
		{"notify bad event", []string{"notify", "test", "--event", "bogus"}, exitUsage, "", "unknown event \"bogus\""},
		{"notify extra arg", []string{"notify", "test", "x"}, exitUsage, "", "unexpected argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if got := run(tt.args, &out, &errb); got != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", got, tt.wantCode, errb.String())
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want substring %q", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errb.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want substring %q", errb.String(), tt.wantStderr)
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	mod := func(v string) *debug.BuildInfo { return &debug.BuildInfo{Main: debug.Module{Version: v}} }
	tests := []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"ldflags win", "0.1.0", mod("v9.9.9"), true, "0.1.0"},
		{"go install tag", "dev", mod("v0.1.0"), true, "0.1.0"},
		{"go install pseudo-version", "dev", mod("v0.1.1-0.20261007120000-abcdef123456"), true, "0.1.1-0.20261007120000-abcdef123456"},
		{"local build", "dev", mod("(devel)"), true, "dev"},
		{"empty module version", "dev", mod(""), true, "dev"},
		{"no build info", "dev", nil, false, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.ldflags, tt.info, tt.ok); got != tt.want {
				t.Errorf("resolveVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionOverride(t *testing.T) {
	old := version
	version = "1.2.3"
	t.Cleanup(func() { version = old })
	var out bytes.Buffer
	run([]string{"version"}, &out, &bytes.Buffer{})
	if out.String() != "igris 1.2.3\n" {
		t.Errorf("got %q", out.String())
	}
}

// Everything igris writes to stderr is cleaned of escape sequences: an
// error can carry text igris didn't write (a flag, a git hook's stderr, a
// value from .igris/), V06-07.
func TestStderrIsCleaned(t *testing.T) {
	var out, errb bytes.Buffer
	run([]string{"history", "-x\x1b]52;c;aGk=\x07\x1b[2J"}, &out, &errb)
	if strings.ContainsRune(errb.String(), 0x1b) || strings.ContainsRune(errb.String(), 0x07) || !strings.Contains(errb.String(), "flag provided but not defined") {
		t.Errorf("stderr = %q", errb.String())
	}
}
