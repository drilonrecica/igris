// Package tui is igris's terminal UI (SPEC §15): a Bubble Tea program fed
// by the engine's events that sends the owner's actions back as engine
// commands.
package tui

import (
	"context"
	"errors"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
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
	// Theme is [tui] theme: "dark" or "light" colors, or "auto" (also "")
	// to go by the terminal's background (SPEC §15.4).
	Theme string
	// RankColors is [tui.rank_colors]: rank -> "#rrggbb" or an ANSI number.
	RankColors map[string]string

	// PlanPath is the plan the task list shows; it is re-read when the run
	// changes it.
	PlanPath    string
	PlanOptions plan.Options

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
	m := newModel(ctx, opts)
	// Bubble Tea asked the terminal for its background when the process
	// started, so "auto" costs no query here.
	m.th = newTheme(lipgloss.NewRenderer(os.Stdout), darkTheme(opts.Theme, lipgloss.HasDarkBackground), opts.RankColors)
	p := tea.NewProgram(m, programOptions(ctx, opts.Mouse)...)
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // ctx ended: not a failure of the TUI
	}
	return err
}

// darkTheme reports whether to use the colors for a dark terminal: as the
// owner set it, else as detected.
func darkTheme(setting string, detect func() bool) bool {
	switch setting {
	case "dark":
		return true
	case "light":
		return false
	}
	return detect()
}

// programOptions are the Bubble Tea options for a run.
func programOptions(ctx context.Context, mouse bool) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	if mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	return opts
}
