package notify

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/config"
)

// fakeNow is a settable clock for quiet hours.
type fakeNow struct{ t time.Time }

func (f *fakeNow) Now() time.Time { return f.t }

// at sets the clock to hh:mm on day 1 or later (day 2 is the next day).
func (f *fakeNow) at(day, hh, mm int) { f.t = time.Date(2026, 10, day, hh, mm, 0, 0, time.UTC) }

func window(t *testing.T, s string) *config.QuietWindow {
	t.Helper()
	w, ok, err := config.ParseQuiet(s)
	if err != nil || !ok {
		t.Fatalf("ParseQuiet(%q) = %v, %v", s, ok, err)
	}
	return &w
}

var defaultBreak = []Event{NeedsInput, SessionLost, TaskOverdue}

// events lists what a channel got, a digest as "digest(N)".
func (f *fakeChannel) events() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.got {
		if m.Event == Digest {
			out = append(out, fmt.Sprintf("digest(%d)", len(m.Held)))
			continue
		}
		out = append(out, string(m.Event))
	}
	return strings.Join(out, " ")
}

func (f *fakeChannel) last() Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got[len(f.got)-1]
}

func results(rs []Result) string {
	var out []string
	for _, r := range rs {
		s := r.Channel + ":" + string(r.Event)
		switch {
		case r.Held:
			s += "(held)"
		case r.Event == Digest:
			s += fmt.Sprintf("(%d)", r.Count)
		}
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// A window that crosses midnight holds from its start to its end the next
// morning; urgent events and the toast get through; the digest goes out at
// the first flush after the window.
func TestQuietHoursAcrossMidnight(t *testing.T) {
	ctx := context.Background()
	clock := &fakeNow{}
	phone, toast := &fakeChannel{name: "ntfy"}, &fakeChannel{name: "backend"}
	r := New(Options{
		Sleep: noSleep, Now: clock.Now, Quiet: window(t, "22:00-07:00"), BreakThrough: defaultBreak,
		Channels: []Entry{{Channel: phone, Events: AllEvents}, {Channel: toast, NeverHeld: true}},
	})
	steps := []struct {
		day, hh, mm int
		ev          Event // "" flushes
		want        string
	}{
		{1, 21, 59, PhaseDone, "ntfy:phase_done backend:phase_done"},
		{1, 22, 0, TaskDone, "ntfy:task_done(held)"},
		{1, 23, 30, NeedsInput, "ntfy:needs_input backend:needs_input"},
		{1, 23, 45, "", ""},
		{2, 2, 10, PhaseStuck, "ntfy:phase_stuck(held) backend:phase_stuck"},
		{2, 6, 59, "", ""},
		{2, 7, 0, "", "ntfy:digest(2)"},
		{2, 7, 1, "", ""},
		{2, 7, 2, RunError, "ntfy:run_error backend:run_error"},
	}
	for _, s := range steps {
		clock.at(s.day, s.hh, s.mm)
		var got []Result
		if s.ev == "" {
			got = r.Flush(ctx)
		} else {
			got = r.Notify(ctx, Message{Event: s.ev, Project: "demo", Phase: "M1", TaskID: "M1-01", Title: "One", What: "w"})
		}
		if results(got) != s.want {
			t.Errorf("%d %02d:%02d %s: results %q, want %q", s.day, s.hh, s.mm, s.ev, results(got), s.want)
		}
	}
	if got, want := phone.events(), "phase_done needs_input digest(2) run_error"; got != want {
		t.Errorf("ntfy got %s, want %s", got, want)
	}
	if got := toast.events(); strings.Contains(got, "digest") || strings.Count(got, " ")+1 != 4 {
		t.Errorf("toast got %s, want every message at once and no digest", got)
	}
}

func TestQuietHoursDigestText(t *testing.T) {
	ctx := context.Background()
	clock := &fakeNow{}
	ch := &fakeChannel{name: "c"}
	r := New(Options{Sleep: noSleep, Now: clock.Now, Quiet: window(t, "22:00-07:00"), Channels: []Entry{{Channel: ch, Events: AllEvents}}})
	for i := range 25 {
		clock.at(1, 22, i)
		r.Notify(ctx, Message{Event: TaskDone, Project: "demo", Phase: "M1", TaskID: fmt.Sprintf("M1-%02d", i+1), Title: "**Bold**\x1b[2J", What: "done", RunID: "run-1"})
	}
	// An empty break_through holds the urgent ones too.
	clock.at(1, 23, 0)
	if got := results(r.Notify(ctx, Message{Event: NeedsInput, Project: "demo", What: "needs you"})); got != "c:needs_input(held)" {
		t.Errorf("needs_input with no break_through: %s", got)
	}
	clock.at(2, 7, 30)
	got := r.Flush(ctx)
	if results(got) != "c:digest(26)" {
		t.Fatalf("flush = %s", results(got))
	}
	d := ch.last()
	lines := strings.Split(d.Body(), "\n")
	if lines[0] != "quiet hours 22:00–07:00: 26 held" {
		t.Errorf("heading %q", lines[0])
	}
	if len(lines) != 22 || lines[1] != "22:00 task_done: phase M1 · M1-01 Bold: done" || lines[20] != "22:19 task_done: phase M1 · M1-20 Bold: done" || lines[21] != "… and 6 more" {
		t.Errorf("digest:\n%s", d.Body())
	}
	if d.Project != "demo" || d.TaskID != "" || len(d.Held) != 26 || d.Held[25].Event != NeedsInput || !d.At.Equal(clock.t) {
		t.Errorf("digest message %+v", d)
	}
	if again := r.Flush(ctx); len(again) != 0 {
		t.Errorf("second flush sent %s", results(again))
	}
}

// A message after the window, before any flush, follows the digest of what
// was held, so the order is kept.
func TestQuietHoursDigestBeforeNextMessage(t *testing.T) {
	ctx := context.Background()
	clock := &fakeNow{}
	ch := &fakeChannel{name: "c"}
	r := New(Options{Sleep: noSleep, Now: clock.Now, Quiet: window(t, "01:00-02:00"), Channels: []Entry{{Channel: ch, Events: AllEvents}}})
	clock.at(1, 1, 30)
	r.Notify(ctx, Message{Event: PhaseDone})
	clock.at(1, 2, 0)
	if got := results(r.Notify(ctx, Message{Event: PhaseStuck})); got != "c:digest(1) c:phase_stuck" {
		t.Errorf("results %s", got)
	}
	if got := ch.events(); got != "digest(1) phase_stuck" {
		t.Errorf("sent %s", got)
	}
}

// At run stop everything held goes out, inside the window too.
func TestFlushAllInsideWindow(t *testing.T) {
	ctx := context.Background()
	clock := &fakeNow{}
	ch := &fakeChannel{name: "c"}
	r := New(Options{Sleep: noSleep, Now: clock.Now, Quiet: window(t, "22:00-07:00"), TaskDoneDigest: config.DigestPhase, Channels: []Entry{{Channel: ch, Events: AllEvents}}})
	clock.at(1, 23, 0)
	r.Notify(ctx, Message{Event: PhaseDone, Phase: "M1"})
	r.Notify(ctx, Message{Event: TaskDone, Phase: "M2", TaskID: "M2-01"})
	r.Notify(ctx, Message{Event: TaskDone, Phase: "M2", TaskID: "M2-02"})
	if got := r.Flush(ctx); len(got) != 0 {
		t.Errorf("flush inside the window sent %s", results(got))
	}
	// The grouped task_done is held like any task_done, then digested.
	if got := results(r.FlushAll(ctx)); got != "c:task_done(held) c:digest(2)" {
		t.Errorf("FlushAll = %s", got)
	}
	d := ch.last()
	if !strings.Contains(d.Body(), "23:00 task_done: phase M2: 2 tasks done: M2-01, M2-02") {
		t.Errorf("digest:\n%s", d.Body())
	}
	if got := r.FlushAll(ctx); len(got) != 0 {
		t.Errorf("second FlushAll sent %s", results(got))
	}
}

func TestTaskDoneDigest(t *testing.T) {
	ctx := context.Background()
	done := func(id string) Message {
		return Message{Event: TaskDone, Project: "demo", Phase: "M1", TaskID: id, Title: "T " + id, What: "done", RunID: "r"}
	}
	t.Run("every 3", func(t *testing.T) {
		ch, other := &fakeChannel{name: "c"}, &fakeChannel{name: "o"}
		r := New(Options{Sleep: noSleep, TaskDoneDigest: 3, Channels: []Entry{{Channel: ch, Events: AllEvents}, {Channel: other}}})
		for _, id := range []string{"M1-01", "M1-02"} {
			if got := r.Notify(ctx, done(id)); len(got) != 0 {
				t.Errorf("%s: sent %s before 3 were collected", id, results(got))
			}
		}
		if got := results(r.Notify(ctx, done("M1-03"))); got != "c:task_done" {
			t.Errorf("third: %s", got)
		}
		m := ch.last()
		if m.Body() != "phase M1: 3 tasks done: M1-01, M1-02, M1-03" || m.TaskID != "" || m.Title != "" || m.RunID != "r" {
			t.Errorf("grouped message %+v", m)
		}
		// Other events are not collected.
		if got := results(r.Notify(ctx, Message{Event: PhaseDone})); got != "c:phase_done o:phase_done" {
			t.Errorf("phase_done: %s", got)
		}
		// A single leftover is sent as it is.
		r.Notify(ctx, done("M1-04"))
		if got := results(r.FlushTasks(ctx)); got != "c:task_done" || ch.last().Body() != "phase M1 · M1-04 T M1-04: done" {
			t.Errorf("FlushTasks = %s, %q", got, ch.last().Body())
		}
		if got := r.FlushTasks(ctx); len(got) != 0 {
			t.Errorf("FlushTasks again: %s", results(got))
		}
	})
	t.Run("phase", func(t *testing.T) {
		ch := &fakeChannel{name: "c"}
		r := New(Options{Sleep: noSleep, TaskDoneDigest: config.DigestPhase, Channels: []Entry{{Channel: ch, Events: AllEvents}}})
		for i := range 12 {
			if got := r.Notify(ctx, done(fmt.Sprintf("M1-%02d", i+1))); len(got) != 0 {
				t.Fatalf("sent %s before the phase ended", results(got))
			}
		}
		r.FlushAll(ctx)
		want := "phase M1: 12 tasks done: M1-01, M1-02, M1-03, M1-04, M1-05, M1-06, M1-07, M1-08, M1-09, M1-10, …"
		if got := ch.last().Body(); got != want {
			t.Errorf("grouped = %q, want %q", got, want)
		}
	})
}

// notify test and adapt send at once.
func TestImmediate(t *testing.T) {
	c := config.Default().Notify
	c.Quiet, c.TaskDoneDigest, c.BreakThrough = "00:00-23:59", 2, config.BreakThrough{}
	r := FromConfig(c, config.Secrets{DiscordWebhook: "https://h/x"}, nil, Immediate)
	if r.o.Quiet != nil || r.o.TaskDoneDigest != 0 {
		t.Errorf("Immediate left quiet %v, digest %v", r.o.Quiet, r.o.TaskDoneDigest)
	}
	r = FromConfig(c, config.Secrets{DiscordWebhook: "https://h/x"}, nil)
	if r.o.Quiet == nil || r.o.TaskDoneDigest != 2 || len(r.o.BreakThrough) != 0 {
		t.Errorf("FromConfig: quiet %v, digest %v, break_through %v", r.o.Quiet, r.o.TaskDoneDigest, r.o.BreakThrough)
	}
	if r = FromConfig(config.Default().Notify, config.Secrets{}, nil); r.o.Quiet != nil || len(r.o.BreakThrough) != 3 {
		t.Errorf("defaults: quiet %v, break_through %v", r.o.Quiet, r.o.BreakThrough)
	}
}

// deadlineChannel fails every send and records how long its context had.
type deadlineChannel struct {
	sends int
	left  []time.Duration
}

func (*deadlineChannel) Name() string { return "d" }

func (d *deadlineChannel) Send(ctx context.Context, _ Message) error {
	d.sends++
	if dl, ok := ctx.Deadline(); ok {
		d.left = append(d.left, time.Until(dl))
	}
	return errorString("down")
}

// The flush at run stop makes one attempt per message, no retry, all of it
// within finalFlushTimeout, so a dead server can't hold igris's exit.
func TestFlushAllIsOneBoundedAttempt(t *testing.T) {
	ctx := context.Background()
	clock := &fakeNow{}
	var sleeps int
	ch := &deadlineChannel{}
	r := New(Options{
		Sleep:          func(context.Context, time.Duration) { sleeps++ },
		Timeout:        time.Hour, // longer than the overall bound
		Now:            clock.Now,
		Quiet:          window(t, "22:00-07:00"),
		TaskDoneDigest: config.DigestPhase,
		Channels:       []Entry{{Channel: ch, Events: AllEvents}},
	})
	clock.at(1, 23, 0)
	r.Notify(ctx, Message{Event: PhaseDone, Phase: "M1"}) // held
	r.Notify(ctx, Message{Event: TaskDone, Phase: "M1", TaskID: "M1-01"})
	clock.at(2, 8, 0) // after the window: the grouped task_done goes out, then the digest
	res := r.FlushAll(ctx)
	if len(res) != 2 || res[0].Err == nil || res[1].Err == nil {
		t.Fatalf("FlushAll = %s", results(res))
	}
	if ch.sends != 2 || sleeps != 0 {
		t.Errorf("%d sends and %d retry pauses, want 2 and 0", ch.sends, sleeps)
	}
	for _, left := range ch.left {
		if left > finalFlushTimeout || left <= 0 {
			t.Errorf("an attempt had %v, want at most %v", left, finalFlushTimeout)
		}
	}
	if len(ch.left) != 2 {
		t.Errorf("sends without a deadline: %v", ch.left)
	}
	// Notify still retries.
	r.Notify(ctx, Message{Event: NeedsInput})
	if ch.sends != 4 || sleeps != 1 {
		t.Errorf("Notify: %d sends and %d pauses, want 4 and 1", ch.sends, sleeps)
	}
}
