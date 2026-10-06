package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// chainPlan has a phase A whose tasks depend on each other in order, and a
// phase B waiting on A.
const chainPlan = `# Demo plan

## A — First phase

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **One** — the first thing | — | ready | sonnet | agent |
| A-2 | **Two** — the second thing | A-1 | blocked | opus | agent |
| A-3 | **Three** — the third thing | A-1, A-2 | blocked | fable | agent + user |

## B — Second phase

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| B-1 | **Four** | A-3 | blocked | sonnet | agent |
`

// harness is a temp project with a fake backend and a fake clock. Sessions
// signal done at their first prompt unless a test changes the auto-signal.
type harness struct {
	t    *testing.T
	root string
	cfg  *config.Config
	be   *fake.Backend
	cmds *runner.Fake // verify and git commands, answered by gitFn and verifyFn
	// gitFn answers git commands, verifyFn the verify command.
	gitFn, verifyFn func(runner.Cmd) (runner.Result, error)
	dir             *state.Dir
	clock           *FakeClock
	eng             *Engine
	events          []Event
	// onEvent runs for every event, on the engine's goroutine.
	onEvent func(Event)
	cancel  context.CancelFunc
}

// newHarness writes planText to tasks.md and tomlText to igris.toml in a
// temp project and loads the config from it, like `igris arise` does.
func newHarness(t *testing.T, planText, tomlText string) *harness {
	t.Helper()
	h := &harness{t: t, root: t.TempDir(), be: fake.New(), cmds: &runner.Fake{}, clock: NewFakeClock(t0)}
	h.write("tasks.md", planText)
	h.write(state.ConfigFile, tomlText)
	cfg, err := config.Load(filepath.Join(h.root, state.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	h.cfg = cfg
	h.dir, err = state.Open(h.root, state.Options{Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	// git sees a clean tree unless a test says otherwise; verify must be
	// scripted by the tests that configure it.
	h.gitFn = func(runner.Cmd) (runner.Result, error) { return runner.Result{}, nil }
	h.verifyFn = func(c runner.Cmd) (runner.Result, error) {
		return runner.Result{}, fmt.Errorf("unexpected command %s", c)
	}
	h.cmds.Func(func(c runner.Cmd) (runner.Result, error) {
		if c.Name == "git" {
			return h.gitFn(c)
		}
		return h.verifyFn(c)
	})
	return h
}

// run runs phase A unless mutate says otherwise. A run that is still going
// after a (fake) day is cancelled and fails the test.
func (h *harness) run(mutate ...func(*Options)) (Result, error) {
	h.t.Helper()
	opts := Options{
		Config:  h.cfg,
		Backend: h.be,
		State:   h.dir,
		Clock:   h.clock,
		Runner:  h.cmds,
		Phase:   "A",
		Events: func(ev Event) {
			h.events = append(h.events, ev)
			if h.onEvent != nil {
				h.onEvent(ev)
			}
		},
	}
	for _, m := range mutate {
		m(&opts)
	}
	eng, err := New(opts)
	if err != nil {
		h.t.Fatalf("New: %v", err)
	}
	h.eng = eng
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.cancel = cancel
	h.clock.At(24*time.Hour, func() {
		h.t.Error("the run did not finish within a day of fake time")
		cancel()
	})
	return eng.Run(ctx)
}

func (h *harness) path(name string) string { return filepath.Join(h.root, name) }

func (h *harness) write(name, content string) {
	h.t.Helper()
	if err := os.WriteFile(h.path(name), []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) read(name string) string {
	h.t.Helper()
	data, err := os.ReadFile(h.path(name))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(data)
}

// signal writes a done signal for id, like `igris done` in a session.
func (h *harness) signal(id string) error {
	return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionDone, Note: "did " + id})
}

// autoSignalExcept keeps the auto-signal for every task but the given ones.
func (h *harness) autoSignalExcept(ids ...string) {
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		for _, skip := range ids {
			if id == skip {
				return nil
			}
		}
		return h.signal(id)
	})
}

// statuses returns "ID=status" for every task, in file order.
func (h *harness) statuses() string {
	h.t.Helper()
	p, err := plan.Load(h.path("tasks.md"), plan.Options{})
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, t := range p.Tasks {
		out = append(out, t.ID+"="+t.Status.String())
	}
	return strings.Join(out, " ")
}

// opened returns the task IDs of the opened sessions, in order.
func (h *harness) opened() string {
	var ids []string
	for _, s := range h.be.Opened() {
		ids = append(ids, s.TaskID)
	}
	return strings.Join(ids, " ")
}

// kinds returns the kinds of the recorded events, in order.
func (h *harness) kinds() string {
	var out []string
	for _, ev := range h.events {
		out = append(out, string(ev.Kind))
	}
	return strings.Join(out, " ")
}

func (h *harness) count(kind EventKind) int {
	n := 0
	for _, ev := range h.events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// event returns the first recorded event of kind for task ("" = any task).
func (h *harness) event(kind EventKind, task string) Event {
	h.t.Helper()
	for _, ev := range h.events {
		if ev.Kind == kind && (task == "" || ev.Task == task) {
			return ev
		}
	}
	h.t.Fatalf("no %s event for %q in: %s", kind, task, h.kinds())
	return Event{}
}

// logged returns the types of the run log's events, in order.
func (h *harness) logged() string {
	h.t.Helper()
	events, err := h.dir.Events()
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		out = append(out, string(e.Type))
	}
	return strings.Join(out, " ")
}

// toasts returns "sound: body" for every backend notification.
func (h *harness) toasts() []string {
	var out []string
	for _, n := range h.be.Notifications() {
		out = append(out, string(n.Sound)+": "+n.Body)
	}
	return out
}

// assertUnlocked fails if the run lock is still held.
func (h *harness) assertUnlocked() {
	h.t.Helper()
	l, err := h.dir.Lock(false)
	if err != nil {
		h.t.Errorf("lock still held after the run: %v", err)
		return
	}
	if err := l.Release(); err != nil {
		h.t.Fatal(err)
	}
}

// calls returns the commands run with program name.
func (h *harness) calls(name string) []runner.Cmd {
	var out []runner.Cmd
	for _, c := range h.cmds.Calls() {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// arg returns the value following flag in a session's arguments.
func arg(spec backend.SessionSpec, flag string) string {
	for i, a := range spec.Args {
		if a == flag && i+1 < len(spec.Args) {
			return spec.Args[i+1]
		}
	}
	return ""
}
