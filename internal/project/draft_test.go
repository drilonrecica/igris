package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/draft"
	"github.com/drilonrecica/igris/internal/tui"
)

// Home's Draft refuses what the command line refuses, before any session
// opens (SPEC §9.1): a plan in the way of a new one, an invalid config, a
// plan to extend that doesn't pass check.
func TestServicesDraftRefusals(t *testing.T) {
	ctx := context.Background()
	env, _ := testEnv(nil)
	req := func(src draft.Source) tui.DraftRequest { return tui.DraftRequest{Source: src, Model: "sonnet"} }

	root := newProject(t, "")
	if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte("# Not a plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewServices(root, env).Draft(ctx, req(draft.Goal), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "igris draft extend") {
		t.Errorf("a plan in the way: %v", err)
	}
	if _, err = NewServices(root, env).Draft(ctx, req(draft.Extend), nil, nil); err == nil {
		t.Error("extend of an invalid plan was not refused")
	}

	root = newProject(t, "default_mode = \"fast\"\n")
	if _, err = NewServices(root, env).Draft(ctx, req(draft.Goal), nil, nil); err == nil {
		t.Error("a new plan was drafted with an invalid igris.toml")
	}
}
