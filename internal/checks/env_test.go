package checks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
)

func TestEnvironment(t *testing.T) {
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
		wantNext    []string
	}{
		{"all good", "", inside, clean, nil, nil, nil},
		{"api key", "sk-secret", inside, clean, []string{"ANTHROPIC_API_KEY is set"}, []bool{true}, []string{"unset ANTHROPIC_API_KEY"}},
		{"not a repo", "", gitAnswer{res: runner.Result{ExitCode: 128}}, clean, []string{"not a git repository"}, []bool{false}, []string{"git init"}},
		{"no git", "", gitAnswer{err: errors.New(`"git" not found in PATH`)}, clean, []string{"not a git repository"}, []bool{false}, []string{"git init"}},
		{"dirty", "", inside, dirty, []string{"uncommitted changes"}, []bool{false}, []string{"git status"}},
		{"status fails", "", inside, gitAnswer{res: runner.Result{ExitCode: 1}}, nil, nil, nil},
		{"everything", "x", inside, dirty, []string{"ANTHROPIC_API_KEY", "uncommitted"}, []bool{true, false}, []string{"unset ANTHROPIC_API_KEY", "git status"}},
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
			all := Run(context.Background(), Options{IDs: []string{IDGit, IDAPIKey}, Root: "/proj", Runner: r, Getenv: getenv})
			if len(all) != 2 || all[0].ID != IDAPIKey || all[1].ID != IDGit {
				t.Fatalf("results = %+v, want api-key then git", all)
			}
			got := Problems(all)
			if len(got) != len(tt.want) {
				t.Fatalf("warnings = %+v, want %d", got, len(tt.want))
			}
			for i, w := range got {
				if !strings.Contains(w.Message, tt.want[i]) || w.Confirm != tt.wantConfirm[i] || w.Next != tt.wantNext[i] || w.Level != Warn {
					t.Errorf("warning %d = %+v, want %q confirm=%v next=%q", i, w, tt.want[i], tt.wantConfirm[i], tt.wantNext[i])
				}
			}
			for _, w := range all {
				if strings.Contains(w.Message, "sk-secret") {
					t.Errorf("%s leaks the API key: %q", w.ID, w.Message)
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

type fakeHerdr struct {
	err  error
	hint string
}

func (f fakeHerdr) Name() string                           { return "herdr" }
func (f fakeHerdr) Available(context.Context) error        { return f.err }
func (f fakeHerdr) IntegrationHint(context.Context) string { return f.hint }

func TestHerdr(t *testing.T) {
	ids := []string{IDHerdrIntegration, IDBackend}
	ok := fakeHerdr{}
	got := Run(context.Background(), Options{IDs: ids, Backend: ok, Integration: ok})
	if len(got) != 2 || got[0].ID != IDBackend || len(Problems(got)) != 0 {
		t.Fatalf("all fine: %+v", got)
	}
	bad := fakeHerdr{err: errors.New("herdr is not installed\x1b[2J"), hint: "run `herdr integration install claude`"}
	got = Run(context.Background(), Options{IDs: ids, Backend: bad, Integration: bad})
	if got[0].Level != Fail || got[0].Message != "herdr is not installed" {
		t.Errorf("availability = %+v", got[0])
	}
	// Hooks cover a missing integration (SPEC §6.3): information only.
	if got[1].Level != OK || got[1].Message != bad.hint {
		t.Errorf("integration = %+v", got[1])
	}
	if got := Run(context.Background(), Options{IDs: ids}); len(got) != 0 {
		t.Errorf("no backend: %+v", got)
	}
}
