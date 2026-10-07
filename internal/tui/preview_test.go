package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/report"
)

// previewDry is a dry run of M2 through M3 with every kind of line.
func previewDry() *report.DryRun {
	return &report.DryRun{
		Interrupted: "M2-02",
		Warnings:    []string{"M2-04 asks for model fable, which igris.toml doesn't define"},
		Scope:       "M2 through M3",
		Sessions:    3,
		Users:       1,
		Steps: []report.DryStep{
			{Kind: report.StepSession, N: 1, Task: "M2-02", Rank: "sonnet", Model: "sonnet", Mode: "plan", Title: "Parse widgets with a title long enough to wrap in the narrow layouts", Resumed: true},
			{Kind: report.StepSession, N: 2, Task: "M2-04", Rank: "opus", Model: "opus", Mode: "yolo", Title: "Wire the dispatcher"},
			{Kind: report.StepUser, N: 3, Task: "M2-05", Title: "Buy the domain"},
			{Kind: report.StepPhaseDone, Phase: "M2"},
			{Kind: report.StepNote, Lines: []string{"mode of M3 follows the Mode column"}},
			{Kind: report.StepSession, N: 4, Task: "M3-01", Rank: "haiku", Model: "haiku", Mode: "default", Title: "Release notes"},
			{Kind: report.StepWarning, Lines: []string{"phase M3 is stuck: M3-02 waits for M9-01"}},
		},
	}
}

func previewAt(th *theme, w, h int, res *report.DryRun, err error) *previewScreen {
	svc := &fakeServices{preview: func(report.RunRequest) (*report.DryRun, error) { return res, err }}
	s := newPreview(context.Background(), svc, th, report.RunRequest{Phase: "M2", Through: "M3"})
	s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	if res != nil || err != nil {
		s.Update(previewMsg{res, err})
	}
	return s
}

func TestPreviewGolden(t *testing.T) {
	states := []struct {
		name string
		res  *report.DryRun
		err  error
	}{
		{"walk", previewDry(), nil},
		{"failed", &report.DryRun{Warnings: []string{"plan hint"}}, errors.New("unknown phase \"X\"")},
		{"empty", &report.DryRun{Scope: "M2"}, nil},
		{"loading", nil, nil},
	}
	for _, st := range states {
		for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 20}} {
			w, h := size[0], size[1]
			t.Run(fmt.Sprintf("%s_%dx%d", st.name, w, h), func(t *testing.T) {
				view := previewAt(&theme{}, w, h, st.res, st.err).View()
				checkFits(t, view, w, h)
				golden(t, fmt.Sprintf("preview_%s_%dx%d", st.name, w, h), view)
			})
		}
	}
}

// Under NO_COLOR the badge and warnings are still told in words.
func TestPreviewNoColor(t *testing.T) {
	view := previewAt(noColorTheme(t), 120, 40, previewDry(), nil).View()
	for _, want := range []string{"[SKIP PERMISSIONS]", "warning:", "resumed: fresh session", "opus → opus", "3 sessions · 1 user task"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

func TestPreviewScrolls(t *testing.T) {
	s := previewAt(&theme{}, 50, 12, previewDry(), nil)
	first := s.View()
	for range 3 {
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if s.View() == first {
		t.Error("page down didn't scroll")
	}
}

// previewRecorder is a Preview that answers dry and notes its requests.
type previewRecorder struct {
	mu   sync.Mutex
	reqs []report.RunRequest
}

func (p *previewRecorder) fn(req report.RunRequest) (*report.DryRun, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	return previewDry(), nil
}

func (p *previewRecorder) last() report.RunRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reqs[len(p.reqs)-1]
}

func previewApp(t *testing.T, edit func(*report.Snapshot)) (*appModel, *homeScreen, *previewRecorder) {
	t.Helper()
	rec := &previewRecorder{}
	svc := &fakeServices{preview: rec.fn}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) {
		s := homeSnap("ready")
		if edit != nil {
			edit(s)
		}
		return s, nil
	}
	a, home := pollApp(t, svc)
	return a, home, rec
}

func TestPreviewFromHomeAndAgain(t *testing.T) {
	a, _, rec := previewApp(t, nil)
	press(a, "v")
	if len(a.stack) != 2 {
		t.Fatalf("stack %d, want home and the preview", len(a.stack))
	}
	if got := rec.last(); got != (report.RunRequest{Phase: "M2"}) {
		t.Errorf("previewed %+v, want the selected phase", got)
	}
	if v := a.View(); !strings.Contains(v, "3 sessions · 1 user task") || !strings.Contains(v, "[SKIP PERMISSIONS]") {
		t.Errorf("preview not drawn:\n%s", v)
	}
	press(a, "v")
	if len(rec.reqs) != 2 {
		t.Errorf("v ran the dry run %d times in all, want 2", len(rec.reqs))
	}
	press(a, "esc")
	if len(a.stack) != 1 {
		t.Errorf("esc left %d screens, want home", len(a.stack))
	}
}

func TestPreviewOfAResumableRunIsTheLastRunsPhases(t *testing.T) {
	a, _, rec := previewApp(t, func(s *report.Snapshot) { s.Run = homeRun("M2-02", homeNow) })
	press(a, "v")
	if got := rec.last(); got != (report.RunRequest{}) {
		t.Errorf("previewed %+v, want the last run's phases (empty phase)", got)
	}
}

func TestPreviewAriseOpensTheWizardPrefilled(t *testing.T) {
	a, home, _ := previewApp(t, nil)
	// Wizard → phase M2, only it, mode plan → summary → Preview.
	press(a, "a", "enter", "enter", "down", "down", "down", "enter")
	wantMode := home.launch.req.Mode
	if wantMode == "" {
		t.Fatalf("no mode picked: %+v", home.launch.req)
	}
	press(a, "down", "enter") // Preview
	if home.launch != nil || len(a.stack) != 2 {
		t.Fatalf("wizard %v, stack %d; want the wizard closed over a preview", home.launch, len(a.stack))
	}
	press(a, "a")
	if len(a.stack) != 1 || home.launch == nil {
		t.Fatalf("stack %d, wizard %v; want home with the wizard", len(a.stack), home.launch)
	}
	if home.launch.step != wizPhase || home.launch.phases[home.dialog.selected] != "M2" {
		t.Errorf("wizard at step %d selecting %v, want the phase dialog on M2", home.launch.step, home.dialog.selected)
	}
	press(a, "enter", "enter") // M2, only it → the mode is next
	if home.launch.step != wizMode {
		t.Fatalf("step %d, want the mode", home.launch.step)
	}
	if got := home.launch.req.Mode; got != wantMode {
		t.Errorf("mode %q, want the previewed %q", got, wantMode)
	}
	if want := slices.IndexFunc(modeActs, func(m struct {
		act         action
		mode, label string
	}) bool {
		return m.mode == wantMode
	}) + 1; home.dialog.selected != want {
		t.Errorf("mode option %d selected, want the previewed mode's (%d)", home.dialog.selected, want)
	}
}

func TestPreviewCantAriseFromAFailedWalk(t *testing.T) {
	s := previewAt(&theme{}, 80, 24, &report.DryRun{}, errors.New("boom"))
	if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")}); cmd != nil {
		t.Error("a failed dry run offered to arise")
	}
}
