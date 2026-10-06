// Package tui is igris's terminal UI (SPEC §15): a Bubble Tea program fed
// by the engine's events that sends the owner's actions back as engine
// commands.
package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
)

// Sender takes the owner's commands; *engine.Engine is one.
type Sender interface {
	Send(engine.Command)
}

// Options configures the TUI of one run.
type Options struct {
	Project string // shown in the header, e.g. the project directory name
	Backend string // backend name for the header
	Mode    string // the run mode chosen for the run; "" for none
	Mouse   bool   // [tui] mouse: click, tap and wheel (SPEC §15.5)

	Feed   *Feed  // the run's events
	Sender Sender // where the owner's commands go
	// Focus brings a session's pane to the front (Open session).
	Focus func(context.Context, backend.SessionRef) error
	// Now is the clock for elapsed times; nil means time.Now.
	Now func() time.Time
}

// Run shows the TUI until the owner quits or ctx ends. Quitting never stops
// the run by itself; the caller decides what follows.
func Run(ctx context.Context, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // ends the wait on the feed
	p := tea.NewProgram(newModel(ctx, opts), programOptions(ctx, opts.Mouse)...)
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // ctx ended: not a failure of the TUI
	}
	return err
}

// programOptions are the Bubble Tea options for a run.
func programOptions(ctx context.Context, mouse bool) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	if mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	return opts
}
