package prompt

import (
	"os"
	"strings"
	"testing"
)

func TestRenderAdaptGolden(t *testing.T) {
	models := Aliases(map[string]string{"sonnet": "sonnet", "opus": "claude-opus-5-5", "haiku": "haiku"})
	tests := []struct {
		name string
		v    AdaptVars
	}{
		{"adapt-issues", AdaptVars{
			PlanFile:     "docs/plan.md",
			ProposalFile: ".igris/adapt/plan.proposed.md",
			Issues: []string{
				"docs/plan.md: no task tables found; add a table with ID, Status and Model columns under a ## heading",
				"docs/plan.md:12: T-3: unknown status \"wip\"; use ready, blocked, in progress, done or skipped",
			},
			Models: models,
		}},
		{"adapt-no-issues", AdaptVars{
			PlanFile:     "tasks.md",
			ProposalFile: ".igris/adapt/tasks.proposed.md",
			Models:       models,
			DoneCommand:  "igris done ADAPT",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RenderAdapt(tt.v)
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

func TestRenderAdaptContent(t *testing.T) {
	got, err := RenderAdapt(AdaptVars{
		PlanFile:     "plan.md",
		ProposalFile: ".igris/adapt/plan.proposed.md",
		Issues:       []string{"plan.md:3: boom"},
		Models:       Aliases(map[string]string{"opus": "claude-opus-5-5"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		".igris/adapt/plan.proposed.md",
		"- plan.md:3: boom",
		"`opus` → claude-opus-5-5",
		"Model `?`",
		"## Adapt notes",
		"igris done ADAPT --note",
		"igris check --plan .igris/adapt/plan.proposed.md",
		"## 3.6 Example", // the embedded canonical format
	} {
		if !strings.Contains(got, want) {
			t.Errorf("adapt prompt missing %q", want)
		}
	}
}

func TestAliasesSorted(t *testing.T) {
	got := Aliases(map[string]string{"sonnet": "s", "fable": "f", "opus": "o"})
	if len(got) != 3 || got[0].Rank != "fable" || got[1].Rank != "opus" || got[2].Rank != "sonnet" {
		t.Errorf("Aliases = %+v", got)
	}
}

func TestAdaptRules(t *testing.T) {
	r := AdaptRules()
	if strings.Contains(r, "{{") {
		t.Error("adapt rules must not contain template variables")
	}
	for _, want := range []string{"igris done ADAPT", "Never edit the original plan", "Model `?`", "## Adapt notes"} {
		if !strings.Contains(r, want) {
			t.Errorf("adapt rules missing %q", want)
		}
	}
}

// TestFormatMatchesSpec keeps the embedded canonical format in step with
// SPEC §3; go:embed can't reach SPEC.md from this package.
func TestFormatMatchesSpec(t *testing.T) {
	data, err := os.ReadFile("../../SPEC.md")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(data)
	start := strings.Index(spec, "\n## 3. ")
	end := strings.Index(spec, "\n## 4. ")
	if start < 0 || end < start {
		t.Fatal("SPEC.md: §3 or §4 heading not found")
	}
	want := strings.TrimSpace(spec[start:end])
	want = strings.TrimSpace(strings.TrimSuffix(want, "---")) + "\n"
	if format != want {
		t.Errorf("internal/prompt/format.md differs from SPEC.md §3; copy the section again")
	}
}
