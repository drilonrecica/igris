package project

import (
	"context"
	"errors"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/runner"
)

// backend = "auto" picks herdr inside a herdr pane, else tmux inside tmux
// (SPEC §11.3).
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
	tests := []struct {
		name, backend string
		getenv        func(string) string
		want          string
		err           error
	}{
		{"auto in herdr", "auto", env("HERDR_WORKSPACE_ID", "w1"), "herdr", nil},
		{"auto in tmux", "auto", env("TMUX", "/tmp/tmux-1000/default,1,0"), "tmux", nil},
		{"auto in tmux inside herdr", "auto", both, "herdr", nil},
		{"auto in neither", "auto", env(), "", ErrNoMultiplexer},
		{"empty is auto", "", env("TMUX", "x"), "tmux", nil},
		{"explicit herdr", "herdr", env("TMUX", "x"), "herdr", nil},
		{"explicit tmux", "tmux", env(), "tmux", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveBackend(tt.backend, tt.getenv)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Errorf("ResolveBackend(%q) = %q, %v; want %q, %v", tt.backend, got, err, tt.want, tt.err)
			}
		})
	}
	if _, err := ResolveBackend("zellij", env()); err == nil {
		t.Error("zellij accepted")
	}

	cfg := config.Default()
	for getenv, want := range map[string]string{"TMUX": "tmux", "HERDR_WORKSPACE_ID": "herdr"} {
		be, err := NewBackend(cfg, t.TempDir(), Env{Getenv: env(getenv, "x"), Runner: &runner.Fake{}})
		if err != nil || be.Name() != want {
			t.Errorf("%s: NewBackend = %v, %v; want %s", getenv, be, err, want)
		}
	}
	if _, err := NewBackend(cfg, t.TempDir(), Env{Getenv: env(), Runner: &runner.Fake{}}); !errors.Is(err, ErrNoMultiplexer) {
		t.Errorf("outside both: %v", err)
	}

	// igris serve outside both runs tmux headless; inside tmux or with herdr
	// it is as before (SPEC §11.3, §11.5).
	for _, tt := range []struct {
		name, backend string
		getenv        func(string) string
		want          string
		headless      bool
	}{
		{"serve auto in neither", "auto", env(), "tmux", true},
		{"serve tmux in neither", "tmux", env(), "tmux", true},
		{"serve auto in tmux", "auto", env("TMUX", "x"), "tmux", false},
		{"serve auto in herdr", "auto", env("HERDR_WORKSPACE_ID", "w1"), "herdr", false},
		{"serve herdr in neither", "herdr", env(), "herdr", false},
	} {
		cfg := config.Default()
		cfg.Backend = tt.backend
		be, err := NewBackend(cfg, "/w/proj", Env{Getenv: tt.getenv, Runner: &runner.Fake{}, Serve: true})
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
