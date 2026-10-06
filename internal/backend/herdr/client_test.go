package herdr

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
)

// fixture reads a recorded herdr response from testdata/ (P0-03).
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// ok is a successful herdr call printing fixture name.
func ok(t *testing.T, name string) runner.Result {
	t.Helper()
	return runner.Result{Stdout: fixture(t, name)}
}

// fail is a failed herdr call printing error fixture name on stderr.
func fail(t *testing.T, name string, exit int) runner.Result {
	t.Helper()
	return runner.Result{Stderr: fixture(t, name), ExitCode: exit}
}

// argv returns the herdr arguments of every recorded call.
func argv(f *runner.Fake) [][]string {
	var out [][]string
	for _, c := range f.Calls() {
		out = append(out, c.Args)
	}
	return out
}

func TestClientCalls(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		res     runner.Result
		call    func(c *Client) (any, error)
		want    any
		args    []string
		timeout time.Duration
	}{
		{
			name: "status server",
			res:  ok(t, "status_server.txt"),
			call: func(c *Client) (any, error) { return c.ServerStatus(ctx) },
			want: ServerStatus{Status: "running", Version: "0.9.1"},
			args: []string{"status", "server"},
		},
		{
			name: "tab create",
			res:  ok(t, "tab_create.json"),
			call: func(c *Client) (any, error) {
				tab, pane, err := c.TabCreate(ctx, TabCreateOpts{Workspace: "w2B", Cwd: "/work/demo", Label: "T-01 · sonnet", Env: []string{"A=1", "B=2"}})
				return [2]any{tab, pane}, err
			},
			want: [2]any{
				Tab{TabID: "w2B:t3", Label: "T-01 · sonnet"},
				Pane{PaneID: "w2B:p3", TabID: "w2B:t3", Status: StatusUnknown},
			},
			args: []string{"tab", "create", "--workspace", "w2B", "--cwd", "/work/demo", "--label", "T-01 · sonnet", "--env", "A=1", "--env", "B=2", "--no-focus"},
		},
		{
			name: "tab create focused",
			res:  ok(t, "tab_create.json"),
			call: func(c *Client) (any, error) {
				tab, _, err := c.TabCreate(ctx, TabCreateOpts{Workspace: "w", Cwd: "/d", Label: "L", Focus: true})
				return tab.TabID, err
			},
			want: "w2B:t3",
			args: []string{"tab", "create", "--workspace", "w", "--cwd", "/d", "--label", "L", "--focus"},
		},
		{
			name: "agent start",
			res:  ok(t, "agent_start_ok.json"),
			call: func(c *Client) (any, error) {
				return c.AgentStart(ctx, "igris-t-01", "w2B:p3", 120*time.Second, []string{"--model", "haiku", "--append-system-prompt-file", "/x y/rules.md"})
			},
			want:    Agent{Name: "p0c", Kind: "claude", Status: StatusIdle, InteractiveReady: true, PaneID: "w2B:p3", TabID: "w2B:t3"},
			args:    []string{"agent", "start", "igris-t-01", "--kind", "claude", "--pane", "w2B:p3", "--timeout", "120000", "--", "--model", "haiku", "--append-system-prompt-file", "/x y/rules.md"},
			timeout: 130 * time.Second,
		},
		{
			name: "agent wait",
			res:  ok(t, "agent_wait_settled.json"),
			call: func(c *Client) (any, error) {
				a, err := c.AgentWait(ctx, "p0c", []string{StatusIdle, StatusDone}, 5*time.Second)
				return a.Status, err
			},
			want:    StatusDone,
			args:    []string{"agent", "wait", "p0c", "--until", "idle", "--until", "done", "--timeout", "5000"},
			timeout: 15 * time.Second,
		},
		{
			name: "agent wait any settled",
			res:  ok(t, "agent_wait_settled.json"),
			call: func(c *Client) (any, error) {
				a, err := c.AgentWait(ctx, "p0c", nil, time.Second)
				return a.Status, err
			},
			want:    StatusDone,
			args:    []string{"agent", "wait", "p0c", "--timeout", "1000"},
			timeout: 11 * time.Second,
		},
		{
			name: "agent prompt multi-line",
			res:  ok(t, "agent_prompt_nowait.json"),
			call: func(c *Client) (any, error) {
				a, err := c.AgentPrompt(ctx, "p0c", "--leading dashes\nline two\n\t`quoted` \"$HOME\"")
				return a.Status, err
			},
			want: StatusDone,
			args: []string{"agent", "prompt", "p0c", "--leading dashes\nline two\n\t`quoted` \"$HOME\""},
		},
		{
			name: "agent read",
			res:  ok(t, "agent_read_recent_unwrapped.txt"),
			call: func(c *Client) (any, error) {
				s, err := c.AgentRead(ctx, "p0c", SourceRecentUnwrapped, 60)
				return strings.Contains(s, "igris done"), err
			},
			want: true,
			args: []string{"agent", "read", "p0c", "--source", "recent-unwrapped", "--lines", "60"},
		},
		{
			name: "pane get",
			res:  ok(t, "pane_get_working.json"),
			call: func(c *Client) (any, error) { return c.PaneGet(ctx, "w2B:p3") },
			want: Pane{PaneID: "w2B:p3", TabID: "w2B:t3", Agent: "claude", Status: StatusWorking},
			args: []string{"pane", "get", "w2B:p3"},
		},
		{
			name: "tab focus",
			res:  ok(t, "tab_focus.json"),
			call: func(c *Client) (any, error) { return c.TabFocus(ctx, "w2B:t3") },
			want: Tab{TabID: "w2B:t3", Label: "T-01 · sonnet", Focused: true},
			args: []string{"tab", "focus", "w2B:t3"},
		},
		{
			name: "tab close",
			res:  ok(t, "tab_close.json"),
			call: func(c *Client) (any, error) { return nil, c.TabClose(ctx, "w2B:t3") },
			want: nil,
			args: []string{"tab", "close", "w2B:t3"},
		},
		{
			name: "notification show",
			res:  ok(t, "notification_show.json"),
			call: func(c *Client) (any, error) { return c.NotificationShow(ctx, "igris", "T-01 needs you", SoundRequest) },
			want: true,
			args: []string{"notification", "show", "igris", "--body", "T-01 needs you", "--sound", "request"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &runner.Fake{}
			f.On([]string{Program}, tt.res, nil)
			got, err := tt.call(NewClient(f))
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if got != tt.want {
				t.Errorf("result = %#v, want %#v", got, tt.want)
			}
			calls := f.Calls()
			if len(calls) != 1 {
				t.Fatalf("calls = %d, want 1", len(calls))
			}
			if !slices.Equal(calls[0].Args, tt.args) {
				t.Errorf("args =\n  %q\nwant\n  %q", calls[0].Args, tt.args)
			}
			want := tt.timeout
			if want == 0 {
				want = DefaultTimeout
			}
			if calls[0].Timeout != want {
				t.Errorf("timeout = %s, want %s", calls[0].Timeout, want)
			}
		})
	}
}

func TestIntegrationStatus(t *testing.T) {
	f := &runner.Fake{}
	f.On([]string{Program, "integration", "status"}, ok(t, "integration_status.txt"), nil)
	st, err := NewClient(f).IntegrationStatus(context.Background())
	if err != nil {
		t.Fatalf("IntegrationStatus: %v", err)
	}
	if st["claude"] != "not installed" {
		t.Errorf("claude = %q, want %q", st["claude"], "not installed")
	}
	if st["letta (experimental)"] != "not installed" {
		t.Errorf("letta = %q", st["letta (experimental)"])
	}
	if len(st) != 18 {
		t.Errorf("kinds = %d, want 18: %v", len(st), slices.Sorted(maps.Keys(st)))
	}
}

func TestClientErrors(t *testing.T) {
	tests := []struct {
		fixture string
		exit    int
		code    string
	}{
		{"agent_start_not_ready.json", 2, CodeAgentNotReady},
		{"error_agent_blocked.json", 1, CodeAgentBlocked},
		{"error_agent_not_found.json", 1, CodeAgentNotFound},
		{"error_pane_not_found.json", 1, CodePaneNotFound},
		{"error_tab_close_not_found.json", 1, CodeTabNotFound},
		{"error_tab_get_not_found.json", 1, CodeTabNotFound},
		{"error_wait_timeout.json", 1, CodeTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			f := &runner.Fake{}
			f.On([]string{Program}, fail(t, tt.fixture, tt.exit), nil)
			_, err := NewClient(f).PaneGet(context.Background(), "w2B:p3")
			var he *Error
			if !errors.As(err, &he) {
				t.Fatalf("err = %v, want *Error", err)
			}
			if he.Code != tt.code || he.Exit != tt.exit || he.Op != "pane get" || he.Message == "" {
				t.Errorf("error = %+v", he)
			}
			if !IsCode(err, tt.code) || IsCode(err, "other") {
				t.Errorf("IsCode wrong for %v", err)
			}
			if !strings.Contains(err.Error(), tt.code) {
				t.Errorf("message %q does not name the code", err)
			}
		})
	}
}

func TestClientFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("non-JSON stderr", func(t *testing.T) {
		f := &runner.Fake{}
		f.On([]string{Program}, runner.Result{Stderr: []byte("unknown option: -x\n"), ExitCode: 2}, nil)
		_, err := NewClient(f).PaneGet(ctx, "p")
		var ee *runner.ExitError
		if !errors.As(err, &ee) || ee.Code != 2 {
			t.Fatalf("err = %v, want ExitError with code 2", err)
		}
		if !strings.Contains(err.Error(), "unknown option") {
			t.Errorf("err %q lacks stderr", err)
		}
	})
	t.Run("runner timeout", func(t *testing.T) {
		f := &runner.Fake{}
		f.On([]string{Program}, runner.Result{ExitCode: -1}, runner.ErrTimeout)
		_, err := NewClient(f).AgentPrompt(ctx, "a", "long\nprompt")
		if !errors.Is(err, runner.ErrTimeout) || !strings.HasPrefix(err.Error(), "herdr agent prompt: ") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad JSON", func(t *testing.T) {
		f := &runner.Fake{}
		f.On([]string{Program}, runner.Result{Stdout: []byte("not json")}, nil)
		if _, err := NewClient(f).PaneGet(ctx, "p"); err == nil || !strings.Contains(err.Error(), "parse output") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no result", func(t *testing.T) {
		f := &runner.Fake{}
		f.On([]string{Program}, runner.Result{Stdout: []byte(`{"id":"x"}`)}, nil)
		if _, err := NewClient(f).PaneGet(ctx, "p"); err == nil || !strings.Contains(err.Error(), "no result") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("status without status line", func(t *testing.T) {
		f := &runner.Fake{}
		f.On([]string{Program}, runner.Result{Stdout: []byte("garbage\n")}, nil)
		if _, err := NewClient(f).ServerStatus(ctx); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestAgentStartValidation(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		args    []string
		want    string
	}{
		{"timeout too short", 3 * time.Second, nil, "timeout"},
		{"timeout too long", 301 * time.Second, nil, "timeout"},
		{"newline", time.Minute, []string{"--model", "a\nb"}, "argument 2"},
		{"tab", time.Minute, []string{"a\tb"}, "argument 1"},
		{"carriage return", time.Minute, []string{"a\rb"}, "argument 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &runner.Fake{}
			_, err := NewClient(f).AgentStart(context.Background(), "n", "p", tt.timeout, tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
			if len(f.Calls()) != 0 {
				t.Errorf("herdr was called: %q", argv(f))
			}
		})
	}
	t.Run("wait needs timeout", func(t *testing.T) {
		f := &runner.Fake{}
		if _, err := NewClient(f).AgentWait(context.Background(), "n", nil, 0); err == nil {
			t.Fatal("want error")
		}
	})
}
