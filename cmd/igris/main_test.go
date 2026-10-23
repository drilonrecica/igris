package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/side"
)

// TestMain keeps the tests off the real claude and herdr: no version check
// runs unless a test asks for one (see withVersions). It runs nothing when
// this binary was started as `igris serve` (project.SpawnedButNotIgris):
// on 2026-10-22 the real project.SpawnServe, reached from an arise test,
// started this binary, which ran the suite, which started it again, until
// the machine was out of memory. After the suite it fails on a child
// process left running.
func TestMain(m *testing.M) {
	if err := project.SpawnedButNotIgris(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// Never the real igris serve: a test that starts a run installs the
	// in-process one (inProcessServe).
	serveSpawn = project.SpawnRefused("the test did not set serveSpawn; the real igris serve is never started from tests")
	compatWarnings = func(context.Context, runner.Runner) []checks.Result { return nil }
	// Nor on the igris in the developer's PATH: this test binary is the
	// igris in PATH.
	igrisSelf = func() (string, string, bool) { return "/usr/local/bin/igris", "/usr/local/bin/igris", true }
	// Nor on the developer's ANTHROPIC_API_KEY: check would warn about it.
	ariseGetenv = func(k string) string {
		if k == checks.APIKeyVar {
			return ""
		}
		return os.Getenv(k)
	}
	interactiveTerm = func() bool { return false } // tests are not on a terminal
	outTerminal = func(io.Writer) (bool, int) { return false, 0 }
	// Text output tells local times against now: both fixed, so the
	// goldens don't depend on where and when they run.
	time.Local = time.UTC
	textNow = func() time.Time { return time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC) }
	// Nor on GitHub: a test that runs igris update points this at its own
	// server; anything else is refused.
	updateAPI = "http://127.0.0.1:1"
	// Run from a side session, done, skip and reset would refuse.
	_ = os.Unsetenv(side.Env)
	code := m.Run()
	if left := leftoverChildren(); len(left) > 0 {
		fmt.Fprintf(os.Stderr, "igris processes left running by the tests: %v\n", left)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
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
			if !strings.HasPrefix(out.String(), "Usage\n  igris "+c.name) || errb.Len() != 0 {
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
		{"no args prints usage", nil, exitUsage, "", "Usage\n"},
		{"help", []string{"--help"}, exitOK, "Signal (from a session)", ""},
		{"help word", []string{"help"}, exitOK, "Plan with AI", ""},
		{"version", []string{"version"}, exitOK, "igris dev", ""},
		{"unknown command", []string{"bogus"}, exitUsage, "", "unknown command"},
		{"check unknown flag", []string{"check", "--nope"}, exitUsage, "", "igris check: unknown flag --nope; see `igris check -h`"},
		{"check missing plan", []string{"check", "--plan", "/nonexistent/p.md"}, exitFail, "", "read plan"},
		{"check extra arg", []string{"check", "x"}, exitUsage, "", "unexpected argument"},
		{"phases extra arg", []string{"phases", "x"}, exitUsage, "", "unexpected argument"},
		{"status two phases", []string{"status", "M0", "M1"}, exitUsage, "", "unexpected argument"},
		{"arise flags", []string{"arise", "M0", "--through", "M1", "--mode", "plan", "--no-tui", "--dry-run", "--plan-is-not-a-flag"}, exitUsage, "", "unknown flag --plan-is-not-a-flag"},
		{"arise force-unlock flag", []string{"arise", "--force-unlock", "--mode", "wild"}, exitUsage, "", "invalid --mode"},
		{"arise help lists force-unlock", []string{"arise", "-h"}, exitOK, "-force-unlock", ""},
		{"arise bad mode", []string{"arise", "--mode", "wild"}, exitUsage, "", "invalid --mode"},
		{"done missing id", []string{"done"}, exitUsage, "", "missing ID"},
		{"skip missing reason", []string{"skip", "M0-01"}, exitUsage, "", "skip needs a reason: igris skip ID --reason TEXT; see `igris skip -h`"},
		{"skip missing id", []string{"skip", "--reason", "x"}, exitUsage, "", "missing ID"},
		{"adapt extra arg", []string{"adapt", "x"}, exitUsage, "", "unexpected argument"},
		{"adapt help lists plan", []string{"adapt", "-h"}, exitOK, "-plan", ""},
		{"adapt bad model", []string{"adapt", "--model", "gpt"}, exitUsage, "", "invalid --model"},
		{"draft missing subcommand", []string{"draft"}, exitUsage, "", "missing subcommand; use `igris draft goal|spec|import|extend`"},
		{"draft unknown subcommand", []string{"draft", "plan"}, exitUsage, "", "unknown subcommand \"plan\" (want goal|spec|import|extend)"},
		{"draft spec missing file", []string{"draft", "spec"}, exitUsage, "", "missing FILE argument"},
		{"draft import empty file", []string{"draft", "import", ""}, exitUsage, "", "missing FILE argument"},
		{"draft goal extra arg", []string{"draft", "goal", "a", "b"}, exitUsage, "", "unexpected argument \"b\""},
		{"draft spec extra arg", []string{"draft", "spec", "a.md", "b"}, exitUsage, "", "unexpected argument \"b\""},
		{"draft bad model", []string{"draft", "goal", "--model", "haiku"}, exitUsage, "", "invalid --model \"haiku\" (want sonnet|opus)"},
		{"draft bad format", []string{"draft", "import", "x.csv", "--format", "csv"}, exitUsage, "", "invalid --format \"csv\" (want github|linear|jira)"},
		{"draft format without import", []string{"draft", "goal", "--format", "github"}, exitUsage, "", "--format is only for `igris draft import`"},
		{"draft help lists the subcommands", []string{"draft", "-h"}, exitOK, "  igris draft import FILE [--format github|linear|jira]", ""},
		{"subcommand help", []string{"check", "-h"}, exitOK, "Usage\n  igris check [--plan PATH]", ""},
		// The exit codes SPEC §14 lists for the command line itself.
		{"help flag", []string{"-h"}, exitOK, "Exit codes  0 ok · 1 failure · 2 usage error", ""},
		{"help command", []string{"help", "check"}, exitOK, "Usage\n  igris check [--plan PATH]", ""},
		{"help unknown command", []string{"help", "nope"}, exitUsage, "", "igris help: unknown command \"nope\""},
		{"help two commands", []string{"help", "check", "status"}, exitUsage, "", "igris help: unexpected argument \"status\"; name one command"},
		{"version -v", []string{"-v"}, exitOK, "igris dev", ""},
		{"version --version", []string{"--version"}, exitOK, "igris dev", ""},
		{"version extra arg", []string{"version", "x"}, exitUsage, "", "igris version: unexpected argument \"x\""},
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
	if strings.ContainsRune(errb.String(), 0x1b) || strings.ContainsRune(errb.String(), 0x07) || !strings.Contains(errb.String(), "unknown flag \"--x\\x1b]52;c;aGk\"; see `igris history -h`") {
		t.Errorf("stderr = %q", errb.String())
	}
}

// An unknown command is answered with one line, the closest command when
// there is one, never the whole help; the help never cites the SPEC
// (SPEC §14).
func TestUnknownCommandAndHelp(t *testing.T) {
	for _, tc := range []struct{ args, want string }{
		{"stauts", "igris: unknown command \"stauts\"; did you mean status?\n"},
		{"histroy", "igris: unknown command \"histroy\"; did you mean history?\n"},
		{"xyzzy", "igris: unknown command \"xyzzy\"; run `igris help`\n"},
		{"help stauts", "igris help: unknown command \"stauts\"; did you mean status?\n"},
		{"status --jsn", "igris status: unknown flag --jsn; see `igris status -h`\n"},
		{"history -n", "igris history: -n needs a value; see `igris history -h`\n"},
		{"status --plan", "igris status: --plan needs a value; see `igris status -h`\n"},
		{"history -n x", "igris history: invalid value \"x\" for -n: parse error; see `igris history -h`\n"},
	} {
		code, out, errb := runCmd(strings.Fields(tc.args)...)
		if code != exitUsage || out != "" || errb != tc.want {
			t.Errorf("igris %s: exit %d, stdout %q, stderr %q; want 2 and %q", tc.args, code, out, errb, tc.want)
		}
	}
	for _, args := range append([][]string{{"help"}}, func() [][]string {
		var all [][]string
		for _, c := range commands() {
			all = append(all, []string{c.name, "-h"})
		}
		return all
	}()...) {
		_, out, _ := runCmd(args...)
		if strings.Contains(out, "SPEC") || strings.Contains(out, "§") {
			t.Errorf("igris %v cites the SPEC:\n%s", args, out)
		}
		if regexp.MustCompile(`\n  -[a-z]{2,}`).MatchString(out) {
			t.Errorf("igris %v writes a flag with one dash:\n%s", args, out)
		}
	}
}
