package runner

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Fake is a Runner for tests. It answers commands from scripted responses
// registered with On, falling back to the function set with Func, and
// records every call. It is safe for concurrent use.
type Fake struct {
	mu       sync.Mutex
	steps    []*fakeStep
	fallback func(Cmd) (Result, error)
	calls    []Cmd
}

type fakeStep struct {
	prefix []string // argv prefix, Name first
	res    Result
	err    error
	used   bool
}

// On registers a one-shot response for the next command whose argv (Name
// followed by Args) starts with argvPrefix. Responses are consumed in the
// order they were registered.
func (f *Fake) On(argvPrefix []string, res Result, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, &fakeStep{prefix: slices.Clone(argvPrefix), res: res, err: err})
}

// Func sets the handler for commands that match no On response.
func (f *Fake) Func(fn func(Cmd) (Result, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallback = fn
}

// Calls returns every command Run was called with, in order.
func (f *Fake) Calls() []Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Run validates c like Exec does, records it and returns the scripted
// response. Commands nobody scripted are an error naming the command.
func (f *Fake) Run(ctx context.Context, c Cmd) (Result, error) {
	if err := validate(c); err != nil {
		return Result{Cmd: c, ExitCode: -1}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{Cmd: c, ExitCode: -1}, fmt.Errorf("run %s: %w", c, err)
	}

	f.mu.Lock()
	f.calls = append(f.calls, c)
	argv := append([]string{c.Name}, c.Args...)
	var step *fakeStep
	for _, s := range f.steps {
		if !s.used && hasPrefix(argv, s.prefix) {
			s.used = true
			step = s
			break
		}
	}
	fallback := f.fallback
	f.mu.Unlock()

	var res Result
	var err error
	switch {
	case step != nil:
		res, err = step.res, step.err
	case fallback != nil:
		res, err = fallback(c)
	default:
		return Result{Cmd: c, ExitCode: -1}, fmt.Errorf("fake runner: unexpected command %s", c)
	}
	res.Cmd = c
	return res, err
}

func hasPrefix(argv, prefix []string) bool {
	return len(prefix) <= len(argv) && slices.Equal(argv[:len(prefix)], prefix)
}
