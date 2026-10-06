package backend

import "testing"

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
