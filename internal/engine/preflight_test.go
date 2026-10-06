package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
)

func TestPreflight(t *testing.T) {
	type gitAnswer struct {
		res runner.Result
		err error
	}
	inside := gitAnswer{res: runner.Result{Stdout: []byte("true\n")}}
	clean := gitAnswer{}
	dirty := gitAnswer{res: runner.Result{Stdout: []byte(" M a.go\n?? b.go\n")}}
	tests := []struct {
		name        string
		apiKey      string
		revParse    gitAnswer
		status      gitAnswer
		want        []string // a substring per warning, in order
		wantConfirm []bool
	}{
		{"all good", "", inside, clean, nil, nil},
		{"api key", "sk-secret", inside, clean, []string{"ANTHROPIC_API_KEY is set"}, []bool{true}},
		{"not a repo", "", gitAnswer{res: runner.Result{ExitCode: 128}}, clean, []string{"not a git repository"}, []bool{false}},
		{"no git", "", gitAnswer{err: errors.New(`"git" not found in PATH`)}, clean, []string{"not a git repository"}, []bool{false}},
		{"dirty", "", inside, dirty, []string{"uncommitted changes"}, []bool{false}},
		{"status fails", "", inside, gitAnswer{res: runner.Result{ExitCode: 1}}, nil, nil},
		{"everything", "x", inside, dirty, []string{"ANTHROPIC_API_KEY", "uncommitted"}, []bool{true, false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner.Fake{}
			r.On([]string{"git", "rev-parse", "--is-inside-work-tree"}, tt.revParse.res, tt.revParse.err)
			r.On([]string{"git", "status", "--porcelain"}, tt.status.res, tt.status.err)
			getenv := func(k string) string {
				if k == APIKeyVar {
					return tt.apiKey
				}
				return ""
			}
			got := Preflight(context.Background(), r, "/proj", getenv)
			if len(got) != len(tt.want) {
				t.Fatalf("warnings = %+v, want %d", got, len(tt.want))
			}
			for i, w := range got {
				if !strings.Contains(w.Text, tt.want[i]) || w.Confirm != tt.wantConfirm[i] {
					t.Errorf("warning %d = %+v, want %q confirm=%v", i, w, tt.want[i], tt.wantConfirm[i])
				}
				if strings.Contains(w.Text, "sk-secret") {
					t.Errorf("warning %d leaks the API key: %q", i, w.Text)
				}
			}
			for _, c := range r.Calls() {
				if c.Dir != "/proj" || c.Timeout <= 0 {
					t.Errorf("git call %s in %q, timeout %s", c, c.Dir, c.Timeout)
				}
			}
		})
	}
}
