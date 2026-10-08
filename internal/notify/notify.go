// Package notify delivers igris events to the owner's channels (SPEC §10):
// the backend toast, ntfy and Discord. Delivery is best effort: every channel
// gets a 10 s timeout and one retry, and a failure is reported, never fatal.
package notify

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Event is a SPEC §10 notification event.
type Event string

// The notification events.
const (
	NeedsInput        Event = "needs_input"
	SessionLost       Event = "session_lost"
	TaskOverdue       Event = "task_overdue"
	VerifyFailedLimit Event = "verify_failed_limit"
	TaskDone          Event = "task_done"
	PhaseDone         Event = "phase_done"
	PhaseStuck        Event = "phase_stuck"
	RunError          Event = "run_error"
)

// AllEvents lists every event.
var AllEvents = []Event{NeedsInput, SessionLost, TaskOverdue, VerifyFailedLimit, TaskDone, PhaseDone, PhaseStuck, RunError}

// DefaultEvents are the events a channel gets when its config lists none.
var DefaultEvents = []Event{NeedsInput, SessionLost, TaskOverdue, PhaseDone, PhaseStuck, RunError, VerifyFailedLimit}

// Urgent reports whether the owner is needed now (high priority on ntfy,
// the "request" sound on a toast).
func (e Event) Urgent() bool { return e == NeedsInput || e == SessionLost || e == TaskOverdue }

// Message is what a notification says. It carries identifiers and a few
// words only: never file contents, diffs or command output (SPEC §10).
type Message struct {
	Event   Event
	Project string // project directory name
	Phase   string
	TaskID  string // empty for events that belong to no task
	Title   string // task title
	What    string // what happened, in a few words
	RunID   string // the run's ID in the run log (SPEC §13); "" outside a run
	At      time.Time
}

// Subject is the short headline: "igris · <project>".
func (m Message) Subject() string { return "igris · " + m.Project }

// Body is "phase P · ID title: what", leaving out what is not known.
func (m Message) Body() string {
	var b strings.Builder
	if m.Phase != "" {
		b.WriteString("phase " + m.Phase)
	}
	if m.TaskID != "" {
		if b.Len() > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(m.TaskID)
		if t := PlainTitle(m.Title); t != "" {
			b.WriteString(" " + t)
		}
	}
	if m.What != "" {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(m.What)
	}
	return b.String()
}

// PlainTitle drops the markdown a task title carries in the plan (`code`,
// **bold**), which a phone shows as raw characters, and a trailing period
// that would run into the ": what" after it.
func PlainTitle(t string) string {
	t = strings.NewReplacer("`", "", "**", "", "__", "").Replace(t)
	return strings.TrimRight(strings.TrimSpace(t), ".")
}

// cutRunes cuts s to at most n characters, ending in "…" when it was longer.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// Channel delivers one message to one destination.
type Channel interface {
	Name() string
	Send(ctx context.Context, m Message) error
}

// Entry is a channel with the events it wants. No events means
// DefaultEvents.
type Entry struct {
	Channel Channel
	Events  []Event
}

func (e Entry) wants(ev Event) bool {
	if len(e.Events) == 0 {
		return slices.Contains(DefaultEvents, ev)
	}
	return slices.Contains(e.Events, ev)
}

// Result is the outcome of one delivery.
type Result struct {
	Channel string
	Event   Event
	// Err is nil on success; its text never contains a secret.
	Err error
}

// Options configure a Router.
type Options struct {
	Channels []Entry
	// Secrets are scrubbed from every error text the router reports.
	Secrets []string
	// Timeout bounds one attempt; zero means 10 s.
	Timeout time.Duration
	// RetryDelay is the pause before the single retry; zero means 1 s.
	RetryDelay time.Duration
	// Sleep waits d or until ctx ends; nil uses a real timer. Tests inject
	// a fake so nothing sleeps.
	Sleep func(ctx context.Context, d time.Duration)
}

// Router sends a message to every channel that wants its event.
type Router struct {
	o Options
}

// New returns a router for o.
func New(o Options) *Router {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.RetryDelay <= 0 {
		o.RetryDelay = time.Second
	}
	if o.Sleep == nil {
		o.Sleep = realSleep
	}
	return &Router{o: o}
}

func realSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// Enabled reports whether any channel wants ev.
func (r *Router) Enabled(ev Event) bool {
	for _, e := range r.o.Channels {
		if e.wants(ev) {
			return true
		}
	}
	return false
}

// Notify delivers m to every channel that wants m.Event, in parallel, and
// returns one Result per channel tried (in channel order). A failing channel
// is retried once after RetryDelay; it never stops the others and Notify
// itself never fails.
func (r *Router) Notify(ctx context.Context, m Message) []Result {
	var results []Result
	r.each(ctx, m, func(n int) { results = make([]Result, n) }, func(i int, res Result) { results[i] = res })
	return results
}

// NotifyEach delivers m as Notify does and calls fn with each channel's
// Result as soon as that delivery is over, so the order is the order in
// which they finish. fn is never called concurrently. NotifyEach returns
// once every delivery is over.
func (r *Router) NotifyEach(ctx context.Context, m Message, fn func(Result)) {
	r.each(ctx, m, func(int) {}, func(_ int, res Result) { fn(res) })
}

// each delivers m to the channels that want it in parallel. start learns
// how many there are; done gets each Result with its channel's position,
// one call at a time.
func (r *Router) each(ctx context.Context, m Message, start func(n int), done func(i int, res Result)) {
	var picked []Channel
	for _, e := range r.o.Channels {
		if e.wants(m.Event) {
			picked = append(picked, e.Channel)
		}
	}
	start(len(picked))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, ch := range picked {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := Result{Channel: ch.Name(), Event: m.Event, Err: r.deliver(ctx, ch, m)}
			mu.Lock()
			defer mu.Unlock()
			done(i, res)
		}()
	}
	wg.Wait()
}

// deliver makes up to two attempts.
func (r *Router) deliver(ctx context.Context, ch Channel, m Message) error {
	var err error
	for attempt := range 2 {
		if attempt == 1 {
			r.o.Sleep(ctx, r.o.RetryDelay)
		}
		if ctx.Err() != nil {
			return redactError(ctx.Err(), r.o.Secrets)
		}
		actx, cancel := context.WithTimeout(ctx, r.o.Timeout)
		err = ch.Send(actx, m)
		cancel()
		if err == nil {
			return nil
		}
	}
	return redactError(err, r.o.Secrets)
}
