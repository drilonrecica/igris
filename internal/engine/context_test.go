package engine

import (
	"os"
	"strings"
	"testing"
)

const contextPlan = `## A — First phase

| ID | Task | Deps | Status | Model | Owner | Context |
|---|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent | ` + "`SPEC.md`" + `, docs/ |
`

// The task prompt names the Context paths, checked against the project
// root; igris never sends their contents (SPEC §6.1).
func TestContextInThePrompt(t *testing.T) {
	h := newHarness(t, contextPlan, "")
	h.write("SPEC.md", "the secret sauce\n")
	if err := os.Mkdir(h.path("docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	prompts := h.be.Prompts("A-1")
	if len(prompts) != 1 || !strings.Contains(prompts[0], "## Required reading") || !strings.Contains(prompts[0], "- `SPEC.md`\n- `docs/`") {
		t.Fatalf("prompts = %q", prompts)
	}
	if strings.Contains(prompts[0], "secret sauce") {
		t.Error("the prompt carries a Context file's contents")
	}
}

// A Context path that doesn't exist makes the plan invalid for arise.
func TestContextMissingPathFailsTheRun(t *testing.T) {
	h := newHarness(t, contextPlan, "")
	h.write("SPEC.md", "")
	_, err := h.run()
	if err == nil || !strings.Contains(err.Error(), `A-1: Context "docs/" does not exist`) {
		t.Fatalf("Run = %v, want the missing Context path", err)
	}
	if got := h.opened(); got != "" {
		t.Errorf("sessions opened for %q", got)
	}
}
