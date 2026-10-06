package engine

import (
	"sort"
	"sync"
	"time"
)

// Clock is the engine's time source, so tests run without sleeping.
type Clock interface {
	Now() time.Time
	// After returns a channel that delivers the time once d has passed.
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// SystemClock returns the real clock, for callers outside the engine.
func SystemClock() Clock { return systemClock{} }

var (
	_ Clock = systemClock{}
	_ Clock = (*FakeClock)(nil)
)

// FakeClock is a Clock for tests. Nothing ever waits on it: After moves the
// time forward by the requested duration and returns a channel that has
// already fired, so a poll loop spins through fake time at full speed. Hooks
// registered with At run inside After, on the caller's goroutine, which lets
// a test act "after 10 seconds" without coordinating goroutines. It is safe
// for concurrent use.
type FakeClock struct {
	mu    sync.Mutex
	start time.Time
	now   time.Time
	hooks []fakeHook // sorted by offset; equal offsets keep registration order
}

type fakeHook struct {
	offset time.Duration
	fn     func()
}

// NewFakeClock returns a clock set to start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{start: start, now: start}
}

// Now returns the fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// At registers fn to run once, as soon as the clock is offset or more past
// its start time.
func (c *FakeClock) At(offset time.Duration, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = append(c.hooks, fakeHook{offset, fn})
	sort.SliceStable(c.hooks, func(i, j int) bool { return c.hooks[i].offset < c.hooks[j].offset })
}

// After advances the clock by d, runs the hooks that are now due and returns
// a channel that has already delivered the new time.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	n := 0
	for n < len(c.hooks) && c.hooks[n].offset <= now.Sub(c.start) {
		n++
	}
	due := c.hooks[:n:n]
	c.hooks = c.hooks[n:]
	c.mu.Unlock()

	// Called without the lock so hooks may use the clock.
	for _, h := range due {
		h.fn()
	}
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}
