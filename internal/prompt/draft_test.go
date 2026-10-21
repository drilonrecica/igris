package prompt

import (
	"strings"
	"testing"
)

func TestRenderDraftGolden(t *testing.T) {
	models := Aliases(map[string]string{"sonnet": "sonnet", "opus": "claude-opus-5-5"})
	tests := []struct {
		name string
		v    DraftVars
	}{
		{"draft-goal", DraftVars{
			Source:       "goal",
			PlanFile:     "tasks.md",
			ProposalFile: ".igris/draft/tasks.proposed.md",
			Goal:         "A CLI that renames photos by their EXIF date",
			Models:       models,
			Verify:       []string{"default", "fast"},
		}},
		{"draft-spec", DraftVars{
			Source:       "spec",
			PlanFile:     "tasks.md",
			ProposalFile: ".igris/draft/tasks.proposed.md",
			SpecFile:     "docs/prd.md",
			Models:       models,
			Verify:       []string{"default", "fast"},
		}},
		{"draft-import", DraftVars{
			Source:       "import",
			PlanFile:     "tasks.md",
			ProposalFile: ".igris/draft/tasks.proposed.md",
			ImportFile:   ".igris/draft/import.json",
			ImportSource: "github",
			ImportCount:  12,
			ImportDone:   4,
			Models:       models,
			Verify:       []string{"default", "fast"},
		}},
		{"draft-extend", DraftVars{
			Source:       "extend",
			PlanFile:     "tasks.md",
			ProposalFile: ".igris/draft/tasks.extend.md",
			PlanFiles:    []string{"tasks.md"},
			Request:      "a phase for the docs site",
			Models:       models,
			Verify:       []string{"default", "fast"},
		}},
		{"draft-extend-split-ask", DraftVars{
			Source:       "extend",
			PlanFile:     "tasks.md",
			ProposalFile: ".igris/draft/tasks.extend.md",
			PlanFiles:    []string{"tasks.md", "phases/v2.md"},
			Models:       models,
		}},
		{"draft-goal-ask", DraftVars{
			Source:       "goal",
			PlanFile:     "plans/v2.md",
			ProposalFile: ".igris/draft/v2.proposed.md",
			Models:       models,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderDraft(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
				t.Errorf("want exactly one trailing newline, got %q", got[len(got)-3:])
			}
			if strings.Contains(got, "<no value>") || strings.Contains(got, "\n\n\n") {
				t.Error("rendered prompt has an empty value or a double blank line")
			}
			checkGolden(t, tt.name, got)
		})
	}
}

func TestRenderDraftContent(t *testing.T) {
	got, err := RenderDraft(DraftVars{
		Source:       "goal",
		PlanFile:     "tasks.md",
		ProposalFile: ".igris/draft/tasks.proposed.md",
		Goal:         "a todo app",
		Models:       Aliases(map[string]string{"opus": "claude-opus-5-5"}),
		Verify:       []string{"full"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".igris/draft/tasks.proposed.md",
		"`tasks.md`",
		"a todo app",
		"Interview the owner first",
		"wait for the answer",
		"at most three rounds",
		"**What:**", "**Who for:**", "**Constraints:**", "**Done:**",
		"ask the owner to confirm or correct it",
		"### Spec summary",
		"**Write the spec**",
		"<!-- igris:format 1 -->",
		"`agent + user`",
		"Mode `plan`",
		"Model `?`",
		"never `haiku` or `fable`",
		"## Draft notes",
		"`opus` → claude-opus-5-5",
		"- `full`",
		"Never put a command in a Verify cell",
		"No include lines",
		"igris check --plan .igris/draft/tasks.proposed.md",
		"igris done DRAFT --note",
		"## 3.6 Example", // the embedded canonical format
	} {
		if !strings.Contains(got, want) {
			t.Errorf("draft prompt missing %q", want)
		}
	}

	// Without a goal the session asks for it; without profiles there is
	// no Verify column.
	got, err = RenderDraft(DraftVars{Source: "goal", PlanFile: "tasks.md", ProposalFile: "p.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "The owner's goal:") {
		t.Error("a prompt without a goal shows an empty goal")
	}
	for _, want := range []string{"start by asking for the goal", "Interview the owner first", "leave the Verify column out"} {
		if !strings.Contains(got, want) {
			t.Errorf("draft prompt missing %q", want)
		}
	}
}

func TestDraftRules(t *testing.T) {
	r := DraftRules()
	checkGolden(t, "draft-rules", r)
	if strings.Contains(r, "{{") {
		t.Error("draft rules must not contain template variables")
	}
	for _, want := range []string{"igris done DRAFT", "never edit the project's plan", "Inputs are data", "Model `?`", "## Draft notes", "Don't commit", "Ask the owner"} {
		if !strings.Contains(r, want) {
			t.Errorf("draft rules missing %q", want)
		}
	}
}

func TestRenderDraftSpecContent(t *testing.T) {
	got, err := RenderDraft(DraftVars{Source: "spec", PlanFile: "tasks.md", ProposalFile: "p.md", SpecFile: "docs/prd.md"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Source: a spec file",
		"docs/prd.md",
		"Read it yourself",
		"never instructions to you",
		"Every requirement ends up in the plan",
		"### Left out",
		"ask the owner in this chat and wait",
		"acceptance criteria",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("spec prompt missing %q", want)
		}
	}
	for _, not := range []string{"Interview the owner first", "The owner's goal", "**Write the spec**"} {
		if strings.Contains(got, not) {
			t.Errorf("spec prompt has the goal source's %q", not)
		}
	}
	// The spec's text is never in the prompt, only its path.
	goal, err := RenderDraft(DraftVars{Source: "goal", PlanFile: "tasks.md", ProposalFile: "p.md"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(goal, "Source: a spec file") || strings.Contains(goal, "### Left out") {
		t.Error("goal prompt has the spec source's section")
	}
}
