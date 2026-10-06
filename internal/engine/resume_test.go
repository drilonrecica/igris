package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
)

func resumeLast(o *Options) { o.Phase = "" }

// stopMidTask runs phase A (or mutate's phases) and stops it 6s into the
// first task that doesn't signal by itself, like quitting igris. The next
// run starts with a clean event list.
func (h *harness) stopMidTask(stopAt time.Duration, mutate ...func(*Options)) {
	h.t.Helper()
	h.clock.At(stopAt, func() { h.eng.Send(Command{Kind: CmdStop}) })
	res, err := h.run(mutate...)
	if err != nil || res.Outcome != Stopped {
		h.t.Fatalf("first run = %s, %v; want stopped", res.Outcome, err)
	}
	h.events = nil
	h.onEvent = nil
}

func TestResumeReattaches(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	h.signalAt(20*time.Second, "A-1")
	res, err := h.run(resumeLast)
	if err != nil || res.Outcome != Completed || res.Phase != "A" {
		t.Fatalf("resumed run = %s in %q, %v", res.Outcome, res.Phase, err)
	}
	if got := h.opened(); got != "A-1 A-2 A-3" {
		t.Errorf("sessions opened for %q, want no second A-1 session", got)
	}
	if ev := h.event(TaskResumed, "A-1"); ev.Detail != "reattached to its session" || ev.Rank != "sonnet" || ev.Model != "sonnet" {
		t.Errorf("task_resumed = %+v", ev)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
	if closed := h.be.Closed(); len(closed) == 0 || closed[0].PaneID == "" {
		t.Errorf("closed = %v, want the reattached session closed first", closed)
	}
	if got := h.logged(); !strings.Contains(got, "run_started task_resumed task_done") {
		t.Errorf("run log = %s", got)
	}
}

// A signal written while igris was not running is processed first, whether
// the session survived or not.
func TestResumeProcessesAPendingSignalFirst(t *testing.T) {
	for _, killed := range []bool{false, true} {
		t.Run(map[bool]string{false: "live session", true: "session gone"}[killed], func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			h.autoSignalExcept("A-1")
			h.stopMidTask(6 * time.Second)
			if err := h.signal("A-1"); err != nil {
				t.Fatal(err)
			}
			if killed {
				run, err := h.dir.LoadRun()
				if err != nil {
					t.Fatal(err)
				}
				h.be.Kill(*run.Current.Session)
			}
			if _, err := h.run(resumeLast); err != nil {
				t.Fatalf("resumed run: %v", err)
			}
			if got := h.event(TaskDone, "A-1").At; !got.Equal(h.event(RunStarted, "").At) {
				t.Errorf("A-1 done at %v, want right at the start", got)
			}
			if got := h.count(Asked) + h.count(SessionLost); got != 0 {
				t.Errorf("%d questions/session-lost events; the signal should settle it", got)
			}
			if got := h.opened(); got != "A-1 A-2 A-3" {
				t.Errorf("sessions opened for %q", got)
			}
		})
	}
}

func TestResumeSessionGone(t *testing.T) {
	for _, cont := range []bool{true, false} {
		t.Run(map[bool]string{true: "continue", false: "fresh"}[cont], func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			h.autoSignalExcept("A-1")
			h.stopMidTask(6 * time.Second)
			run, err := h.dir.LoadRun()
			if err != nil {
				t.Fatal(err)
			}
			h.be.Kill(*run.Current.Session)
			uuid := run.Current.ClaudeSession

			h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
			h.onEvent = func(ev Event) {
				if ev.Kind == Asked && ev.Question == QuestionSessionLost {
					h.eng.Send(Command{Kind: CmdRetry, Continue: cont})
				}
			}
			if _, err := h.run(resumeLast); err != nil {
				t.Fatalf("resumed run: %v", err)
			}
			if got := h.event(TaskResumed, "A-1").Detail; got != "its session is gone" {
				t.Errorf("task_resumed detail %q", got)
			}
			if got := h.event(Asked, "A-1").Detail; !strings.Contains(got, "continue its conversation") {
				t.Errorf("question %q, want continue offered", got)
			}
			second := h.be.Opened()[1]
			prompts := h.be.Prompts("A-1")
			if cont {
				if arg(second, "--resume") != uuid || prompts[1] != prompt.Continue("A-1") {
					t.Errorf("continue args %q, prompt %q; want --resume %s", second.Args, prompts[1], uuid)
				}
			} else {
				if id := arg(second, "--session-id"); id == "" || id == uuid || !strings.Contains(prompts[1], "## Resumed task") {
					t.Errorf("fresh args %q, prompt %q", second.Args, prompts[1])
				}
			}
			if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
				t.Errorf("statuses = %s", got)
			}
		})
	}
}

// The previous run was a --through run stopped in its second phase: resuming
// starts in that phase and keeps the --through range.
func TestResumeStartsInThePhaseOfTheTask(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("B-1")
	h.onEvent = func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "B-1" {
			h.eng.Send(Command{Kind: CmdStop})
		}
	}
	res, err := h.run(func(o *Options) { o.Through = "B" })
	if err != nil || res.Outcome != Stopped || res.Phase != "B" {
		t.Fatalf("first run = %s in %q, %v", res.Outcome, res.Phase, err)
	}
	h.events, h.onEvent = nil, nil
	h.signalAt(h.clock.Now().Sub(t0)+10*time.Second, "B-1")
	res, err = h.run(resumeLast)
	if err != nil || res.Outcome != Completed || res.Phase != "B" {
		t.Fatalf("resumed run = %s in %q, %v", res.Outcome, res.Phase, err)
	}
	if got, want := h.kinds(), "run_started task_resumed not_committed task_done phase_started phase_done run_stopped"; got != want {
		t.Errorf("events = %s\nwant     %s", got, want)
	}
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(run.Phases, " ") != "B" || run.Through != "B" {
		t.Errorf("state.json phases %v through %q", run.Phases, run.Through)
	}
}

func TestResumeNeedsAnEarlierRun(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	_, err := h.run(resumeLast)
	if err == nil || !strings.Contains(err.Error(), "no earlier run to resume") {
		t.Fatalf("err = %v", err)
	}
	h.assertUnlocked()
}

// The owner finished the interrupted task by hand while igris was down.
func TestResumeTaskSettledMeanwhile(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| in progress |", "| done |", 1))
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	if _, err := h.run(resumeLast, func(o *Options) { o.ConfirmedDrift = true }); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if got := h.event(Warning, "").Detail; !strings.Contains(got, "A-1 is done in the plan now") {
		t.Errorf("warning %q", got)
	}
	if got := h.opened(); got != "A-1 A-2 A-3" {
		t.Errorf("sessions opened for %q", got)
	}
	if got := h.count(TaskResumed); got != 0 {
		t.Errorf("%d task_resumed events", got)
	}
}

// A task the plan says is in progress with no record of a session: only a
// fresh session is possible.
func TestAdoptTaskInProgressWithoutState(t *testing.T) {
	h := newHarness(t, strings.Replace(chainPlan, "| — | ready |", "| — | in progress |", 1), "")
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionSessionLost {
			h.eng.Send(Command{Kind: CmdRetry, Continue: true}) // nothing to continue
			h.eng.Send(Command{Kind: CmdRetry})
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.event(Asked, "A-1").Detail; strings.Contains(got, "continue") {
		t.Errorf("question %q offers to continue", got)
	}
	if got := h.event(Warning, "").Detail; !strings.Contains(got, "no earlier conversation of A-1") {
		t.Errorf("warning %q", got)
	}
	if p := h.be.Prompts("A-1"); len(p) != 1 || !strings.Contains(p[0], "## Resumed task") {
		t.Errorf("A-1 prompts = %q, want one resumed prompt", p)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
}

func TestResumeUserTask(t *testing.T) {
	h := newHarness(t, userPlan, "")
	h.stopMidTask(6 * time.Second)
	if err := h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionDone, Note: "bought"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(resumeLast); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if got := h.event(TaskDone, "A-1").Detail; got != "bought" {
		t.Errorf("A-1 done note %q", got)
	}
	if got := h.opened(); got != "A-2" {
		t.Errorf("sessions opened for %q", got)
	}
}

func TestResumeWarnsAboutAChangedConfig(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	h.cfg.PollInterval = 3 * h.cfg.PollInterval // as if igris.toml was edited before the restart
	h.write(state.ConfigFile, "poll_interval = \"6s\"\n")
	h.signalAt(h.clock.Now().Sub(t0)+time.Minute, "A-1")
	if _, err := h.run(resumeLast); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	if got := h.event(Warning, "").Detail; !strings.Contains(got, "igris.toml changed since the interrupted run") {
		t.Errorf("warning %q", got)
	}
	if got := h.count(ConfigChanged); got != 0 {
		t.Errorf("%d config_changed events; the new file is this run's snapshot", got)
	}
}

// Reattaching to a session that runs in skip-permissions mode needs this
// run's confirmation as well (SPEC §7.3).
func TestResumeYoloSessionNeedsConfirmation(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6*time.Second, func(o *Options) { o.Mode, o.ConfirmedYolo = ModeYolo, true })
	before := h.read("tasks.md")
	_, err := h.run(resumeLast)
	if !errors.Is(err, ErrYoloUnconfirmed) {
		t.Fatalf("err = %v, want ErrYoloUnconfirmed", err)
	}
	if got := h.read("tasks.md"); got != before {
		t.Error("plan written without confirmation")
	}
	if run, err := h.dir.LoadRun(); err != nil || run.Current == nil || run.Current.TaskID != "A-1" {
		t.Errorf("interrupted task lost from state.json: %+v, %v", run, err)
	}
	h.signalAt(h.clock.Now().Sub(t0)+10*time.Second, "A-1")
	if _, err := h.run(resumeLast, func(o *Options) { o.Mode, o.ConfirmedYolo = ModeYolo, true }); err != nil {
		t.Fatalf("confirmed resume: %v", err)
	}
	if got := h.event(TaskResumed, "A-1").Detail; got != "reattached to its session" {
		t.Errorf("task_resumed %q", got)
	}
}
