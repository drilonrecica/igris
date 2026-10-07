package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// fakeRunner is an engine for the wizard's tests: it starts at once (its
// first event), then runs until it is stopped, by Stop or its context, or
// ends on its own with end when that is set.
type fakeRunner struct {
	events func(engine.Event)
	end    *engine.Result
	// hold makes Run wait for its context before its first event, as a
	// slow herdr check would.
	hold bool

	mu   sync.Mutex
	sent []engine.Command
	stop chan struct{}
	once sync.Once
}

func newFakeRunner(events func(engine.Event)) *fakeRunner {
	return &fakeRunner{events: events, stop: make(chan struct{})}
}

func (r *fakeRunner) Run(ctx context.Context) (engine.Result, error) {
	if r.hold {
		<-ctx.Done()
		return engine.Result{}, errors.New("backend herdr is not available: " + ctx.Err().Error())
	}
	r.events(engine.Event{Kind: engine.RunStarted, Detail: "phase M2", At: t0})
	r.events(engine.Event{Kind: engine.PhaseStarted, Phase: "M2", At: t0})
	if r.end != nil {
		r.events(engine.Event{Kind: engine.RunStopped, Detail: r.end.Outcome.String(), At: t0})
		return *r.end, nil
	}
	select {
	case <-ctx.Done():
	case <-r.stop:
	}
	r.events(engine.Event{Kind: engine.RunStopped, Detail: "stopped", At: t0})
	return engine.Result{Outcome: engine.Stopped, Phase: "M2"}, nil
}

func (r *fakeRunner) Send(c engine.Command) {
	r.mu.Lock()
	r.sent = append(r.sent, c)
	r.mu.Unlock()
	if c.Kind == engine.CmdStop {
		r.once.Do(func() { close(r.stop) })
	}
}

// starts records what each Start was asked, and answers with the next of
// its answers (an error, or a runner when the answer is nil); after the
// last one it keeps failing.
type starts struct {
	mu      sync.Mutex
	reqs    []report.RunRequest
	confs   []report.Confirmations
	answers []error
	runner  func(events func(engine.Event)) *fakeRunner
	last    *fakeRunner
}

var errStopHere = errors.New("fake: stop here")

func (s *starts) start(req report.RunRequest, c report.Confirmations, events func(engine.Event)) (Runner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs, s.confs = append(s.reqs, req), append(s.confs, c)
	n := len(s.reqs) - 1
	if n >= len(s.answers) {
		return nil, errStopHere
	}
	if err := s.answers[n]; err != nil {
		return nil, err
	}
	mk := s.runner
	if mk == nil {
		mk = newFakeRunner
	}
	s.last = mk(events)
	return s.last, nil
}

func (s *starts) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func (s *starts) conf(i int) report.Confirmations {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.confs[i]
}

func (s *starts) req(i int) report.RunRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reqs[i]
}

// wizardApp is an app on the ready sinjal project, with snap changed by
// edit, read and checked.
func wizardApp(t *testing.T, st *starts, pre report.Prelaunch, edit func(*report.Snapshot)) (*appModel, *homeScreen) {
	t.Helper()
	svc := &fakeServices{prelaunch: pre, start: st.start}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) {
		s := homeSnap("ready")
		if edit != nil {
			edit(s)
		}
		return s, nil
	}
	a, home := pollApp(t, svc)
	return a, home
}

// press sends keys to the app and drives what they start.
func press(a *appModel, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "backspace":
			msg = tea.KeyMsg{Type: tea.KeyBackspace}
		default:
			if len([]rune(k)) == 1 {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
			} else {
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k), Paste: true}
			}
		}
		_, cmd := a.Update(msg)
		drive(a, cmd)
	}
}

// labels are the open dialog's options.
func labels(d *dialog) []string {
	var out []string
	for _, o := range d.options {
		out = append(out, o.label)
	}
	return out
}

func TestWizardSteps(t *testing.T) {
	st := &starts{}
	pre := report.Prelaunch{Warnings: []checks.Result{{ID: checks.IDGit, Level: checks.Warn, Message: "the working tree has uncommitted changes"}}}
	a, home := wizardApp(t, st, pre, nil)

	press(a, "a")
	d := home.dialog
	if d == nil || d.title != "Phase" {
		t.Fatalf("a opened %+v, want the phase dialog (nothing to resume)", d)
	}
	// Phases with work first, complete ones last; the selected phase (M2)
	// is the default.
	want := []string{"M2 Config & state · 1/4 next", "M3 Engine · 0/3 wait", "M4 TUI · 0/2 wait", "M0 Foundations · 3/3 complete", "M1 Plan parser · 2/2 complete"}
	if got := labels(d); strings.Join(got, "|") != strings.Join(want, "|") || d.selected != 0 {
		t.Fatalf("phases %q (selected %d), want %q with M2 selected", got, d.selected, want)
	}

	press(a, "enter")
	d = home.dialog
	want = []string{"Only M2", "M2 through M3", "M2 through M4"}
	if d.title != "Through" || strings.Join(labels(d), "|") != strings.Join(want, "|") {
		t.Fatalf("through dialog %q %q, want %q", d.title, labels(d), want)
	}

	press(a, "down", "enter")
	d = home.dialog
	if d.title != "Mode" || d.selected != 0 || d.options[0].label != "As planned (Mode column, then default_mode: default)" || len(d.options) != 6 {
		t.Fatalf("mode dialog %q %q (selected %d)", d.title, labels(d), d.selected)
	}
	if last := d.options[5].label; !strings.Contains(last, "yolo [SKIP PERMISSIONS]") {
		t.Errorf("last mode %q, want yolo with its badge", last)
	}

	press(a, "enter") // As planned; the checks are back at once here
	d = home.dialog
	wantTitle := "Arise M2 through M3 · mode as planned · 6 tasks · first M2-02 opus"
	if d.title != wantTitle || !strings.Contains(d.detail, "warning: the working tree has uncommitted changes") {
		t.Fatalf("summary %q / %q, want %q with the warning", d.title, d.detail, wantTitle)
	}
	if got := strings.Join(labels(d), "|"); got != "Arise|Preview|Cancel" || d.selected != 0 {
		t.Errorf("summary choices %q (selected %d)", got, d.selected)
	}

	press(a, "esc")
	if home.dialog != nil || home.launch != nil || st.count() != 0 {
		t.Errorf("esc left dialog %v, launch %v, %d starts; want home with nothing started", home.dialog, home.launch, st.count())
	}
}

func TestWizardOnlyWhenAriseApplies(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, func(s *report.Snapshot) { s.Lock = state.LockState{Held: true, Alive: true} })
	press(a, "a")
	if home.launch != nil || home.dialog != nil {
		t.Errorf("a opened the wizard under a live lock")
	}
}

func TestWizardResume(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{Interrupted: "M2-02"}, func(s *report.Snapshot) {
		s.Run = homeRun("M2-02", homeNow.Add(-time.Hour))
	})
	press(a, "a")
	d := home.dialog
	if d.title != "What to run" || strings.Join(labels(d), "|") != "Resume last run (M2-02 first)|Start a phase…" || d.selected != 0 {
		t.Fatalf("first dialog %q %q (selected %d), want Resume as the default", d.title, labels(d), d.selected)
	}
	press(a, "enter") // Resume: no phase or through
	if home.dialog.title != "Mode" {
		t.Fatalf("after Resume: %q, want the mode", home.dialog.title)
	}
	press(a, "5") // plan
	d = home.dialog
	if want := "Resume M2 through M3 · mode plan · 6 tasks · first M2-02 opus"; d.title != want {
		t.Errorf("summary %q, want %q", d.title, want)
	}
	if !strings.Contains(d.detail, "the interrupted task M2-02 is picked up first") {
		t.Errorf("summary detail %q lacks the interrupted task", d.detail)
	}
	press(a, "enter")
	if st.count() != 1 {
		t.Fatalf("%d starts, want 1", st.count())
	}
	if got := st.req(0); got != (report.RunRequest{Mode: "plan"}) {
		t.Errorf("request %+v, want Phase \"\" (resume) with mode plan", got)
	}
	// The fake fails: the error dialog, with Doctor, and no retry.
	d = home.dialog
	if d.title != "The run did not start" || !strings.Contains(d.detail, "fake: stop here") || strings.Join(labels(d), "|") != "OK|Doctor" {
		t.Errorf("error dialog %q %q %q", d.title, d.detail, labels(d))
	}
	if a.run != nil {
		t.Errorf("a failed launch left the app holding a run")
	}
	press(a, "enter")
	if home.launch != nil || st.count() != 1 {
		t.Errorf("OK left launch %v, %d starts", home.launch, st.count())
	}
}

func TestWizardStartAPhaseInsteadOfResuming(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, func(s *report.Snapshot) { s.Run = homeRun("", time.Time{}) })
	press(a, "a")
	if want := "Resume last run (M2 through M3)"; home.dialog.options[0].label != want {
		t.Errorf("resume option %q, want %q (stopped between tasks)", home.dialog.options[0].label, want)
	}
	press(a, "2", "3", "1", "1", "enter") // Start a phase…, M4, only, as planned, Arise
	if got := st.req(0); got != (report.RunRequest{Phase: "M4"}) {
		t.Errorf("request %+v, want M4 only", got)
	}
}

func TestWizardYoloNeedsThePhrasePerLaunch(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, nil)
	press(a, "a", "enter", "enter", "6")
	d := home.dialog
	if d.input == nil || !d.inField || d.options[0].label != "Cancel" {
		t.Fatalf("yolo opened %q %q, want the typed phrase with Cancel first", d.title, labels(d))
	}
	// Enter twice never confirms: leaving the field lands on Cancel, and
	// Cancel goes back to the mode with nothing chosen.
	press(a, "enter", "enter")
	if home.dialog.title != "Mode" || st.count() != 0 {
		t.Fatalf("enter twice: %q, %d starts; want the mode dialog again", home.dialog.title, st.count())
	}
	press(a, "6", "skip permission", "enter", "down", "enter")
	if d = home.dialog; d.input == nil || d.input.hint != "Type exactly: skip permissions" {
		t.Fatalf("a wrong phrase gave %q %+v", d.title, d.input)
	}
	press(a, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace", "backspace",
		"backspace", "backspace", "backspace", "backspace", "backspace", "s", "kip permissions", "enter", "down", "enter")
	d = home.dialog
	if !strings.HasPrefix(d.title, "Arise M2 · mode yolo [SKIP PERMISSIONS]") {
		t.Fatalf("after the phrase: %q, want the summary in yolo", d.title)
	}
	press(a, "enter")
	if c := st.conf(0); !c.Yolo || st.req(0).Mode != engine.ModeYolo {
		t.Errorf("start got %+v / %+v, want yolo confirmed", st.req(0), c)
	}
	press(a, "enter") // OK on the error

	// The next launch asks again: nothing is remembered.
	press(a, "a", "enter", "enter", "enter", "enter")
	if c := st.conf(1); c.Yolo {
		t.Errorf("the second launch carried the phrase over: %+v", c)
	}
}

// TestWizardParity holds the wizard to `igris arise`'s start-up questions
// (execArise): the same question, Cancel as the default (arise's [y/N]),
// and the same meaning when confirmed, then a retry.
func TestWizardParity(t *testing.T) {
	drift := &engine.DriftError{Changes: []plan.Change{{ID: "M2-04", From: plan.Ready, To: plan.Blocked}}}
	stale := &state.StaleLockError{Info: state.LockInfo{PID: 4242, Host: "here"}, Reason: "from pid 4242 on here (process no longer running)"}
	remote := &state.LockedError{Info: state.LockInfo{PID: 7, Host: "mbp"}, Path: "/src/sinjal/.igris/igris.lock", Remote: true}
	live := &state.LockedError{Info: state.LockInfo{PID: 7, Host: "here"}, Path: "/src/sinjal/.igris/igris.lock"}
	apiKey := report.Prelaunch{Warnings: []checks.Result{{ID: checks.IDAPIKey, Level: checks.Warn, Message: "ANTHROPIC_API_KEY is set", Confirm: true}}}
	tests := []struct {
		name string
		pre  report.Prelaunch
		err  error // what the first Start fails with
		// question is the dialog's title: arise's own question where it
		// asks one.
		question string
		input    bool // a typed phrase, not a yes
		confirm  string
		// meaning is the answer the retry carries; nil: no retry.
		meaning func(report.Confirmations) bool
	}{
		{name: "start-up warning", pre: apiKey, question: "Start the run anyway?", confirm: "Start anyway",
			meaning: func(c report.Confirmations) bool { return c == report.Confirmations{} }},
		{name: "drift", err: drift, question: "Let igris fix them?", confirm: "Let igris fix them",
			meaning: func(c report.Confirmations) bool { return c.Drift && !c.Yolo && !c.ForceUnlock }},
		{name: "yolo", err: engine.ErrYoloUnconfirmed, question: "Skip permissions?", input: true, confirm: "Skip permissions",
			meaning: func(c report.Confirmations) bool { return c.Yolo && !c.Drift && !c.ForceUnlock }},
		{name: "stale lock", err: stale, question: "Clear the lock and start?", confirm: "Clear the lock and start",
			meaning: func(c report.Confirmations) bool { return c.ForceUnlock && !c.Drift && !c.Yolo }},
		{name: "remote lock", err: remote, question: "Clear the lock and start?", confirm: "Clear the lock and start",
			meaning: func(c report.Confirmations) bool { return c.ForceUnlock && !c.Drift && !c.Yolo }},
		{name: "live lock", err: live, question: "The run did not start"},
		{name: "backend unavailable", err: errors.New("backend herdr is not available: no socket"), question: "The run did not start"},
	}
	// toQuestion opens the wizard and answers it up to the case's question.
	toQuestion := func(t *testing.T, st *starts, pre report.Prelaunch) (*appModel, *homeScreen) {
		a, home := wizardApp(t, st, pre, nil)
		press(a, "a", "enter", "enter", "enter", "enter")
		return a, home
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answers := []error{tt.err}
			if tt.err == nil {
				answers = nil
			}
			for _, how := range []string{"enter", "esc"} {
				st := &starts{answers: answers}
				a, home := toQuestion(t, st, tt.pre)
				d := home.dialog
				if d == nil || d.title != tt.question {
					t.Fatalf("question %+v, want %q", d, tt.question)
				}
				if tt.meaning == nil {
					// No retry: the error, with its next step.
					if !strings.Contains(d.detail, "Doctor shows") || d.options[0].label != "OK" {
						t.Errorf("error dialog %q %q", d.detail, labels(d))
					}
					press(a, how)
					if home.launch != nil || st.count() != 1 {
						t.Errorf("%s: launch %v, %d starts; want closed after one", how, home.launch, st.count())
					}
					continue
				}
				if d.options[0].label != "Cancel" || d.cancel != actClose || (d.input == nil) == tt.input {
					t.Fatalf("choices %q (field %v), want Cancel first", labels(d), d.input != nil)
				}
				if got := d.options[1].label; got != tt.confirm {
					t.Errorf("confirming choice %q, want %q", got, tt.confirm)
				}
				// The default answer is no: nothing starts (again).
				before := st.count()
				if tt.input {
					press(a, "enter") // leave the field: on Cancel
				}
				press(a, how)
				if home.launch != nil || st.count() != before || home.status != notStarted {
					t.Errorf("%s on the default: launch %v, %d starts (had %d), status %q; want %q", how, home.launch, st.count(), before, home.status, notStarted)
				}
			}

			// Confirming retries with the answer, and only that one.
			st := &starts{answers: answers}
			a, home := toQuestion(t, st, tt.pre)
			if tt.meaning == nil {
				return
			}
			before := st.count()
			if tt.input {
				press(a, engine.YoloPhrase, "enter", "down", "enter")
			} else {
				press(a, "2")
			}
			if st.count() != before+1 || !tt.meaning(st.conf(before)) {
				t.Fatalf("confirmed: %d starts (had %d), answers %+v", st.count(), before, st.confs)
			}
			// The retry failed with something else: no question twice.
			if home.dialog.title != "The run did not start" {
				t.Errorf("after the retry: %q", home.dialog.title)
			}
		})
	}
}

func TestWizardDriftListsTheChangesAndScrolls(t *testing.T) {
	var changes []plan.Change
	for i := range 30 {
		changes = append(changes, plan.Change{ID: fmt.Sprintf("M2-%02d", i), From: plan.Ready, To: plan.Blocked})
	}
	st := &starts{answers: []error{&engine.DriftError{Changes: changes}}}
	a, home := wizardApp(t, st, report.Prelaunch{}, nil)
	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	press(a, "a", "enter", "enter", "enter", "enter")
	view := home.View()
	if !strings.Contains(view, "· M2-00: ready → blocked") || strings.Contains(view, "M2-29") {
		t.Fatalf("drift dialog:\n%s", view)
	}
	for range 10 { // pgdown scrolls the list
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		drive(a, cmd)
	}
	if view = home.View(); !strings.Contains(view, "· M2-29: ready → blocked") || strings.Contains(view, "M2-00") {
		t.Errorf("scrolled to the end:\n%s", view)
	}
}

func TestWizardPrefilledFromArise(t *testing.T) {
	st := &starts{}
	svc := &fakeServices{start: st.start}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return homeSnap("ready"), nil }
	a := newApp(context.Background(), AppOptions{Services: svc, Poll: -1, Start: Wizard{Mode: "plan", Through: "M3", ForceUnlock: true}}, &theme{})
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	home := a.stack[0].(*homeScreen)
	drive(a, home.refresh())
	if home.dialog != nil {
		t.Fatalf("the wizard opened before herdr was checked")
	}
	drive(a, home.checkBackend())
	if home.dialog == nil || home.dialog.title != "Phase" {
		t.Fatalf("no wizard once home read the project")
	}
	press(a, "enter")
	if d := home.dialog; d.title != "Through" || d.options[d.selected].label != "M2 through M3" {
		t.Errorf("through %q, want --through M3 selected", d.options[d.selected].label)
	}
	press(a, "enter")
	if d := home.dialog; d.options[d.selected].label != modeActs[3].label {
		t.Errorf("mode %q, want --mode plan selected", d.options[d.selected].label)
	}
	press(a, "enter", "enter")
	if c := st.conf(0); !c.ForceUnlock || st.req(0) != (report.RunRequest{Phase: "M2", Through: "M3", Mode: "plan"}) {
		t.Errorf("start %+v %+v, want the flags", st.req(0), c)
	}
}

func TestWizardPendingWhenAriseDoesNotApply(t *testing.T) {
	svc := &fakeServices{backendErr: errors.New("no herdr")}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return homeSnap("ready"), nil }
	a := newApp(context.Background(), AppOptions{Services: svc, Poll: -1, Start: Wizard{}}, &theme{})
	home := a.stack[0].(*homeScreen)
	drive(a, home.refresh())
	drive(a, home.checkBackend())
	if home.launch != nil || !strings.Contains(home.status, "Arise isn't available here") {
		t.Errorf("launch %v, status %q", home.launch, home.status)
	}
}

func TestExitText(t *testing.T) {
	tests := []struct {
		res  engine.Result
		err  error
		want string
	}{
		{engine.Result{Outcome: engine.Completed}, nil, "run completed"},
		{engine.Result{Outcome: engine.Stopped}, nil, "igris stopped; a running session keeps running — `igris arise` resumes"},
		{engine.Result{Outcome: engine.Stuck, Phase: "M2"}, nil, "phase M2 is stuck: unfinished tasks, none can start (see `igris status M2`)"},
		{engine.Result{}, errors.New("boom\x1b[2J"), "the run failed: boom"},
	}
	for _, tt := range tests {
		if got := ExitText(tt.res, tt.err); got != tt.want {
			t.Errorf("ExitText(%v, %v) = %q, want %q", tt.res, tt.err, got, tt.want)
		}
	}
}

// The embedded run, in a real program.

// launchProgram runs the app on the ready project; Start hands out runners
// made by mk.
func launchProgram(t *testing.T, st *starts) (*teatest.TestModel, *fakeServices) {
	t.Helper()
	svc := &fakeServices{start: st.start}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return homeSnap("ready"), nil }
	return appProgram(t, svc, 120, 40), svc
}

// ariseM2 walks the wizard to the launch of M2 only, as planned.
func ariseM2(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	tm.Type("a")
	for range 3 {
		tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	}
	seen(t, tm, "Arise M2 · mode as planned") // the checks are back
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestEmbeddedRunLeave(t *testing.T) {
	st := &starts{answers: []error{nil}}
	tm, _ := launchProgram(t, st)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	seen(t, tm, "[Home]")
	tm.Type("q") // Home: the engine stops, the session keeps running
	seen(t, tm, "igris stopped; a running session keeps running")
	tm.Type("q")
	a := finalApp(t, tm)
	if len(a.stack) != 1 || a.run != nil {
		t.Errorf("stack %d, run %v; want home alone and no run", len(a.stack), a.run)
	}
	if r := a.finish(); r.Stopped || r.Res.Phase != "" {
		t.Errorf("finish %+v, want nothing to report", r)
	}
}

func TestEmbeddedRunStopReturnsHome(t *testing.T) {
	st := &starts{answers: []error{nil}}
	tm, _ := launchProgram(t, st)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	seen(t, tm, "[Home]")
	tm.Type("x")
	seen(t, tm, "Stop igris?")
	tm.Type("2")
	seen(t, tm, "igris stopped; a running session keeps running", "READY")
	tm.Type("q")
	if a := finalApp(t, tm); len(a.stack) != 1 || a.run != nil {
		t.Errorf("stack %d, run %v", len(a.stack), a.run)
	}
}

func TestEmbeddedRunEndsOnItsOwn(t *testing.T) {
	st := &starts{answers: []error{nil}, runner: func(events func(engine.Event)) *fakeRunner {
		r := newFakeRunner(events)
		r.end = &engine.Result{Outcome: engine.Stuck, Phase: "M2"}
		return r
	}}
	tm, _ := launchProgram(t, st)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	// The view stays with its end banner until the owner goes home.
	seen(t, tm, "press q to go home")
	tm.Type("q")
	seen(t, tm, "phase M2 is stuck: unfinished tasks, none can start")
	tm.Type("q")
	if a := finalApp(t, tm); len(a.stack) != 1 || a.run != nil {
		t.Errorf("stack %d, run %v", len(a.stack), a.run)
	}
}

func TestEmbeddedRunQuitStopsAndWaits(t *testing.T) {
	st := &starts{answers: []error{nil}}
	tm, _ := launchProgram(t, st)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	seen(t, tm, "[Home]")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	a := finalApp(t, tm)
	r := a.finish() // waits for the engine: the lock is released
	if !r.Stopped || r.Res.Outcome != engine.Stopped || r.Res.Phase != "M2" {
		t.Errorf("finish %+v, want the stopped run", r)
	}
}

func TestEmbeddedRunCancelWhileStarting(t *testing.T) {
	st := &starts{answers: []error{nil}, runner: func(events func(engine.Event)) *fakeRunner {
		r := newFakeRunner(events)
		r.hold = true
		return r
	}}
	tm, _ := launchProgram(t, st)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	seen(t, tm, "checking herdr, taking the lock")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Cancel
	seen(t, tm, "nothing was started")
	tm.Type("q")
	a := finalApp(t, tm)
	if a.run != nil || len(a.stack) != 1 || st.count() != 1 {
		t.Errorf("run %v, stack %d, starts %d", a.run, len(a.stack), st.count())
	}
}

func TestEmbeddedRunQuitWhileStartingWaits(t *testing.T) {
	st := &starts{answers: []error{nil}, runner: func(events func(engine.Event)) *fakeRunner {
		r := newFakeRunner(events)
		r.hold = true
		return r
	}}
	tm, _ := launchProgram(t, st)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	seen(t, tm, "checking herdr, taking the lock")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	a := finalApp(t, tm)
	if a.run == nil {
		t.Fatal("the app forgot the engine it was starting")
	}
	if r := a.finish(); r.Stopped || r.RunErr != nil {
		t.Errorf("finish %+v, want nothing to report: the run never started", r)
	}
	select {
	case <-a.run.feed.Ended():
	default:
		t.Error("finish returned before the engine ended")
	}
}

// The wizard's dialogs over home, at the three sizes (SPEC §17).
func TestWizardGolden(t *testing.T) {
	pre := report.Prelaunch{Interrupted: "M2-02", Warnings: []checks.Result{
		{ID: checks.IDGit, Level: checks.Warn, Message: "the working tree has uncommitted changes"},
		{ID: checks.IDAPIKey, Level: checks.Warn, Message: "ANTHROPIC_API_KEY is set: Claude Code bills that key", Confirm: true},
	}}
	drift := &engine.DriftError{Changes: []plan.Change{{ID: "M2-04", From: plan.Ready, To: plan.Blocked}, {ID: "M3-01", From: plan.Ready, To: plan.Blocked}}}
	steps := []struct {
		name    string
		answers []error
		keys    []string
	}{
		{name: "phase", keys: []string{"a"}},
		{name: "through", keys: []string{"a", "enter"}},
		{name: "mode", keys: []string{"a", "enter", "down", "enter"}},
		{name: "yolo", keys: []string{"a", "enter", "down", "enter", "6"}},
		{name: "summary", keys: []string{"a", "enter", "down", "enter", "enter"}},
		{name: "confirm", keys: []string{"a", "enter", "down", "enter", "enter", "enter"}},
		{name: "drift", answers: []error{drift}, keys: []string{"a", "enter", "down", "enter", "enter", "enter", "2"}},
		{name: "failed", answers: []error{errors.New("backend herdr is not available: no socket")}, keys: []string{"a", "enter", "down", "enter", "enter", "enter", "2"}},
	}
	for _, s := range steps {
		for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 20}} {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s_%dx%d", s.name, w, h), func(t *testing.T) {
				a, home := wizardApp(t, &starts{answers: s.answers}, pre, nil)
				a.Update(tea.WindowSizeMsg{Width: w, Height: h})
				press(a, s.keys...)
				view := home.View()
				checkFits(t, view, w, h)
				golden(t, fmt.Sprintf("wizard_%s_%dx%d", s.name, w, h), view)
			})
		}
	}
}
