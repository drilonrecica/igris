package hook

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

// The UUID in the recorded payloads (testdata/*.json).
const fixtureUUID = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"

var fixedNow = func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) }

func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, state.DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

// Each recorded Claude Code 2.1.293 payload maps to the state V03-P1 decided.
func TestRecordedEvents(t *testing.T) {
	tests := []struct {
		file string
		want backend.AgentState
	}{
		{"sessionstart_startup.json", backend.Idle},
		{"userpromptsubmit.json", backend.Working},
		{"pretooluse_bash.json", backend.Working},
		{"posttooluse.json", backend.Working},
		{"pretooluse_askuserquestion.json", backend.Blocked},
		{"permissionrequest.json", backend.Blocked},
		{"notification_permission_prompt.json", backend.Blocked},
		{"notification_idle_prompt.json", backend.Idle},
		{"stop.json", backend.Idle},
		{"sessionend.json", backend.Exited},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			root := project(t)
			if err := Run(context.Background(), root, f, fixedNow); err != nil {
				t.Fatalf("Run: %v", err)
			}
			got, ok, err := state.ReadAgentState(root, fixtureUUID)
			if err != nil || !ok {
				t.Fatalf("ReadAgentState = %v, %v", ok, err)
			}
			if got.State != tt.want || !got.At.Equal(fixedNow()) {
				t.Errorf("state = %+v, want %s at %s", got, tt.want, fixedNow())
			}
		})
	}
}

func TestMapIgnores(t *testing.T) {
	for _, p := range []Payload{
		{Event: "SessionStart", Source: "compact"},
		{Event: "SubagentStop"},
		{Event: "PreCompact"},
		{},
	} {
		if st, ok := Map(p); ok {
			t.Errorf("Map(%+v) = %s, want ignored", p, st)
		}
	}
	if st, _ := Map(Payload{Event: "PreToolUse", ToolName: "ExitPlanMode"}); st != backend.Blocked {
		t.Errorf("ExitPlanMode = %s, want blocked", st)
	}
	if st, _ := Map(Payload{Event: "SessionStart", Source: "resume"}); st != backend.Idle {
		t.Errorf("resume = %s, want idle", st)
	}
}

func TestRunRejects(t *testing.T) {
	tests := map[string]string{
		"not json":       "nope",
		"bad session id": `{"session_id":"--model","hook_event_name":"Stop"}`,
		"too large":      `{"session_id":"` + strings.Repeat("a", maxInput) + `"}`,
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			root := project(t)
			if err := Run(context.Background(), root, strings.NewReader(in), fixedNow); err == nil {
				t.Error("Run succeeded")
			}
			if entries, _ := os.ReadDir(state.AgentStateDir(root)); len(entries) != 0 {
				t.Errorf("wrote %d files", len(entries))
			}
		})
	}
}

// Outside a project igris manages (no .igris/), nothing is created.
func TestRunNeedsStateDir(t *testing.T) {
	root := t.TempDir()
	in := `{"session_id":"` + fixtureUUID + `","hook_event_name":"Stop"}`
	if err := Run(context.Background(), root, strings.NewReader(in), fixedNow); err == nil {
		t.Error("Run succeeded without .igris/")
	}
	if _, err := os.Stat(filepath.Join(root, state.DirName)); err == nil {
		t.Error(".igris/ was created")
	}
}

// A stdin that never ends doesn't hold the hook past its deadline.
func TestRunDeadline(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, project(t), r, fixedNow); err == nil {
		t.Error("Run returned nil on a blocked stdin")
	}
}

func TestSettings(t *testing.T) {
	data, err := Settings("/opt/my igris/igris", "/home/o'brien/proj")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type, Command string
				Timeout       int
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Hooks) != len(Events) {
		t.Fatalf("%d events, want %d", len(s.Hooks), len(Events))
	}
	for _, ev := range Events {
		m := s.Hooks[ev]
		if len(m) != 1 || len(m[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", ev, m)
		}
		h := m[0].Hooks[0]
		want := `'/opt/my igris/igris' hook --root '/home/o'\''brien/proj' ` + ev
		if h.Type != "command" || h.Command != want || h.Timeout != commandTimeout {
			t.Errorf("%s: %+v, want command %s", ev, h, want)
		}
	}
	if s.Hooks["PreToolUse"][0].Matcher != "*" || s.Hooks["Stop"][0].Matcher != "" {
		t.Error("matchers wrong")
	}
	// Only hooks: nothing else that could change permissions (SPEC §7.4).
	var top map[string]json.RawMessage
	_ = json.Unmarshal(data, &top)
	if len(top) != 1 {
		t.Errorf("%d top-level keys, want only hooks", len(top))
	}
}
