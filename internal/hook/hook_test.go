package hook

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
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
		// Recorded with Claude Code 2.1.299 (WAIT-01): a Stop while a
		// background shell runs, the Stop after it finished and resumed the
		// session, and a Stop with a scheduled wakeup.
		{"stop_shell_running.json", backend.Waiting},
		{"stop_after_shell.json", backend.Idle},
		{"stop_cron.json", backend.Waiting},
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

// What a Stop leaves behind decides between idle and waiting. The fields
// are read leniently: an unexpected shape is no pending work, never a lost
// Stop.
func TestStopPending(t *testing.T) {
	tests := []struct {
		name, bg, crons string
		want            backend.AgentState
	}{
		{"none", ``, ``, backend.Idle},
		{"empty", `[]`, `[]`, backend.Idle},
		{"running shell", `[{"type":"shell","status":"running"}]`, `[]`, backend.Waiting},
		{"status unknown to igris", `[{"type":"monitor","status":"pending"}]`, `[]`, backend.Waiting},
		{"no status", `[{"type":"subagent"}]`, `[]`, backend.Waiting},
		{"only finished", `[{"status":"completed"},{"status":"failed"},{"status":"killed"}]`, `[]`, backend.Idle},
		{"finished and running", `[{"status":"completed"},{"status":"running"}]`, ``, backend.Waiting},
		{"cron", `[]`, `[{"id":"7b51c6e5","recurring":false}]`, backend.Waiting},
		{"not an array", `{"status":"running"}`, `"x"`, backend.Idle},
		{"null", `null`, `null`, backend.Idle},
		{"entry of another shape", `[{"status":1}]`, ``, backend.Waiting},
		{"entry not an object", `["shell"]`, ``, backend.Waiting},
	}
	for _, tt := range tests {
		p := Payload{SessionID: fixtureUUID, Event: "Stop", BackgroundTasks: json.RawMessage(tt.bg), SessionCrons: json.RawMessage(tt.crons)}
		if got, ok := Map(p); !ok || got != tt.want {
			t.Errorf("%s: Map = %s, %v; want %s", tt.name, got, ok, tt.want)
		}
	}
}

// Claude Code sends idle_prompt a minute after the Stop even while the
// background shell runs (recorded with 2.1.299): the session still waits.
// The next Stop or prompt replaces waiting as usual.
func TestIdlePromptKeepsWaiting(t *testing.T) {
	root := project(t)
	steps := []struct {
		file string
		want backend.AgentState
	}{
		{"stop_shell_running.json", backend.Waiting},
		{"notification_idle_prompt_waiting.json", backend.Waiting},
		{"notification_permission_prompt.json", backend.Blocked},
		{"stop_shell_running.json", backend.Waiting},
		{"userpromptsubmit.json", backend.Working},
		{"stop_after_shell.json", backend.Idle},
		{"notification_idle_prompt_waiting.json", backend.Idle},
	}
	for i, s := range steps {
		f, err := os.Open(filepath.Join("testdata", s.file))
		if err != nil {
			t.Fatal(err)
		}
		err = Run(context.Background(), root, f, fixedNow)
		_ = f.Close()
		if err != nil {
			t.Fatalf("step %d (%s): Run: %v", i, s.file, err)
		}
		if got, _, _ := state.ReadAgentState(root, fixtureUUID); got.State != s.want {
			t.Errorf("step %d (%s): state %s, want %s", i, s.file, got.State, s.want)
		}
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
	data, err := Settings("/opt/my igris/igris", "/home/o'brien/proj", nil)
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

// In a run's worktree the session also gets the `igris done` allow rules,
// and nothing more (SPEC §6.8, §7.4).
func TestSettingsAllow(t *testing.T) {
	rules := []string{"Bash(igris done:*)", "Bash(igris done *)"}
	data, err := Settings("/bin/igris", "/p", rules)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Permissions map[string][]string `json:"permissions"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Permissions) != 1 || !slices.Equal(s.Permissions["allow"], rules) {
		t.Errorf("permissions = %v, want only allow %v", s.Permissions, rules)
	}
}

// What a session's hooks report is reduced to a state: the commands and
// descriptions of its background tasks never reach .igris/ (SPEC §6.3).
func TestRunStoresOnlyTheState(t *testing.T) {
	root := project(t)
	in := `{"session_id":"` + fixtureUUID + `","hook_event_name":"Stop","background_tasks":[{"id":"b1","type":"shell","status":"running","description":"SECRET-DESC","command":"curl SECRET-CMD"}],"session_crons":[{"prompt":"SECRET-CRON"}]}`
	if err := Run(context.Background(), root, strings.NewReader(in), fixedNow); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, state.DirName, "agent-state", "*"))
	if err != nil || len(files) != 1 {
		t.Fatalf("agent-state files %v, %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SECRET") || !strings.Contains(string(data), `"waiting"`) {
		t.Errorf("stored agent state = %s", data)
	}
}
