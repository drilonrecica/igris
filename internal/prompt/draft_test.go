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
		"Interview the owner",
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
	for _, want := range []string{"Ask for the goal first", "leave the Verify column out"} {
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
