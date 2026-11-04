package project

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/runner"
)

// backend = "auto" picks herdr inside a herdr pane, else tmux inside tmux,
// else (since 1.9) tmux headless when tmux is in PATH (SPEC §11.3).
func TestResolveBackend(t *testing.T) {
	env := func(vars ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(vars); i += 2 {
				if vars[i] == k {
					return vars[i+1]
				}
			}
			return ""
		}
	}
	both := env("HERDR_WORKSPACE_ID", "w1", "TMUX", "/tmp/tmux-1000/default,1,0")
	noTmux := func(string) (string, error) { return "", exec.ErrNotFound }
	hasTmux := func(name string) (string, error) {
		if name == "tmux" {
			return "/usr/bin/tmux", nil
		}
		return "", exec.ErrNotFound
	}
	tests := []struct {
		name, backend string
		getenv        func(string) string
		lookPath      func(string) (string, error)
		want          string
		err           error
	}{
		{"auto in herdr", "auto", env("HERDR_WORKSPACE_ID", "w1"), noTmux, "herdr", nil},
		{"auto in tmux", "auto", env("TMUX", "/tmp/tmux-1000/default,1,0"), noTmux, "tmux", nil},
		{"auto in tmux inside herdr", "auto", both, hasTmux, "herdr", nil},
		{"auto in neither, tmux installed", "auto", env(), hasTmux, "tmux", nil},
		{"auto in neither, no tmux", "auto", env(), noTmux, "", ErrNoMultiplexer},
		{"empty is auto", "", env("TMUX", "x"), noTmux, "tmux", nil},
		{"explicit herdr", "herdr", env("TMUX", "x"), hasTmux, "herdr", nil},
		{"explicit tmux", "tmux", env(), noTmux, "tmux", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveBackend(tt.backend, tt.getenv, tt.lookPath)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Errorf("ResolveBackend(%q) = %q, %v; want %q, %v", tt.backend, got, err, tt.want, tt.err)
			}
		})
	}
	if _, err := ResolveBackend("zellij", env(), hasTmux); err == nil {
		t.Error("zellij accepted")
	}

	cfg := config.Default()
	for getenv, want := range map[string]string{"TMUX": "tmux", "HERDR_WORKSPACE_ID": "herdr"} {
		be, err := NewBackend(cfg, t.TempDir(), Env{Getenv: env(getenv, "x"), Runner: &runner.Fake{}, LookPath: noTmux})
		if err != nil || be.Name() != want {
			t.Errorf("%s: NewBackend = %v, %v; want %s", getenv, be, err, want)
		}
	}
	if _, err := NewBackend(cfg, t.TempDir(), Env{Getenv: env(), Runner: &runner.Fake{}, LookPath: noTmux}); !errors.Is(err, ErrNoMultiplexer) {
		t.Errorf("outside both without tmux: %v", err)
	}

	// Outside both, auto runs tmux headless wherever tmux is installed
	// (since 1.9, serve and arise alike), and serve runs an explicit tmux
	// headless as since 1.4; inside tmux or with herdr it is as before
	// (SPEC §11.3, §11.5).
	for _, tt := range []struct {
		name, backend string
		serve         bool
		getenv        func(string) string
		want          string
		headless      bool
	}{
		{"serve auto in neither", "auto", true, env(), "tmux", true},
		{"arise auto in neither", "auto", false, env(), "tmux", true},
		{"serve tmux in neither", "tmux", true, env(), "tmux", true},
		{"arise tmux in neither", "tmux", false, env(), "tmux", false},
		{"serve auto in tmux", "auto", true, env("TMUX", "x"), "tmux", false},
		{"arise auto in tmux", "auto", false, env("TMUX", "x"), "tmux", false},
		{"serve auto in herdr", "auto", true, env("HERDR_WORKSPACE_ID", "w1"), "herdr", false},
		{"serve herdr in neither", "herdr", true, env(), "herdr", false},
	} {
		cfg := config.Default()
		cfg.Backend = tt.backend
		be, err := NewBackend(cfg, "/w/proj", Env{Getenv: tt.getenv, Runner: &runner.Fake{}, Serve: tt.serve, LookPath: hasTmux})
		if err != nil || be.Name() != tt.want {
			t.Errorf("%s: NewBackend = %v, %v; want %s", tt.name, be, err, tt.want)
			continue
		}
		if tb, ok := be.(*tmux.Backend); ok && (tb.Headless() != "") != tt.headless {
			t.Errorf("%s: headless session %q", tt.name, tb.Headless())
		}
	}
}

// A client's place: herdr first, as auto chooses, then tmux with its
// server's socket, else nowhere (SPEC §15.3).
func TestPlace(t *testing.T) {
	get := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	for _, tt := range []struct {
		vars map[string]string
		want backend.Host
	}{
		{map[string]string{"TMUX": "/tmp/tmux-1000/work,4242,1"}, backend.Host{Backend: "tmux", Socket: "/tmp/tmux-1000/work"}},
		{map[string]string{"HERDR_WORKSPACE_ID": "w1", "TMUX": "/s,1,0"}, backend.Host{Backend: "herdr"}},
		{nil, backend.Host{}},
	} {
		if got := Place(get(tt.vars)); got != tt.want {
			t.Errorf("Place(%v) = %+v, want %+v", tt.vars, got, tt.want)
		}
	}
}

// serve says where the sessions live: a tmux backend inside tmux asks its
// server; a backend that can't say is named; one that fails gives none.
func TestSessionHost(t *testing.T) {
	ctx := context.Background()
	f := &runner.Fake{}
	f.On([]string{"tmux", "display-message", "-p", "#{session_name}"}, runner.Result{Stdout: []byte("main\n")}, nil)
	be := tmux.NewFromEnv(f, func(k string) string { return map[string]string{"TMUX": "/tmp/tmux-1000/default,1,0"}[k] })
	if h, err := SessionHost(ctx, be); err != nil || *h != (backend.Host{Backend: "tmux", Socket: "/tmp/tmux-1000/default", Session: "main"}) {
		t.Errorf("tmux: %+v, %v", h, err)
	}
	if h, err := SessionHost(ctx, fake.New()); err != nil || *h != (backend.Host{Backend: "fake"}) {
		t.Errorf("fake: %+v, %v", h, err)
	}
	broken := &runner.Fake{}
	broken.On([]string{"tmux"}, runner.Result{Stderr: []byte("no server running on /tmp/tmux-1000/default\n"), ExitCode: 1}, nil)
	if h, err := SessionHost(ctx, tmux.NewFromEnv(broken, func(string) string { return "/tmp/tmux-1000/default,1,0" })); err == nil || h != nil {
		t.Errorf("unreachable server: %+v, %v", h, err)
	}
}

// Since 1.9, backend = "auto" outside herdr and tmux runs a run's sessions
// headless when tmux is installed, as an explicit tmux does in serve: the
// engine arise and home build gets the headless backend and doctor says
// where the sessions go; without tmux both say what to install (SPEC §11.3,
// decisions.md V19-P1).
func TestAutoOutsideHerdrAndTmux(t *testing.T) {
	ctx := context.Background()
	root := newProject(t, "")
	for _, installed := range []bool{true, false} {
		env, _ := testEnv(nil)
		env.Backend = nil // the real choice
		f := &runner.Fake{}
		f.On([]string{"tmux", "-V"}, runner.Result{Stdout: []byte("tmux 3.7\n")}, nil)
		env.Runner = f
		if installed {
			env.LookPath = func(name string) (string, error) {
				if name == "tmux" {
					return "/usr/bin/tmux", nil
				}
				return "", exec.ErrNotFound
			}
		}
		for _, serve := range []bool{false, true} {
			env.Serve = serve
			p, err := Load(root, "", env)
			if err != nil {
				t.Fatal(err)
			}
			l, err := p.Launch(report.RunRequest{Phase: "M0"})
			if !installed {
				if !errors.Is(err, ErrNoMultiplexer) || !strings.Contains(err.Error(), "install tmux") {
					t.Errorf("serve %v, no tmux: Launch = %v, want the error naming tmux to install", serve, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("serve %v: Launch = %v", serve, err)
			}
			tb, ok := l.Backend.(*tmux.Backend)
			if !ok || tb.Headless() != tmux.SessionName(root) {
				t.Errorf("serve %v: backend %T, not headless in %s", serve, l.Backend, tmux.SessionName(root))
			}
			if s := p.Snapshot(); s.Backend != "tmux" {
				t.Errorf("serve %v: home names the backend %q", serve, s.Backend)
			}
		}
		var got checks.Result
		for _, r := range Doctor(ctx, root, env) {
			if r.ID == checks.IDBackend {
				got = r
			}
		}
		switch {
		case installed && (got.Level != checks.OK || !strings.Contains(got.Message, tmux.SessionName(root))):
			t.Errorf("doctor with tmux: %v %q", got.Level, got.Message)
		case !installed && (got.Level == checks.OK || !strings.Contains(got.Message, "install tmux")):
			t.Errorf("doctor without tmux: %v %q", got.Level, got.Message)
		}
	}
}
