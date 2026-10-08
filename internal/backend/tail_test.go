package backend

import (
	"slices"
	"strings"
	"testing"
)

func TestTailLines(t *testing.T) {
	rule := strings.Repeat("─", 40)
	box := []string{rule, "❯ ", rule, "  sonnet | /work/demo | main", "  ⏸ manual mode on"}
	for _, tc := range []struct {
		name  string
		lines []string
		n     int
		want  []string
	}{
		{"plain", []string{"one", "", "two", "three", "", ""}, 2, []string{"two", "three"}},
		{"input box left out", append([]string{"reply", "", "✻ Baked for 3s", "", "", ""}, box...), 6, []string{"reply", "✻ Baked for 3s"}},
		{"crlf", []string{"a\r", "b\r", ""}, 6, []string{"a", "b"}},
		// Only the box on screen: nothing else to show, so show it.
		{"box only", append([]string{"", ""}, box...), 6, []string{rule, "❯ ", rule, "  sonnet | /work/demo | main", "  ⏸ manual mode on"}},
		// A rule in the output above the box doesn't move the cut.
		{"rule in the output", append([]string{"above", rule, "below"}, box...), 6, []string{"above", rule, "below"}},
		{"one rule only", []string{"x", rule, "y"}, 6, []string{"x", rule, "y"}},
		{"short dashes are no rule", []string{"x", "─────", "y", "─────", "z"}, 6, []string{"x", "─────", "y", "─────", "z"}},
		{"none asked", []string{"x"}, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TailLines(strings.Join(tc.lines, "\n"), tc.n); !slices.Equal(got, tc.want) {
				t.Errorf("TailLines = %q, want %q", got, tc.want)
			}
		})
	}
}
