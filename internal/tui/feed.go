package tui

import (
	"context"
	"sync"

	"github.com/drilonrecica/igris/internal/engine"
)

// Feed carries a run's events from the engine to the TUI. Push never
// blocks the engine, and events pushed before the TUI starts are kept, so
// the caller can wait for the run to start before taking over the terminal.
type Feed struct {
	mu     sync.Mutex
	events []engine.Event // not yet delivered
	ended  bool           // End was called; delivered after the events
	res    engine.Result
	err    error

	wake      chan struct{}
	startedCh chan struct{}
	endedCh   chan struct{}
	startOnce sync.Once
	endOnce   sync.Once
}

// NewFeed returns an empty feed.
func NewFeed() *Feed {
	return &Feed{
		wake:      make(chan struct{}, 1),
		startedCh: make(chan struct{}),
		endedCh:   make(chan struct{}),
	}
}

// Push queues ev. Use it as engine.Options.Events.
func (f *Feed) Push(ev engine.Event) {
	f.mu.Lock()
	f.events = append(f.events, ev)
	f.mu.Unlock()
	f.startOnce.Do(func() { close(f.startedCh) })
	f.poke()
}

// End records how the run ended. Only the first call counts.
func (f *Feed) End(res engine.Result, err error) {
	f.endOnce.Do(func() {
		f.mu.Lock()
		f.ended, f.res, f.err = true, res, err
		f.mu.Unlock()
		close(f.endedCh)
		f.poke()
	})
}

// Started is closed once the first event arrived: the run passed its
// start-up checks.
func (f *Feed) Started() <-chan struct{} { return f.startedCh }

// Ended is closed once End was called.
func (f *Feed) Ended() <-chan struct{} { return f.endedCh }

// Result is what End recorded; call it after Ended is closed.
func (f *Feed) Result() (engine.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.res, f.err
}

func (f *Feed) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// batch is what one wait on the feed delivers.
type batch struct {
	events []engine.Event
	ended  bool // the run is over; no more events follow
}

// next waits until there is something new and takes it. The end is
// delivered once, after the last events; ok is false when ctx ended first
// or nothing more will come.
func (f *Feed) next(ctx context.Context, endSeen bool) (b batch, ok bool) {
	for {
		f.mu.Lock()
		b = batch{events: f.events, ended: f.ended && !endSeen}
		f.events = nil
		f.mu.Unlock()
		if len(b.events) > 0 || b.ended {
			return b, true
		}
		if endSeen {
			return batch{}, false
		}
		select {
		case <-ctx.Done():
			return batch{}, false
		case <-f.wake:
		}
	}
}
