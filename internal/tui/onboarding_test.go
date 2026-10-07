package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/report"
)

// Onboarding stages of the fake project: what Snapshot reads after each
// step of the first run.
const (
	stageEmpty   = iota // no igris.toml
	stageInit           // igris.toml, no plan
	stageExample        // igris.toml and a valid plan
)

// onboardingServices is a project that changes as Init runs.
func onboardingServices() (*fakeServices, *atomic.Int32, *starts) {
	var stage atomic.Int32
	st := &starts{answers: []error{nil}}
	svc := &fakeServices{start: st.start}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) {
		switch stage.Load() {
		case stageEmpty:
			return &report.Snapshot{Root: "/src/newproj", Project: "newproj", Found: true, Config: config.Default(), NoConfig: true,
				ConfigPath: "/src/newproj/igris.toml", PlanPath: "/src/newproj/tasks.md", PlanMissing: true}, nil
		case stageInit:
			return &report.Snapshot{Root: "/src/newproj", Project: "newproj", Found: true, Config: config.Default(),
				ConfigPath: "/src/newproj/igris.toml", PlanPath: "/src/newproj/tasks.md", PlanMissing: true}, nil
		}
		s := homeSnap("ready")
		s.Recent = nil
		return s, nil
	}
	svc.preview = func(report.RunRequest) (*report.DryRun, error) { return &report.DryRun{Scope: "M2"}, nil }
	svc.onInit = func(example bool) {
		if example {
			stage.Store(stageExample)
		} else {
			stage.Store(stageInit)
		}
	}
	return svc, &stage, st
}

var initSteps = []report.Step{
	{ID: report.StepConfig, Path: "igris.toml", Message: "created igris.toml"},
	{ID: report.StepState, Path: ".igris/", Message: "ready .igris/"},
	{ID: report.StepGitignore, Path: ".gitignore", Message: "added .igris/ to .gitignore"},
	{ID: report.StepClaudeSettings, Path: ".claude/settings.local.json", Message: "allowed `igris done` in .claude/settings.local.json"},
	{ID: report.StepHerdrHint, Message: "recommended: run `herdr integration install claude`"},
}

// The whole first run in one program: GET STARTED, Init, the example
// plan, Check, Preview and Arise, without leaving the TUI.
func TestOnboardingFirstRunReachesArise(t *testing.T) {
	svc, _, st := onboardingServices()
	svc.initSteps = initSteps
	tm := appProgram(t, svc, 120, 40)

	seen(t, tm, "GET STARTED", "⨯ 1 igris.toml", "[›Init‹]")
	tm.Type("I")
	seen(t, tm, ".gitignore", ".claude/settings.local.json", "1. Create files")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	seen(t, tm, "Init · done", "created igris.toml", "added .igris/ to .gitignore", "herdr", "[Example plan]")
	if n := svc.called("Init"); n != 1 {
		t.Fatalf("Init called %d times, want 1", n)
	}

	// The page offers the example plan; it goes back to home, which asks.
	tm.Type("E")
	seen(t, tm, "1. Create example plan")
	svc.initSteps = []report.Step{{ID: report.StepExamplePlan, Path: "tasks.md", Message: "created tasks.md (example plan)"}}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	seen(t, tm, "Init · done", "created tasks.md (example plan)")
	if n := svc.called("Init"); n != 2 {
		t.Fatalf("Init called %d times, want 2", n)
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	seen(t, tm, "READY", "init: created tasks.md (example plan)")

	tm.Type("c")
	seen(t, tm, "c checks again")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	seen(t, tm, "[Notify]")
	tm.Type("v")
	seen(t, tm, "v runs again")
	tm.Type("a")
	seen(t, tm, "Which phase to run.") // the wizard, over home
	for range 3 {                      // phase, through, mode
		tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	}
	seen(t, tm, "Arise M2 · mode as planned")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	seen(t, tm, "[Home]")
	if st.count() != 1 {
		t.Errorf("Start called %d times, want 1", st.count())
	}
	tm.Type("q")
	seen(t, tm, "igris stopped; a running session keeps running")
	tm.Type("q")
	if a := finalApp(t, tm); len(a.stack) != 1 {
		t.Errorf("stack has %d screens, want home alone", len(a.stack))
	}
}

func TestOnboardingInitDialogCancelWritesNothing(t *testing.T) {
	svc, _, _ := onboardingServices()
	a := testApp(svc, 100, 30)
	drive(a, a.Init())
	press(a, "I")
	if v := a.View(); !strings.Contains(v, "1. Create files") && !strings.Contains(v, "Create files") {
		t.Fatalf("no Init dialog:\n%s", v)
	}
	press(a, "esc")
	home := a.stack[0].(*homeScreen)
	if home.initing != nil || home.dialog != nil || svc.called("Init") != 0 {
		t.Errorf("esc: initing %v, dialog %v, Init calls %d; want nothing written", home.initing, home.dialog, svc.called("Init"))
	}
	// The default choice is Create files, never a destructive one.
	press(a, "I")
	if d := a.stack[0].(*homeScreen).dialog; d == nil || d.options[d.selected].act != actInitCreate {
		t.Errorf("default choice is not Create files: %+v", d)
	}
}

func TestOnboardingInitStoppedShowsStepsAndError(t *testing.T) {
	svc, _, _ := onboardingServices()
	svc.initSteps = initSteps[:2]
	svc.initErr = errors.New("write igris.toml: permission denied")
	a := testApp(svc, 100, 30)
	drive(a, a.Init())
	press(a, "I", "enter")
	v := a.View()
	for _, want := range []string{"Init · stopped", "created igris.toml", "init stopped: write igris.toml: permission denied", "run Init again"} {
		if !strings.Contains(v, want) {
			t.Errorf("result page lacks %q:\n%s", want, v)
		}
	}
	if st := a.stack[0].(*homeScreen).status; !strings.Contains(st, "init failed") {
		t.Errorf("status %q", st)
	}
}

// With a non-canonical plan in place, the stepper counts its problems and
// the bar offers Check and Adapt instead of the example plan.
func TestOnboardingNonCanonicalPlanOffersCheckAndAdapt(t *testing.T) {
	f := invalidFix()
	f.snap.NoConfig, f.snap.ConfigPath = true, "/src/sinjal/igris.toml"
	a, _ := planApp(t, f)
	v := a.View()
	for _, want := range []string{"GET STARTED", "⨯ 1 igris.toml", "tasks.md:", "Check", "Adapt"} {
		if !strings.Contains(v, want) {
			t.Errorf("no %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Example plan") {
		t.Errorf("example plan offered over an existing plan:\n%s", v)
	}
}
