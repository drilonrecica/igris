package project

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/side"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// Setup offers the [models] ranks, [side] model first, and [side] mode.
func TestSideSetup(t *testing.T) {
	ctx := context.Background()
	root := newProject(t, "[models]\nsonnet = \"sonnet\"\nopus = \"claude-opus-5-5\"\nhaiku = \"haiku\"\n\n[side]\nmodel = \"opus\"\nmode = \"plan\"\n")
	env, _ := testEnv(nil)
	got, err := NewServices(root, env).Side().Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// [models] adds to the default ranks.
	want := tui.SideSetup{Ranks: []tui.SideRank{
		{Rank: "opus", Model: "claude-opus-5-5"}, {Rank: "fable", Model: "fable"}, {Rank: "haiku", Model: "haiku"}, {Rank: "sonnet", Model: "sonnet"},
	}, Mode: "plan"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Setup = %+v, want %+v", got, want)
	}

	// Unset, sonnet first and default.
	root = newProject(t, "")
	got, err = NewServices(root, env).Side().Setup(ctx)
	if err != nil || got.Ranks[0].Rank != "sonnet" || got.Mode != "default" || got.Run != nil {
		t.Errorf("defaults: %+v, %v", got, err)
	}

	// A run this igris holds the lock for: its task, and whether it works
	// in a worktree of its own.
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := dir.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if err := dir.SaveRun(&state.Run{Phases: []string{"M0"}, Current: &state.Current{TaskID: "M0-01", Mode: "default", ClaudeSession: "7c1d2e3f-4a5b-4c6d-8e7f-000000000001"}}); err != nil {
		t.Fatal(err)
	}
	got, err = NewServices(root, env).Side().Setup(ctx)
	if err != nil || got.Run == nil || *got.Run != (tui.SideRun{Task: "M0-01"}) {
		t.Errorf("during a run: %+v, %v", got.Run, err)
	}

	// An invalid igris.toml opens nothing.
	root = newProject(t, "[side]\nmode = \"yolo\"\n")
	if _, err := NewServices(root, env).Side().Setup(ctx); err == nil || !strings.Contains(err.Error(), "igris.toml is invalid") {
		t.Errorf("invalid config: %v", err)
	}
	// Outside a project there are none.
	if _, err := NewServices(t.TempDir(), env).Side().Setup(ctx); err == nil || !strings.Contains(err.Error(), "igris init") {
		t.Errorf("no project: %v", err)
	}
}

// Open, List and Close through the fake backend, and Services.Side is one
// object, so home and its run view share the sessions held.
func TestSideOpenListClose(t *testing.T) {
	ctx := context.Background()
	root := newProject(t, "[models]\nsonnet = \"claude-sonnet-5-5\"\n\n[claude]\nextra_args = [\"--verbose\"]\n")
	env, be := testEnv(nil)
	svc := NewServices(root, env)
	if svc.Side() != svc.Side() {
		t.Error("Side isn't the same object each call")
	}
	ss := svc.Side()
	id, err := ss.Open(ctx, tui.SideRequest{Preset: state.SidePresetTask, Task: "M0-01", Rank: "sonnet", Mode: "accept", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	specs := be.Opened()
	if len(specs) != 1 || specs[0].Side != id || specs[0].Label != "side · M0-01 · sonnet" || specs[0].Dir != root {
		t.Fatalf("opened %+v", specs)
	}
	if args := strings.Join(specs[0].Args, " "); !strings.Contains(args, "--model claude-sonnet-5-5") || !strings.Contains(args, "--verbose") {
		t.Errorf("args %q", args)
	}
	if !slices.Contains(specs[0].Env, side.Env+"="+id) {
		t.Errorf("env %q", specs[0].Env)
	}

	// A new igris (a restarted home) lists it again.
	list, err := NewServices(root, env).Side().List(ctx)
	if err != nil || len(list.Rows) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	r := list.Rows[0]
	if r.ID != id || r.Subject != "M0-01" || r.Rank != "sonnet" || r.Mode != "accept" || r.Worktree || r.State == "" {
		t.Errorf("row %+v", r)
	}
	if err := ss.Focus(ctx, id); err != nil || len(be.Focused()) != 1 {
		t.Errorf("Focus: %v, focused %v", err, be.Focused())
	}
	line, err := ss.Close(ctx, id)
	if err != nil || line != "side "+id+" closed" {
		t.Errorf("Close = %q, %v", line, err)
	}
	if list, _ := ss.List(ctx); len(list.Rows) != 0 {
		t.Errorf("after Close: %+v", list)
	}
	if _, err := ss.Open(ctx, tui.SideRequest{Preset: state.SidePresetBlank, Rank: "mythos", Mode: "default"}); err == nil || !strings.Contains(err.Error(), "[models]") {
		t.Errorf("unknown rank: %v", err)
	}
}

// A session found gone is a feed line, given by the igris that claims it.
func TestSideListEnded(t *testing.T) {
	ctx := context.Background()
	root := newProject(t, "")
	env, be := testEnv(nil)
	ss := NewServices(root, env).Side()
	id, err := ss.Open(ctx, tui.SideRequest{Preset: state.SidePresetIgris, Rank: "sonnet", Mode: "default"})
	if err != nil {
		t.Fatal(err)
	}
	recs, _, err := state.ReadSideRecords(root)
	if err != nil || len(recs) != 1 {
		t.Fatalf("records %+v, %v", recs, err)
	}
	be.Kill(recs[0].Ref)
	list, err := ss.List(ctx)
	if err != nil || len(list.Rows) != 0 || !slices.Contains(list.Lines, "side "+id+" ended") {
		t.Errorf("List = %+v, %v", list, err)
	}
}

// The presets' prompts come from the plan and the project's state.
func TestSidePrefill(t *testing.T) {
	ctx := context.Background()
	root := newProject(t, "")
	env, _ := testEnv(nil)
	env.Version = "1.2.0"
	ss := NewServices(root, env).Side()
	got, err := ss.Prefill(ctx, tui.SidePrefill{Preset: state.SidePresetTask, Task: "M0-02", Running: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Task: M0-02 — Second", "Phase: M0 — Test", "Plan: " + filepath.Join(root, "tasks.md"), "Running now: yes", "Question: "} {
		if !strings.Contains(got, want) {
			t.Errorf("task prompt lacks %q:\n%s", want, got)
		}
	}
	if _, err := ss.Prefill(ctx, tui.SidePrefill{Preset: state.SidePresetTask, Task: "M9-01"}); err == nil {
		t.Error("a task not in the plan")
	}
	got, err = ss.Prefill(ctx, tui.SidePrefill{Preset: state.SidePresetIgris, Run: &side.RunInfo{Phase: "M0", Task: "M0-01", Agent: "working"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"igris version: 1.2.0", "Backend: fake", "Run: phase M0, current task M0-01, agent state working", "Doctor:", "don't change the plan"} {
		if !strings.Contains(got, want) {
			t.Errorf("igris prompt lacks %q:\n%s", want, got)
		}
	}
}
