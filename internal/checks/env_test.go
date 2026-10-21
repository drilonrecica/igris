package checks

import (
	"context"
	"errors"
	"github.com/drilonrecica/igris/internal/config"
	"slices"
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

// The start-up git calls never get the variables the notification secrets
// come from: `git status` runs programs a repository's config names
// (core.fsmonitor), and a session can write that config (V06-07).
func TestGitWithoutSecrets(t *testing.T) {
	t.Setenv("NTFY_T", "s3cret")
	cfg := config.Default()
	cfg.Notify.Ntfy.Token = "env:NTFY_T"
	r := &runner.Fake{}
	r.Func(func(c runner.Cmd) (runner.Result, error) { return runner.Result{Stdout: []byte("true\n")}, nil })
	Run(context.Background(), Options{IDs: []string{IDGit}, Root: "/proj", Runner: r, Config: cfg})
	calls := r.Calls()
	if len(calls) == 0 {
		t.Fatal("git didn't run")
	}
	for _, c := range calls {
		if c.Env == nil || slices.ContainsFunc(c.Env, func(kv string) bool { return strings.HasPrefix(kv, "NTFY_T=") }) {
			t.Errorf("git %v env: nil %v, has the secret %v", c.Args, c.Env == nil, slices.Contains(c.Env, "NTFY_T=s3cret"))
		}
	}
}

// The igris in PATH, which sessions run igris done with, is this one, or
// a warning says which it is (V12-F); never a failure nor a confirmation.
func TestIgrisPath(t *testing.T) {
	const self, other = "/opt/igris/bin/igris", "/usr/local/bin/igris"
	tests := []struct {
		name    string
		inPath  string
		same    bool
		version string // what `<other> version` prints; "" fails
		this    string // this igris's version
		want    Level
		msg     string
	}{
		{"same file", self, true, "", "1.2.0", OK, "igris in PATH is this igris"},
		{"other with version", other, false, "igris 1.1.0\n", "1.2.0", Warn,
			"sessions run igris done with /usr/local/bin/igris (1.1.0), not this igris (1.2.0); put this igris first in PATH"},
		{"other without version", other, false, "", "1.2.0", Warn,
			"sessions run igris done with /usr/local/bin/igris, not this igris (1.2.0); put this igris first in PATH"},
		{"other, this version unknown", other, false, "igris 1.1.0", "", Warn,
			"sessions run igris done with /usr/local/bin/igris (1.1.0), not this igris; put this igris first in PATH"},
		{"missing", "", false, "", "1.2.0", Warn, "igris is not in PATH; sessions can't run igris done: put this igris in PATH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner.Fake{}
			if tt.version != "" {
				r.On([]string{other, "version"}, runner.Result{Stdout: []byte(tt.version)}, nil)
			} else {
				r.On([]string{other, "version"}, runner.Result{ExitCode: 2}, nil)
			}
			o := Options{IDs: []string{IDIgrisPath}, Runner: r, Version: tt.this, Self: func() (string, string, bool) { return self, tt.inPath, tt.same }}
			got := Run(context.Background(), o)
			if len(got) != 1 || got[0].ID != IDIgrisPath || got[0].Level != tt.want || got[0].Message != tt.msg || got[0].Confirm {
				t.Fatalf("results = %+v, want one %s %q", got, tt.want, tt.msg)
			}
			if calls := r.Calls(); (tt.inPath == other) != (len(calls) == 1) || len(calls) == 1 && calls[0].Timeout != versionTimeout {
				t.Errorf("version calls = %+v", calls)
			}
		})
	}
	// It comes first, and is left out without Self.
	o := Options{IDs: []string{IDAPIKey, IDIgrisPath}, Getenv: func(string) string { return "" }, Self: func() (string, string, bool) { return self, self, true }}
	if got := Run(context.Background(), o); len(got) != 2 || got[0].ID != IDIgrisPath {
		t.Errorf("order = %+v, want igris-path first", got)
	}
	if got := Run(context.Background(), Options{IDs: []string{IDIgrisPath}}); len(got) != 0 {
		t.Errorf("no Self: %+v", got)
	}
}
