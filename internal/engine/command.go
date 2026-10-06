package engine

import (
	"context"
	"time"
)

// CommandKind says what the owner asked for.
type CommandKind int

// Owner commands.
const (
	// CmdPause toggles pause-after-task (SPEC §15.3): while it is on, the
	// current task finishes normally but no further task is launched; the
	// run stays alive and continues when it is toggled off.
	CmdPause CommandKind = iota + 1
	// CmdStop ends the run now. A running session is left open and the state
	// is kept, so a later `igris arise` can pick it up (SPEC §13).
	CmdStop
)

// Command is an owner action sent to a running engine.
type Command struct {
	Kind CommandKind
}

// Send queues c for the run. It may be called from any goroutine, including
// from the Events callback; it never blocks and never drops a command.
func (e *Engine) Send(c Command) {
	e.mu.Lock()
	e.queue = append(e.queue, c)
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// drain applies the queued commands in order.
func (e *Engine) drain() {
	e.mu.Lock()
	queue := e.queue
	e.queue = nil
	e.mu.Unlock()
	for _, c := range queue {
		switch c.Kind {
		case CmdPause:
			e.pause = !e.pause
			if e.pause {
				e.emit(Event{Kind: PauseOn})
			} else {
				e.emit(Event{Kind: PauseOff})
			}
		case CmdStop:
			e.stop = true
		}
	}
}

// stopping applies pending commands and reports whether the run should end
// now: the owner sent stop or ctx is done.
func (e *Engine) stopping(ctx context.Context) bool {
	e.drain()
	return e.stop || ctx.Err() != nil
}

// wait blocks for d, or less if a command arrives or ctx ends. Every loop
// that waits goes through here, so commands are never left sitting for a
// whole poll interval.
func (e *Engine) wait(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-e.wake:
	case <-e.clock.After(d):
	}
}
