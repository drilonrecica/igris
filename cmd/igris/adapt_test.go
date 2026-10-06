package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

// A synthetic plan in another format: igris finds no task table in it.
const foreignPlan = `# Widgets

| Key | What | State | LLM |
|---|---|---|---|
| W-1 | Parse widgets | todo | sonnet |
`

const adaptedPlan = `## W — Widgets

| ID | Task | Status | Model |
|---|---|---|---|
| W-1 | Parse widgets | ready | sonnet |
`

// adaptProject sets up a project whose plan is planText, with a fake
// backend whose adapt session writes proposal and signals done.
func adaptProject(t *testing.T, planFile, planText, proposal string) (root string, be *fake.Backend) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, planFile), []byte(planText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "igris.toml"), []byte("[adapt]\nmodel = \"opus\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	clock := engine.NewFakeClock(time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC))
	be = fake.New()
	be.SetAutoSignal(func(context.Context, string) error {
		d, err := state.Open(root, state.Options{Now: clock.Now})
		if err != nil {
			return err
		}
		if err := os.WriteFile(adapt.ProposalPath(d, filepath.Join(root, planFile)), []byte(proposal), 0o600); err != nil {
			return err
		}
		return d.WriteSignal(state.Signal{ID: adapt.ID, Action: state.ActionDone, Note: "converted"})
	})
	savedBackend, savedClock, savedGetenv := ariseBackend, adaptClock, ariseGetenv
	t.Cleanup(func() { ariseBackend, adaptClock, ariseGetenv = savedBackend, savedClock, savedGetenv })
	ariseBackend = func(*config.Config) (backend.Backend, error) { return be, nil }
	adaptClock = clock
	ariseGetenv = func(string) string { return "" }
	return root, be
}

func TestAdaptCommand(t *testing.T) {
	root, be := adaptProject(t, "tasks.md", foreignPlan, adaptedPlan)

	var out, errb bytes.Buffer
	if got := run([]string{"adapt"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s\nstdout: %s", got, errb.String(), out.String())
	}
	for _, want := range []string{"adapt session started (opus", "adapt session done: converted", "tasks.proposed.md", "✓ the proposal passes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out.String())
		}
	}
	if opened := be.Opened(); len(opened) != 1 || opened[0].Label != "ADAPT · opus" {
		t.Errorf("opened = %+v, want the adapt.model from igris.toml", opened)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "tasks.md")); string(data) != foreignPlan { //nolint:gosec // test temp dir
		t.Error("adapt changed the plan before the owner's review")
	}
}

func TestAdaptCommandPlanAndModelFlags(t *testing.T) {
	_, be := adaptProject(t, "other.md", foreignPlan, adaptedPlan)

	var out, errb bytes.Buffer
	if got := run([]string{"adapt", "--plan", "other.md", "--model", "sonnet"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if !strings.Contains(out.String(), "other.proposed.md") {
		t.Errorf("stdout = %s, want the proposal of other.md", out.String())
	}
	if opened := be.Opened(); len(opened) != 1 || opened[0].Label != "ADAPT · sonnet" {
		t.Errorf("opened = %+v, want --model to win over adapt.model", opened)
	}
	if p := be.Prompts(adapt.ID); len(p) != 1 || !strings.Contains(p[0], "`other.md`") {
		t.Errorf("prompt does not name other.md")
	}
}

func TestAdaptCommandValidPlan(t *testing.T) {
	_, be := adaptProject(t, "tasks.md", adaptedPlan, "")

	var out, errb bytes.Buffer
	if got := run([]string{"adapt"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if !strings.Contains(out.String(), "nothing to adapt") || len(be.Opened()) != 0 {
		t.Errorf("stdout = %q, opened = %d", out.String(), len(be.Opened()))
	}
}

func TestAdaptCommandMissingPlan(t *testing.T) {
	adaptProject(t, "tasks.md", foreignPlan, "")

	var out, errb bytes.Buffer
	if got := run([]string{"adapt", "--plan", "nope.md"}, &out, &errb); got != exitFail {
		t.Fatalf("exit = %d, want %d", got, exitFail)
	}
	if !strings.Contains(errb.String(), "read plan") {
		t.Errorf("stderr = %q", errb.String())
	}
}
