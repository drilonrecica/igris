package project

import (
	"reflect"
	"testing"

	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// serveArgs carries every run flag of the request to igris serve, --parallel
// included (SPEC §14); an unset one is left out.
func TestServeArgs(t *testing.T) {
	got := serveArgs(report.RunRequest{Phase: "A", Through: "B", Mode: "auto", Selection: state.Selection{Only: []string{"A-1", "A-2"}}, Worktree: true, Parallel: 3}, report.Confirmations{ForceUnlock: true})
	want := []string{"--through", "B", "--mode", "auto", "--only", "A-1,A-2", "--worktree", "--parallel", "3", "--force-unlock", "--", "A"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serveArgs = %q, want %q", got, want)
	}
	got = serveArgs(report.RunRequest{}, report.Confirmations{})
	if !reflect.DeepEqual(got, []string{"--"}) {
		t.Errorf("serveArgs of an empty request = %q", got)
	}
}
