package project

import (
	"errors"
	"testing"

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
}
