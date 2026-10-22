package checks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/config"
)

// A phase marked parallel under commit = "never" runs one task at a time;
// check says so once per such phase with work left (SPEC §6.11).
func TestParallelWithoutCommitHint(t *testing.T) {
	const plan = "<!-- igris:format 3 -->\n## A — First\n\n<!-- igris:parallel -->\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| a | — | ready | sonnet |\n| b | — | ready | sonnet |\n" +
		"## B — Done\n\n<!-- igris:parallel -->\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| c | — | done | sonnet |\n"
	root := writePlan(t, plan)
	cfg := config.Default()
	cfg.Run.Commit = "never"
	got := Problems(Run(context.Background(), Options{IDs: []string{IDPlanHints}, Root: root, Config: cfg}))
	if len(got) != 1 {
		t.Fatalf("problems = %+v, want the one hint", got)
	}
	want := `phase A is marked parallel, but commit = "never"; it runs one task at a time`
	if got[0].ID != IDPlanHints || got[0].Level != Warn || got[0].File != filepath.Join(root, "tasks.md") || got[0].Line != 2 || !strings.Contains(got[0].Message, want) {
		t.Errorf("hint = %+v, want %q at tasks.md:2", got[0], want)
	}
	if got := Problems(Run(context.Background(), Options{IDs: []string{IDPlanHints}, Root: root, Config: config.Default()})); len(got) != 0 {
		t.Errorf("with commit = ask: %+v", got)
	}
}
