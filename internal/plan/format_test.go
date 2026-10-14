package plan

import (
	"strings"
	"testing"
)

// The format marker (SPEC §3): optional, before the first phase heading,
// format 1 without one; a format this igris doesn't read is the plan's
// only issue.
func TestFormatMarker(t *testing.T) {
	const table = "## M0 — One\n\n| ID | Status | Model |\n|---|---|---|\n| a | ready | sonnet |\n"
	tests := []struct {
		name, plan string
		format     int
		issue      string // the one issue wanted; "" for a valid plan
	}{
		{"none", table, 1, ""},
		{"format 1", "# Plan\n\n<!-- igris:format 1 -->\n\n" + table, 1, ""},
		{"no spaces", "<!--igris:format 1-->\n" + table, 1, ""},
		{"indented, extra spaces", "  <!--   igris:format   1   -->  \n" + table, 1, ""},
		{"in a fence", "```\n<!-- igris:format 2 -->\n```\n" + table, 1, ""},
		{"after a heading", table + "\n<!-- igris:format 1 -->\n", 1, "7: the format marker must come before the first ## heading"},
		{"twice", "<!-- igris:format 1 -->\n<!-- igris:format 1 -->\n" + table, 1, "2: a second format marker (the first is on line 1)"},
		{"not a number", "<!-- igris:format x -->\n" + table, 1, "1: the format marker needs a whole number"},
		{"empty", "<!-- igris:format -->\n" + table, 1, "1: the format marker needs a whole number"},
		{"zero", "<!-- igris:format 0 -->\n" + table, 1, "needs a whole number"},
		{"fraction", "<!-- igris:format 1.5 -->\n" + table, 1, "needs a whole number"},
		{"signed", "<!-- igris:format +1 -->\n" + table, 1, "needs a whole number"},
		{"another word", "<!-- igris:formatted 1 -->\n" + table, 1, ""},
		// Format 2's tables may follow other rules: their problems would
		// only mislead, so the format is the one issue.
		{"newer", "<!-- igris:format 2 -->\n## M0\n\n| Key | Lane |\n|---|---|\n| a | b |\n", 2,
			"1: plan format 2 needs a newer igris (this igris reads format 1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Parse("tasks.md", []byte(tt.plan), Options{})
			if p.Format != tt.format {
				t.Errorf("format %d, want %d", p.Format, tt.format)
			}
			issues := p.Validate(testRules)
			switch {
			case tt.issue == "" && len(issues) > 0:
				t.Errorf("want a valid plan, got %v", issues)
			case tt.issue != "" && (len(issues) != 1 || !strings.Contains(issues[0].Error(), tt.issue)):
				t.Errorf("want one issue %q, got %v", tt.issue, issues)
			}
		})
	}
}
