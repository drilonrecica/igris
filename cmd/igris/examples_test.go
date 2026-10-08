package main

import (
	"path/filepath"
	"testing"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
)

// The shipped examples must stay valid, or the README's first steps break.
func TestExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "examples")

	cfg, err := config.Load(filepath.Join(dir, "igris.toml"))
	if err != nil {
		t.Fatalf("examples/igris.toml: %v", err)
	}

	p, err := plan.Load(filepath.Join(dir, cfg.Plan), plan.Options{Columns: cfg.Columns})
	if err != nil {
		t.Fatalf("load example plan: %v", err)
	}
	if err := p.Check(cfg.Rules("")); err != nil {
		t.Fatalf("examples/tasks.md: %v", err)
	}
	if got := p.Readiness(); len(got) != 0 {
		t.Errorf("examples/tasks.md has Status cells out of sync with Deps: %v", got)
	}
	for _, id := range []string{"P1", "P2"} {
		if p.Phase(id) == nil {
			t.Errorf("examples/tasks.md has no phase %s", id)
		}
	}
}

// examples/advanced shows the optional Verify, Timeout and Context
// columns with the verify profiles they need; it must validate as
// `igris check` run in that directory sees it.
func TestAdvancedExample(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "advanced")

	cfg, err := config.Load(filepath.Join(dir, "igris.toml"))
	if err != nil {
		t.Fatalf("examples/advanced/igris.toml: %v", err)
	}

	p, err := plan.Load(filepath.Join(dir, cfg.Plan), plan.Options{Columns: cfg.Columns})
	if err != nil {
		t.Fatalf("load advanced example plan: %v", err)
	}
	if err := p.Check(cfg.Rules(dir)); err != nil {
		t.Fatalf("examples/advanced/tasks.md: %v", err)
	}
	if got := p.Readiness(); len(got) != 0 {
		t.Errorf("examples/advanced/tasks.md has Status cells out of sync with Deps: %v", got)
	}
	var verify, timeout, context bool
	for _, task := range p.Tasks {
		verify = verify || task.Verify != ""
		timeout = timeout || task.Timeout > 0
		context = context || len(task.Context) > 0
	}
	if !verify || !timeout || !context {
		t.Errorf("examples/advanced/tasks.md should show every v0.4 column: verify %v, timeout %v, context %v", verify, timeout, context)
	}
	for id := range cfg.Phases {
		if p.Phase(id) == nil {
			t.Errorf("examples/advanced/igris.toml: [phases.%s] names no phase of the plan", id)
		}
	}
}

// The README demo records docs/demo; a plan that no longer loads breaks `make demo`.
func TestDemoPlan(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "demo")

	cfg, err := config.Load(filepath.Join(dir, "igris.toml"))
	if err != nil {
		t.Fatalf("docs/demo/igris.toml: %v", err)
	}

	p, err := plan.Load(filepath.Join(dir, cfg.Plan), plan.Options{Columns: cfg.Columns})
	if err != nil {
		t.Fatalf("load demo plan: %v", err)
	}
	if err := p.Check(cfg.Rules("")); err != nil {
		t.Fatalf("docs/demo/tasks.md: %v", err)
	}
	if got := p.Readiness(); len(got) != 0 {
		t.Errorf("docs/demo/tasks.md has Status cells out of sync with Deps: %v", got)
	}
	if p.Phase("D1") == nil {
		t.Error("docs/demo/tasks.md has no phase D1 (demo.tape runs it)")
	}
}
