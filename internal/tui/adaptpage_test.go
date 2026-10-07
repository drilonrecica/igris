package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
)

const adaptOriginal = `# Widgets

| Key | What | State | LLM |
|---|---|---|---|
| W-1 | Parse widgets | todo | sonnet |
`

const adaptProposed = `## W — Widgets

| ID | Task | Status | Model |
|---|---|---|---|
| W-1 | Parse widgets | ready | ? |
`

// adaptResult is a finished adapt session on the sinjal project; the
// proposal has one problem left.
func adaptResult() *adapt.Result {
	return &adapt.Result{
		PlanPath:     "/src/sinjal/tasks.md",
		ProposalPath: "/src/sinjal/.igris/adapt/tasks.proposed.md",
		Original:     []byte(adaptOriginal),
		Proposed:     []byte(adaptProposed),
		Issues:       []plan.Issue{{File: "/src/sinjal/.igris/adapt/tasks.proposed.md", Line: 5, Msg: `W-1: unknown model rank "?"`}},
		Note:         "converted",
	}
}

type adaptFunc = func(context.Context, string, io.Writer, func(backend.SessionRef)) (*adapt.Result, error)

// finishedAdapt reports, opens its session and returns res and err at
// once. Its last line has no newline and an escape sequence in it.
func finishedAdapt(res *adapt.Result, err error) adaptFunc {
	return func(_ context.Context, model string, out io.Writer, opened func(backend.SessionRef)) (*adapt.Result, error) {
		fmt.Fprintf(out, "12:00:00 adapt session started (%s); answer its questions in the ADAPT pane\n", model)
		opened(backend.SessionRef{Backend: "fake", PaneID: "p1"})
		fmt.Fprint(out, "12:00:09 adapt session done: \x1b[31mconverted")
		return res, err
	}
}

// workingAdapt opens its session and works until it is cancelled.
func workingAdapt(ctx context.Context, model string, out io.Writer, opened func(backend.SessionRef)) (*adapt.Result, error) {
	fmt.Fprintf(out, "12:00:00 adapt session started (%s); answer its questions in the ADAPT pane\n", model)
	opened(backend.SessionRef{Backend: "fake", PaneID: "p1"})
	<-ctx.Done()
	return nil, fmt.Errorf("adapt stopped; the ADAPT session stays open: %w", ctx.Err())
}

// adaptSnap is the sinjal project with an invalid plan.
func adaptSnap(apiKey bool) *report.Snapshot {
	s := invalidFix().snap
	s.APIKeySet = apiKey
	return s
}

// adaptApp is an app on the sinjal project with an invalid plan and herdr
// reachable.
func adaptApp(t *testing.T, svc *fakeServices, apiKey bool) (*appModel, *homeScreen) {
	t.Helper()
	snap := adaptSnap(apiKey)
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return snap, nil }
	return pollApp(t, svc)
}

func top(a *appModel) tea.Model { return a.stack[len(a.stack)-1] }

func TestAdaptDialog(t *testing.T) {
	for _, apiKey := range []bool{false, true} {
		svc := &fakeServices{}
		a, home := adaptApp(t, svc, apiKey)
		press(a, "A")
		d := home.dialog
		if d == nil || !home.adapting {
			t.Fatal("A didn't open the adapt dialog")
		}
		if got := strings.Join(labels(d), " · "); got != "Cancel · Adapt with sonnet · Adapt with opus" {
			t.Errorf("options = %s", got)
		}
		if !strings.Contains(d.detail, "nothing changes until you accept") || !strings.Contains(d.detail, "adapt.model in igris.toml is sonnet") {
			t.Errorf("detail = %q", d.detail)
		}
		if got := strings.Contains(d.detail, checks.APIKeyWarning); got != apiKey {
			t.Errorf("API key set %v, warning shown %v: %q", apiKey, got, d.detail)
		}
		press(a, "enter") // Cancel is the default
		if home.dialog != nil || home.adapting || len(a.stack) != 1 || svc.called("Adapt") != 0 {
			t.Errorf("enter: dialog %v, %d screens, adapt ran %d times", home.dialog, len(a.stack), svc.called("Adapt"))
		}
		press(a, "A", "esc")
		if home.dialog != nil || home.adapting || svc.called("Adapt") != 0 {
			t.Error("esc didn't cancel the adapt dialog")
		}
	}
}

func TestAdaptHiddenWithoutHerdr(t *testing.T) {
	svc := &fakeServices{backendErr: errors.New("not inside a herdr pane")}
	a, home := adaptApp(t, svc, false)
	press(a, "A")
	if home.dialog != nil || len(a.stack) != 1 {
		t.Error("A opened adapt without herdr")
	}
}

func TestAdaptAccept(t *testing.T) {
	svc := &fakeServices{adapt: finishedAdapt(adaptResult(), nil), backup: "/src/sinjal/.igris/adapt/tasks.20261007-094100.bak.md"}
	a, home := adaptApp(t, svc, false)
	press(a, "A", "3")
	if svc.called("Adapt opus") != 1 {
		t.Fatalf("adapt ran %d times with opus", svc.called("Adapt opus"))
	}
	rv, ok := top(a).(*review)
	if !ok || len(a.stack) != 2 {
		t.Fatalf("the review didn't replace the progress screen: %d screens, top %T", len(a.stack), top(a))
	}
	if rv.o.PlanPath != "tasks.md" || len(rv.o.Issues) != 1 || len(rv.o.Review.Prose)+len(rv.o.Review.Sections) == 0 {
		t.Errorf("review options %+v", rv.o)
	}
	if v := a.View(); !strings.Contains(v, "W-1") || !strings.Contains(v, "unknown model rank") {
		t.Errorf("review view:\n%s", v)
	}
	press(a, "a") // the proposal doesn't pass check: asks again
	if rv.dialog == nil || svc.called("AcceptAdapt") != 0 {
		t.Fatal("accepting a proposal with problems didn't ask again")
	}
	press(a, "2")
	if len(a.stack) != 1 || svc.called("AcceptAdapt") != 1 {
		t.Fatalf("Replace anyway: %d screens, accepted %d times", len(a.stack), svc.called("AcceptAdapt"))
	}
	for _, want := range []string{"adapt accepted: tasks.md replaced", ".igris/adapt/tasks.20261007-094100.bak.md", "1 problem left", "Check shows them"} {
		if !strings.Contains(home.status, want) {
			t.Errorf("status %q lacks %q", home.status, want)
		}
	}
}

func TestAdaptAcceptFails(t *testing.T) {
	res := adaptResult()
	res.Issues = nil
	svc := &fakeServices{adapt: finishedAdapt(res, nil), acceptErr: errors.New("tasks.md changed since it was read; the plan is unchanged")}
	a, home := adaptApp(t, svc, false)
	press(a, "A", "2", "a")
	if len(a.stack) != 1 || svc.called("AcceptAdapt") != 1 || home.status != "adapt: tasks.md changed since it was read; the plan is unchanged" {
		t.Errorf("%d screens, status %q", len(a.stack), home.status)
	}
}

func TestAdaptReject(t *testing.T) {
	svc := &fakeServices{adapt: finishedAdapt(adaptResult(), nil)}
	a, home := adaptApp(t, svc, false)
	press(a, "A", "2", "r")
	if len(a.stack) != 1 || svc.called("AcceptAdapt") != 0 || svc.called("Adapt sonnet") != 1 {
		t.Fatalf("reject: %d screens, accepted %d times", len(a.stack), svc.called("AcceptAdapt"))
	}
	if want := "adapt rejected: tasks.md is unchanged; the proposal stays in .igris/adapt/tasks.proposed.md"; home.status != want {
		t.Errorf("status %q, want %q", home.status, want)
	}
}

func TestAdaptAlreadyValid(t *testing.T) {
	svc := &fakeServices{adapt: finishedAdapt(nil, adapt.ErrAlreadyValid)}
	a, home := adaptApp(t, svc, false)
	press(a, "A", "2")
	if len(a.stack) != 1 || home.status != "tasks.md passes check; nothing to adapt" {
		t.Errorf("%d screens, status %q", len(a.stack), home.status)
	}
}

func TestAdaptFailureStaysToBeRead(t *testing.T) {
	svc := &fakeServices{adapt: finishedAdapt(nil, errors.New("the adapt session ended without `igris done ADAPT`; run `igris adapt` again"))}
	a, home := adaptApp(t, svc, false)
	press(a, "A", "2")
	s, ok := top(a).(*adaptScreen)
	if !ok {
		t.Fatalf("top is %T, want the progress screen", top(a))
	}
	v := s.View()
	for _, want := range []string{"Adapt · sonnet · failed", "adapt session started (sonnet)", "adapt session done: converted", "ended without", "The plan is unchanged", "[Close]"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "\x1b") || strings.Contains(v, "Open session") || strings.Contains(v, "Cancel") {
		t.Errorf("view has an escape sequence, Open session or Cancel after the end:\n%q", v)
	}
	if !strings.HasPrefix(home.status, "adapt failed: the adapt session ended without") {
		t.Errorf("status %q", home.status)
	}
	press(a, "esc")
	if len(a.stack) != 1 {
		t.Errorf("esc left %d screens", len(a.stack))
	}
}

// adaptKey is a key press.
func adaptKey(k string) tea.KeyMsg {
	if k == "esc" {
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// progressScreen starts adapt on a progress screen over a home on snap
// and takes its news until the session is open. The screen's own wait
// isn't driven: the test takes the news itself.
func progressScreen(t *testing.T, svc *fakeServices, w, h int) (*adaptScreen, *homeScreen) {
	t.Helper()
	home := newHome(context.Background(), svc, &theme{})
	home.now = func() time.Time { return homeNow }
	home.Update(snapshotMsg{adaptSnap(false), nil})
	home.Update(backendMsg{nil})
	s := newAdaptScreen(home, "sonnet")
	s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	s.Init()
	for s.ref == nil {
		s.Update(s.run.next())
	}
	return s, home
}

func TestAdaptOpenSessionAndCancel(t *testing.T) {
	svc := &fakeServices{adapt: workingAdapt}
	s, home := progressScreen(t, svc, 80, 24)
	v := s.View()
	for _, want := range []string{"Adapt · sonnet · working…", "adapt session started (sonnet)", "[Open session]", "[Cancel]"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}

	_, cmd := s.Update(adaptKey("o"))
	req, ok := cmd().(asyncReq)
	if !ok {
		t.Fatal("o didn't ask to open the session")
	}
	s.Update(req.fn())
	if svc.called("Focus") != 1 {
		t.Errorf("Focus called %d times", svc.called("Focus"))
	}

	s.Update(adaptKey("esc"))
	if v := s.View(); !strings.Contains(v, "cancelling…") {
		t.Errorf("esc: view\n%s", v)
	}
	var msg adaptNewsMsg
	for !msg.ended {
		msg = s.run.next()
	}
	_, cmd = s.Update(msg)
	if _, ok := cmd().(popMsg); !ok {
		t.Error("the cancelled screen didn't close")
	}
	if want := "adapt cancelled; nothing changed, and the ADAPT session stays open"; home.status != want {
		t.Errorf("status %q, want %q", home.status, want)
	}
}

func TestAdaptOpenSessionFails(t *testing.T) {
	svc := &fakeServices{adapt: workingAdapt, focusErr: errors.New("session is gone")}
	s, _ := progressScreen(t, svc, 80, 24)
	_, cmd := s.Update(adaptKey("o"))
	s.Update(cmd().(asyncReq).fn())
	if v := s.View(); !strings.Contains(v, "open session: session is gone") {
		t.Errorf("view:\n%s", v)
	}
	s.Update(adaptKey("q"))
	<-s.run.done
}

// Quitting the app stops adapt and waits for it, so its lock is released.
func TestAppQuitStopsAdapt(t *testing.T) {
	a := testApp(&fakeServices{}, 80, 24)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()
	a.Update(bgMsg{&bgWork{stop: stop, done: done}})
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	a.finish()
	select {
	case <-done:
	default:
		t.Error("finish returned while adapt still ran")
	}

	// Work that starts while the app is quitting is stopped at once.
	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	a.Update(bgMsg{&bgWork{stop: stop2, done: ctx2.Done()}})
	if ctx2.Err() == nil {
		t.Error("work handed over while quitting wasn't stopped")
	}
}

// The adapt dialog and the progress screen at the three sizes (SPEC §17).
func TestAdaptGolden(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 20}} {
		w, h := size[0], size[1]
		t.Run(fmt.Sprintf("dialog_%dx%d", w, h), func(t *testing.T) {
			snap := adaptSnap(true)
			home := homeAt(&theme{}, w, h, homeFixture{snap: snap, doctor: checkWarnings, doctorDone: true})
			home.svc = &fakeServices{}
			home.activate(actAdapt)
			view := home.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("adapt_dialog_%dx%d", w, h), view)
		})
		t.Run(fmt.Sprintf("progress_%dx%d", w, h), func(t *testing.T) {
			s, _ := progressScreen(t, &fakeServices{adapt: workingAdapt}, w, h)
			view := s.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("adapt_progress_%dx%d", w, h), view)
			s.Update(adaptKey("esc"))
			<-s.run.done
		})
	}
}
