package state

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
)

// batch_base and commit_override round-trip through state.json (SPEC
// §6.5, since 1.8); a value igris doesn't write is damage.
func TestRunCommitFields(t *testing.T) {
	base := strings.Repeat("ab", 20)
	for _, bad := range []struct{ from, to string }{
		{`"` + base + `"`, `"HEAD~3"`},
		{`"commit_override": "auto"`, `"commit_override": "never"`},
	} {
		d := testDir(t)
		if err := d.SaveRun(&Run{Phases: []string{"A"}, ConfigHash: "h", BatchBase: base, CommitOverride: CommitAll}); err != nil {
			t.Fatal(err)
		}
		got, err := d.LoadRun()
		if err != nil || got.BatchBase != base || got.CommitOverride != "auto" {
			t.Fatalf("LoadRun = %+v, %v", got, err)
		}
		data, err := os.ReadFile(d.runPath())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), bad.from) {
			t.Fatalf("state.json lacks %s: %s", bad.from, data)
		}
		if err := os.WriteFile(d.runPath(), []byte(strings.Replace(string(data), bad.from, bad.to, 1)), 0o600); err != nil { //nolint:gosec // the test's own temp dir
			t.Fatal(err)
		}
		if _, err := d.LoadRun(); !errors.Is(err, ErrRunDamaged) {
			t.Errorf("%s: %v", bad.to, err)
		}
	}
}

// A cause is kept only on a blocked record, a tool only with a
// permission, each of the shape igris writes (SPEC §6.3).
func TestAgentStateCause(t *testing.T) {
	tests := []struct {
		in          string
		cause, tool string
	}{
		{`{"state":"blocked","event":"PermissionRequest","cause":"permission","tool":"Bash"}`, CausePermission, "Bash"},
		{`{"state":"blocked","event":"PreToolUse","cause":"question","tool":"Bash"}`, CauseQuestion, ""},
		{`{"state":"blocked","event":"PermissionRequest","cause":"permission","tool":"rm -rf /"}`, CausePermission, ""},
		{`{"state":"blocked","event":"x","cause":"\u001b[2J"}`, "", ""},
		{`{"state":"working","event":"PostToolUse","cause":"question"}`, "", ""},
	}
	for _, tt := range tests {
		s, err := ParseAgentState([]byte(tt.in))
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if s.Cause != tt.cause || s.Tool != tt.tool {
			t.Errorf("%s: cause %q tool %q, want %q %q", tt.in, s.Cause, s.Tool, tt.cause, tt.tool)
		}
	}
	if s, _ := ParseAgentState([]byte(`{"state":"blocked","event":"x"}`)); s.State != backend.Blocked || s.Cause != "" {
		t.Errorf("an igris before 1.8: %+v", s)
	}
}
