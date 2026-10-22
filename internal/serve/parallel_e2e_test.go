package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// A served run of a parallel phase on the fake backend and a fake clock
// (SPEC §6.11, §13, §17): a client attached while three tasks are active
// sees each of them and detaches, and the run goes on; a second client
// sees what is left and stops the run, which leaves every session open and
// every active task in state.json for the next arise.

const e2ePlan = "<!-- igris:format 3 -->\n# Synthetic parallel plan\n\n## A — Parallel\n\n<!-- igris:parallel -->\n\n" +
	"| ID | Task | Deps | Status | Model | Owner | Group |\n|---|---|---|---|---|---|---|\n" +
	"| A-1 | **One** | — | ready | sonnet | agent | — |\n" +
	"| A-2 | **Two** | — | ready | sonnet | agent | — |\n" +
	"| A-3 | **Three** | — | ready | opus | agent | — |\n" +
	"| A-4 | **Four** | A-1 | blocked | sonnet | agent | — |\n"

const e2eTOML = "nudge = false\nneeds_input_after = \"1h\"\n[run]\ncommit = \"auto\"\n[parallel]\nmax = 3\n[usage]\nenabled = false\n"

// sendFunc is a Sender that tells the test each command it passes on.
type sendFunc func(engine.Command)

func (f sendFunc) Send(c engine.Command) { f(c) }

func TestServedParallelRunDetachAndStop(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"tasks.md": e2ePlan, state.ConfigFile: e2eTOML} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join(root, state.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	clock := engine.NewFakeClock(t0)
	dir, err := state.Open(root, state.Options{Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	be := fake.New() // sessions signal nothing by themselves
	git := &runner.Fake{}
	git.Func(func(c runner.Cmd) (runner.Result, error) {
		switch c.Args[0] {
		case "rev-parse":
			return runner.Result{Stdout: []byte("3fa29c1e0b6d4a8f9c2e7b1d5a0f6e3c8b9d2a4f\n")}, nil
		case "symbolic-ref":
			return runner.Result{Stdout: []byte("main\n")}, nil
		case "rev-list":
			return runner.Result{Stdout: []byte("0\n")}, nil
		}
		return runner.Result{}, nil // a clean tree: nothing to commit
	})

	var eng *engine.Engine
	sent := make(chan engine.Command, 4)
	srv := New(Options{
		Version: "1.5.0", Backend: be.Name(),
		Sender: sendFunc(func(c engine.Command) { eng.Send(c); sent <- c }),
		Tail:   func(ctx context.Context, task string, n int) ([]string, error) { return eng.TailTask(ctx, task, n) },
		Plan: func() (*plan.Plan, error) {
			return plan.Load(filepath.Join(root, "tasks.md"), plan.Options{Root: root})
		},
	})
	eng, err = engine.New(engine.Options{
		Config: cfg, Backend: be, State: dir, Clock: clock, Runner: git, Phase: "A",
		Events: srv.Publish,
	})
	if err != nil {
		t.Fatal(err)
	}
	cards := func(snap Snapshot) string {
		var out []string
		for _, c := range snap.Active {
			slot := "-"
			if c.Slot != nil {
				slot = fmt.Sprint(*c.Slot)
			}
			out = append(out, c.Task+":"+slot)
		}
		return strings.Join(out, " ")
	}

	// The fake clock runs these on the engine's goroutine, so the run
	// stands still while a client attaches.
	var first *sink
	clock.At(time.Minute, func() {
		c, k, _ := attachTo(t, srv)
		first = k
		k.waitFor(t, "the snapshot", func(s *sink) bool { return len(s.snaps) == 1 })
		if got := cards(k.snaps[0]); got != "A-1:0 A-2:1 A-3:2" {
			t.Errorf("first client's cards: %s, want every active task with its slot", got)
		}
		if cur := k.snaps[0].Current; cur == nil || cur.Task != "A-1" {
			t.Errorf("first client's current: %+v, want A-1", cur)
		}
		c.Close() // detach
		select {
		case <-c.Done():
		case <-time.After(patience):
			t.Error("the first client did not detach")
		}
	})
	clock.At(2*time.Minute, func() {
		if err := dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionDone, Note: "did A-1"}); err != nil {
			t.Error(err)
		}
	})
	var second *sink
	clock.At(5*time.Minute, func() {
		c, k, _ := attachTo(t, srv)
		second = k
		k.waitFor(t, "the snapshot", func(s *sink) bool { return len(s.snaps) == 1 })
		// A-1 went on without a client and was done; A-4 took its slot.
		if got := cards(k.snaps[0]); got != "A-2:1 A-3:2 A-4:0" {
			t.Errorf("second client's cards: %s", got)
		}
		c.Send(engine.Command{Kind: engine.CmdStop})
		select {
		case cmd := <-sent:
			if cmd.Kind != engine.CmdStop {
				t.Errorf("the engine got %+v, want the stop", cmd)
			}
		case <-time.After(patience):
			t.Error("the stop did not reach the engine")
		}
	})
	clock.At(24*time.Hour, func() { t.Error("the run did not stop within a day of fake time") })

	res, err := eng.Run(context.Background())
	if err != nil || res.Outcome != engine.Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if first == nil || second == nil {
		t.Fatal("a client never attached")
	}
	second.waitFor(t, "the run's end", func(s *sink) bool { return s.count("end") == 1 })
	if second.res.Outcome != engine.Stopped {
		t.Errorf("the second client saw the run end %s", second.res.Outcome)
	}
	first.mu.Lock()
	if first.count("end") != 0 || first.count(string(engine.TaskDone)) != 0 {
		t.Errorf("the detached client still got events: %v", first.log)
	}
	first.mu.Unlock()
	if len(sent) != 0 {
		t.Errorf("commands from the detach: %v", len(sent))
	}

	// Detaching sent nothing and the run went on: A-1 was done and A-4
	// started while no client was attached. The stop left every session
	// open and the three active tasks in state.json.
	p, err := plan.Load(filepath.Join(root, "tasks.md"), plan.Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for _, task := range p.Tasks {
		statuses = append(statuses, task.ID+"="+task.Status.String())
	}
	if got := strings.Join(statuses, " "); got != "A-1=done A-2=in progress A-3=in progress A-4=in progress" {
		t.Errorf("statuses: %s", got)
	}
	if closed := be.Closed(); len(closed) != 1 {
		t.Errorf("sessions closed: %v, want only A-1's", closed)
	}
	run, err := dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	var active []string
	for _, a := range run.Active {
		if a.Session == nil {
			t.Errorf("%s has no session in state.json", a.TaskID)
		}
		active = append(active, a.TaskID)
	}
	if got := strings.Join(active, " "); got != "A-2 A-3 A-4" {
		t.Errorf("active in state.json: %s", got)
	}
}
