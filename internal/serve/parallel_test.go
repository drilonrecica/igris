package serve

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
)

// A parallel phase (SPEC §6.11, §13): the snapshot lists every active task
// in plan order, each with its slot, and every pending question.

const parallelPlan = "<!-- igris:format 3 -->\n\n## M1 — Parallel\n<!-- igris:parallel -->\n\n" +
	"| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n" +
	"| M1-03 | **Parser** | — | in progress | sonnet | agent |\n" +
	"| M1-04 | **Writer** | — | in progress | sonnet | agent |\n" +
	"| M1-05 | **Docs** | — | in progress | opus | agent |\n"

func parallelServer(t *testing.T) *Server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(parallelPlan), 0o600); err != nil {
		t.Fatal(err)
	}
	return New(Options{Version: "1.5.0", Plan: func() (*plan.Plan, error) { return plan.Load(path, plan.Options{}) }})
}

func slot(n int) *int { return &n }

func TestSnapshotSeveralActive(t *testing.T) {
	s := parallelServer(t)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	start := func(sec int, id string, n int) engine.Event {
		return engine.Event{At: at(sec), Kind: engine.TaskStarted, Phase: "M1", Task: id, Model: "sonnet", Slot: slot(n)}
	}
	for _, ev := range []engine.Event{
		{At: at(0), Kind: engine.RunStarted, Run: "r", Phase: "M1"},
		start(1, "M1-05", 0),
		start(2, "M1-03", 1),
		start(3, "M1-04", 2),
		{At: at(4), Kind: engine.SessionOpened, Task: "M1-04", Session: &backend.SessionRef{Backend: "fake", PaneID: "p4"}},
		{At: at(5), Kind: engine.VerifyStarted, Task: "M1-03"},
		{At: at(6), Kind: engine.Asked, Task: "M1-05", Question: engine.QuestionCommit, Detail: "commit M1-05?"},
		{At: at(7), Kind: engine.Asked, Task: "M1-04", Question: engine.QuestionSessionLost, Detail: "lost"},
	} {
		s.Publish(ev)
	}
	got := snap(t, s)
	if !got.Phases[0].Parallel {
		t.Error("the phase isn't marked parallel")
	}
	var ids []string
	for _, c := range got.Active {
		ids = append(ids, c.Task)
	}
	if !reflect.DeepEqual(ids, []string{"M1-03", "M1-04", "M1-05"}) {
		t.Fatalf("active = %v, want plan order", ids)
	}
	if a := got.Active; *a[0].Slot != 1 || *a[1].Slot != 2 || *a[2].Slot != 0 {
		t.Errorf("slots = %v %v %v", *a[0].Slot, *a[1].Slot, *a[2].Slot)
	}
	if a := got.Active; a[0].State != StateVerifying || a[1].State != StateQuestion || a[1].Session == nil || a[2].State != StateQuestion {
		t.Errorf("each card's own state: %+v", a)
	}
	if got.Current == nil || got.Current.Task != "M1-03" {
		t.Errorf("current = %+v, want the first active task", got.Current)
	}
	if len(got.Questions) != 2 || got.Questions[0].Task != "M1-04" || got.Questions[1].Task != "M1-05" {
		t.Errorf("questions = %+v, want M1-04's then M1-05's", got.Questions)
	}
	if got.Question == nil || got.Question.Task != "M1-04" {
		t.Errorf("question = %+v, want the first in plan order", got.Question)
	}

	// A task's end drops its card and question only.
	s.Publish(engine.Event{At: at(8), Kind: engine.TaskDone, Task: "M1-04"})
	got = snap(t, s)
	if len(got.Active) != 2 || got.Active[0].Task != "M1-03" || got.Active[1].Task != "M1-05" {
		t.Errorf("after M1-04 ended: %+v", got.Active)
	}
	if len(got.Questions) != 1 || got.Question.Task != "M1-05" {
		t.Errorf("after M1-04 ended: questions %+v", got.Questions)
	}
	s.Publish(engine.Event{At: at(9), Kind: engine.Committed, Task: "M1-05"})
	if got = snap(t, s); got.Question != nil || got.Questions != nil {
		t.Errorf("the commit didn't settle M1-05's question: %+v", got.Questions)
	}
}

func TestSlotOnTheWire(t *testing.T) {
	ev := fromEngine(engine.Event{Kind: engine.TaskStarted, Task: "M1-03", Slot: slot(3)}, "r")
	if ev.Slot == nil || *ev.Slot != 3 {
		t.Fatalf("wire slot = %v", ev.Slot)
	}
	back, ok := ev.Engine()
	if !ok || back.Slot == nil || *back.Slot != 3 {
		t.Errorf("slot back = %v", back.Slot)
	}
	ev.Slot = slot(99) // not a slot: dropped, never drawn
	if back, _ := ev.Engine(); back.Slot != nil {
		t.Errorf("slot 99 kept: %v", *back.Slot)
	}
}

func TestTailOfATask(t *testing.T) {
	var asked []string
	s := New(Options{Version: "1.5.0", Tail: func(_ context.Context, task string, _ int) ([]string, error) {
		asked = append(asked, task)
		return []string{"lines of " + task}, nil
	}})
	c, _, _ := attachTo(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	lines, err := c.TailTask(ctx, "M1-04", 10)
	if err != nil || !reflect.DeepEqual(lines, []string{"lines of M1-04"}) {
		t.Fatalf("TailTask = %q, %v", lines, err)
	}
	if _, err := c.Tail(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asked, []string{"M1-04", ""}) {
		t.Errorf("serve asked for %q, want M1-04 then the current task", asked)
	}
	p := attach(t, s)
	p.hello()
	p.send(`{"v":1,"type":"tail","id":"t","n":2,"task":"no such id"}`)
	if m := p.next(); m["type"] != TypeError || m["id"] != "t" {
		t.Errorf("a tail for a malformed task = %v", m)
	}
}

// A resolver session's card (SPEC §6.11): working, with why and its words,
// kept across the waits it clears, and the task's own again after it.
func TestSnapshotResolverCard(t *testing.T) {
	s := parallelServer(t)
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	for _, ev := range []engine.Event{
		{At: at(0), Kind: engine.RunStarted, Run: "r", Phase: "M1"},
		{At: at(1), Kind: engine.TaskStarted, Phase: "M1", Task: "M1-03", Model: "sonnet", Slot: slot(0)},
		{At: at(2), Kind: engine.IntegrationFailed, Task: "M1-03", Detail: "didn't integrate: the rebase onto the run branch conflicts in a.go"},
		{At: at(3), Kind: engine.ResolverOpened, Task: "M1-03", Reason: "conflict", Detail: "resolving a conflict in 1 file"},
		{At: at(4), Kind: engine.NeedsYou, Task: "M1-03", Reason: "idle", Detail: "idle"},
		{At: at(5), Kind: engine.NeedsYouClear, Task: "M1-03"},
	} {
		s.Publish(ev)
	}
	c := snap(t, s).Active[0]
	if c.State != StateWorking || c.Resolver != "conflict" || c.Detail != "resolving a conflict in 1 file" {
		t.Errorf("card while the resolver works: %+v", c)
	}
	s.Publish(engine.Event{At: at(6), Kind: engine.ResolverDone, Task: "M1-03", Detail: "resolver resolved it: kept both"})
	if c := snap(t, s).Active[0]; c.Resolver != "" {
		t.Errorf("card after the resolver: %+v", c)
	}
}
