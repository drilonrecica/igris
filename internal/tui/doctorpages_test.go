package tui

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/report"
)

// doctorMixed has an ok, a warning with a fix command and a failure.
var doctorMixed = []checks.Result{
	{ID: checks.IDClaude, Level: checks.OK, Message: "claude 2.1.4"},
	{ID: checks.IDAPIKey, Level: checks.Warn, Message: "ANTHROPIC_API_KEY is set: runs would bill the API", Next: "unset ANTHROPIC_API_KEY", Confirm: true},
	{ID: checks.IDConfigValid, Level: checks.Fail, File: "igris.toml", Line: 7, Message: "mode: unknown value \"turbo\"", Next: "edit igris.toml, then igris doctor"},
	{ID: checks.IDAllowRules, Level: checks.Warn, Message: "`igris done` is not allowed in .claude/settings.local.json", Next: "igris init"},
	{ID: checks.IDNotify, Level: checks.OK, Message: "ntfy topic set"},
}

func doctorFix(rs []checks.Result) homeFixture {
	f := readyFix()
	f.doctor = rs
	return f
}

func runHistory() report.History {
	return report.History{Runs: []report.HistoryRun{
		{
			StartedAt: "2026-10-07T07:12:00Z", DurationS: 3000, Phases: []string{"M2"}, End: "stuck", Done: 1,
			Tasks: []report.TaskRun{
				{ID: "M2-01", Rank: "sonnet", Model: "sonnet", Result: report.ResultDone, Attempts: 1, DurationS: 600, VerifyPassed: 1},
				{ID: "M2-02", Rank: "opus", Model: "opus", Result: report.ResultOpen, Attempts: 2, DurationS: 2400, VerifyFailed: 2},
			},
			Commits: []string{"a1b2c3d"}, Errors: []string{"M2-02: verify failed twice"},
		},
		{StartedAt: "2026-10-06T21:10:00Z", DurationS: 4500, Phases: []string{"M1", "M2"}, End: "completed", Done: 3, Skipped: 1,
			Tasks: []report.TaskRun{{ID: "M1-01", Result: report.ResultDone, Attempts: 1}}},
		{StartedAt: "2026-10-05T10:00:00Z", Phases: []string{"M1"}, End: report.EndInterrupted},
	}}
}

func TestDoctorHistoryGolden(t *testing.T) {
	pages := []struct {
		name string
		make func(w, h int) (tea.Model, func() string)
	}{
		{"doctor", func(w, h int) (tea.Model, func() string) {
			s := newDoctorScreen(planPagesHome(w, h, doctorFix(doctorMixed)))
			return s, s.View
		}},
		{"doctor_clean", func(w, h int) (tea.Model, func() string) {
			s := newDoctorScreen(planPagesHome(w, h, doctorFix(homeDoctor[:1])))
			return s, s.View
		}},
		{"doctor_waiting", func(w, h int) (tea.Model, func() string) {
			f := doctorFix(nil)
			f.doctorDone = false
			s := newDoctorScreen(planPagesHome(w, h, f))
			return s, s.View
		}},
		{"history_runs", func(w, h int) (tea.Model, func() string) {
			s := newHistoryScreen(planPagesHome(w, h, readyFix()))
			s.Update(historyMsg{h: runHistory()})
			return s, s.View
		}},
		{"history_tasks", func(w, h int) (tea.Model, func() string) {
			s := newHistoryScreen(planPagesHome(w, h, readyFix()))
			s.Update(historyMsg{h: runHistory()})
			s.descend()
			return s, s.View
		}},
		{"history_attempts", func(w, h int) (tea.Model, func() string) {
			s := newHistoryScreen(planPagesHome(w, h, readyFix()))
			s.Update(historyMsg{h: runHistory()})
			s.descend()
			s.move(1)
			s.descend()
			return s, s.View
		}},
		{"history_loading", func(w, h int) (tea.Model, func() string) {
			s := newHistoryScreen(planPagesHome(w, h, readyFix()))
			return s, s.View
		}},
		{"history_none", func(w, h int) (tea.Model, func() string) {
			s := newHistoryScreen(planPagesHome(w, h, readyFix()))
			s.Update(historyMsg{})
			return s, s.View
		}},
		{"history_failed", func(w, h int) (tea.Model, func() string) {
			s := newHistoryScreen(planPagesHome(w, h, readyFix()))
			s.Update(historyMsg{err: errors.New("runs.jsonl: permission denied")})
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
				golden(t, fmt.Sprintf("%s_%dx%d", pg.name, w, h), v)
			})
		}
	}
}

// Levels are told in words as well as glyphs, and only the selected row
// shows its next step.
func TestDoctorPageWordsAndExpansion(t *testing.T) {
	th := noColorTheme(t)
	h := homeAt(th, 120, 40, doctorFix(doctorMixed))
	s := newDoctorScreen(h)
	sized(s, 120, 40)
	v := s.View()
	for _, want := range []string{"✓ ok", "! warn", "⨯ fail", "igris.toml:7:", "edit igris.toml, then igris doctor", "next: ", "5 checks · 1 fail · 2 warn"} {
		if !strings.Contains(v, want) {
			t.Errorf("doctor page lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "unset ANTHROPIC_API_KEY") {
		t.Errorf("an unselected row expanded:\n%s", v)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyUp})
	if v := s.View(); !strings.Contains(v, "unset ANTHROPIC_API_KEY") || !strings.Contains(v, "asks you before a run starts") {
		t.Errorf("the selected row didn't expand:\n%s", v)
	}
}

func TestDoctorPageCopyAndAgain(t *testing.T) {
	var out bytes.Buffer
	a, svc := planApp(t, doctorFix(doctorMixed))
	home := a.stack[0].(*homeScreen)
	home.out = &out
	press(a, "i")
	if _, ok := a.stack[len(a.stack)-1].(*doctorScreen); !ok {
		t.Fatal("i didn't open the doctor page")
	}
	press(a, "y") // starts on the failure
	if !strings.Contains(out.String(), "\x1b]52;") || !strings.Contains(a.View(), "copied") {
		t.Errorf("y didn't copy the fix command: %q", out.String())
	}
	n := svc.called("Doctor")
	press(a, "i")
	if svc.called("Doctor") != n+1 {
		t.Errorf("i ran doctor %d more times, want 1", svc.called("Doctor")-n)
	}
	// A row with nothing to run copies nothing and offers no Copy button.
	press(a, "k", "k")
	out.Reset()
	if v := a.View(); strings.Contains(v, "Copy fix command") {
		t.Errorf("Copy offered on a row without a fix:\n%s", v)
	}
	press(a, "y")
	if out.Len() != 0 {
		t.Errorf("y copied %q from a row without a fix", out.String())
	}
}

func TestDoctorPageRowActions(t *testing.T) {
	a, _ := planApp(t, doctorFix(doctorMixed))
	home := a.stack[0].(*homeScreen)
	press(a, "i")
	// The failing config row: Edit config goes home.
	if v := a.View(); !strings.Contains(v, "Edit config") || strings.Contains(v, "Init") {
		t.Errorf("config row's buttons:\n%s", v)
	}
	press(a, "I") // not this row's action
	if len(a.stack) != 2 {
		t.Fatal("I left the page on the config row")
	}
	press(a, "e")
	if len(a.stack) != 1 || !strings.Contains(home.status, "Edit config") {
		t.Errorf("e: %d screens, status %q", len(a.stack), home.status)
	}
	// The allow-rule row: Init, which home does not offer while igris.toml
	// exists, so the button is absent too.
	press(a, "i", "down")
	if v := a.View(); strings.Contains(v, "[Init]") {
		t.Errorf("Init offered where home doesn't:\n%s", v)
	}
}

func TestDoctorPageInitWhereHomeOffersIt(t *testing.T) {
	rs := []checks.Result{{ID: checks.IDConfigValid, Level: checks.OK, Message: "no igris.toml here; using the defaults", Next: "igris init"}}
	f := homeFixture{snap: homeStates[3].fix().snap, doctor: rs, doctorDone: true} // get_started
	a, _ := planApp(t, f)
	press(a, "i")
	if !strings.Contains(a.View(), "Init") {
		t.Fatalf("no Init button on the no-config row:\n%s", a.View())
	}
	press(a, "I")
	home := a.stack[0].(*homeScreen)
	if len(a.stack) != 1 || !strings.Contains(home.status, "Init") {
		t.Errorf("I: %d screens, status %q", len(a.stack), home.status)
	}
}

func TestDoctorPageCheckAndAdapt(t *testing.T) {
	rs := []checks.Result{{ID: checks.IDPlanValid, Level: checks.Fail, File: "tasks.md", Line: 12, Message: "duplicate task ID M1-01", Next: "igris adapt"}}
	f := invalidFix()
	f.doctor = rs
	a, _ := planApp(t, f)
	press(a, "i")
	v := a.View()
	if !strings.Contains(v, "Check") || !strings.Contains(v, "Adapt") {
		t.Fatalf("plan row lacks Check and Adapt:\n%s", v)
	}
	press(a, "c")
	if len(a.stack) != 2 {
		t.Fatalf("c left %d screens", len(a.stack))
	}
	if _, ok := a.stack[1].(*checkScreen); !ok {
		t.Errorf("c on the plan row opened %T", a.stack[1])
	}
}

func TestDoctorPageMouse(t *testing.T) {
	h := planPagesHome(100, 30, doctorFix(doctorMixed))
	s := newDoctorScreen(h)
	sized(s, 100, 30)
	s.View()
	for y, o := range s.owner {
		if o == 1 {
			s.Update(tea.MouseMsg{X: 5, Y: y + 2 - s.p.top, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			break
		}
	}
	if s.sel != 1 {
		t.Errorf("a click selected %d, want 1", s.sel)
	}
}

func TestHistoryPageLevels(t *testing.T) {
	a, svc := planApp(t, readyFix())
	a.stack[0].(*homeScreen).loc = time.UTC
	svc.history = runHistory()
	press(a, "h")
	if _, ok := a.stack[len(a.stack)-1].(*historyScreen); !ok {
		t.Fatal("h didn't open the history page")
	}
	if svc.called("History") == 0 {
		t.Error("history page didn't read the run log")
	}
	v := a.View()
	for _, want := range []string{"Oct 07 07:12", "M2", "1 done · stuck · 50m", "Oct 06 21:10", "3 done · 1 skipped · completed · 1h15m", "interrupted"} {
		if !strings.Contains(v, want) {
			t.Errorf("runs level lacks %q:\n%s", want, v)
		}
	}
	press(a, "enter")
	v = a.View()
	for _, want := range []string{"run Oct 07 07:12", "1 commit: a1b2c3d", "! M2-02: verify failed twice", "M2-01", "sonnet → sonnet", "unfinished · opus → opus · 2 attempts · 40m"} {
		if !strings.Contains(v, want) {
			t.Errorf("tasks level lacks %q:\n%s", want, v)
		}
	}
	press(a, "down", "enter")
	v = a.View()
	for _, want := range []string{"M2-02", "result", "unfinished", "attempts", "2 failed, 0 passed"} {
		if !strings.Contains(v, want) {
			t.Errorf("attempts level lacks %q:\n%s", want, v)
		}
	}
	// esc climbs one level at a time, then closes.
	press(a, "esc")
	if !strings.Contains(a.View(), "1 commit") || len(a.stack) != 2 {
		t.Errorf("esc from the attempts didn't return to the tasks:\n%s", a.View())
	}
	press(a, "esc")
	if !strings.Contains(a.View(), "enter opens") || len(a.stack) != 2 {
		t.Errorf("esc from the tasks didn't return to the runs:\n%s", a.View())
	}
	press(a, "esc")
	if len(a.stack) != 1 {
		t.Errorf("esc on the runs left %d screens", len(a.stack))
	}
}

func TestHistoryPageIsReadOnlyAndMouse(t *testing.T) {
	h := planPagesHome(100, 30, readyFix())
	s := newHistoryScreen(h)
	sized(s, 100, 30)
	s.Update(historyMsg{h: runHistory()})
	s.View()
	for _, k := range []string{"a", "e", "d", "s", "x", "y", "i"} {
		if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}); cmd != nil {
			t.Errorf("%q does something on a read-only page", k)
		}
	}
	if strings.Contains(s.View(), "[Edit") {
		t.Error("history offers an edit button")
	}
	row := func(i int) int {
		for y, o := range s.owner {
			if o == i {
				return y + 2 - s.p.top
			}
		}
		t.Fatalf("run %d not drawn", i)
		return 0
	}
	click := func(y int) {
		s.Update(tea.MouseMsg{X: 5, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		s.View()
	}
	click(row(1))
	if s.runSel != 1 || s.level != 0 {
		t.Fatalf("a click: run %d, level %d", s.runSel, s.level)
	}
	click(row(1))
	if s.level != 1 {
		t.Errorf("a second click: level %d, want 1", s.level)
	}
}

// A refreshed log that no longer holds the open run brings the page back
// to the runs.
func TestHistoryPageSurvivesLogChange(t *testing.T) {
	s := newHistoryScreen(planPagesHome(80, 24, readyFix()))
	sized(s, 80, 24)
	s.Update(historyMsg{h: runHistory()})
	s.runSel = 2
	s.descend()
	s.Update(historyMsg{})
	if s.level != 0 {
		t.Errorf("level %d after the run vanished", s.level)
	}
	s.View()
}
