package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

func TestNewRejectsIncompleteOptions(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	full := Options{Config: h.cfg, Backend: h.be, State: h.dir, Phase: "A"}
	tests := []struct {
		name   string
		mutate func(*Options)
	}{
		{"no config", func(o *Options) { o.Config = nil }},
		{"no backend", func(o *Options) { o.Backend = nil }},
		{"no state", func(o *Options) { o.State = nil }},
		{"unknown mode", func(o *Options) { o.Mode = "wild" }},
		{"invalid config", func(o *Options) { c := *h.cfg; c.PollInterval = 0; o.Config = &c }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := full
			tt.mutate(&o)
			if _, err := New(o); err == nil {
				t.Error("New succeeded, want an error")
			}
		})
	}
	if _, err := New(full); err != nil {
		t.Errorf("New with complete options: %v", err)
	}
}

func TestRunPhase(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	res, err := h.run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outcome != Completed || res.Phase != "A" {
		t.Errorf("result = %s in phase %q, want completed in A", res.Outcome, res.Phase)
	}

	// One session per task, in plan order, each with its own model.
	specs := h.be.Opened()
	if got := h.opened(); got != "A-1 A-2 A-3" {
		t.Fatalf("sessions opened for %q, want A-1 A-2 A-3", got)
	}
	seen := map[string]bool{}
	for i, want := range []struct{ model, label string }{
		{"sonnet", "A-1 · sonnet"}, {"opus", "A-2 · opus"}, {"fable", "A-3 · fable"},
	} {
		s := specs[i]
		if got := arg(s, "--model"); got != want.model {
			t.Errorf("%s: --model %q, want %q", s.TaskID, got, want.model)
		}
		if s.Label != want.label || s.Dir != h.root {
			t.Errorf("%s: label %q dir %q, want %q in %q", s.TaskID, s.Label, s.Dir, want.label, h.root)
		}
		if got, want := arg(s, "--append-system-prompt-file"), filepath.Join(h.dir.PromptsDir(), s.TaskID+".rules.md"); got != want {
			t.Errorf("%s: rules file %q, want %q", s.TaskID, got, want)
		} else if !strings.Contains(h.read(filepath.Join(state.DirName, "prompts", s.TaskID+".rules.md")), "igris done") {
			t.Errorf("%s: rules file does not hold the igris rules", s.TaskID)
		}
		id := arg(s, "--session-id")
		if id == "" || seen[id] {
			t.Errorf("%s: session ID %q is empty or reused", s.TaskID, id)
		}
		seen[id] = true
		// The hooks-only settings file and the UUID that keys the hook
		// state (SPEC §6.3).
		if s.ClaudeSession != id {
			t.Errorf("%s: spec ClaudeSession %q, want %q", s.TaskID, s.ClaudeSession, id)
		}
		if got, want := arg(s, "--settings"), filepath.Join(h.root, state.DirName, "hooks", s.TaskID+".settings.json"); got != want {
			t.Errorf("%s: settings file %q, want %q", s.TaskID, got, want)
		} else if hooks := h.read(filepath.Join(state.DirName, "hooks", s.TaskID+".settings.json")); !strings.Contains(hooks, "hook --root") || !strings.Contains(hooks, `"Stop"`) {
			t.Errorf("%s: settings file holds no igris hooks: %s", s.TaskID, hooks)
		}
		// The task prompt never travels as an argument (SPEC §6, §11.2).
		for _, a := range s.Args {
			if strings.ContainsAny(a, "\n\t") || strings.Contains(a, "the first thing") {
				t.Errorf("%s: argument %q carries prompt text", s.TaskID, a)
			}
		}
		prompts := h.be.Prompts(s.TaskID)
		if len(prompts) != 1 || !strings.Contains(prompts[0], "# Task "+s.TaskID) || !strings.Contains(prompts[0], "igris done "+s.TaskID) {
			t.Errorf("%s: prompts = %q, want the task prompt once", s.TaskID, prompts)
		}
	}
	if got := len(h.be.Closed()); got != 3 {
		t.Errorf("closed %d sessions, want 3", got)
	}

	// Only Status cells changed; dependents were unblocked.
	want := strings.NewReplacer(
		"| — | ready | sonnet |", "| — | done | sonnet |",
		"| A-1 | blocked | opus |", "| A-1 | done | opus |",
		"| A-1, A-2 | blocked | fable |", "| A-1, A-2 | done | fable |",
		"| A-3 | blocked | sonnet |", "| A-3 | ready | sonnet |",
	).Replace(chainPlan)
	if got := h.read("tasks.md"); got != want {
		t.Errorf("plan after the run:\n%s\nwant:\n%s", got, want)
	}

	if got, want := h.kinds(), "run_started phase_started"+
		strings.Repeat(" task_started session_opened not_committed task_done", 3)+" phase_done run_stopped"; got != want {
		t.Errorf("events = %s\nwant     %s", got, want)
	}
	started := h.event(TaskStarted, "A-2")
	if started.Rank != "opus" || started.Model != "opus" || started.Mode != ModeDefault || started.Title != "Two" || started.Phase != "A" {
		t.Errorf("task_started = %+v", started)
	}
	if got := h.event(TaskDone, "A-1"); got.Detail != "did A-1" || len(got.Changes) != 2 {
		t.Errorf("task_done A-1 = %+v, want the note and 2 status changes", got)
	}

	if got, want := h.logged(), "run_started"+strings.Repeat(" task_started task_done", 3)+" notification run_stopped"; got != want {
		t.Errorf("run log = %s\nwant      %s", got, want)
	}
	if got := h.toasts(); len(got) != 1 || !strings.HasPrefix(got[0], "done: ") || !strings.Contains(got[0], "phase A") {
		t.Errorf("toasts = %q, want one phase-done toast", got)
	}

	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if run.Current != nil || strings.Join(run.Phases, " ") != "A" || run.ConfigHash != h.cfg.Hash() || !run.StartedAt.Equal(t0) {
		t.Errorf("state.json = %+v", run)
	}
	if sigs, bad, err := h.dir.ListSignals(); err != nil || len(sigs)+len(bad) != 0 {
		t.Errorf("signals left behind: %v %v %v", sigs, bad, err)
	}
	h.assertUnlocked()
}

func TestRunWaitsForTheSignal(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	// The agent looks finished long before it signals; that alone never
	// advances the run (SPEC §6.3).
	h.be.Script("A-1", backend.Working, backend.Done)
	h.clock.At(10*time.Second, func() {
		if got := h.statuses(); !strings.HasPrefix(got, "A-1=in progress A-2=blocked") {
			t.Errorf("before the signal: %s", got)
		}
		if err := h.signal("A-1"); err != nil {
			t.Error(err)
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := h.event(TaskDone, "A-1").At, t0.Add(10*time.Second); !got.Equal(want) {
		t.Errorf("A-1 accepted at %v, want %v", got, want)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
}

func TestRunThrough(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	res, err := h.run(func(o *Options) { o.Through = "b" })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outcome != Completed || res.Phase != "B" {
		t.Errorf("result = %s in phase %q, want completed in B", res.Outcome, res.Phase)
	}
	if got := h.opened(); got != "A-1 A-2 A-3 B-1" {
		t.Errorf("sessions opened for %q", got)
	}
	if got := h.count(PhaseDone); got != 2 {
		t.Errorf("%d phase_done events, want 2", got)
	}
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(run.Phases, " ") != "A B" || run.Through != "b" {
		t.Errorf("state.json phases = %v through %q", run.Phases, run.Through)
	}
}

func TestRunUnknownPhase(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	_, err := h.run(func(o *Options) { o.Phase = "Z" })
	if err == nil || !strings.Contains(err.Error(), "phases are: A, B") {
		t.Fatalf("err = %v, want the list of phases", err)
	}
	if _, err := h.dir.LoadRun(); !errors.Is(err, state.ErrNoRun) {
		t.Errorf("state.json written for a run that never started: %v", err)
	}
	h.assertUnlocked()
}

const stuckPlan = `## A

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet |
| A-2 | **Two** | C-1 | blocked | sonnet |

## B

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| B-1 | **Three** | — | ready | sonnet |
| B-2 | **Four** | C-1 | blocked | sonnet |

## C

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| C-1 | **Five** | — | ready | sonnet |
`

func TestRunStuck(t *testing.T) {
	tests := []struct {
		name, phase, through string
		wantPhase, wantOpen  string
		wantWaiting          string
	}{
		{"first phase", "A", "", "A", "A-1", "A-2 waits on C-1 (ready, phase C)"},
		{"stops a --through run early", "B", "C", "B", "B-1", "B-2 waits on C-1 (ready, phase C)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, stuckPlan, "")
			res, err := h.run(func(o *Options) { o.Phase, o.Through = tt.phase, tt.through })
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if res.Outcome != Stuck || res.Phase != tt.wantPhase {
				t.Errorf("result = %s in %q, want stuck in %s", res.Outcome, res.Phase, tt.wantPhase)
			}
			if len(res.Waiting) != 1 || res.Waiting[0].String() != tt.wantWaiting {
				t.Errorf("waiting = %v, want %q", res.Waiting, tt.wantWaiting)
			}
			if got := h.opened(); got != tt.wantOpen {
				t.Errorf("sessions opened for %q, want %q", got, tt.wantOpen)
			}
			if ev := h.event(PhaseStuck, ""); len(ev.Waiting) != 1 || ev.Phase != tt.wantPhase {
				t.Errorf("phase_stuck event = %+v", ev)
			}
			if got := h.toasts(); len(got) != 1 || !strings.HasPrefix(got[0], "request: ") || !strings.Contains(got[0], "stuck") {
				t.Errorf("toasts = %q, want one stuck toast", got)
			}
		})
	}
}

func TestPauseHoldsTheNextTask(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskStarted && ev.Task == "A-1" {
			h.eng.Send(Command{Kind: CmdPause})
		}
	}
	h.clock.At(time.Minute, func() {
		if got := h.opened(); got != "A-1" {
			t.Errorf("while paused: sessions opened for %q, want only A-1", got)
		}
		if got := h.statuses(); !strings.HasPrefix(got, "A-1=done A-2=ready") {
			t.Errorf("while paused: %s", got)
		}
		h.eng.Send(Command{Kind: CmdPause})
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.opened(); got != "A-1 A-2 A-3" {
		t.Errorf("sessions opened for %q", got)
	}
	if on, held, off := h.count(PauseOn), h.count(Paused), h.count(PauseOff); on != 1 || held != 1 || off != 1 {
		t.Errorf("pause events on/held/off = %d/%d/%d, want 1/1/1 in %s", on, held, off, h.kinds())
	}
	if got, want := h.event(TaskStarted, "A-2").At, t0.Add(time.Minute); !got.Equal(want) {
		t.Errorf("A-2 started at %v, want %v (when pause was toggled off)", got, want)
	}
}

func TestPauseToggledTwiceDoesNotHold(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskStarted && ev.Task == "A-1" {
			h.eng.Send(Command{Kind: CmdPause})
			h.eng.Send(Command{Kind: CmdPause})
		}
	}
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.count(Paused); got != 0 {
		t.Errorf("%d paused events, want none", got)
	}
}

func TestStopWhilePaused(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskStarted && ev.Task == "A-1" {
			h.eng.Send(Command{Kind: CmdPause})
		}
	}
	h.clock.At(10*time.Second, func() { h.eng.Send(Command{Kind: CmdStop}) })
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v; want stopped", res.Outcome, err)
	}
	if got := h.statuses(); got != "A-1=done A-2=ready A-3=blocked B-1=blocked" {
		t.Errorf("statuses = %s", got)
	}
	if got := h.opened(); got != "A-1" {
		t.Errorf("sessions opened for %q", got)
	}
}

func TestStopLeavesTheSessionOpen(t *testing.T) {
	tests := []struct {
		name string
		stop func(h *harness)
	}{
		{"stop command", func(h *harness) { h.eng.Send(Command{Kind: CmdStop}) }},
		{"context cancelled", func(h *harness) { h.cancel() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, chainPlan, "")
			h.be.SetAutoSignal(nil)
			h.clock.At(6*time.Second, func() { tt.stop(h) })
			res, err := h.run()
			if err != nil || res.Outcome != Stopped || res.Phase != "A" {
				t.Fatalf("Run = %s in %q, %v; want stopped in A", res.Outcome, res.Phase, err)
			}
			if got := len(h.be.Closed()); got != 0 {
				t.Errorf("closed %d sessions, want the session left open", got)
			}
			if got := h.statuses(); !strings.HasPrefix(got, "A-1=in progress A-2=blocked") {
				t.Errorf("statuses = %s", got)
			}
			run, err := h.dir.LoadRun()
			if err != nil {
				t.Fatal(err)
			}
			cur := run.Current
			if cur == nil || cur.TaskID != "A-1" || cur.Mode != ModeDefault || !cur.StartedAt.Equal(t0) {
				t.Fatalf("state.json current = %+v", cur)
			}
			spec := h.be.Opened()[0]
			if cur.ClaudeSession == "" || cur.ClaudeSession != arg(spec, "--session-id") {
				t.Errorf("stored Claude session %q, launched with %q", cur.ClaudeSession, arg(spec, "--session-id"))
			}
			if cur.Session == nil || cur.Session.Backend != fake.Name || cur.Session.PaneID == "" {
				t.Errorf("stored session ref = %+v", cur.Session)
			}
			if got := h.kinds(); !strings.HasSuffix(got, " run_stopped") {
				t.Errorf("events = %s", got)
			}
			if got := h.logged(); got != "run_started task_started run_stopped" {
				t.Errorf("run log = %s", got)
			}
			h.assertUnlocked()
		})
	}
}

// waitingClock never fires: After reports that the run is waiting and
// returns a channel nothing is ever sent on.
type waitingClock struct{ waiting chan struct{} }

func (waitingClock) Now() time.Time { return t0 }

func (c waitingClock) After(time.Duration) <-chan time.Time {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return nil
}

// Send must wake a waiting run from another goroutine: with a clock that
// never fires, only the wake-up can end this run.
func TestSendWakesTheRun(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.be.SetAutoSignal(nil)
	clock := waitingClock{waiting: make(chan struct{}, 1)}
	eng, err := New(Options{Config: h.cfg, Backend: h.be, State: h.dir, Clock: clock, Phase: "A"})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome)
	go func() {
		res, err := eng.Run(context.Background())
		done <- outcome{res, err}
	}()
	<-clock.waiting // the run is now blocked in its poll wait
	eng.Send(Command{Kind: CmdStop})
	if got := <-done; got.err != nil || got.res.Outcome != Stopped {
		t.Errorf("Run = %s, %v; want stopped", got.res.Outcome, got.err)
	}
}

// A context cancelled in the middle of a backend call is a stop, not a
// failed run.
func TestCancelDuringABackendCallIsAStop(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskStarted {
			h.cancel() // OpenSession now fails with the context's error
		}
	}
	res, err := h.run()
	if err != nil || res.Outcome != Stopped || res.Phase != "A" {
		t.Fatalf("Run = %s in %q, %v; want stopped in A", res.Outcome, res.Phase, err)
	}
	if got := h.count(RunFailed); got != 0 {
		t.Errorf("%d run_error events, want none", got)
	}
	if got := h.logged(); got != "run_started task_started run_stopped" {
		t.Errorf("run log = %s", got)
	}
}

// The config file is watched between tasks too, e.g. while the run is paused.
func TestConfigChangeIsReportedWhilePaused(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskStarted && ev.Task == "A-1" {
			h.eng.Send(Command{Kind: CmdPause})
		}
	}
	h.clock.At(10*time.Second, func() { h.write(state.ConfigFile, "default_mode = \"plan\"\n") })
	h.clock.At(20*time.Second, func() { h.eng.Send(Command{Kind: CmdStop}) })
	if res, err := h.run(); err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v; want stopped", res.Outcome, err)
	}
	if got, want := h.event(ConfigChanged, "").At, t0.Add(10*time.Second); !got.Equal(want) {
		t.Errorf("config change reported at %v, want %v", got, want)
	}
}

func TestConfigChangeIsReportedOncePerChange(t *testing.T) {
	const original = "poll_interval = \"2s\"\n"
	h := newHarness(t, chainPlan, original)
	h.autoSignalExcept("A-1")
	edit := func(at time.Duration, content string) {
		h.clock.At(at, func() { h.write(state.ConfigFile, content) })
	}
	edit(4*time.Second, "poll_interval = \"2s\"\n\n[models]\nopus = \"haiku\"\nsonnet = \"sonnet\"\n") // change 1
	edit(8*time.Second, "# a comment\npoll_interval = \"2s\"\n\n[models]\nopus = \"haiku\"\nsonnet = \"sonnet\"\n")
	edit(12*time.Second, "this is = = not toml\n")                                 // change 2
	edit(16*time.Second, original)                                                 // restored
	edit(20*time.Second, "poll_interval = \"2s\"\n\n[models]\nopus = \"haiku\"\n") // change 3
	h.clock.At(24*time.Second, func() {
		if err := h.signal("A-1"); err != nil {
			t.Error(err)
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if changed, restored := h.count(ConfigChanged), h.count(ConfigRestored); changed != 3 || restored != 1 {
		t.Errorf("config events changed/restored = %d/%d, want 3/1 in %s", changed, restored, h.kinds())
	}
	needsYou := 0
	for _, toast := range h.toasts() {
		if strings.HasPrefix(toast, "request: ") && strings.Contains(toast, state.ConfigFile) {
			needsYou++
		}
	}
	if needsYou != 3 {
		t.Errorf("%d config toasts, want 3: %q", needsYou, h.toasts())
	}
	// The run keeps the snapshot: A-2 still launches with the model the
	// config had at start (SPEC §13).
	if got := arg(h.be.Opened()[1], "--model"); got != "opus" {
		t.Errorf("A-2 launched with --model %q after the config was edited, want opus", got)
	}
}

func TestConfigRemovedCountsAsChange(t *testing.T) {
	h := newHarness(t, chainPlan, "default_mode = \"accept\"\n")
	h.autoSignalExcept("A-1")
	h.clock.At(4*time.Second, func() {
		// No file means defaults, which differ from the snapshot.
		if err := os.Remove(h.path(state.ConfigFile)); err != nil {
			t.Error(err)
		}
	})
	h.clock.At(8*time.Second, func() {
		if err := h.signal("A-1"); err != nil {
			t.Error(err)
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(ConfigChanged); got != 1 {
		t.Errorf("%d config_changed events, want 1", got)
	}
	if got := arg(h.be.Opened()[2], "--permission-mode"); got != "acceptEdits" {
		t.Errorf("A-3 launched with --permission-mode %q, want the snapshot's acceptEdits", got)
	}
}

const driftPlan = `## A

| ID | Task | Deps | Status | Model |
|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet |
| A-2 | **Two** | A-1 | ready | sonnet |
`

func TestDriftNeedsConfirmation(t *testing.T) {
	h := newHarness(t, driftPlan, "")
	_, err := h.run()
	var drift *DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("err = %v, want a *DriftError", err)
	}
	if len(drift.Changes) != 1 || drift.Changes[0].String() != "A-2: ready → blocked" {
		t.Errorf("drift = %v", drift.Changes)
	}
	if got := h.read("tasks.md"); got != driftPlan {
		t.Errorf("plan written without confirmation:\n%s", got)
	}
	if got := h.opened(); got != "" {
		t.Errorf("sessions opened for %q without confirmation", got)
	}
	if _, err := h.dir.LoadRun(); !errors.Is(err, state.ErrNoRun) {
		t.Errorf("state.json written: %v", err)
	}
	h.assertUnlocked()

	res, err := h.run(func(o *Options) { o.ConfirmedDrift = true })
	if err != nil || res.Outcome != Completed {
		t.Fatalf("confirmed Run = %s, %v", res.Outcome, err)
	}
	if got := h.statuses(); got != "A-1=done A-2=done" {
		t.Errorf("statuses = %s", got)
	}
	// The first write also fixed the drifted cell.
	if got := h.event(TaskStarted, "A-1").Changes; len(got) != 2 || got[1].String() != "A-2: ready → blocked" {
		t.Errorf("first write changed %v, want A-1 in progress and the drift fix", got)
	}
}

const modePlan = `## A

| ID | Task | Deps | Status | Model | Mode |
|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | — |
| A-2 | **Two** | A-1 | blocked | sonnet | TASKMODE |
`

func TestSkipPermissionsNeedsConfirmation(t *testing.T) {
	const flag = "--dangerously-skip-permissions"
	tests := []struct {
		name, toml, taskMode, runMode string
		wantYolo                      string // tasks launched with the flag once confirmed
	}{
		{"--mode yolo", "", "—", "yolo", "A-1 A-2"},
		{"default_mode in config", "default_mode = \"yolo\"\n", "—", "", "A-1 A-2"},
		{"Mode column of a later task", "", "yolo", "", "A-2"},
		{"Mode column overrides a yolo run", "", "plan", "yolo", "A-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planText := strings.Replace(modePlan, "TASKMODE", tt.taskMode, 1)
			h := newHarness(t, planText, tt.toml)
			_, err := h.run(func(o *Options) { o.Mode = tt.runMode })
			if !errors.Is(err, ErrYoloUnconfirmed) {
				t.Fatalf("err = %v, want ErrYoloUnconfirmed", err)
			}
			if got := h.opened(); got != "" {
				t.Errorf("sessions opened for %q without confirmation", got)
			}
			if got := h.read("tasks.md"); got != planText {
				t.Errorf("plan written without confirmation:\n%s", got)
			}

			res, err := h.run(func(o *Options) { o.Mode, o.ConfirmedYolo = tt.runMode, true })
			if err != nil || res.Outcome != Completed {
				t.Fatalf("confirmed Run = %s, %v", res.Outcome, err)
			}
			var yolo []string
			for _, s := range h.be.Opened() {
				for _, a := range s.Args {
					if a == flag {
						yolo = append(yolo, s.TaskID)
					}
				}
			}
			if got := strings.Join(yolo, " "); got != tt.wantYolo {
				t.Errorf("%s passed to %q, want %q", flag, got, tt.wantYolo)
			}
		})
	}
}

// A plan edited mid-run can't switch a later task to skip-permissions, not
// even after the owner resumed past the plan-change hold.
func TestSkipPermissionsIsCheckedAgainAtLaunch(t *testing.T) {
	planText := strings.Replace(modePlan, "TASKMODE", "—", 1)
	h := newHarness(t, planText, "")
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened && ev.Task == "A-1":
			h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| blocked | sonnet | — |", "| blocked | sonnet | yolo |", 1))
		case ev.Kind == PlanChanged:
			h.eng.Send(Command{Kind: CmdPause}) // resume
		}
	}
	_, err := h.run()
	if !errors.Is(err, ErrYoloUnconfirmed) {
		t.Fatalf("err = %v, want ErrYoloUnconfirmed", err)
	}
	if ev := h.event(PlanChanged, ""); !strings.Contains(ev.Detail, "A-2 Mode — → yolo") {
		t.Errorf("plan_changed detail = %q", ev.Detail)
	}
	if got := h.opened(); got != "A-1" {
		t.Errorf("sessions opened for %q, want only A-1", got)
	}
	if got := h.statuses(); got != "A-1=done A-2=ready" {
		t.Errorf("statuses = %s, want A-2 never marked in progress", got)
	}
	if got := h.count(RunFailed); got != 1 {
		t.Errorf("%d run_error events, want 1", got)
	}
}

func TestRunRefusesWhenLocked(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	held, err := h.dir.Lock(false) // this test process is alive, so the lock is live
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.run()
	var locked *state.LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("err = %v, want a *state.LockedError", err)
	}
	if got := h.opened(); got != "" {
		t.Errorf("sessions opened for %q while locked", got)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestRunNeedsAnAvailableBackend(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.be.SetAvailable(errors.New("herdr server is not running"))
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), "herdr server is not running") {
		t.Fatalf("err = %v", err)
	}
	if got := h.read("tasks.md"); got != chainPlan {
		t.Error("plan written although the backend is unavailable")
	}
	h.assertUnlocked()
}

// A signal written before its task started (e.g. an early `igris done`) is
// kept and reported, never applied (SPEC §6.2).
func TestStaleSignalIsNotApplied(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	early := state.Signal{ID: "A-1", Action: state.ActionDone, Note: "early", At: t0.Add(-time.Minute)}
	if err := h.dir.WriteSignal(early); err != nil {
		t.Fatal(err)
	}
	h.clock.At(6*time.Second, func() {
		if sig, err := h.dir.ReadSignal("A-1"); err != nil || sig == nil || sig.Note != "early" {
			t.Errorf("stale signal not kept: %+v, %v", sig, err)
		}
		if err := h.signal("A-1"); err != nil {
			t.Error(err)
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(StaleSignal); got != 1 {
		t.Errorf("%d stale_signal events, want 1", got)
	}
	done := h.event(TaskDone, "A-1")
	if want := t0.Add(6 * time.Second); !done.At.Equal(want) || done.Detail != "did A-1" {
		t.Errorf("A-1 accepted at %v with note %q, want %v with the fresh signal's note", done.At, done.Detail, want)
	}
}

func TestOpenSessionFailure(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.be.FailOpen(errors.New("no pane for you"))
	res, err := h.run()
	if err == nil || !strings.Contains(err.Error(), "no pane for you") || !strings.Contains(err.Error(), "check that `claude` starts in fake") {
		t.Fatalf("err = %v", err)
	}
	if res.Phase != "A" {
		t.Errorf("result phase = %q", res.Phase)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=in progress") {
		t.Errorf("statuses = %s", got)
	}
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if run.Current == nil || run.Current.TaskID != "A-1" || run.Current.Session != nil {
		t.Errorf("state.json current = %+v, want A-1 without a session", run.Current)
	}
	if got := h.logged(); got != "run_started task_started error notification run_stopped" {
		t.Errorf("run log = %s", got)
	}
	if got := h.toasts(); len(got) != 1 || !strings.HasPrefix(got[0], "request: ") || strings.Contains(got[0], "no pane for you") {
		t.Errorf("toasts = %q, want one error toast without the error text", got)
	}
	if ev := h.event(RunFailed, ""); !strings.Contains(ev.Detail, "no pane for you") || ev.Task != "A-1" {
		t.Errorf("run_error event = %+v", ev)
	}
	h.assertUnlocked()
}

// The owner finishes a task by hand between selection and igris's write:
// igris must not overwrite that. The edit holds the run (SPEC §5.4); once
// the owner resumes, igris moves on to the next task.
func TestPlanEditedBeforeMarking(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	edited := false
	h.onEvent = func(ev Event) {
		if ev.Kind == PlanChanged {
			h.eng.Send(Command{Kind: CmdPause}) // resume
		}
	}
	res, err := h.run(func(o *Options) {
		o.beforeMark = func() {
			if !edited {
				edited = true
				h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| — | ready |", "| — | done |", 1))
			}
		}
	})
	if err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if ev := h.event(PlanChanged, ""); !strings.Contains(ev.Detail, "A-1 Status ready → done") {
		t.Errorf("plan_changed detail = %q", ev.Detail)
	}
	if on, off := h.count(PauseOn), h.count(PauseOff); on != 1 || off != 1 {
		t.Errorf("pause on/off = %d/%d, want 1/1 in %s", on, off, h.kinds())
	}
	if got := h.opened(); got != "A-2 A-3" {
		t.Errorf("sessions opened for %q, want A-2 A-3", got)
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
}

// A plan that can't be written when a task is about to start leaves no
// trace of that task: it never started.
func TestPlanInvalidBeforeMarking(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	_, err := h.run(func(o *Options) {
		o.beforeMark = func() {
			h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| blocked | fable |", "| nearly | fable |", 1))
		}
	})
	var invalid *plan.Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want a *plan.Invalid", err)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=ready A-2=blocked") {
		t.Errorf("statuses = %s, want the plan untouched", got)
	}
	if run, err := h.dir.LoadRun(); err != nil || run.Current != nil {
		t.Errorf("state.json current = %+v, %v; want none", run.Current, err)
	}
	if got := h.opened(); got != "" {
		t.Errorf("sessions opened for %q", got)
	}
}

func TestPlanBecomesInvalidMidRun(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == TaskDone && ev.Task == "A-1" {
			h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| A-1 | ready | opus |", "| A-1 | nearly | opus |", 1))
		}
	}
	_, err := h.run()
	var invalid *plan.Invalid
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want a *plan.Invalid", err)
	}
	if got := h.opened(); got != "A-1" {
		t.Errorf("sessions opened for %q, want only A-1", got)
	}
	if got := h.read("tasks.md"); !strings.Contains(got, "| nearly |") {
		t.Error("igris wrote the plan while it was invalid")
	}
}

func TestToastsCanBeDisabled(t *testing.T) {
	h := newHarness(t, chainPlan, "[notify.backend]\nenabled = false\n")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.toasts(); len(got) != 0 {
		t.Errorf("toasts = %q, want none", got)
	}
}

func TestCustomPromptTemplate(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\nprompt_template = \"my.tmpl\"\ncommit = \"never\"\n")
	h.write("my.tmpl", "{{.ID}} on {{.Model}} from {{.PlanFile}}, commit {{.CommitPolicy}}, resumed {{.Resumed}}\n")
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "A-2 on opus from tasks.md, commit never, resumed false\n"
	if got := h.be.Prompts("A-2"); len(got) != 1 || got[0] != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
}

func TestOutcomeString(t *testing.T) {
	for o, want := range map[Outcome]string{Completed: "completed", Stuck: "stuck", Stopped: "stopped", 0: "unknown"} {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d) = %q, want %q", o, got, want)
		}
	}
}

func TestConfigDefaultsAreUsedWithoutAFile(t *testing.T) {
	// A project without igris.toml runs on the defaults; that is not a change.
	h := newHarness(t, chainPlan, "")
	if err := os.Remove(h.path(state.ConfigFile)); err != nil {
		t.Fatal(err)
	}
	h.cfg = config.Default()
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := h.count(ConfigChanged); got != 0 {
		t.Errorf("%d config_changed events, want none", got)
	}
}

// Mode changes apply to the next session; skip-permissions needs the typed
// confirmation (SPEC §7.2, §7.3).
func TestModeCommand(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind != TaskStarted {
			return
		}
		switch ev.Task {
		case "A-1":
			h.eng.Send(Command{Kind: CmdMode, Text: "accept"})
		case "A-2":
			h.eng.Send(Command{Kind: CmdMode, Text: "wild"})
			h.eng.Send(Command{Kind: CmdMode, Text: "yolo"}) // not confirmed
			h.eng.Send(Command{Kind: CmdMode, Text: "YOLO", Yes: true})
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	specs := h.be.Opened()
	if got := arg(specs[0], "--permission-mode"); got != "" {
		t.Errorf("A-1 --permission-mode %q, want none (it was running when the mode changed)", got)
	}
	if got := arg(specs[1], "--permission-mode"); got != "acceptEdits" {
		t.Errorf("A-2 --permission-mode %q, want acceptEdits", got)
	}
	if got := strings.Join(specs[2].Args, " "); !strings.Contains(got, "--dangerously-skip-permissions") {
		t.Errorf("A-3 args %q, want skip permissions", got)
	}
	if got := h.count(ModeChanged); got != 2 {
		t.Errorf("%d mode_changed events, want 2", got)
	}
	if got := h.count(Warning); got != 2 {
		t.Errorf("%d warnings, want the unknown mode and the unconfirmed yolo rejected", got)
	}
}

// A per-task override beats the run mode for that task's next session;
// skip-permissions still needs the typed confirmation (SPEC §7.2, §7.3).
func TestTaskModeCommand(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	var changed []Event
	h.onEvent = func(ev Event) {
		if ev.Kind == ModeChanged || ev.Kind == TaskModeChanged {
			changed = append(changed, ev)
		}
		if ev.Kind != TaskStarted {
			return
		}
		switch ev.Task {
		case "A-1":
			h.eng.Send(Command{Kind: CmdMode, Text: "plan"})
			h.eng.Send(Command{Kind: CmdTaskMode, Task: "A-2", Text: "accept"})
			h.eng.Send(Command{Kind: CmdTaskMode, Task: "A-3", Text: "yolo"}) // not confirmed
			h.eng.Send(Command{Kind: CmdTaskMode, Task: "A-3", Text: "wild"})
			h.eng.Send(Command{Kind: CmdTaskMode, Text: "auto"}) // no task
		case "A-2":
			h.eng.Send(Command{Kind: CmdTaskMode, Task: "A-3", Text: "Yolo", Yes: true})
		}
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	specs := h.be.Opened()
	if got := arg(specs[1], "--permission-mode"); got != "acceptEdits" {
		t.Errorf("A-2 --permission-mode %q, want the override acceptEdits over the run mode plan", got)
	}
	if got := strings.Join(specs[2].Args, " "); !strings.Contains(got, "--dangerously-skip-permissions") {
		t.Errorf("A-3 args %q, want skip permissions", got)
	}
	want := []Event{{Kind: ModeChanged, Detail: "plan"}, {Kind: TaskModeChanged, Task: "A-2", Detail: "accept"}, {Kind: TaskModeChanged, Task: "A-3", Detail: "yolo"}}
	if len(changed) != len(want) {
		t.Fatalf("mode events %+v, want %+v", changed, want)
	}
	for i, ev := range changed {
		if ev.Kind != want[i].Kind || ev.Detail != want[i].Detail || (ev.Kind == TaskModeChanged && ev.Task != want[i].Task) {
			t.Errorf("mode event %d = %s %q %q, want %s %q %q", i, ev.Kind, ev.Task, ev.Detail, want[i].Kind, want[i].Task, want[i].Detail)
		}
	}
	if got := h.count(Warning); got != 3 {
		t.Errorf("%d warnings, want the unconfirmed yolo, the unknown mode and the missing task rejected", got)
	}
}
