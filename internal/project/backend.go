package project

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/herdr"
	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/state"
)

// ErrNoMultiplexer is returned by ResolveBackend for backend = "auto" when
// igris runs neither inside a herdr pane nor inside tmux and no tmux is in
// PATH to run the sessions headless (SPEC §11.3).
var ErrNoMultiplexer = fmt.Errorf("igris needs herdr or tmux (backend = %q found neither %s nor %s, and no tmux in PATH); install tmux 3.2 or newer and igris runs its sessions in a detached tmux session, or run igris inside a herdr pane", config.BackendAuto, herdr.WorkspaceEnv, tmux.Env)

// ResolveBackend returns the backend igris uses for name, the config's
// backend (SPEC §11.3): herdr or tmux as named, and for auto herdr inside a
// herdr pane, else tmux inside tmux, else (since 1.9) tmux run headless
// when lookPath finds tmux, else ErrNoMultiplexer. A nil lookPath is
// exec.LookPath.
func ResolveBackend(name string, getenv func(string) string, lookPath func(string) (string, error)) (string, error) {
	switch name {
	case herdr.Name, tmux.Name:
		return name, nil
	case config.BackendAuto, "":
		switch {
		case getenv(herdr.WorkspaceEnv) != "":
			return herdr.Name, nil
		case getenv(tmux.Env) != "":
			return tmux.Name, nil
		}
		if lookPath == nil {
			lookPath = exec.LookPath
		}
		if _, err := lookPath(tmux.Name); err == nil {
			return tmux.Name, nil
		}
		return "", ErrNoMultiplexer
	}
	return "", fmt.Errorf("unknown backend %q in igris.toml; use \"auto\", \"herdr\" or \"tmux\"", name)
}

// BackendName is ResolveBackend's name, "" when none can be chosen.
func BackendName(cfg *config.Config, getenv func(string) string, lookPath func(string) (string, error)) string {
	name, err := ResolveBackend(cfg.Backend, getenv, lookPath)
	if err != nil {
		return ""
	}
	return name
}

// headless says the tmux backend runs headless, in the detached session of
// the project's (SPEC §11.5): outside herdr and tmux, for backend auto
// (since 1.9) and for igris serve (serve) with an explicit tmux.
func headless(cfg *config.Config, getenv func(string) string, serve bool) bool {
	if getenv(tmux.Env) != "" || getenv(herdr.WorkspaceEnv) != "" {
		return false
	}
	return serve || cfg.Backend == config.BackendAuto || cfg.Backend == ""
}

// NewBackend returns the backend the config chooses for the project at
// root, reading its sessions' hook state from root's .igris/ (SPEC §6.3).
// Outside herdr and tmux, tmux runs headless in a detached session of the
// project's (SPEC §11.3, §11.5): always for backend auto, and for an
// explicit tmux in igris serve (env.Serve).
func NewBackend(cfg *config.Config, root string, env Env) (backend.Backend, error) {
	getenv := env.getenv()
	name, err := ResolveBackend(cfg.Backend, getenv, env.lookPath())
	if err != nil {
		return nil, err
	}
	hooks := state.HookStates(root)
	if name == tmux.Name && headless(cfg, getenv, env.Serve) {
		return tmux.NewHeadless(env.runner(), root).WithHookStates(hooks), nil
	}
	if name == tmux.Name {
		return tmux.NewFromEnv(env.runner(), getenv).WithHookStates(hooks), nil
	}
	return herdr.NewFromEnv(env.runner(), getenv).WithHookStates(hooks), nil
}

// SessionHost is where be's sessions live, for the snapshot's session_host
// (SPEC §13, since 1.9): what a backend that can say (backend.Hoster)
// says, else its name. A Hoster that can't tell gives nil and the error:
// clients then focus as igris 1.4 did.
func SessionHost(ctx context.Context, be backend.Backend) (*backend.Host, error) {
	hb, ok := be.(backend.Hoster)
	if !ok {
		return &backend.Host{Backend: be.Name()}, nil
	}
	h, err := hb.Host(ctx)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// Place is where a client runs, for comparing with a run's session_host
// (SPEC §15.3): herdr in a herdr pane, tmux with its server's socket in a
// tmux client (herdr first, as auto chooses), else nowhere.
func Place(getenv func(string) string) backend.Host {
	switch {
	case getenv(herdr.WorkspaceEnv) != "":
		return backend.Host{Backend: herdr.Name}
	case getenv(tmux.Env) != "":
		return backend.Host{Backend: tmux.Name, Socket: tmux.SocketOf(getenv(tmux.Env))}
	}
	return backend.Host{}
}
