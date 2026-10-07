package project

import (
	"fmt"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/herdr"
	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/state"
)

// ErrNoMultiplexer is returned by ResolveBackend for backend = "auto" when
// igris runs neither inside a herdr pane nor inside tmux.
var ErrNoMultiplexer = fmt.Errorf("igris must run inside a herdr pane or a tmux session (backend = %q found neither %s nor %s); start herdr or tmux and run igris there, or set backend in igris.toml", config.BackendAuto, herdr.WorkspaceEnv, tmux.Env)

// ResolveBackend returns the backend igris uses for name, the config's
// backend (SPEC §11.3): herdr or tmux as named, and for auto herdr inside a
// herdr pane, else tmux inside tmux, else ErrNoMultiplexer.
func ResolveBackend(name string, getenv func(string) string) (string, error) {
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
		return "", ErrNoMultiplexer
	}
	return "", fmt.Errorf("unknown backend %q in igris.toml; use \"auto\", \"herdr\" or \"tmux\"", name)
}

// BackendName is ResolveBackend's name, "" when none can be chosen.
func BackendName(cfg *config.Config, getenv func(string) string) string {
	name, err := ResolveBackend(cfg.Backend, getenv)
	if err != nil {
		return ""
	}
	return name
}

// NewBackend returns the backend the config chooses for the project at
// root, reading its sessions' hook state from root's .igris/ (SPEC §6.3).
func NewBackend(cfg *config.Config, root string, env Env) (backend.Backend, error) {
	name, err := ResolveBackend(cfg.Backend, env.getenv())
	if err != nil {
		return nil, err
	}
	hooks := state.HookStates(root)
	if name == tmux.Name {
		return tmux.NewFromEnv(env.runner(), env.getenv()).WithHookStates(hooks), nil
	}
	return herdr.NewFromEnv(env.runner(), env.getenv()).WithHookStates(hooks), nil
}
