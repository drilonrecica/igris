package tmux

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
)

// sim is a tmux server for tests: it answers the commands the backend
// sends with the output and error shapes recorded in testdata/ (V03-P2),
// and holds the hook states a real Claude Code would write.
type sim struct {
	mu      sync.Mutex
	next    int
	windows map[string]*simPane // by window ID
	panes   map[string]*simPane // by pane ID
	buffers map[string]string
	hooks   map[string]backend.AgentState // by Claude session UUID
	toasts  []string
	// startHook is the state the hooks report as soon as a window opens
	// with that session's UUID; "" leaves the session without a hook
	// (a startup question).
	startHook backend.AgentState
}

type simPane struct {
	window, pane, name string
	argv               []string
	dead               bool
	pasted             []string // prompts submitted with Enter
	typed              string   // pasted, not yet submitted
}

func newSim() *sim {
	return &sim{
		windows: map[string]*simPane{}, panes: map[string]*simPane{},
		buffers: map[string]string{}, hooks: map[string]backend.AgentState{},
		startHook: backend.Idle,
	}
}

func (s *sim) runner() *runner.Fake {
	f := &runner.Fake{}
	f.Func(s.run)
	return f
}

// backend returns a tmux backend inside this server that never sleeps.
func (s *sim) backend() *Backend {
	b := New(s.runner(), true).WithHookStates(s.hookState)
	b.sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	return b
}

func (s *sim) hookState(uuid string) (backend.AgentState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.hooks[uuid]
	return st, ok
}

func (s *sim) setHook(uuid string, st backend.AgentState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks[uuid] = st
}

func (s *sim) pane(id string) *simPane {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.panes[id]
}

func (s *sim) kill(paneID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.panes[paneID]; p != nil {
		delete(s.windows, p.window)
		delete(s.panes, paneID)
	}
}

func (s *sim) die(paneID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.panes[paneID]; p != nil {
		p.dead = true
	}
}

func errResult(msg string) (runner.Result, error) {
	return runner.Result{Stderr: []byte(msg + "\n"), ExitCode: 1}, nil
}

func flag(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}

func (s *sim) run(c runner.Cmd) (runner.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := c.Args
	switch a[0] {
	case "new-window":
		s.next++
		p := &simPane{window: fmt.Sprintf("@%d", s.next), pane: fmt.Sprintf("%%%d", s.next), name: flag(a, "-n")}
		for i, x := range a {
			if x == "--" {
				p.argv = a[i+1:]
			}
		}
		s.windows[p.window], s.panes[p.pane] = p, p
		if s.startHook != "" {
			s.hooks[flag(p.argv, "--session-id")] = s.startHook
		}
		return runner.Result{Stdout: []byte(p.window + " " + p.pane + "\n")}, nil
	case "set-option":
		return runner.Result{}, nil
	case "list-panes":
		p := s.panes[flag(a, "-t")]
		if p == nil {
			return errResult("can't find pane: " + flag(a, "-t"))
		}
		dead := "0"
		if p.dead {
			dead = "1"
		}
		return runner.Result{Stdout: []byte(p.pane + "\t" + dead + "\t0\tclaude\n")}, nil
	case "load-buffer":
		b, _ := io.ReadAll(c.Stdin)
		s.buffers[flag(a, "-b")] = string(b)
		return runner.Result{}, nil
	case "paste-buffer":
		p := s.panes[flag(a, "-t")]
		if p == nil {
			return errResult("can't find pane: " + flag(a, "-t"))
		}
		text, ok := s.buffers[flag(a, "-b")]
		if !ok {
			return errResult("no buffer " + flag(a, "-b"))
		}
		delete(s.buffers, flag(a, "-b"))
		p.typed += text
		return runner.Result{}, nil
	case "send-keys":
		p := s.panes[flag(a, "-t")]
		if p == nil {
			return errResult("can't find pane: " + flag(a, "-t"))
		}
		p.pasted, p.typed = append(p.pasted, p.typed), ""
		return runner.Result{}, nil
	case "select-window", "kill-window":
		p := s.windows[flag(a, "-t")]
		if p == nil {
			return errResult("can't find window: " + flag(a, "-t"))
		}
		if a[0] == "kill-window" {
			delete(s.windows, p.window)
			delete(s.panes, p.pane)
		}
		return runner.Result{}, nil
	case "display-message":
		if flag(a, "-p") == "#{version}" {
			return runner.Result{Stdout: []byte("3.7c\n")}, nil
		}
		s.toasts = append(s.toasts, a[len(a)-1])
		return runner.Result{}, nil
	}
	return runner.Result{}, fmt.Errorf("sim: unexpected tmux %s", strings.Join(a, " "))
}
