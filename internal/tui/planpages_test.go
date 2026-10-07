package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
)

// checkWarnings are the warnings of check: setup, hints, drift.
var checkWarnings = []checks.Result{
	{ID: checks.IDClaude, Level: checks.OK, Message: "claude 2.1"},
	{ID: checks.IDConfig, Level: checks.Warn, File: "igris.toml", Line: 4, Message: "unknown key tui.moose"},
	{ID: checks.IDPlanHints, Level: checks.Warn, File: "tasks.md", Line: 21, Message: "M2-03 has a Status cell that reads as blocked but depends on nothing"},
	{ID: checks.IDDrift, Level: checks.Warn, Task: "M2-04", Message: "M2-04 is marked ready but waits on M2-03"},
}

// planPagesHome is a ready home that has heard from herdr and doctor.
func planPagesHome(w, h int, fix homeFixture) *homeScreen {
	return homeAt(&theme{}, w, h, fix)
}

func readyFix() homeFixture {
	return homeFixture{snap: homeSnap("in progress"), doctor: checkWarnings, doctorDone: true}
}

func invalidFix() homeFixture {
	s := homeSnap("ready")
	s.Plan = plan.Parse("tasks.md", []byte(homeInvalidPlan), plan.Options{})
	s.Issues = report.Issues(s.Plan.Validate(s.Config.Models))
	s.Status = nil
	return homeFixture{snap: s, doctor: checkWarnings, doctorDone: true}
}

func sized(m tea.Model, w, h int) {
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

func taskHistory() report.History {
	return report.History{Runs: []report.HistoryRun{
		{StartedAt: "2026-10-07T07:12:00Z", Tasks: []report.TaskRun{{ID: "M2-01", Result: report.ResultDone}, {ID: "M2-02", Rank: "opus", Model: "opus", Result: report.ResultOpen, Attempts: 1, DurationS: 840}}},
		{StartedAt: "2026-10-06T21:10:00Z", Tasks: []report.TaskRun{{ID: "M2-02", Rank: "sonnet", Model: "sonnet", Result: report.ResultDone, Attempts: 2, DurationS: 4500, VerifyFailed: 1, VerifyPassed: 1}}},
	}}
}

func TestPlanPagesGolden(t *testing.T) {
	pages := []struct {
		name string
		make func(w, h int) (tea.Model, func() string)
	}{
		{"phase", func(w, h int) (tea.Model, func() string) {
			s := newPhaseScreen(planPagesHome(w, h, readyFix()), "M2")
			return s, s.View
		}},
		{"phase_waiting", func(w, h int) (tea.Model, func() string) {
			s := newPhaseScreen(planPagesHome(w, h, readyFix()), "M3")
			return s, s.View
		}},
		{"phase_gone", func(w, h int) (tea.Model, func() string) {
			s := newPhaseScreen(planPagesHome(w, h, readyFix()), "M9")
			return s, s.View
		}},
		{"task", func(w, h int) (tea.Model, func() string) {
			s := newTaskScreen(planPagesHome(w, h, readyFix()), "M2-02")
			s.Update(taskHistoryMsg{h: taskHistory()})
			return s, s.View
		}},
		{"task_loading", func(w, h int) (tea.Model, func() string) {
			s := newTaskScreen(planPagesHome(w, h, readyFix()), "M2-03")
			return s, s.View
		}},
		{"task_none", func(w, h int) (tea.Model, func() string) {
			s := newTaskScreen(planPagesHome(w, h, readyFix()), "M2-03")
			s.Update(taskHistoryMsg{h: taskHistory()})
			return s, s.View
		}},
		{"task_failed", func(w, h int) (tea.Model, func() string) {
			s := newTaskScreen(planPagesHome(w, h, readyFix()), "M2-03")
			s.Update(taskHistoryMsg{err: errors.New("runs.jsonl: permission denied")})
			return s, s.View
		}},
		{"check_valid", func(w, h int) (tea.Model, func() string) {
			s := newCheckScreen(planPagesHome(w, h, readyFix()))
			return s, s.View
		}},
		{"check_invalid", func(w, h int) (tea.Model, func() string) {
			s := newCheckScreen(planPagesHome(w, h, invalidFix()))
			return s, s.View
		}},
		{"check_clean", func(w, h int) (tea.Model, func() string) {
			f := readyFix()
			f.doctor = homeDoctor
			s := newCheckScreen(planPagesHome(w, h, f))
			return s, s.View
		}},
		{"check_waiting", func(w, h int) (tea.Model, func() string) {
			f := readyFix()
			f.doctorDone = false
			s := newCheckScreen(planPagesHome(w, h, f))
			return s, s.View
		}},
	}
	for _, pg := range pages {
		for _, size := range homeSizes {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s_%dx%d", pg.name, w, h), func(t *testing.T) {
				m, view := pg.make(w, h)
				sized(m, w, h)
				v := view()
				checkFits(t, v, w, h)
				golden(t, fmt.Sprintf("plan_%s_%dx%d", pg.name, w, h), v)
			})
		}
	}
}

// Under NO_COLOR outcomes and warnings are still told in words.
func TestPlanPagesNoColor(t *testing.T) {
	th := noColorTheme(t)
	h := homeAt(th, 120, 40, readyFix())
	ph := newPhaseScreen(h, "M2")
	sized(ph, 120, 40)
	if v := ph.View(); !strings.Contains(v, "next — M2-02 is next") || !strings.Contains(v, "4 tasks") {
		t.Errorf("phase page lacks the outcome word or counts:\n%s", v)
	}
	ck := newCheckScreen(h)
	sized(ck, 120, 40)
	if v := ck.View(); !strings.Contains(v, "tasks.md:21:") || !strings.Contains(v, "3 warnings") {
		t.Errorf("check page lacks its warnings in words:\n%s", v)
	}
}

// planApp is the app on the sinjal plan with its pages reachable.
func planApp(t *testing.T, fix homeFixture) (*appModel, *fakeServices) {
	t.Helper()
	svc := &fakeServices{history: taskHistory()}
	svc.doctor = fix.doctor
	svc.backendErr = fix.backend
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return fix.snap, nil }
	a, _ := pollApp(t, svc)
	return a, svc
}

// focusPhases puts the focus on home's phase list, as tab does.
func focusPhases(a *appModel) { a.stack[0].(*homeScreen).focus = homePhases }

func TestPhasePageOpensFromHomeAndNavigates(t *testing.T) {
	a, svc := planApp(t, readyFix())
	focusPhases(a)
	press(a, "enter")
	if _, ok := a.stack[len(a.stack)-1].(*phaseScreen); !ok || len(a.stack) != 2 {
		t.Fatalf("enter on a phase didn't open its page: %d screens", len(a.stack))
	}
	if v := a.View(); !strings.Contains(v, "Phase M2 · Config & state") || !strings.Contains(v, "M2-02") {
		t.Errorf("phase page not drawn:\n%s", v)
	}
	// The in-progress task starts selected; down then enter opens M2-03.
	press(a, "down", "enter")
	if _, ok := a.stack[len(a.stack)-1].(*taskScreen); !ok {
		t.Fatalf("enter on a task didn't open its details")
	}
	if svc.called("History") == 0 {
		t.Error("the task page didn't read the run log")
	}
	press(a, "esc")
	press(a, "esc")
	if len(a.stack) != 1 {
		t.Errorf("esc left %d screens", len(a.stack))
	}
}

func TestPhasePageActions(t *testing.T) {
	rec := &previewRecorder{}
	a, svc := planApp(t, readyFix())
	svc.preview = rec.fn
	focusPhases(a)
	press(a, "enter")
	phase := a.stack[1].(*phaseScreen)
	if !strings.Contains(a.View(), "Arise this phase…") {
		t.Errorf("no Arise button where arise applies:\n%s", a.View())
	}
	// Preview this phase pushes the dry run; its Arise goes home.
	press(a, "v")
	if got := rec.last(); got != (report.RunRequest{Phase: "M2"}) {
		t.Errorf("previewed %+v", got)
	}
	if _, ok := a.stack[len(a.stack)-1].(*previewScreen); !ok {
		t.Fatal("v didn't open the preview")
	}
	press(a, "a")
	if len(a.stack) != 1 {
		t.Fatalf("Arise from the preview left %d screens, want home", len(a.stack))
	}
	if a.stack[0].(*homeScreen).launch == nil {
		t.Error("the wizard didn't open")
	}
	_ = phase
}

func TestPhasePageAriseAndEdit(t *testing.T) {
	a, svc := planApp(t, readyFix())
	home := a.stack[0].(*homeScreen)
	focusPhases(a)
	press(a, "enter", "a")
	if len(a.stack) != 1 || home.launch == nil || home.dialog == nil {
		t.Fatalf("a on the phase page: %d screens, launch %+v", len(a.stack), home.launch)
	}
	if d := home.dialog; d.title != "Phase" || !strings.HasPrefix(d.options[d.selected].label, "M2 ") {
		t.Errorf("the wizard doesn't start on this phase: %+v", d)
	}
	// Edit plan goes home, which owns the editor.
	press(a, "esc") // the wizard's dialog
	for home.launch != nil {
		home.launch, home.dialog = nil, nil
	}
	focusPhases(a)
	press(a, "enter", "e")
	if len(a.stack) != 1 || svc.called("EditCommand") != 1 {
		t.Errorf("e: %d screens, status %q", len(a.stack), home.status)
	}
}

func TestPhasePageHidesAriseWithoutHerdr(t *testing.T) {
	f := readyFix()
	f.backend = errors.New("herdr not found")
	a, _ := planApp(t, f)
	focusPhases(a)
	press(a, "enter")
	if v := a.View(); strings.Contains(v, "Arise this phase") {
		t.Errorf("Arise offered without herdr:\n%s", v)
	}
}

func TestPhasePageMouse(t *testing.T) {
	h := planPagesHome(100, 30, readyFix())
	s := newPhaseScreen(h, "M2")
	sized(s, 100, 30)
	s.View()
	row := func(task int) int { // the screen row of task i
		for y, o := range s.owner {
			if o == task {
				return y + 2 - s.p.top
			}
		}
		t.Fatalf("task %d not drawn", task)
		return 0
	}
	click := func(y int) tea.Cmd {
		_, c := s.Update(tea.MouseMsg{X: 5, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		s.View()
		return c
	}
	if c := click(row(2)); c != nil || s.sel != 2 {
		t.Fatalf("a click should select task 2: sel %d", s.sel)
	}
	c := click(row(2))
	if c == nil {
		t.Fatal("a second click should open the task")
	}
	if _, ok := c().(pushMsg).s.(*taskScreen); !ok {
		t.Error("second click didn't push the task page")
	}
}

func TestPhasePageFollowsSelection(t *testing.T) {
	h := planPagesHome(50, 20, readyFix())
	s := newPhaseScreen(h, "M3")
	sized(s, 50, 8) // room for ~2 task rows
	s.View()
	for range 2 {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
		s.View()
	}
	if v := s.View(); !strings.Contains(v, "M3-03") {
		t.Errorf("the selected task scrolled out of view:\n%s", v)
	}
}

func TestTaskPageAttemptsAndCopy(t *testing.T) {
	var out bytes.Buffer
	h := planPagesHome(100, 30, readyFix())
	h.out = &out
	s := newTaskScreen(h, "M2-02")
	sized(s, 100, 30)
	s.Update(taskHistoryMsg{h: taskHistory()})
	v := s.View()
	for _, want := range []string{"Last attempts", "Oct 07 07:12", "unfinished", "opus → opus", "14m", "Oct 06 21:10", "done · sonnet → sonnet · 2 attempts · 1h15m · verify 1 failed, 1 passed"} {
		if !strings.Contains(v, want) {
			t.Errorf("task page lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "M2-01") && strings.Contains(v[strings.Index(v, "Last attempts"):], "M2-01") {
		t.Error("another task's attempts are listed")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if !strings.Contains(out.String(), "\x1b]52;") || !strings.Contains(s.View(), "copied") {
		t.Errorf("y didn't copy the ID: %q", out.String())
	}
}

func TestCheckPageAgainAndHandOver(t *testing.T) {
	a, svc := planApp(t, invalidFix())
	press(a, "c")
	if _, ok := a.stack[len(a.stack)-1].(*checkScreen); !ok {
		t.Fatal("c didn't open the check page")
	}
	v := a.View()
	for _, want := range []string{"plan invalid", "Adapt", "Edit plan", "tasks.md:", "warning"} {
		if !strings.Contains(v, want) {
			t.Errorf("check page lacks %q:\n%s", want, v)
		}
	}
	before := svc.called("Snapshot")
	doctors := svc.called("Doctor")
	press(a, "c")
	if svc.called("Snapshot") != before+1 || svc.called("Doctor") != doctors+1 {
		t.Errorf("c again read the project %d and ran doctor %d times", svc.called("Snapshot")-before, svc.called("Doctor")-doctors)
	}
	press(a, "e")
	home := a.stack[0].(*homeScreen)
	if len(a.stack) != 1 || svc.called("EditCommand") != 1 {
		t.Errorf("e: %d screens, status %q", len(a.stack), home.status)
	}
	press(a, "c", "A")
	if len(a.stack) != 1 || !strings.Contains(home.status, "Adapt") {
		t.Errorf("A: %d screens, status %q", len(a.stack), home.status)
	}
}

func TestCheckPageValidHidesAdapt(t *testing.T) {
	a, _ := planApp(t, readyFix())
	press(a, "c")
	if v := a.View(); strings.Contains(v, "Adapt") || !strings.Contains(v, "plan valid") {
		t.Errorf("valid plan's check page:\n%s", v)
	}
}

// Untrusted text in issues and warnings reaches the screen cleaned by
// the report layer; the page draws what it is given as text.
func TestCheckPageDrawsWarningsInOrder(t *testing.T) {
	h := planPagesHome(120, 40, readyFix())
	s := newCheckScreen(h)
	sized(s, 120, 40)
	v := s.View()
	a, b, c := strings.Index(v, "igris.toml:4:"), strings.Index(v, "tasks.md:21:"), strings.Index(v, "M2-04 is marked ready")
	if a < 0 || b < a || c < b {
		t.Errorf("warnings missing or out of check's order:\n%s", v)
	}
}
