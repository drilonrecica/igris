package backend

import (
	"slices"
	"testing"
)

func TestSettled(t *testing.T) {
	for st, want := range map[AgentState]bool{
		Idle: true, Done: true, Blocked: true,
		Working: false, Unknown: false, Exited: false,
	} {
		if got := st.Settled(); got != want {
			t.Errorf("%s.Settled() = %v, want %v", st, got, want)
		}
	}
}

func TestLastLines(t *testing.T) {
	for _, tc := range []struct {
		text string
		n    int
		want []string
	}{
		{"a\nb\nc\n", 2, []string{"b", "c"}},
		{"a\n\nb\n  \n\n", 5, []string{"a", "", "b"}},
		{"a\r\nb\r\n", 5, []string{"a", "b"}},
		{"", 3, nil},
		{"\n \n", 3, nil},
		{"a\nb", 0, nil},
	} {
		if got := LastLines(tc.text, tc.n); !slices.Equal(got, tc.want) {
			t.Errorf("LastLines(%q, %d) = %q, want %q", tc.text, tc.n, got, tc.want)
		}
	}
}
