// Package notify delivers igris events to the owner's channels (SPEC §10):
// the backend toast, ntfy, Discord, a webhook, Slack and Gotify. Delivery is
// best effort: every channel gets a 10 s timeout and one retry, and a
// failure is reported, never fatal. Quiet hours and the task_done digest
// hold messages back per channel until a flush sends them.
package notify

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/config"
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

// Digest is the event of a quiet-hours digest: the messages one channel
// held, sent as one (SPEC §10). It is no config event.
const Digest Event = "digest"

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
	// Held is set on a digest (Event Digest): the messages it stands for.
	// What is then the digest's text.
	Held []Message
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
	// NeverHeld exempts the channel from quiet hours: the backend toast,
	// which shows on the machine running igris, not a phone.
	NeverHeld bool
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
	// Held says the message was held for quiet hours, not sent.
	Held bool
	// Count is how many held messages a digest (Event Digest) carried.
	Count int
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

	// Quiet is the quiet-hours window; nil means none. Inside it a message
	// whose event is not in BreakThrough is held per channel (except for
	// NeverHeld entries) and later sent as a digest (SPEC §10).
	Quiet        *config.QuietWindow
	BreakThrough []Event
	// TaskDoneDigest groups task_done messages per channel.
	TaskDoneDigest config.TaskDoneDigest
	// Now is the clock quiet hours go by, in the location whose wall clock
	// counts; nil means time.Now.
	Now func() time.Time
}

// Immediate turns quiet hours and the task_done digest off, for a router
// whose messages must go out at once (`notify test`, `adapt`): pass it to
// FromConfig.
func Immediate(o *Options) {
	o.Quiet, o.TaskDoneDigest = nil, 0
}

// Router sends a message to every channel that wants its event. It holds
// what quiet hours and the task_done digest keep back until Flush,
// FlushTasks or FlushAll sends it.
type Router struct {
	o Options

	mu     sync.Mutex
	queues []queue // per entry of o.Channels
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
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Router{o: o, queues: make([]queue, len(o.Channels))}
}

func realSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// HasChannels reports whether the router has any channel at all.
func (r *Router) HasChannels() bool { return len(r.o.Channels) > 0 }

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
// returns the Results in channel order: one per delivery, held message or
// digest. A failing channel is retried once after RetryDelay; it never
// stops the others and Notify itself never fails. A channel that collects
// m for a task_done digest reports nothing yet.
func (r *Router) Notify(ctx context.Context, m Message) []Result {
	return r.collect(ctx, r.route(m))
}

// NotifyEach delivers m as Notify does and calls fn with each Result as
// soon as that delivery is over, so the order is the order in which they
// finish. fn is never called concurrently. NotifyEach returns once every
// delivery is over.
func (r *Router) NotifyEach(ctx context.Context, m Message, fn func(Result)) {
	r.run(ctx, r.route(m), func(_ int, res Result) { fn(res) })
}

// collect runs jobs and returns their Results in job order.
func (r *Router) collect(ctx context.Context, jobs []job) []Result {
	per := make([][]Result, len(jobs))
	r.run(ctx, jobs, func(i int, res Result) { per[i] = append(per[i], res) })
	var out []Result
	for _, rs := range per {
		out = append(out, rs...)
	}
	return out
}

// run sends each job's steps in order, the jobs in parallel. done gets
// each Result with its job's position, one call at a time.
func (r *Router) run(ctx context.Context, jobs []job, done func(i int, res Result)) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, s := range j.steps {
				res := Result{Channel: j.ch.Name(), Event: s.m.Event, Held: s.hold, Count: len(s.m.Held)}
				if !s.hold {
					res.Err = r.deliver(ctx, j.ch, s.m, j.once)
				}
				mu.Lock()
				done(i, res)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

// deliver makes up to two attempts, or one when once is set.
func (r *Router) deliver(ctx context.Context, ch Channel, m Message, once bool) error {
	attempts := 2
	if once {
		attempts = 1
	}
	var err error
	for attempt := range attempts {
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
