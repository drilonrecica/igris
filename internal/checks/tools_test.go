package checks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want [3]int
		ok   bool
	}{
		{"2.1.292 (Claude Code)\n", [3]int{2, 1, 292}, true},
		{"herdr 0.9.1\n", [3]int{0, 9, 1}, true},
		{"v1.2.3-beta.4", [3]int{1, 2, 3}, true},
		{"tool 3.4", [3]int{3, 4, 0}, true},
		{"", [3]int{}, false},
		{"no version here", [3]int{}, false},
		{"version 7", [3]int{}, false},
		{"99999999999999999999.1.1", [3]int{}, false},
	}
	for _, tt := range tests {
		got, ok := parseVersion(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("parseVersion(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// Every version in Tools must parse, and Min must not be newer than Tested.
func TestToolsTable(t *testing.T) {
	for _, tl := range Tools {
		minV, ok1 := parseVersion(tl.Min)
		tested, ok2 := parseVersion(tl.Tested)
		if !ok1 || !ok2 || compareVersions(minV, tested) > 0 {
			t.Errorf("%s: Min %q, Tested %q", tl.Name, tl.Min, tl.Tested)
		}
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestToolVersions(t *testing.T) {
	type answer struct {
		res runner.Result
		err error
	}
	out := func(s string) answer { return answer{res: runner.Result{Stdout: []byte(s)}} }
	claudeOK := answer{res: runner.Result{Stdout: fixture(t, "version_claude.txt")}}
	herdrOK := answer{res: runner.Result{Stdout: fixture(t, "version_herdr.txt")}}
	notFound := answer{err: fmt.Errorf("run claude --version: %q not found in PATH; install it or fix PATH: %w", "claude", exec.ErrNotFound)}
	timeout := answer{err: fmt.Errorf("run claude --version: %w after 5s", runner.ErrTimeout)}
	tests := []struct {
		name          string
		claude, herdr answer
		want          []string // a substring per warning, in order
	}{
		{"recorded versions", claudeOK, herdrOK, nil},
		{"equal to Min", out("2.1.291 (Claude Code)"), out("herdr 0.9.1"), nil},
		{"newer patch and minor", out("2.9.1000 (Claude Code)"), out("herdr 0.12.0"), nil},
		{"older", out("2.0.5 (Claude Code)"), out("herdr 0.8.9"), []string{
			"Claude Code 2.0.5 is older than 2.1.291", "herdr 0.8.9 is older than 0.9.1"}},
		{"newer major", out("3.0.0 (Claude Code)"), out("herdr 1.0.0"), []string{
			"Claude Code 3.0.0 is a newer major version", "herdr 1.0.0 is a newer major version than igris was verified with (0.9.1)"}},
		{"garbage", out("Claude Code, latest"), herdrOK, []string{"couldn't determine the Claude Code version (no version in the output"}},
		{"exit 1", answer{res: runner.Result{ExitCode: 1, Stdout: []byte("1.0.0")}}, herdrOK, []string{"exited with 1"}},
		{"not found", notFound, herdrOK, []string{"claude not found in PATH; igris arise needs Claude Code 2.1.291 or later"}},
		{"timeout", timeout, herdrOK, []string{"`claude --version` took longer than 5s"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner.Fake{}
			r.On([]string{"claude", "--version"}, tt.claude.res, tt.claude.err)
			r.On([]string{"herdr", "--version"}, tt.herdr.res, tt.herdr.err)
			r.On([]string{"tmux", "-V"}, runner.Result{Stdout: fixture(t, "version_tmux.txt")}, nil)
			all := ToolVersions(context.Background(), r)
			if len(all) != len(Tools) || all[0].ID != IDClaude || all[1].ID != IDHerdr || all[2].ID != IDTmux {
				t.Fatalf("results = %+v, want one per tool", all)
			}
			got := Problems(all)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d warnings %+v, want %d", len(got), got, len(tt.want))
			}
			for i, w := range tt.want {
				if got[i].Level != Warn || !strings.Contains(got[i].Message, w) {
					t.Errorf("warning %d = %+v, want it to contain %q", i, got[i], w)
				}
			}
			for _, res := range all {
				if res.Level == OK && !strings.Contains(res.Message, ".") {
					t.Errorf("ok result without a version: %+v", res)
				}
			}
			for _, c := range r.Calls() {
				if c.Timeout != versionTimeout {
					t.Errorf("%s: timeout %s", c, c.Timeout)
				}
			}
		})
	}
}

// tmux prints its version with -V (V03-P2).
func TestTmuxVersion(t *testing.T) {
	for in, warn := range map[string]bool{"tmux 3.7c\n": false, "tmux 3.1b\n": true, "tmux next-3.8\n": false} {
		r := &runner.Fake{}
		r.Func(func(c runner.Cmd) (runner.Result, error) {
			return runner.Result{Stdout: []byte("2.1.292\n0.9.1\n")}, nil
		})
		r.On([]string{"tmux", "-V"}, runner.Result{Stdout: []byte(in)}, nil)
		got := Pick(ToolVersions(context.Background(), r), IDTmux)
		if len(got) != 1 || (got[0].Level == Warn) != warn {
			t.Errorf("%q: %+v, want warn=%v", in, got, warn)
		}
	}
}
