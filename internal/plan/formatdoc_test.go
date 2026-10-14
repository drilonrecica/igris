package plan

import (
	"os"
	"strings"
	"testing"
)

// docs/plan-format.md describes format 1 for people and tools writing
// plans (SPEC §19). Every ```markdown block in it with a phase heading is
// a plan igris must read as valid, with Status cells in step with their
// dependencies; examples of mistakes are written as ```text.
func TestPlanFormatDocExamples(t *testing.T) {
	data, err := os.ReadFile("../../docs/plan-format.md")
	if err != nil {
		t.Fatal(err)
	}
	// The default ranks (config.Default; config imports plan).
	rules := Rules{Models: map[string]string{"sonnet": "sonnet", "opus": "opus", "fable": "fable", "haiku": "haiku"}}
	var block []string
	in, n := false, 0
	for i, line := range strings.Split(string(data), "\n") {
		switch {
		case !in && line == "```markdown":
			in, block = true, nil
		case in && line == "```":
			in = false
			src := strings.Join(block, "\n") + "\n"
			if !strings.Contains(src, "\n## ") {
				continue
			}
			n++
			p := Parse("plan-format.md", []byte(src), Options{})
			if issues := p.Validate(rules); len(issues) > 0 {
				t.Errorf("example ending on line %d is not valid: %v", i+1, issues)
			}
			if drift := p.Readiness(); len(drift) > 0 {
				t.Errorf("example ending on line %d has Status out of step with Deps: %v", i+1, drift)
			}
		case in:
			block = append(block, line)
		}
	}
	if n == 0 {
		t.Fatal("docs/plan-format.md has no example plan")
	}
}
