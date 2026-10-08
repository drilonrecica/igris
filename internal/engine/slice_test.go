package engine

// Runs limited to a slice of the phases (SPEC §5.5): --only, --from,
// --until, with and without a named phase and --through.

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

const slicePlan = `# Slice plan

## M1 — First

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M1-01 | **One** | — | ready | sonnet | agent |
| M1-02 | **Two** | M1-01 | blocked | sonnet | agent |
| M1-03 | **Three** | — | ready | sonnet | agent |
| M1-04 | **Four** | M1-02 | blocked | sonnet | agent |

## M2 — Second

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M2-01 | **Five** | — | ready | sonnet | agent |
| M2-02 | **Six** | M2-01 | blocked | sonnet | agent |

## M3 — Third

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M3-01 | **Seven** | — | ready | sonnet | agent |
`

// slice returns a mutation running phase..through limited to sel.
func slice(phase, through string, sel state.Selection) func(*Options) {
	return func(o *Options) { o.Phase, o.Through, o.Selection = phase, through, sel }
}

func only(ids ...string) state.Selection { return state.Selection{Only: ids} }

func (h *harness) phasesOf(kind EventKind) string {
	var out []string
	for _, ev := range h.events {
		if ev.Kind == kind {
			out = append(out, ev.Phase)
		}
	}
	return strings.Join(out, " ")
}

func TestSliceRuns(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Options)
		opened   string
		statuses string
		started  string // phases started
		done     string // phases reported complete
		notRun   []string
		scope    string
		through  string // in state.json
	}{
		{
			name:     "only, in plan order, phase from the IDs",
			mutate:   slice("", "", only("M1-03", "M1-01")),
			opened:   "M1-01 M1-03",
			statuses: "M1-01=done M1-02=ready M1-03=done M1-04=blocked M2-01=ready M2-02=blocked M3-01=ready",
			started:  "M1",
			scope:    "phase M1; only M1-03, M1-01",
		},
		{
			name:     "only, a task whose deps are unmet is not run",
			mutate:   slice("", "", only("M1-04", "M2-01")),
			opened:   "M2-01",
			statuses: "M1-01=ready M1-02=blocked M1-03=ready M1-04=blocked M2-01=done M2-02=ready M3-01=ready",
			started:  "M1 M2",
			notRun:   []string{"M1-04 waits on M1-02 (blocked, phase M1)"},
			scope:    "phase M1, M2; only M1-04, M2-01",
			through:  "M2",
		},
		{
			name:     "from and until in one phase",
			mutate:   slice("", "", state.Selection{From: "M1-01", Until: "M1-03"}),
			opened:   "M1-01 M1-02 M1-03",
			statuses: "M1-01=done M1-02=done M1-03=done M1-04=ready M2-01=ready M2-02=blocked M3-01=ready",
			started:  "M1",
			scope:    "phase M1; from M1-01 until M1-03",
		},
		{
			name:     "from alone runs the rest of its phase",
			mutate:   slice("", "", state.Selection{From: "M2-01"}),
			opened:   "M2-01 M2-02",
			statuses: "M1-01=ready M1-02=blocked M1-03=ready M1-04=blocked M2-01=done M2-02=done M3-01=ready",
			started:  "M2",
			done:     "M2",
			scope:    "phase M2; from M2-01",
		},
		{
			name:     "until alone stops after it",
			mutate:   slice("", "", state.Selection{Until: "M1-02"}),
			opened:   "M1-01 M1-02",
			statuses: "M1-01=done M1-02=done M1-03=ready M1-04=ready M2-01=ready M2-02=blocked M3-01=ready",
			started:  "M1",
			scope:    "phase M1; until M1-02",
		},
		{
			name:     "named phase, --through and --until",
			mutate:   slice("M1", "M2", state.Selection{Until: "M2-01"}),
			opened:   "M1-01 M1-02 M1-03 M1-04 M2-01",
			statuses: "M1-01=done M1-02=done M1-03=done M1-04=done M2-01=done M2-02=ready M3-01=ready",
			started:  "M1 M2",
			done:     "M1",
			scope:    "phase M1, M2; until M2-01",
			through:  "M2",
		},
		{
			name:     "from and until across phases, unmet deps not run",
			mutate:   slice("", "", state.Selection{From: "M1-02", Until: "M2-01"}),
			opened:   "M1-03 M2-01",
			statuses: "M1-01=ready M1-02=blocked M1-03=done M1-04=blocked M2-01=done M2-02=ready M3-01=ready",
			started:  "M1 M2",
			notRun:   []string{"M1-02 waits on M1-01 (ready, phase M1)", "M1-04 waits on M1-02 (blocked, phase M1)"},
			scope:    "phase M1, M2; from M1-02 until M2-01",
			through:  "M2",
		},
		{
			name:     "a phase without slice tasks is passed over",
			mutate:   slice("M1", "M3", only("M1-01", "M3-01")),
			opened:   "M1-01 M3-01",
			statuses: "M1-01=done M1-02=ready M1-03=ready M1-04=blocked M2-01=ready M2-02=blocked M3-01=done",
			started:  "M1 M3",
			done:     "M3",
			scope:    "phase M1, M2, M3; only M1-01, M3-01",
			through:  "M3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, slicePlan, "")
			res, err := h.run(tt.mutate)
			if err != nil || res.Outcome != Completed {
				t.Fatalf("Run = %s, %v; want completed", res.Outcome, err)
			}
			if got := h.opened(); got != tt.opened {
				t.Errorf("sessions opened for %q, want %q", got, tt.opened)
			}
			if got := h.statuses(); got != tt.statuses {
				t.Errorf("statuses:\n%s\nwant:\n%s", got, tt.statuses)
			}
			if got := h.phasesOf(PhaseStarted); got != tt.started {
				t.Errorf("phases started %q, want %q", got, tt.started)
			}
			if got := h.phasesOf(PhaseDone); got != tt.done {
				t.Errorf("phases complete %q, want %q", got, tt.done)
			}
			var notRun []string
			for _, w := range res.NotRun {
				notRun = append(notRun, w.String())
			}
			if !reflect.DeepEqual(notRun, tt.notRun) {
				t.Errorf("not run %q, want %q", notRun, tt.notRun)
			}
			var reported []string
			for _, ev := range h.events {
				if ev.Kind == NotRun {
					for _, w := range ev.Waiting {
						reported = append(reported, w.String())
					}
				}
			}
			if !reflect.DeepEqual(reported, tt.notRun) {
				t.Errorf("not_run events %q, want %q", reported, tt.notRun)
			}
			// Not run is not stuck: no phase_stuck event or notification.
			if h.count(PhaseStuck) != 0 || strings.Contains(strings.Join(h.toasts(), "\n"), "stuck") {
				t.Errorf("a slice reported a stuck phase: %s / %q", h.kinds(), h.toasts())
			}
			if got := len(h.toasts()); got != len(strings.Fields(tt.done)) {
				t.Errorf("toasts %q, want one per complete phase (%s)", h.toasts(), tt.done)
			}
			if got := h.event(RunStarted, "").Detail; got != tt.scope {
				t.Errorf("run_started %q, want %q", got, tt.scope)
			}
			events, err := h.dir.Events()
			if err != nil {
				t.Fatal(err)
			}
			if got := events[0]; got.Type != state.EventRunStarted || got.Detail != tt.scope {
				t.Errorf("logged %s %q, want run_started %q", got.Type, got.Detail, tt.scope)
			}
			if last := events[len(events)-1]; last.Type != state.EventRunStopped || last.Detail != "completed" {
				t.Errorf("last logged %s %q, want run_stopped completed", last.Type, last.Detail)
			}
			run, err := h.dir.LoadRun()
			if err != nil {
				t.Fatal(err)
			}
			var o Options
			tt.mutate(&o)
			sel := o.Selection
			if run.Selection == nil || !reflect.DeepEqual(*run.Selection, sel) || run.Through != tt.through {
				t.Errorf("state.json selection %+v through %q, want %+v through %q", run.Selection, run.Through, sel, tt.through)
			}
			h.assertUnlocked()
		})
	}
}

// A wrong selection fails before anything is written, saying what to change.
func TestSliceValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Options)
		want   string
	}{
		{"unknown ID", slice("", "", only("M1-01", "M9-01")), "--only M9-01 is not a task in "},
		{"until after the phase", slice("M1", "", state.Selection{Until: "M3-01"}),
			"--until M3-01 is in phase M3, outside the run's phase M1; widen --through or drop it"},
		{"until after --through", slice("M1", "M2", state.Selection{Until: "M3-01"}),
			"--until M3-01 is in phase M3, outside the run's phases M1…M2; widen --through or drop it"},
		{"only before the phase", slice("M2", "", only("M1-01")),
			"--only M1-01 is in phase M1, outside the run's phase M2; start at an earlier phase or drop it"},
		{"from after until", slice("", "", state.Selection{From: "M1-03", Until: "M1-01"}),
			"--from M1-03 comes after --until M1-01 in the plan; swap them"},
		{"from after until across phases", slice("M1", "M2", state.Selection{From: "M2-01", Until: "M1-04"}),
			"--from M2-01 comes after --until M1-04 in the plan; swap them"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, slicePlan, "")
			_, err := h.run(tt.mutate)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Run error %v, want %q", err, tt.want)
			}
			if h.read("tasks.md") != slicePlan {
				t.Error("the plan was written")
			}
			if _, err := h.dir.LoadRun(); !errors.Is(err, state.ErrNoRun) {
				t.Errorf("state.json written: %v", err)
			}
			if len(h.events) != 0 || len(h.be.Opened()) != 0 {
				t.Errorf("events %s, sessions %q", h.kinds(), h.opened())
			}
		})
	}
}

// A bare `arise` resumes the last run's slice, and its interrupted task.
func TestSliceResumes(t *testing.T) {
	h := newHarness(t, slicePlan, "")
	h.autoSignalExcept("M1-03")
	h.stopMidTask(time.Minute, slice("", "", only("M1-01", "M1-03")))
	h.autoSignalExcept()
	h.signalAt(2*time.Minute, "M1-03")
	res, err := h.run(resumeLast)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed run = %s, %v", res.Outcome, err)
	}
	if got := h.opened(); got != "M1-01 M1-03" {
		t.Errorf("sessions opened for %q, want nothing outside the slice", got)
	}
	if got := h.event(RunStarted, "").Detail; got != "phase M1; only M1-01, M1-03" {
		t.Errorf("run_started %q", got)
	}
	if ev := h.event(TaskResumed, "M1-03"); ev.Detail != "reattached to its session" {
		t.Errorf("task_resumed %q", ev.Detail)
	}
	if got := h.statuses(); got != "M1-01=done M1-02=ready M1-03=done M1-04=blocked M2-01=ready M2-02=blocked M3-01=ready" {
		t.Errorf("statuses = %s", got)
	}
	if got := h.count(Warning); got != 0 {
		t.Errorf("%d warnings; the interrupted task is in the slice", got)
	}
}

// The interrupted task of an earlier run is picked up first, also when it is
// outside the slice, and the run says so.
func TestSliceResumesTaskOutsideIt(t *testing.T) {
	h := newHarness(t, slicePlan, "")
	h.autoSignalExcept("M1-01")
	h.stopMidTask(6*time.Second, slice("M1", "", state.Selection{}))
	h.autoSignalExcept()
	h.signalAt(time.Minute, "M1-01")
	res, err := h.run(slice("", "", only("M2-01")))
	if err != nil || res.Outcome != Completed {
		t.Fatalf("run = %s, %v", res.Outcome, err)
	}
	if got := h.opened(); got != "M1-01 M2-01" {
		t.Errorf("sessions opened for %q", got)
	}
	if got := h.event(Warning, "").Detail; got != "the interrupted task M1-01 is outside this run's slice (only M2-01); igris picks it up first" {
		t.Errorf("warning %q", got)
	}
	if got := h.kinds(); !strings.Contains(got, "warning task_resumed") {
		t.Errorf("events %s: want the warning before the task is resumed", got)
	}
	if got := h.statuses(); got != "M1-01=done M1-02=ready M1-03=ready M1-04=blocked M2-01=done M2-02=ready M3-01=ready" {
		t.Errorf("statuses = %s", got)
	}
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(run.Phases, " ") != "M2" || run.Selection == nil || run.Current != nil {
		t.Errorf("state.json = %+v", run)
	}
}

// A slice resumed from state.json whose task has left the plan names it.
func TestSliceResumeAfterThePlanChanged(t *testing.T) {
	h := newHarness(t, slicePlan, "")
	if err := h.dir.SaveRun(&state.Run{Phases: []string{"M1"}, Selection: &state.Selection{Only: []string{"M1-09"}}}); err != nil {
		t.Fatal(err)
	}
	_, err := h.run(resumeLast)
	if err == nil || !strings.Contains(err.Error(), "resume the last run's only M1-09: --only M1-09 is not a task in") ||
		!strings.Contains(err.Error(), "name a phase to start a new run") {
		t.Fatalf("Run error %v", err)
	}
}

// Only the slice's tasks need the skip-permissions confirmation up front.
func TestSliceYoloOutsideIt(t *testing.T) {
	const yoloPlan = `## M1

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M1-01 | **One** | — | ready | sonnet | agent | — |
| M1-02 | **Two** | — | ready | sonnet | agent | yolo |
`
	h := newHarness(t, yoloPlan, "")
	if res, err := h.run(slice("", "", only("M1-01"))); err != nil || res.Outcome != Completed {
		t.Fatalf("run = %s, %v", res.Outcome, err)
	}
	h = newHarness(t, yoloPlan, "")
	if _, err := h.run(slice("", "", only("M1-02"))); !errors.Is(err, ErrYoloUnconfirmed) {
		t.Fatalf("run error %v, want ErrYoloUnconfirmed", err)
	}
}
