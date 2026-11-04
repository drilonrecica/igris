package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// WizardSlice is a case of testdata/wizard_slices.json: the arise flags,
// the wizard's keys on the ready sinjal project that mean the same, and
// what both must ask for. cmd/igris parses the same args with arise's own
// flags and holds them to the same request (TestAriseFlagsEqualWizardSlices).
type wizardSlice struct {
	Name    string   `json:"name"`
	Args    []string `json:"args"`
	Keys    []string `json:"keys"`
	Phase   string   `json:"phase"`
	Through string   `json:"through"`
	Only    []string `json:"only"`
	From    string   `json:"from"`
	Until   string   `json:"until"`
	Slice   []string `json:"slice"`
	Summary string   `json:"summary"`
}

func readWizardSlices(t *testing.T) []wizardSlice {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "wizard_slices.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []wizardSlice
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// sliceIDs are the tasks of p a run of req may select, in plan order, as
// the engine resolves them.
func sliceIDs(t *testing.T, p *plan.Plan, req report.RunRequest) []string {
	t.Helper()
	rng, err := engine.ResolveRange(p, req.Phase, req.Through, req.Selection)
	if err != nil {
		t.Fatalf("resolve %+v: %v", req, err)
	}
	var ids []string
	for _, ph := range rng.Phases {
		for _, tk := range ph.Tasks {
			if rng.In(tk) {
				ids = append(ids, tk.ID)
			}
		}
	}
	return ids
}

// The Tasks step (SPEC §15.6) asks for what --only, --from and --until
// ask for (§5.5): the same request, so the same slice, and the summary
// names it.
func TestWizardSliceEqualsTheFlags(t *testing.T) {
	for _, c := range readWizardSlices(t) {
		t.Run(c.Name, func(t *testing.T) {
			st := &starts{}
			a, home := wizardApp(t, st, report.Prelaunch{}, nil)
			press(a, c.Keys...)
			d := home.dialog
			if d == nil || d.title != c.Summary {
				t.Fatalf("after %q: dialog %+v, want the summary %q", c.Keys, d, c.Summary)
			}
			press(a, "enter") // Arise
			if st.count() != 1 {
				t.Fatalf("%d starts, want 1", st.count())
			}
			want := report.RunRequest{Phase: c.Phase, Through: c.Through, Selection: state.Selection{Only: c.Only, From: c.From, Until: c.Until}}
			got := st.req(0)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("request %+v, want %+v (arise %q)", got, want, c.Args)
			}
			if ids := sliceIDs(t, homeSnap("ready").Plan, got); !reflect.DeepEqual(ids, c.Slice) {
				t.Errorf("slice %q, want %q", ids, c.Slice)
			}
		})
	}
}

// The pickers show each task's status, rank and what it waits on; Only
// needs a pick before it goes on, as --only needs an ID.
func TestWizardOnlyPicker(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, nil)
	press(a, "a", "enter", "enter", "2")
	d := home.dialog
	want := []string{
		"[ ] M2-02 Lock file · ready · opus",
		"[ ] M2-03 State file · ready · sonnet · waits on M2-02",
		"[ ] M2-04 Signals · ready · haiku · waits on M2-02, M2-03",
		"Continue", "Back",
	}
	if d.title != "Only · 0 tasks picked" || !reflect.DeepEqual(labels(d), want) {
		t.Fatalf("only picker %q %q, want %q", d.title, labels(d), want)
	}
	press(a, "4") // Continue with nothing picked
	if d = home.dialog; d.title != "Only · pick at least one task" || home.launch.step != wizOnly {
		t.Fatalf("an empty Only went on: %q (step %d)", d.title, home.launch.step)
	}
	press(a, "2", "2") // M2-03 picked, then dropped
	if d = home.dialog; d.title != "Only · 0 tasks picked" || !strings.HasPrefix(d.options[1].label, "[ ] ") {
		t.Errorf("picked twice: %q %q", d.title, d.options[1].label)
	}
	press(a, "3")
	if d = home.dialog; d.title != "Only · 1 task picked" || !strings.HasPrefix(d.options[2].label, "[x] M2-04") {
		t.Errorf("picked M2-04: %q %q", d.title, d.options[2].label)
	}
	press(a, "esc") // Back to the Tasks step, Only still selected
	if d = home.dialog; d.title != "Tasks" || d.selected != tasksOnly {
		t.Errorf("esc: %q (selected %d), want the Tasks step on Only…", d.title, d.selected)
	}
}

// Back goes one step back: Until to From, From to the Tasks step; the
// answers given stay selected.
func TestWizardFromUntilBack(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, nil)
	press(a, "a", "enter", "enter", "3", "2") // From…, M2-03
	if d := home.dialog; d.title != "Until" || d.options[0].label != "To the end of the range" || d.selected != 0 {
		t.Fatalf("until %q %q (selected %d)", d.title, labels(home.dialog), d.selected)
	}
	press(a, "esc")
	if d := home.dialog; d.title != "From" || d.selected != 1 {
		t.Fatalf("back from Until: %q (selected %d), want From on M2-03", d.title, d.selected)
	}
	press(a, "esc")
	if d := home.dialog; d.title != "Tasks" || d.selected != tasksFrom {
		t.Fatalf("back from From: %q (selected %d), want the Tasks step on From…", d.title, d.selected)
	}
	press(a, "1", "1", "enter") // All tasks drops the slice
	if got := st.req(0); !got.Selection.Empty() {
		t.Errorf("All tasks after a From: %+v", got.Selection)
	}
}

// With one task with work in the range there is nothing to slice: the
// step is skipped.
func TestWizardTasksSkippedForOneTask(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, func(s *report.Snapshot) {
		s.Plan.Task("M2-03").Status = plan.Done
		s.Plan.Task("M2-04").Status = plan.Done
	})
	press(a, "a", "enter", "enter") // M2, only it
	if d := home.dialog; d.title != "Mode" {
		t.Errorf("after the through: %q, want the mode (M2 has one task with work)", d.title)
	}
}

// A slice that can't run says why in arise's words and goes back to the
// Tasks step; nothing starts.
func TestWizardSliceRefused(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, nil)
	press(a, "a", "enter", "down", "enter") // M2 through M3: the Tasks step
	drive(a, home.sliceDone(state.Selection{From: "M3-01", Until: "M2-03"}))
	d := home.dialog
	if d.title != "This slice can't run" || d.detail != "--from M3-01 comes after --until M2-03 in the plan; swap them" {
		t.Fatalf("refused slice %q %q", d.title, d.detail)
	}
	press(a, "enter")
	if home.dialog.title != "Tasks" || st.count() != 0 {
		t.Errorf("after Back: %q, %d starts", home.dialog.title, st.count())
	}
}

// Resume names the slice it resumes, from state.json (SPEC §5.5).
func TestWizardResumeNamesTheSlice(t *testing.T) {
	st := &starts{}
	a, home := wizardApp(t, st, report.Prelaunch{}, func(s *report.Snapshot) {
		s.Run = homeRun("", time.Time{})
		s.Run.Selection = &state.Selection{Only: []string{"M2-02", "M3-01"}}
	})
	press(a, "a", "enter", "1") // Resume, as planned
	if want := "Resume M2 through M3 · only M2-02, M3-01 · mode as planned · 2 tasks · first M2-02 opus"; home.dialog.title != want {
		t.Errorf("summary %q, want %q", home.dialog.title, want)
	}
}

// Preview from the summary walks the slice, and Arise from the preview
// opens the wizard with it picked again.
func TestWizardPreviewKeepsTheSlice(t *testing.T) {
	a, home, rec := previewApp(t, nil)
	press(a, "a", "enter", "enter", "2", "3", "4", "1") // Only M2-04, as planned
	press(a, "down", "enter")                           // Preview
	want := state.Selection{Only: []string{"M2-04"}}
	if got := rec.last(); !reflect.DeepEqual(got.Selection, want) {
		t.Fatalf("previewed %+v, want only M2-04", got)
	}
	press(a, "a", "enter", "enter") // the wizard again: M2, only it
	if d := home.dialog; d.title != "Tasks" || d.selected != tasksOnly {
		t.Fatalf("tasks %q (selected %d), want Only… selected", d.title, d.selected)
	}
	press(a, "enter")
	if d := home.dialog; d.title != "Only · 1 task picked" || !strings.HasPrefix(d.options[2].label, "[x] M2-04") {
		t.Errorf("only %q %q, want M2-04 picked", d.title, labels(d))
	}
}
