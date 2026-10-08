package engine

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/notify"
)

// phone is a notify.Channel that records what it is sent and when, by the
// engine's clock.
type phone struct {
	clock *FakeClock
	mu    sync.Mutex
	got   []string
}

func (p *phone) Name() string { return "phone" }

func (p *phone) Send(_ context.Context, m notify.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	at := p.clock.Now().Format("15:04")
	if m.Event == notify.Digest {
		p.got = append(p.got, at+" digest: "+m.What)
		return nil
	}
	p.got = append(p.got, at+" "+string(m.Event)+": "+m.Body())
	return nil
}

func (p *phone) sent() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.got...)
}

func quietWindow(t *testing.T, s string) *config.QuietWindow {
	t.Helper()
	w, _, err := config.ParseQuiet(s)
	if err != nil {
		t.Fatal(err)
	}
	return &w
}

// The window ends while the run waits on the owner's answer: the digest
// goes out at the end of the window, not when the owner comes back.
func TestQuietHoursFlushDuringOwnerWait(t *testing.T) {
	h := newHarness(t, chainPlan, "") // t0 is 12:00 UTC
	h.dirtyTree()
	h.onEvent = func(ev Event) {
		if ev.Kind != Asked || ev.Question != QuestionCommit {
			return
		}
		at := ev.At.Sub(t0) + 5*time.Second
		if ev.Task == "A-2" {
			at = 45 * time.Minute // the owner answers at 12:45
		}
		h.clock.At(at, func() { h.eng.Send(Command{Kind: CmdAnswer, Yes: true}) })
	}
	ph := &phone{clock: h.clock}
	router := notify.New(notify.Options{
		Sleep: noSleep, Now: h.clock.Now, Quiet: quietWindow(t, "11:00-12:30"),
		BreakThrough: []notify.Event{notify.NeedsInput},
		Channels:     []notify.Entry{{Channel: ph, Events: []notify.Event{notify.TaskDone, notify.PhaseDone}}},
	})
	res, err := h.run(func(o *Options) { o.Notifier = router })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	got := ph.sent()
	want := []string{
		"12:30 digest: quiet hours 11:00–12:30: 1 held\n12:00 task_done: phase A · A-1 One: done",
		"12:45 task_done: phase A · A-2 Two: done",
		"12:45 task_done: phase A · A-3 Three: done",
		"12:45 phase_done: phase A: complete",
	}
	if strings.Join(got, "\n|") != strings.Join(want, "\n|") {
		t.Errorf("phone got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	logged := h.read(".igris/runs.jsonl")
	for _, w := range []string{`"detail":"task_done held for phone (quiet hours)"`, `"detail":"digest of 1 via phone"`, `"detail":"phase_done via phone"`} {
		if !strings.Contains(logged, w) {
			t.Errorf("runs.jsonl lacks %s:\n%s", w, logged)
		}
	}
}

// Held messages go out as a digest when the run stops, inside the window.
// The router is the one arise builds (FromConfig in New), so quiet hours
// follow the engine's clock; the toast is never held.
func TestQuietHoursFlushAtStop(t *testing.T) {
	discord, srv := newRecorder(t, http.StatusNoContent)
	h := newHarness(t, chainPlan, "[notify]\nquiet = \"11:00-13:00\"\n[notify.discord]\nwebhook_url = \"env:D\"\nevents = [\"task_done\", \"phase_done\"]\n")
	res, err := h.run(func(o *Options) { o.Secrets = config.Secrets{DiscordWebhook: srv.URL + "/api/webhooks/1/x"} })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.toasts(); len(got) != 1 {
		t.Errorf("toasts %q, want the phase_done toast at once", got)
	}
	if discord.count() != 1 {
		t.Fatalf("discord got %d messages, want one digest: %q", discord.count(), discord.bodies)
	}
	for _, w := range []string{"quiet hours 11:00–13:00: 4 held", "12:00 task_done: phase A · A-1 One", "phase_done: phase A: complete"} {
		if !strings.Contains(discord.bodies[0], w) {
			t.Errorf("digest %s lacks %q", discord.bodies[0], w)
		}
	}
	logged := h.read(".igris/runs.jsonl")
	if n := strings.Count(logged, "held for discord (quiet hours)"); n != 4 {
		t.Errorf("%d held lines in runs.jsonl, want 4", n)
	}
	stop := strings.Index(logged, `"type":"run_stopped"`)
	if d := strings.Index(logged, `"detail":"digest of 4 via discord"`); d < 0 || d > stop {
		t.Errorf("digest not logged before run_stopped:\n%s", logged)
	}
}

// task_done_digest groups task_done messages and sends what is left at the
// end of each phase, before its phase_done.
func TestTaskDoneDigestByPhase(t *testing.T) {
	for _, tt := range []struct {
		digest string
		want   []string
	}{
		{`"phase"`, []string{
			"phase A: 3 tasks done: A-1, A-2, A-3 (task_done)", "phase A: complete (phase_done)",
			"phase B · B-1 Four: done (task_done)", "phase B: complete (phase_done)",
		}},
		{"2", []string{
			"phase A: 2 tasks done: A-1, A-2 (task_done)", "phase A · A-3 Three: done (task_done)", "phase A: complete (phase_done)",
			"phase B · B-1 Four: done (task_done)", "phase B: complete (phase_done)",
		}},
	} {
		t.Run(tt.digest, func(t *testing.T) {
			discord, srv := newRecorder(t, http.StatusNoContent)
			h := newHarness(t, chainPlan, fmt.Sprintf("[notify]\ntask_done_digest = %s\n[notify.discord]\nwebhook_url = \"env:D\"\nevents = [\"task_done\", \"phase_done\"]\n", tt.digest))
			res, err := h.run(func(o *Options) {
				o.Through = "B"
				o.Secrets = config.Secrets{DiscordWebhook: srv.URL + "/api/webhooks/1/x"}
			})
			if err != nil || res.Outcome != Completed {
				t.Fatalf("Run = %s, %v", res.Outcome, err)
			}
			if discord.count() != len(tt.want) {
				t.Fatalf("discord got %d messages, want %d: %q", discord.count(), len(tt.want), discord.bodies)
			}
			for i, w := range tt.want {
				if !strings.Contains(discord.bodies[i], w) {
					t.Errorf("message %d = %s, want %q", i, discord.bodies[i], w)
				}
			}
		})
	}
}
