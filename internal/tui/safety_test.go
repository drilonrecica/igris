package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// The home safety checklist (imp-docs/home-tui.md §8, SPEC §7.3, §16):
// every untrusted text source is cleaned before home draws it, yolo is
// never one key or one click, home is read-only under a live lock, and the
// run lock is released on every exit path of a run home started.

// zap is the marker injected into every text source: an SGR sequence, a
// BEL, and a word that must still be readable once cleaned.
const zap = "\x1b[31mZAP\x07"

// zapPlan is a valid plan with the marker in a phase title and a task
// title; the parser cleans cells, so pages drawing the plan stay clean.
var zapPlan = "## M2 — Config " + zap + " state\n\n" +
	"| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n" +
	"| M2-01 | **Lock " + zap + " file** | — | ready | sonnet | agent |\n" +
	"| M2-02 | **State file** | M2-01 | blocked | opus | agent |\n\n" +
	"## M3 — Engine\n\n" +
	"| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n" +
	"| M3-01 | **Scheduler** | M2-02 | blocked | opus | agent |\n"

// noControl fails when view holds a control character (the only one home
// draws is the newline between lines).
func noControl(t *testing.T, name, view string) {
	t.Helper()
	for _, r := range view {
		if r != '\n' && (r < 0x20 || r == 0x7f) {
			t.Errorf("%s: control character %q drawn:\n%s", name, r, view)
			return
		}
	}
}

// shows is clean, and the injected word is on screen: the source is
// drawn, not dropped.
func shows(t *testing.T, name, view string) {
	t.Helper()
	noControl(t, name, view)
	if !strings.Contains(view, "ZAP") {
		t.Errorf("%s: the injected text isn't drawn:\n%s", name, view)
	}
}

// zapEvents is a run log with the marker in every text field.
func zapEvents() []state.Event {
	at := func(m int) time.Time { return homeNow.Add(time.Duration(m-120) * time.Minute) }
	return []state.Event{
		{At: at(0), Type: state.EventRunStarted, Detail: "phase M2" + zap},
		{At: at(1), Type: state.EventTaskStarted, Task: "M2-02", Rank: "sonnet" + zap, Model: "claude-sonnet" + zap},
		{At: at(20), Type: state.EventVerifyFailed, Task: "M2-02", Detail: "attempt 1 of 2: bad" + zap},
		{At: at(40), Type: state.EventTaskDone, Task: "M2-02", Detail: "note " + zap},
		{At: at(41), Type: state.EventRunStopped, Detail: "completed"},
	}
}

// zapSnapshot is the project as Snapshot would read it with the marker in
// every file but the plan: the paths from igris.toml, its string values,
// state.json's session and runs.jsonl. The plan is planText: the parser
// refuses a plan with control characters, so one is drawn as invalid.
func zapSnapshot(t *testing.T, planText string) *report.Snapshot {
	t.Helper()
	cfg := config.Default()
	cfg.Run.Verify = "make " + zap + " test"
	cfg.Notify.Ntfy.Topic = "igris" + zap
	s := &report.Snapshot{
		Root: "/src/sinjal", Project: "sinjal", Found: true, Config: cfg,
		PlanPath:   "/src/sinjal/ta" + zap + "sks.md",
		ConfigPath: "/src/sinjal/ig" + zap + "ris.toml",
		Settings:   report.NewSettings(cfg, nil, nil),
		Lock:       state.LockState{Path: "/src/sinjal/.igris/igris.lock"},
	}
	s.Plan = plan.Parse("tasks.md", []byte(planText), plan.Options{})
	s.Issues = report.Issues(s.Plan.Validate(cfg.Models))
	if len(s.Issues) == 0 {
		st, err := report.Status(s.Plan, "")
		if err != nil {
			t.Fatal(err)
		}
		s.Status = &st
	}
	s.Recent = report.NewHistory(report.HistoryInput{Events: zapEvents(), N: 3}).Runs
	s.Run = report.NewRunInfo(report.RunInput{Run: &state.Run{
		Phases: []string{"M2"}, StartedAt: homeNow.Add(-2 * time.Hour),
		Current: &state.Current{TaskID: "M2-01", Mode: "plan", StartedAt: homeNow.Add(-2 * time.Hour),
			Session: &backend.SessionRef{Backend: "herdr", TabID: "tab" + zap}},
	}})
	return s
}

// zapVersions is the tool version check answering with the marker.
func zapVersions(context.Context, runner.Runner) []checks.Result {
	return []checks.Result{{ID: checks.IDClaude, Level: checks.Warn, Message: "claude " + zap, Confirm: true}}
}

// TestHomeCleansEveryTextSource injects the marker into every text source
// home and its pages draw (SPEC §16, §17) and checks each screen.
func TestHomeCleansEveryTextSource(t *testing.T) {
	ctx := context.Background()
	noEnv := func(string) string { return "" }

	// The dry run on a project whose start-up checks hold the marker.
	validPlan := strings.ReplaceAll(homePlan, "STATUS", "ready")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte(validPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	dry, err := engine.DryRun(ctx, engine.DryRunOptions{Root: root, Config: config.Default(), Phase: "M2", Runner: &runner.Fake{}, Getenv: noEnv, Versions: zapVersions})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}

	snap := zapSnapshot(t, validPlan)
	svc := &fakeServices{
		snapshot:  func(context.Context) (*report.Snapshot, error) { return snap, nil },
		doctor:    checks.Doctor(ctx, checks.DoctorOptions{Dir: root, Options: checks.Options{Runner: &runner.Fake{}, Getenv: noEnv, Versions: zapVersions}}),
		history:   report.NewHistory(report.HistoryInput{Events: zapEvents(), N: 10}),
		preview:   func(report.RunRequest) (*report.DryRun, error) { return &dry, nil },
		prelaunch: report.Prelaunch{Warnings: checks.Problems(checks.Run(ctx, checks.Options{IDs: []string{checks.IDClaude}, Runner: &runner.Fake{}, Versions: zapVersions}))},
		notify:    []report.NotifyResult{{Event: "needs_input", Channel: "ntfy", Err: "post https://[redacted]/: " + zap}},
		edit:      func(string) (tea.ExecCommand, error) { return nil, errors.New("exec: " + zap) },
	}
	st := &starts{answers: []error{
		&state.LockedError{Info: state.LockInfo{PID: 7, Host: "mbp" + zap, StartedAt: homeNow}, Path: "/src/sinjal/.igris/igris.lock", Remote: true},
		&state.StaleLockError{Reason: "at /src/sinjal/.igris/igris.lock (" + zap + ")"},
		&engine.DriftError{Changes: []plan.Change{{ID: "M2-02", From: plan.Blocked, To: plan.Ready}}},
		fmt.Errorf("task %s would run in yolo mode: %w", "M2-01"+zap, engine.ErrYoloUnconfirmed),
		errors.New("backend herdr is not available: " + zap),
	}}
	svc.start = st.start
	a, home := pollApp(t, svc)
	home.loc = time.UTC
	home.now = func() time.Time { return homeNow }
	view := func() string { return a.View() }

	for _, size := range homeSizes {
		a.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		shows(t, fmt.Sprintf("home %dx%d", size[0], size[1]), view())
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// The pages.
	press(a, "c")
	noControl(t, "check page", view())
	press(a, "esc", "i")
	shows(t, "doctor page", view())
	press(a, "esc", "h")
	shows(t, "history runs", view())
	press(a, "enter")
	shows(t, "history run", view())
	press(a, "enter")
	shows(t, "history task", view())
	press(a, "esc", "esc", "esc", ",")
	shows(t, "settings page", view())
	press(a, "esc", "v")
	shows(t, "preview page", view())
	press(a, "esc")
	focusPhases(a)
	press(a, "enter")
	noControl(t, "phase page", view())
	press(a, "enter")
	shows(t, "task page", view()) // the attempts from runs.jsonl
	press(a, "esc", "esc")
	if len(a.stack) != 1 {
		t.Fatalf("%d screens open, want home alone", len(a.stack))
	}

	// Notify test: the dialog, then the page with the delivery errors.
	press(a, "n")
	noControl(t, "notify dialog", view())
	press(a, "enter")
	shows(t, "notify page", view())
	press(a, "esc")

	// The editor: one that can't start, and one that failed.
	press(a, "e")
	shows(t, "editor can't start", home.status)
	drive(a, func() tea.Msg {
		return ownedMsg{home, editedMsg{editTarget{path: snap.PlanPath, plan: true}, errors.New("exit " + zap)}}
	})
	shows(t, "editor failed", home.status)
	shows(t, "home with the status line", view())

	// The wizard: what to run (the interrupted task from state.json), the
	// phase list, the start-up warnings, and every error a launch can fail
	// with (the lock's host and reason among them).
	for i, want := range []string{"Clear the lock and start?", "Clear the lock and start?", "Let igris fix them?", "Skip permissions?", "The run did not start"} {
		press(a, "a")
		noControl(t, "wizard what to run", view())
		press(a, "2") // Start a phase…
		noControl(t, "wizard phase", view())
		press(a, "enter", "enter", "enter")
		shows(t, "wizard summary", view())
		press(a, "enter") // Arise
		shows(t, "wizard confirmation", view())
		press(a, "2") // Start anyway
		if home.dialog == nil || home.dialog.title != want {
			t.Fatalf("launch %d: dialog %+v, want %q", i, home.dialog, want)
		}
		shows(t, "launch failed: "+want, view())
		if want == "Skip permissions?" {
			press(a, "enter") // leave the field
		}
		press(a, "esc")
		if home.launch != nil {
			t.Fatalf("launch %d: the wizard is still open", i)
		}
	}
	press(a, "a", "2", "enter", "enter", "enter", "2") // Preview from the summary
	shows(t, "preview from the wizard", view())
	press(a, "esc")

	// A project that can't be read: the card and Settings say why.
	bad := homeAt(&theme{}, 120, 40, homeFixture{snapErr: errors.New("permission denied " + zap)})
	shows(t, "unreadable card", bad.View())
	s := newSettingsScreen(bad)
	sized(s, 120, 40)
	shows(t, "unreadable settings", s.View())

	// No plan yet: the GET STARTED card and the Example plan dialog name
	// the path from igris.toml.
	missing := zapSnapshot(t, validPlan)
	missing.Plan, missing.Status, missing.Run, missing.Recent, missing.PlanMissing = nil, nil, nil, nil, true
	none := &fakeServices{snapshot: func(context.Context) (*report.Snapshot, error) { return missing, nil }}
	na, nh := pollApp(t, none)
	shows(t, "no plan card", na.View())
	press(na, "E")
	if nh.initing == nil || nh.dialog == nil {
		t.Fatal("E didn't open the Example plan dialog")
	}
	shows(t, "example plan dialog", na.View())
	press(na, "esc")

	// A plan with the marker in it is invalid: the card and Check list the
	// problems, and the phase titles never reach the screen.
	invalid := homeAt(&theme{}, 120, 40, homeFixture{snap: zapSnapshot(t, zapPlan), doctorDone: true})
	if invalid.kind() != nowPlanInvalid {
		t.Fatalf("a plan with control characters is %v, want invalid", invalid.kind())
	}
	cells := func(v string) bool {
		return strings.Contains(v, "Config ZAP state") || strings.Contains(v, "Lock ZAP file")
	}
	v := invalid.View()
	noControl(t, "invalid plan card", v)
	if !strings.Contains(v, "PLAN INVALID") || cells(v) {
		t.Errorf("the invalid plan's cells are drawn:\n%s", v)
	}
	c := newCheckScreen(invalid)
	sized(c, 120, 40)
	v = c.View()
	noControl(t, "invalid plan check page", v)
	if !strings.Contains(v, "control character") || cells(v) {
		t.Errorf("check page:\n%s", v)
	}
}

// TestYoloNeverOneClick: a click on the yolo dialog's confirming choice,
// or its number, confirms nothing without the phrase (SPEC §7.3).
func TestYoloNeverOneClick(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, nil)
	press(a, "a", "enter", "enter", "6")
	d := home.dialog
	if d == nil || d.title != "Skip permissions?" || !d.inField {
		t.Fatalf("dialog %+v, want the phrase field", d)
	}
	home.View() // records the option zones
	var confirm *zone
	for i := range home.zones.list {
		if z := &home.zones.list[i]; z.t.act == actOption && z.t.option == 1 {
			confirm = z
		}
	}
	if confirm == nil {
		t.Fatal("the confirming choice has no click zone")
	}
	_, cmd := a.Update(tea.MouseMsg{X: confirm.r.x, Y: confirm.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	drive(a, cmd)
	if home.dialog != d || !d.inField || d.input.hint == "" || home.launch.conf.Yolo || home.launch.req.Mode == engine.ModeYolo {
		t.Errorf("a click confirmed yolo: dialog %+v, launch %+v", home.dialog, home.launch)
	}
	press(a, "enter", "2") // leave the field, then the choice's number
	if home.dialog != d || !d.inField || home.launch.conf.Yolo || home.launch.req.Mode == engine.ModeYolo {
		t.Errorf("a number confirmed yolo: dialog %+v, launch %+v", home.dialog, home.launch)
	}
	if st.count() != 0 {
		t.Errorf("%d runs started", st.count())
	}
}

// TestHomeReadOnlyUnderALiveLock: while another igris on this host holds
// the lock, nothing on home or its pages starts a run, init or adapt, and
// editing the plan asks first (SPEC §13, §15.6).
func TestHomeReadOnlyUnderALiveLock(t *testing.T) {
	live := func(f homeFixture) homeFixture {
		f.snap.Lock = state.LockState{Held: true, Alive: true, Info: state.LockInfo{PID: 4242, Host: "this-host", StartedAt: homeNow}, Path: f.snap.Root + "/.igris/igris.lock"}
		return f
	}
	noConfig := func() homeFixture {
		s := &report.Snapshot{Root: "/src/newproj", Project: "newproj", Found: true, Config: config.Default(), NoConfig: true, ConfigPath: "/src/newproj/igris.toml", PlanPath: "/src/newproj/tasks.md", PlanMissing: true}
		return homeFixture{snap: s, doctor: []checks.Result{{ID: checks.IDConfigValid, Level: checks.Fail, Message: "igris.toml not found", Next: "igris init"}}, doctorDone: true}
	}
	offered := func(s interface{ buttons() []pageButton }, a action) bool {
		for _, b := range s.buttons() {
			if b.act == a {
				return true
			}
		}
		return false
	}
	quiet := func(t *testing.T, a *appModel, home *homeScreen, svc *fakeServices, keys ...string) {
		t.Helper()
		for _, k := range keys {
			press(a, k)
			if home.launch != nil || home.initing != nil || home.adapting || home.dialog != nil || len(a.stack) != 1 {
				t.Errorf("%s: launch %v, init %v, adapt %v, dialog %v, %d screens", k, home.launch, home.initing, home.adapting, home.dialog, len(a.stack))
			}
		}
		for _, call := range []string{"Start", "Init", "Adapt"} {
			if n := svc.called(call); n != 0 {
				t.Errorf("%s called %d times", call, n)
			}
		}
	}

	t.Run("valid plan", func(t *testing.T) {
		a, svc := planApp(t, live(readyFix()))
		home := a.stack[0].(*homeScreen)
		if home.kind() != nowRunningElsewhere {
			t.Fatalf("state %v", home.kind())
		}
		quiet(t, a, home, svc, "a", "A", "I", "E")
		press(a, "e")
		if d := home.dialog; d == nil || d.title != "A run is going" || d.options[0].label != "Cancel" || svc.called("EditCommand") != 0 {
			t.Errorf("e: dialog %+v, EditCommand called %d times; want a question first", d, svc.called("EditCommand"))
		}
		press(a, "esc")
		focusPhases(a)
		press(a, "enter")
		s, ok := top(a).(*phaseScreen)
		if !ok || offered(s, actArise) {
			t.Fatalf("phase page %T offers Arise", top(a))
		}
		press(a, "a")
		if home.launch != nil || svc.called("Start") != 0 {
			t.Error("a on the phase page started a run")
		}
		press(a, "esc")
	})

	t.Run("invalid plan", func(t *testing.T) {
		a, svc := planApp(t, live(invalidFix()))
		home := a.stack[0].(*homeScreen)
		quiet(t, a, home, svc, "a", "A")
		press(a, "c")
		s, ok := top(a).(*checkScreen)
		if !ok || offered(s, actAdapt) {
			t.Fatalf("check page %T offers Adapt", top(a))
		}
		press(a, "A")
		if home.adapting || svc.called("Adapt") != 0 {
			t.Error("A on the check page started adapt")
		}
		press(a, "esc", "i")
		d, ok := top(a).(*doctorScreen)
		if !ok || offered(d, actAdapt) {
			t.Fatalf("doctor page %T offers Adapt", top(a))
		}
		press(a, "esc")
	})

	t.Run("no config", func(t *testing.T) {
		a, svc := planApp(t, live(noConfig()))
		home := a.stack[0].(*homeScreen)
		quiet(t, a, home, svc, "I", "E")
		press(a, ",")
		s, ok := top(a).(*settingsScreen)
		if !ok || offered(s, actInit) {
			t.Fatalf("settings page %T offers Init", top(a))
		}
		press(a, "I")
		if home.initing != nil || svc.called("Init") != 0 {
			t.Error("I on the settings page started init")
		}
		press(a, "esc", "i")
		d, ok := top(a).(*doctorScreen)
		if !ok || offered(d, actInit) {
			t.Fatalf("doctor page %T offers Init", top(a))
		}
		press(a, "I")
		if home.initing != nil || svc.called("Init") != 0 {
			t.Error("I on the doctor page started init")
		}
	})
}

// A real engine on a temp project, for the lock tests.

// goingPlan has one task; the fake backend's session never signals, so
// the run goes on until it is stopped.
const goingPlan = `## M2 — Config & state

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M2-01 | **Lock file** | — | ready | sonnet | agent |
`

// driftPlan marks M2-02 ready though it waits on M2-01: the engine takes
// the lock, then refuses to start without the drift confirmed.
const driftPlan = goingPlan + `| M2-02 | **State file** | M2-01 | ready | sonnet | agent |
`

// realProject writes planText to a temp project and returns its root and
// a Start that builds `igris arise`'s engine on it with a fake backend.
func realProject(t *testing.T, planText string) (string, func(report.RunRequest, report.Confirmations, func(engine.Event)) (Runner, error)) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte(planText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Run.Commit = "never"
	cmds := &runner.Fake{}
	cmds.Func(func(runner.Cmd) (runner.Result, error) { return runner.Result{}, nil })
	be := fake.New()
	start := func(req report.RunRequest, c report.Confirmations, events func(engine.Event)) (Runner, error) {
		dir, err := state.Open(root, state.Options{})
		if err != nil {
			return nil, err
		}
		return engine.New(engine.Options{
			Config: cfg, Backend: be, State: dir, Runner: cmds,
			Phase: req.Phase, Through: req.Through, Mode: req.Mode,
			ForceUnlock: c.ForceUnlock, ConfirmedDrift: c.Drift, ConfirmedYolo: c.Yolo,
			Events: events,
		})
	}
	return root, start
}

// lockHeld says whether root's run lock is held right now.
func lockHeld(t *testing.T, root string) bool {
	t.Helper()
	l, err := state.PeekLock(root)
	if err != nil {
		t.Fatal(err)
	}
	return l.Held
}

// realProgram runs the app on a real project in a real program: the fake
// snapshot names the real plan, so the run view reads it.
func realProgram(t *testing.T, planText string) (*teatest.TestModel, string) {
	t.Helper()
	root, start := realProject(t, planText)
	svc := &fakeServices{start: start}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) {
		s := homeSnap("ready")
		s.Root, s.PlanPath = root, filepath.Join(root, "tasks.md")
		return s, nil
	}
	return appProgram(t, svc, 120, 40), root
}

// TestEmbeddedRunReleasesTheLock: the run lock is released on every way
// out of a run home started (home-tui §8); `make test-race` runs this
// under the race detector.
func TestEmbeddedRunReleasesTheLock(t *testing.T) {
	t.Run("leave", func(t *testing.T) {
		tm, root := realProgram(t, goingPlan)
		seen(t, tm, "READY")
		ariseM2(t, tm)
		seen(t, tm, "[Home]")
		if !lockHeld(t, root) {
			t.Fatal("the run holds no lock")
		}
		tm.Type("q")
		seen(t, tm, "igris stopped; a running session keeps running")
		if lockHeld(t, root) {
			t.Error("the lock is still held after leaving the run")
		}
		tm.Type("q")
		finalApp(t, tm)
	})

	t.Run("stop", func(t *testing.T) {
		tm, root := realProgram(t, goingPlan)
		seen(t, tm, "READY")
		ariseM2(t, tm)
		seen(t, tm, "[Home]")
		tm.Type("x")
		seen(t, tm, "Stop igris?")
		tm.Type("2")
		seen(t, tm, "igris stopped; a running session keeps running", "READY")
		if lockHeld(t, root) {
			t.Error("the lock is still held after Stop")
		}
		tm.Type("q")
		finalApp(t, tm)
	})

	t.Run("ctrl+c", func(t *testing.T) {
		tm, root := realProgram(t, goingPlan)
		seen(t, tm, "READY")
		ariseM2(t, tm)
		seen(t, tm, "[Home]")
		tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
		a := finalApp(t, tm)
		if r := a.finish(); !r.Stopped {
			t.Errorf("finish %+v, want the run stopped", r)
		}
		if lockHeld(t, root) {
			t.Error("the lock is still held after ctrl+c")
		}
	})

	t.Run("launch failure", func(t *testing.T) {
		tm, root := realProgram(t, driftPlan)
		seen(t, tm, "READY")
		ariseM2(t, tm)
		seen(t, tm, "Let igris fix them?")
		if lockHeld(t, root) {
			t.Error("the lock is still held while the drift question is open")
		}
		tm.Type("2") // Let igris fix them: the run starts
		seen(t, tm, "[Home]")
		if !lockHeld(t, root) {
			t.Error("the run holds no lock")
		}
		tm.Type("q")
		seen(t, tm, "igris stopped; a running session keeps running")
		if lockHeld(t, root) {
			t.Error("the lock is still held after leaving the run")
		}
		tm.Type("q")
		finalApp(t, tm)
	})

	t.Run("panic", func(t *testing.T) {
		root, start := realProject(t, goingPlan)
		feed := NewFeed()
		runCtx, stop := context.WithCancel(context.Background())
		defer stop()
		r, err := start(report.RunRequest{Phase: "M2"}, report.Confirmations{}, feed.Push)
		if err != nil {
			t.Fatal(err)
		}
		go func() { feed.End(r.Run(runCtx)) }()
		<-feed.Started()
		if !lockHeld(t, root) {
			t.Fatal("the run holds no lock")
		}
		// The app holds the run, and a screen on top panics on its next
		// key. Bubble Tea recovers, Run returns, and App's finish stops
		// the run and waits for it.
		a := testApp(&fakeServices{}, 80, 24)
		a.Update(runMsg{&runHandle{feed: feed, stop: stop}})
		drive(a, push(&panicker{}))
		p := tea.NewProgram(a, tea.WithInput(bytes.NewBuffer(nil)), tea.WithOutput(io.Discard), tea.WithoutSignals(), tea.WithoutRenderer())
		go p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
		err = silenced(func() error {
			_, err := p.Run()
			return err
		})
		if !errors.Is(err, tea.ErrProgramPanic) {
			t.Fatalf("Run returned %v, want the recovered panic", err)
		}
		if res := a.finish(); !res.Stopped {
			t.Errorf("finish %+v, want the run stopped", res)
		}
		if lockHeld(t, root) {
			t.Error("the lock is still held after the panic")
		}
	})
}

// panicker is a screen whose first key press panics.
type panicker struct{}

func (*panicker) Init() tea.Cmd { return nil }
func (p *panicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		panic("a bug in a screen")
	}
	return p, nil
}
func (*panicker) View() string { return "panicker" }

// silenced runs fn with stdout and stderr sent to the null device: Bubble
// Tea prints the panic and its stack trace when it recovers.
func silenced(fn func() error) error {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fn()
	}
	defer func() { _ = null.Close() }()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	return fn()
}
