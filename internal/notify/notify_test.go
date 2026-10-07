package notify

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeChannel records messages and fails the first fail sends.
type fakeChannel struct {
	name string
	mu   sync.Mutex
	got  []Message
	fail int
	err  error
	// block makes Send wait for its context to end.
	block bool
}

func (f *fakeChannel) Name() string { return f.name }

func (f *fakeChannel) Send(ctx context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.block {
		f.mu.Unlock()
		<-ctx.Done()
		f.mu.Lock()
		f.got = append(f.got, m)
		return ctx.Err()
	}
	f.got = append(f.got, m)
	if f.fail > 0 {
		f.fail--
		return f.err
	}
	return nil
}

func (f *fakeChannel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

func noSleep(context.Context, time.Duration) {}

func TestMessageBody(t *testing.T) {
	tests := []struct {
		name string
		m    Message
		want string
	}{
		{"task event", Message{Phase: "M5", TaskID: "M5-01", Title: "Router", What: "your turn"}, "phase M5 · M5-01 Router: your turn"},
		{"phase event", Message{Phase: "M5", What: "complete"}, "phase M5: complete"},
		{"what only", Message{What: "the run stopped"}, "the run stopped"},
		{"id without title", Message{Phase: "P", TaskID: "X-1", What: "w"}, "phase P · X-1: w"},
		{"markdown in the title", Message{Phase: "S0", TaskID: "S0-01", Title: "Create `hello.txt` containing the word `hello`.", What: "done"},
			"phase S0 · S0-01 Create hello.txt containing the word hello: done"},
		{"bold title", Message{TaskID: "M0-02", Title: "**Entrypoint**", What: "done"}, "M0-02 Entrypoint: done"},
	}
	for _, tt := range tests {
		if got := tt.m.Body(); got != tt.want {
			t.Errorf("%s: Body() = %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := (Message{Project: "demo"}).Subject(); got != "igris · demo" {
		t.Errorf("Subject() = %q", got)
	}
}

func TestEventFilters(t *testing.T) {
	def, only, all := &fakeChannel{name: "default"}, &fakeChannel{name: "only"}, &fakeChannel{name: "all"}
	r := New(Options{Sleep: noSleep, Channels: []Entry{
		{Channel: def},
		{Channel: only, Events: []Event{PhaseDone}},
		{Channel: all, Events: AllEvents},
	}})

	tests := []struct {
		ev             Event
		def, only, all int // cumulative sends after this event
	}{
		{NeedsInput, 1, 0, 1},
		{TaskDone, 1, 0, 2}, // not in the default list
		{PhaseDone, 2, 1, 3},
		{RunError, 3, 1, 4},
	}
	for _, tt := range tests {
		res := r.Notify(context.Background(), Message{Event: tt.ev})
		if got := def.count(); got != tt.def {
			t.Errorf("%s: default channel has %d sends, want %d", tt.ev, got, tt.def)
		}
		if got := only.count(); got != tt.only {
			t.Errorf("%s: filtered channel has %d sends, want %d", tt.ev, got, tt.only)
		}
		if got := all.count(); got != tt.all {
			t.Errorf("%s: all-events channel has %d sends, want %d", tt.ev, got, tt.all)
		}
		for _, x := range res {
			if x.Err != nil {
				t.Errorf("%s: %s failed: %v", tt.ev, x.Channel, x.Err)
			}
		}
	}
	if r.Enabled(TaskDone) != true || New(Options{Channels: []Entry{{Channel: def}}}).Enabled(TaskDone) {
		t.Error("Enabled does not follow the event filters")
	}
}

func TestRetryOnce(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name     string
		fail     int
		attempts int
		wantErr  bool
	}{
		{"works first time", 0, 1, false},
		{"works on the retry", 1, 2, false},
		{"fails twice, gives up", 5, 2, true},
	}
	for _, tt := range tests {
		ch := &fakeChannel{name: "c", fail: tt.fail, err: boom}
		var slept []time.Duration
		r := New(Options{
			Channels:   []Entry{{Channel: ch}},
			RetryDelay: 3 * time.Second,
			Sleep:      func(_ context.Context, d time.Duration) { slept = append(slept, d) },
		})
		res := r.Notify(context.Background(), Message{Event: NeedsInput})
		if len(res) != 1 || (res[0].Err != nil) != tt.wantErr {
			t.Errorf("%s: results = %+v", tt.name, res)
		}
		if got := ch.count(); got != tt.attempts {
			t.Errorf("%s: %d attempts, want %d", tt.name, got, tt.attempts)
		}
		if (tt.attempts == 2) != (len(slept) == 1) || (len(slept) == 1 && slept[0] != 3*time.Second) {
			t.Errorf("%s: retry delays = %v", tt.name, slept)
		}
	}
}

func TestFailureDoesNotBlockOtherChannels(t *testing.T) {
	bad := &fakeChannel{name: "bad", fail: 9, err: errors.New("down")}
	good := &fakeChannel{name: "good"}
	r := New(Options{Sleep: noSleep, Channels: []Entry{{Channel: bad}, {Channel: good}}})
	res := r.Notify(context.Background(), Message{Event: RunError})
	if len(res) != 2 || res[0].Err == nil || res[1].Err != nil {
		t.Fatalf("results = %+v", res)
	}
	if good.count() != 1 {
		t.Errorf("good channel got %d messages, want 1", good.count())
	}
}

func TestAttemptTimeout(t *testing.T) {
	ch := &fakeChannel{name: "slow", block: true}
	r := New(Options{Channels: []Entry{{Channel: ch}}, Timeout: 5 * time.Millisecond, Sleep: noSleep})
	res := r.Notify(context.Background(), Message{Event: NeedsInput})
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "deadline exceeded") {
		t.Errorf("err = %v, want a deadline error", res[0].Err)
	}
	if ch.count() != 2 {
		t.Errorf("%d attempts, want 2 (one retry)", ch.count())
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := &fakeChannel{name: "c", fail: 9, err: errors.New("down")}
	r := New(Options{Channels: []Entry{{Channel: ch}}, Sleep: func(context.Context, time.Duration) { cancel() }})
	res := r.Notify(ctx, Message{Event: NeedsInput})
	if res[0].Err == nil || ch.count() != 1 {
		t.Errorf("err = %v after %d attempts, want an error after 1", res[0].Err, ch.count())
	}
}

func TestSecretsAreScrubbedFromErrors(t *testing.T) {
	const hook, token = "https://discord.example/api/webhooks/123/abcSECRET", "tk_supersecret"
	ch := &fakeChannel{name: "c", fail: 9, err: errors.New(`Post "` + hook + `": dial failed (auth ` + token + `)`)}
	r := New(Options{Sleep: noSleep, Secrets: []string{hook, "", token}, Channels: []Entry{{Channel: ch}}})
	res := r.Notify(context.Background(), Message{Event: NeedsInput})
	msg := res[0].Err.Error()
	if strings.Contains(msg, "abcSECRET") || strings.Contains(msg, token) {
		t.Errorf("error leaks a secret: %q", msg)
	}
	if !strings.Contains(msg, redacted) || !strings.Contains(msg, "dial failed") {
		t.Errorf("error lost its meaning: %q", msg)
	}
}

// gatedChannel waits for its gate before it delivers.
type gatedChannel struct {
	name string
	gate chan struct{}
}

func (g *gatedChannel) Name() string { return g.name }

func (g *gatedChannel) Send(ctx context.Context, _ Message) error {
	select {
	case <-g.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestNotifyEachStreamsResults(t *testing.T) {
	slow := &gatedChannel{name: "slow", gate: make(chan struct{})}
	bad := &fakeChannel{name: "bad", fail: 9, err: errors.New("down")}
	skip := &fakeChannel{name: "skip"}
	r := New(Options{Sleep: noSleep, Channels: []Entry{
		{Channel: slow}, {Channel: bad}, {Channel: skip, Events: []Event{TaskDone}},
	}})
	var got []Result
	r.NotifyEach(context.Background(), Message{Event: RunError}, func(res Result) {
		got = append(got, res)
		if res.Channel == "bad" {
			close(slow.gate) // the slow delivery ends only after this one was reported
		}
	})
	if len(got) != 2 || got[0].Channel != "bad" || got[0].Err == nil || got[1].Channel != "slow" || got[1].Err != nil {
		t.Fatalf("results = %+v, want bad (failed) then slow (ok)", got)
	}
	if got[0].Event != RunError || skip.count() != 0 {
		t.Errorf("event = %s, skip channel got %d messages", got[0].Event, skip.count())
	}
}

// sentChannel closes sent when it delivers.
type sentChannel struct {
	name string
	sent chan struct{}
}

func (c *sentChannel) Name() string { return c.name }

func (c *sentChannel) Send(context.Context, Message) error {
	close(c.sent)
	return nil
}

func TestNotifyKeepsChannelOrder(t *testing.T) {
	slow := &gatedChannel{name: "slow", gate: make(chan struct{})}
	fast := &sentChannel{name: "fast", sent: make(chan struct{})}
	r := New(Options{Sleep: noSleep, Channels: []Entry{{Channel: slow}, {Channel: fast}}})
	go func() {
		<-fast.sent // slow finishes last
		close(slow.gate)
	}()
	res := r.Notify(context.Background(), Message{Event: RunError})
	if len(res) != 2 || res[0].Channel != "slow" || res[1].Channel != "fast" {
		t.Errorf("results = %+v, want slow then fast", res)
	}
}
