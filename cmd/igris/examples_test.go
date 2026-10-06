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
	if err := p.Check(cfg.Models); err != nil {
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
