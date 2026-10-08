package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

const resetPlan = `## M0 — Test

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **First** | — | done | sonnet | agent |
| M0-02 | **Second** | M0-01 | in progress | sonnet | agent |
| M0-03 | **Third** | M0-01 | ready | sonnet | agent |
| M0-04 | **Sign** | M0-02 | blocked | — | user |
`

// resetProject is signalProject with resetPlan.
func resetProject(t *testing.T) (root string) {
	t.Helper()
	root = signalProject(t)
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte(resetPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Without a running igris, reset writes the Status cells itself.
func TestResetDirect(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		code    int
		out     string // stdout, exactly
		errText string // in stderr
		plan    func(string) string
		logged  string // the task_reset detail; "" = none
	}{
		{
			name: "in progress", args: []string{"reset", "M0-02"}, code: exitOK,
			out:    "M0-02: in progress → ready\n",
			plan:   func(s string) string { return strings.Replace(s, "| M0-01 | in progress |", "| M0-01 | ready |", 1) },
			logged: "in progress",
		},
		{
			name: "done needs force", args: []string{"reset", "M0-01"}, code: exitFail,
			errText: "igris reset: M0-01 is done; pass --force to reset it\n",
		},
		{
			name: "done forced", args: []string{"reset", "--force", "M0-01"}, code: exitOK,
			out: "M0-01: done → ready\nM0-03: ready → blocked\n" +
				"M0-02 depends on M0-01 and is in progress; reset leaves it as it is\n",
			plan: func(s string) string {
				return strings.NewReplacer("| — | done |", "| — | ready |", "| M0-01 | ready |", "| M0-01 | blocked |").Replace(s)
			},
			logged: "done",
		},
		{name: "ready", args: []string{"reset", "M0-03"}, code: exitOK, out: "M0-03 is ready: nothing to reset\n"},
		{name: "blocked user task", args: []string{"reset", "M0-04", "--force"}, code: exitOK, out: "M0-04 is blocked: nothing to reset\n"},
		{name: "unknown task", args: []string{"reset", "M9-99"}, code: exitFail, errText: "is not in"},
		{name: "bad id", args: []string{"reset", "../x"}, code: exitUsage, errText: "invalid task ID"},
		{name: "id with a space", args: []string{"reset", "bad id"}, code: exitUsage, errText: `invalid task ID "bad id"`},
		{name: "no id", args: []string{"reset"}, code: exitUsage, errText: "missing ID argument"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := resetProject(t)
			var out, errb bytes.Buffer
			if got := run(tt.args, &out, &errb); got != tt.code {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, tt.code, errb.String())
			}
			if out.String() != tt.out {
				t.Errorf("stdout = %q, want %q", out.String(), tt.out)
			}
			if !strings.Contains(errb.String(), tt.errText) {
				t.Errorf("stderr = %q, want %q", errb.String(), tt.errText)
			}
			want := resetPlan
			if tt.plan != nil {
				// Rows are matched by "| <dep> | <status> |" fragments.
				want = tt.plan(resetPlan)
			}
			if got := readFile(t, filepath.Join(root, "tasks.md")); got != want {
				t.Errorf("plan:\n%s\nwant:\n%s", got, want)
			}
			events, _ := state.PeekEvents(root)
			switch {
			case tt.logged == "" && len(events) > 0:
				t.Errorf("logged %+v, want nothing", events)
			case tt.logged != "" && (len(events) != 1 || events[0].Type != state.EventTaskReset || events[0].Detail != tt.logged || events[0].Task == ""):
				t.Errorf("run log = %+v, want one task_reset %q", events, tt.logged)
			}
		})
	}
}

func TestResetRefusesInvalidPlan(t *testing.T) {
	root := resetProject(t)
	bad := strings.Replace(resetPlan, "| ready |", "| todo |", 1)
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if got := run([]string{"reset", "M0-02"}, &out, &errb); got != exitFail {
		t.Fatalf("exit = %d", got)
	}
	if !strings.Contains(errb.String(), "is not valid; run `igris check` and fix:") || !strings.Contains(errb.String(), `unknown status "todo"`) {
		t.Errorf("stderr = %q", errb.String())
	}
	if readFile(t, filepath.Join(root, "tasks.md")) != bad {
		t.Error("the plan changed")
	}
}

// The interrupted run's task may still have a session open.
func TestResetInterruptedTask(t *testing.T) {
	root := resetProject(t)
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.SaveRun(&state.Run{StartedAt: time.Now(), Phases: []string{"M0"}, Current: &state.Current{TaskID: "M0-02", StartedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if got := run([]string{"reset", "M0-02"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if want := "M0-02: in progress → ready\nM0-02 was the task of the interrupted run; its session may still be open: close it by hand\n"; out.String() != want {
		t.Errorf("stdout = %q", out.String())
	}
}

// With a live igris here, reset hands the change to it as a request in
// the task's reset slot, which the owner confirms there (SPEC §6.2); a
// pending done stays as it is.
func TestResetDuringARun(t *testing.T) {
	root := resetProject(t)
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.WriteSignal(state.Signal{ID: "M0-01", Action: state.ActionDone}); err != nil {
		t.Fatal(err)
	}
	lock, err := dir.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	var out, errb bytes.Buffer
	if got := run([]string{"reset", "M0-01", "--force"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if want := "M0-01: reset requested; confirm it in the running igris (if none is running, the next `igris arise` asks)\nM0-02 depends on M0-01 and is in progress; reset leaves it as it is\n"; out.String() != want {
		t.Errorf("stdout = %q", out.String())
	}
	s, err := dir.ReadReset("M0-01")
	if err != nil || s == nil || s.Action != state.ActionReset || !s.Force {
		t.Errorf("reset signal = %+v, %v; want a forced reset", s, err)
	}
	if s, err := dir.ReadSignal("M0-01"); err != nil || s == nil || s.Action != state.ActionDone {
		t.Errorf("done signal = %+v, %v; want it kept", s, err)
	}
	if readFile(t, filepath.Join(root, "tasks.md")) != resetPlan {
		t.Error("the plan changed; only the running igris writes it")
	}
	if events, _ := state.PeekEvents(root); len(events) != 0 {
		t.Errorf("logged %+v; the running igris logs the reset when it applies it", events)
	}
}

func TestResetRemoteLock(t *testing.T) {
	root := resetProject(t)
	if _, err := state.Open(root, state.Options{}); err != nil {
		t.Fatal(err)
	}
	lock := `{"pid":4242,"host":"elsewhere.invalid","started_at":"2026-10-08T10:00:00Z"}`
	if err := os.WriteFile(filepath.Join(root, ".igris", "igris.lock"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if got := run([]string{"reset", "M0-02"}, &out, &errb); got != exitFail {
		t.Fatalf("exit = %d", got)
	}
	if e := errb.String(); !strings.Contains(e, "on another host") || !strings.Contains(e, "igris reset M0-02` there") || !strings.Contains(e, "igris arise --force-unlock") {
		t.Errorf("stderr = %q", e)
	}
	if readFile(t, filepath.Join(root, "tasks.md")) != resetPlan {
		t.Error("the plan changed")
	}
}

// The direct write takes the run lock for its duration: an `igris arise`
// that started after reset looked at the lock makes it fail, plan untouched.
func TestResetDirectTakesTheLock(t *testing.T) {
	root := resetProject(t)
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := dir.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	p, err := plan.Load(filepath.Join(root, "tasks.md"), plan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if got := resetDirect(root, p.Path, plan.Options{}, plan.Rules{}, p.Task("M0-02"), false, false, &out, &errb); got != exitFail {
		t.Fatalf("exit = %d, want %d", got, exitFail)
	}
	if e := errb.String(); !strings.Contains(e, "igris is already running") || !strings.Contains(e, "run `igris reset M0-02` again") {
		t.Errorf("stderr = %q", e)
	}
	if readFile(t, filepath.Join(root, "tasks.md")) != resetPlan {
		t.Error("the plan was written under another run's lock")
	}
}
