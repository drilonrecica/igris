package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
)

// The end-of-run summary's Arise (SPEC §15.3), end to end with the fake
// engine: M2 completes, Arise M3 on its summary goes back to home and
// opens the wizard there with M3 selected, and the wizard's launch starts
// M3.
func TestAriseNextPhaseFromTheSummary(t *testing.T) {
	// The plan as the run leaves it: M2 done, M3 with work.
	after := strings.ReplaceAll(homePlan, "STATUS", "done")
	after = strings.Replace(after, "| **State file** | M2-02 | ready |", "| **State file** | M2-02 | done |", 1)
	after = strings.Replace(after, "| **Signals** | M2-02, M2-03 | ready |", "| **Signals** | M2-02, M2-03 | done |", 1)
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}

	st := &starts{answers: []error{nil, nil}}
	st.runner = func(events func(engine.Event)) *fakeRunner {
		r := newFakeRunner(events)
		if len(st.reqs) == 1 { // M2 completes; M3 runs until stopped
			r.end = &engine.Result{Outcome: engine.Completed, Phase: "M2"}
		}
		return r
	}
	svc := &fakeServices{start: st.start}
	svc.snapshot = func(context.Context) (*report.Snapshot, error) {
		s := homeSnap("ready")
		s.PlanPath = path
		if st.count() > 0 { // home reads the project again after the run
			s.Plan = plan.Parse("tasks.md", []byte(after), plan.Options{})
			st, err := report.Status(s.Plan, "")
			if err != nil {
				return nil, err
			}
			s.Status = &st
		}
		return s, nil
	}
	tm := appProgram(t, svc, 120, 40)
	seen(t, tm, "READY")
	ariseM2(t, tm)
	seen(t, tm, "Done ✓ completed", "[›q Home‹]", "[a Arise M3…]")
	tm.Type("a")
	// The wizard over home, on its Phase step with M3 selected.
	seen(t, tm, "Which phase to run.", "› 1. M3 Engine")
	key(tm, "enter", "enter", "enter", "enter") // M3, Only M3, All tasks, as planned
	seen(t, tm, "Arise M3 · mode as planned")
	key(tm, "enter")
	seen(t, tm, "[q Home]")
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	finalApp(t, tm)
	if n := st.count(); n != 2 {
		t.Fatalf("%d starts, want M2 then M3", n)
	}
	if got := st.req(1); got.Phase != "M3" || got.Through != "" || !got.Selection.Empty() {
		t.Errorf("second start %+v, want M3 alone", got)
	}
}
