package herdr

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
)

var spec = backend.SessionSpec{
	TaskID: "T-01",
	Dir:    "/work/demo",
	Label:  "T-01 · sonnet",
	Args:   []string{"--model", "sonnet", "--session-id", "0b0e1f6a-0000-4000-8000-000000000001"},
	Env:    []string{"IGRIS_TASK=T-01"},
}

// shellPane is a pane where no agent runs any more (Claude Code exited).
var shellPane = runner.Result{Stdout: []byte(`{"id":"cli:pane:get","result":{"pane":{"agent_status":"unknown","pane_id":"w2B:p3","tab_id":"w2B:t3"},"type":"pane_info"}}`)}

func cmd(words ...string) []string { return append([]string{Program}, words...) }

// open opens spec on f, failing the test on error.
func open(t *testing.T, f *runner.Fake) *Session {
	t.Helper()
	s, err := New(f, "w2B").OpenSession(context.Background(), spec)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	return s.(*Session)
}

func started(t *testing.T) *runner.Fake {
	t.Helper()
	f := &runner.Fake{}
	f.On(cmd("tab", "create"), ok(t, "tab_create.json"), nil)
	f.On(cmd("agent", "start"), ok(t, "agent_start_ok.json"), nil)
	return f
}

func startupBlocked(t *testing.T) *runner.Fake {
	t.Helper()
	f := &runner.Fake{}
	f.On(cmd("tab", "create"), ok(t, "tab_create.json"), nil)
	f.On(cmd("agent", "start"), fail(t, "agent_start_not_ready.json", 2), nil)
	return f
}

// prompts returns the text of every `agent prompt` call.
func prompts(f *runner.Fake) []string {
	var out []string
	for _, a := range argv(f) {
		if len(a) >= 4 && a[0] == "agent" && a[1] == "prompt" {
			out = append(out, a[3])
		}
	}
	return out
}

func TestAgentName(t *testing.T) {
	tests := []struct{ id, want string }{
		{"T-01", "igris-t-01"},
		{"M3-G", "igris-m3-g"},
		{"P0_02", "igris-p0_02"},
		{"A.1 b", "igris-a-1-b"},
		{"Ü-1", "igris---1"},
		{"VERY-LONG-TASK-IDENTIFIER-1234567890", "igris-very-long-task-identifier-"},
	}
	for _, tt := range tests {
		got := AgentName(tt.id)
		if got != tt.want {
			t.Errorf("AgentName(%q) = %q, want %q", tt.id, got, tt.want)
		}
		if len(got) > maxAgentName || strings.Trim(got, "abcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
			t.Errorf("AgentName(%q) = %q is not a valid herdr name", tt.id, got)
		}
	}
}

func TestOpenSession(t *testing.T) {
	f := started(t)
	s := open(t, f)
	want := backend.SessionRef{Backend: Name, TabID: "w2B:t3", PaneID: "w2B:p3", Agent: "igris-t-01"}
	if s.Ref() != want {
		t.Errorf("ref = %+v, want %+v", s.Ref(), want)
	}
	wantArgs := [][]string{
		{"tab", "create", "--workspace", "w2B", "--cwd", "/work/demo", "--label", "T-01 · sonnet", "--env", "IGRIS_TASK=T-01", "--no-focus"},
		{"agent", "start", "igris-t-01", "--kind", "claude", "--pane", "w2B:p3", "--timeout", "120000", "--",
			"--model", "sonnet", "--session-id", "0b0e1f6a-0000-4000-8000-000000000001"},
	}
	if got := argv(f); !slices.EqualFunc(got, wantArgs, slices.Equal) {
		t.Errorf("calls =\n  %q\nwant\n  %q", got, wantArgs)
	}
}

func TestOpenSessionFailures(t *testing.T) {
	t.Run("tab create fails", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("tab", "create"), runner.Result{Stderr: []byte("boom"), ExitCode: 1}, nil)
		_, err := New(f, "w2B").OpenSession(context.Background(), spec)
		if err == nil || !strings.Contains(err.Error(), "open a herdr tab for T-01") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("start fails closes the tab", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("tab", "create"), ok(t, "tab_create.json"), nil)
		f.On(cmd("agent", "start"), runner.Result{Stderr: []byte(`{"error":{"code":"invalid_agent_argument","message":"cannot be encoded"}}`), ExitCode: 1}, nil)
		f.On(cmd("tab", "close", "w2B:t3"), ok(t, "tab_close.json"), nil)
		_, err := New(f, "w2B").OpenSession(context.Background(), spec)
		if !IsCode(err, CodeInvalidAgentArgument) {
			t.Fatalf("err = %v, want invalid_agent_argument", err)
		}
		if got := argv(f); len(got) != 3 || got[2][1] != "close" {
			t.Errorf("calls = %q, want tab close last", got)
		}
	})
	t.Run("close failure is reported", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("tab", "create"), ok(t, "tab_create.json"), nil)
		f.On(cmd("agent", "start"), fail(t, "error_wait_timeout.json", 1), nil)
		f.On(cmd("tab", "close"), runner.Result{Stderr: []byte("down"), ExitCode: 1}, nil)
		_, err := New(f, "w2B").OpenSession(context.Background(), spec)
		if err == nil || !strings.Contains(err.Error(), "close herdr tab w2B:t3 by hand") {
			t.Fatalf("err = %v", err)
		}
	})
}

// sleeps replaces b's sleep with one that records each wait and returns
// what next returns (nil when next is nil), so tests never sleep.
func sleeps(b *Backend, next func() error) *[]time.Duration {
	var got []time.Duration
	b.sleep = func(_ context.Context, d time.Duration) error {
		got = append(got, d)
		if next != nil {
			return next()
		}
		return nil
	}
	return &got
}

func TestOpenSessionShellNotReady(t *testing.T) {
	busy := func(t *testing.T) runner.Result { return fail(t, "error_agent_pane_busy.json", 1) }

	t.Run("retries until the shell is at its prompt", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("tab", "create"), ok(t, "tab_create.json"), nil)
		f.On(cmd("agent", "start"), busy(t), nil)
		f.On(cmd("agent", "start"), busy(t), nil)
		f.On(cmd("agent", "start"), ok(t, "agent_start_ok.json"), nil)
		b := New(f, "w2B")
		waits := sleeps(b, nil)
		s, err := b.OpenSession(context.Background(), spec)
		if err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		if s.(*Session).startupBlocked {
			t.Error("session marked startup-blocked")
		}
		if want := []time.Duration{paneReadyPoll, paneReadyPoll}; !slices.Equal(*waits, want) {
			t.Errorf("waits = %v, want %v", *waits, want)
		}
		if got := len(argv(f)); got != 4 {
			t.Errorf("%d calls, want tab create + 3 agent starts", got)
		}
	})

	t.Run("gives up after paneReadyWait and closes the tab", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("tab", "create"), ok(t, "tab_create.json"), nil)
		f.Func(func(c runner.Cmd) (runner.Result, error) {
			if c.Args[0] == "agent" {
				return busy(t), nil
			}
			return ok(t, "tab_close.json"), nil
		})
		b := New(f, "w2B")
		waits := sleeps(b, nil)
		_, err := b.OpenSession(context.Background(), spec)
		if !IsCode(err, CodeAgentPaneBusy) || !strings.Contains(err.Error(), "did not reach its prompt within 15s") {
			t.Fatalf("err = %v, want agent_pane_busy after 15s", err)
		}
		if want := int(paneReadyWait / paneReadyPoll); len(*waits) != want {
			t.Errorf("%d waits, want %d", len(*waits), want)
		}
		if got := argv(f); got[len(got)-1][1] != "close" {
			t.Errorf("last call = %q, want tab close", got[len(got)-1])
		}
	})

	t.Run("cancelled while waiting closes the tab", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("tab", "create"), ok(t, "tab_create.json"), nil)
		f.On(cmd("agent", "start"), busy(t), nil)
		f.On(cmd("tab", "close", "w2B:t3"), ok(t, "tab_close.json"), nil)
		b := New(f, "w2B")
		sleeps(b, func() error { return context.Canceled })
		_, err := b.OpenSession(context.Background(), spec)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if got := argv(f); len(got) != 3 || got[2][1] != "close" {
			t.Errorf("calls = %q, want tab close last", got)
		}
	})
}

func TestSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleep on a cancelled context = %v, want context.Canceled", err)
	}
}

func TestPrompt(t *testing.T) {
	const text = "--first line\nsecond line"
	tests := []struct {
		name    string
		wait    runner.Result
		prompt  *runner.Result // nil: no agent prompt call expected
		gone    bool
		blocked bool
	}{
		{name: "settled", wait: ok(t, "agent_wait_settled.json"), prompt: ptr(ok(t, "agent_prompt_nowait.json"))},
		{name: "still working", wait: fail(t, "error_wait_timeout.json", 1), prompt: ptr(ok(t, "agent_prompt_nowait.json"))},
		{name: "blocked at approval", wait: ok(t, "agent_prompt_wait_blocked.json"), blocked: true},
		{name: "rejected as blocked", wait: ok(t, "agent_wait_settled.json"), prompt: ptr(fail(t, "error_agent_blocked.json", 1)), blocked: true},
		{name: "agent gone on wait", wait: fail(t, "error_agent_not_found.json", 1), gone: true},
		{name: "agent gone on prompt", wait: ok(t, "agent_wait_settled.json"), prompt: ptr(fail(t, "error_agent_not_found.json", 1)), gone: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := started(t)
			s := open(t, f)
			f.On(cmd("agent", "wait", "igris-t-01"), tt.wait, nil)
			if tt.prompt != nil {
				f.On(cmd("agent", "prompt", "igris-t-01"), *tt.prompt, nil)
			}
			err := s.Prompt(context.Background(), text)
			if got := errors.Is(err, backend.ErrSessionGone); got != tt.gone {
				t.Errorf("gone = %v (err %v), want %v", got, err, tt.gone)
			}
			if got := errors.Is(err, ErrAgentBlocked); got != tt.blocked {
				t.Errorf("blocked = %v (err %v), want %v", got, err, tt.blocked)
			}
			if !tt.gone && !tt.blocked && err != nil {
				t.Errorf("err = %v", err)
			}
			if tt.prompt != nil && !slices.Equal(prompts(f), []string{text}) {
				t.Errorf("prompts = %q, want the text as one argument", prompts(f))
			}
			if tt.prompt == nil && len(prompts(f)) != 0 {
				t.Errorf("prompted while blocked or gone: %q", prompts(f))
			}
		})
	}
}

func TestState(t *testing.T) {
	tests := []struct {
		name string
		res  runner.Result
		want backend.AgentState
	}{
		{"idle", ok(t, "pane_get_idle.json"), backend.Idle},
		{"working", ok(t, "pane_get_working.json"), backend.Working},
		{"blocked", ok(t, "pane_get_blocked.json"), backend.Blocked},
		{"done", ok(t, "pane_get_done.json"), backend.Done},
		{"pane gone", fail(t, "error_pane_not_found.json", 1), backend.Exited},
		{"claude exited", shellPane, backend.Exited},
		{"unclassified", runner.Result{Stdout: []byte(`{"result":{"pane":{"agent":"claude","agent_status":"thinking"}}}`)}, backend.Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := started(t)
			s := open(t, f)
			f.On(cmd("pane", "get", "w2B:p3"), tt.res, nil)
			got, err := s.State(context.Background())
			if err != nil || got != tt.want {
				t.Errorf("State = %s, %v; want %s", got, err, tt.want)
			}
		})
	}
	t.Run("herdr failure", func(t *testing.T) {
		f := started(t)
		s := open(t, f)
		f.On(cmd("pane", "get"), runner.Result{}, runner.ErrTimeout)
		if st, err := s.State(context.Background()); !errors.Is(err, runner.ErrTimeout) || st != backend.Unknown {
			t.Errorf("State = %s, %v", st, err)
		}
	})
}

func TestStartupBlocked(t *testing.T) {
	ctx := context.Background()
	f := startupBlocked(t)
	s := open(t, f)

	// The task prompt is held while the folder-trust prompt is up.
	f.On(cmd("pane", "get"), ok(t, "pane_get_blocked.json"), nil)
	if err := s.Prompt(ctx, "task\nprompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(prompts(f)) != 0 {
		t.Fatalf("prompt sent during startup: %q", prompts(f))
	}

	// The engine's watch sees Blocked and raises Needs you.
	f.On(cmd("pane", "get"), ok(t, "pane_get_blocked.json"), nil)
	if st, err := s.State(ctx); err != nil || st != backend.Blocked {
		t.Fatalf("State = %s, %v; want blocked", st, err)
	}

	// Past the prompt: the held prompt goes out and the agent works.
	f.On(cmd("pane", "get"), ok(t, "pane_get_idle.json"), nil)
	f.On(cmd("agent", "prompt", "igris-t-01", "task\nprompt"), ok(t, "agent_prompt_nowait.json"), nil)
	if st, err := s.State(ctx); err != nil || st != backend.Working {
		t.Fatalf("State = %s, %v; want working after delivery", st, err)
	}

	// Afterwards State maps normally and Prompt takes the regular path.
	f.On(cmd("pane", "get"), ok(t, "pane_get_idle.json"), nil)
	if st, err := s.State(ctx); err != nil || st != backend.Idle {
		t.Fatalf("State = %s, %v; want idle", st, err)
	}
	f.On(cmd("agent", "wait"), ok(t, "agent_wait_settled.json"), nil)
	f.On(cmd("agent", "prompt"), ok(t, "agent_prompt_nowait.json"), nil)
	if err := s.Prompt(ctx, "follow-up"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if got := prompts(f); !slices.Equal(got, []string{"task\nprompt", "follow-up"}) {
		t.Errorf("prompts = %q", got)
	}
}

// Prompt text is typed into Claude Code's terminal as a paste: escape
// sequences in it (a verify failure quotes the session's own output) are
// removed so they can't end the paste and act as keystrokes.
func TestPromptIsCleaned(t *testing.T) {
	f := &runner.Fake{}
	f.On(cmd("agent", "wait"), ok(t, "agent_wait_settled.json"), nil)
	f.On(cmd("agent", "prompt"), ok(t, "agent_prompt_nowait.json"), nil)
	s := &Session{c: NewClient(f), id: "t-01", ref: backend.SessionRef{Backend: Name, TabID: "w2B:t3", PaneID: "w2B:p3", Agent: "igris-t-01"}}
	if err := s.Prompt(context.Background(), "```\n\x1b[31mFAIL\x1b[0m\x1b[201~\x1b[Z\n```\n\tfix it"); err != nil {
		t.Fatal(err)
	}
	if got := prompts(f); !slices.Equal(got, []string{"```\nFAIL\n```\n\tfix it"}) {
		t.Errorf("prompts = %q", got)
	}
}

func TestStartupBlockedVariants(t *testing.T) {
	ctx := context.Background()
	t.Run("already past the prompt", func(t *testing.T) {
		f := startupBlocked(t)
		s := open(t, f)
		f.On(cmd("pane", "get"), ok(t, "pane_get_idle.json"), nil)
		f.On(cmd("agent", "prompt"), ok(t, "agent_prompt_nowait.json"), nil)
		if err := s.Prompt(ctx, "task"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if !slices.Equal(prompts(f), []string{"task"}) {
			t.Errorf("prompts = %q", prompts(f))
		}
	})
	t.Run("blocked again on delivery", func(t *testing.T) {
		f := startupBlocked(t)
		s := open(t, f)
		f.On(cmd("pane", "get"), ok(t, "pane_get_blocked.json"), nil)
		if err := s.Prompt(ctx, "task"); err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		f.On(cmd("pane", "get"), ok(t, "pane_get_idle.json"), nil)
		f.On(cmd("agent", "prompt"), fail(t, "error_agent_blocked.json", 1), nil)
		if st, err := s.State(ctx); err != nil || st != backend.Blocked {
			t.Fatalf("State = %s, %v; want blocked", st, err)
		}
		// Still held: the next idle poll delivers it.
		f.On(cmd("pane", "get"), ok(t, "pane_get_idle.json"), nil)
		f.On(cmd("agent", "prompt"), ok(t, "agent_prompt_nowait.json"), nil)
		if st, err := s.State(ctx); err != nil || st != backend.Working {
			t.Fatalf("State = %s, %v; want working", st, err)
		}
	})
	t.Run("claude exits at the prompt", func(t *testing.T) {
		f := startupBlocked(t)
		s := open(t, f)
		f.On(cmd("pane", "get"), shellPane, nil)
		if err := s.Prompt(ctx, "task"); !errors.Is(err, backend.ErrSessionGone) {
			t.Fatalf("Prompt err = %v, want session gone", err)
		}
	})
	t.Run("pane closed", func(t *testing.T) {
		f := startupBlocked(t)
		s := open(t, f)
		f.On(cmd("pane", "get"), fail(t, "error_pane_not_found.json", 1), nil)
		if err := s.Prompt(ctx, "task"); !errors.Is(err, backend.ErrSessionGone) {
			t.Fatalf("Prompt err = %v, want session gone", err)
		}
	})
}

func TestFocusClose(t *testing.T) {
	ctx := context.Background()
	f := started(t)
	s := open(t, f)

	f.On(cmd("tab", "focus", "w2B:t3"), ok(t, "tab_focus.json"), nil)
	if err := s.Focus(ctx); err != nil {
		t.Errorf("Focus: %v", err)
	}
	f.On(cmd("tab", "close", "w2B:t3"), ok(t, "tab_close.json"), nil)
	if err := s.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Once the tab is gone: Focus fails as gone, Close is a no-op.
	f.On(cmd("tab", "focus"), fail(t, "error_tab_get_not_found.json", 1), nil)
	if err := s.Focus(ctx); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Focus err = %v, want session gone", err)
	}
	f.On(cmd("tab", "close"), fail(t, "error_tab_close_not_found.json", 1), nil)
	if err := s.Close(ctx); err != nil {
		t.Errorf("Close again: %v", err)
	}
	f.On(cmd("tab", "close"), runner.Result{}, runner.ErrTimeout)
	if err := s.Close(ctx); !errors.Is(err, runner.ErrTimeout) {
		t.Errorf("Close err = %v, want timeout", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAttach(t *testing.T) {
	ref := backend.SessionRef{Backend: Name, TabID: "w2B:t3", PaneID: "w2B:p3", Agent: "igris-t-01"}
	attach := func(f *runner.Fake, ref backend.SessionRef) (backend.Session, error) {
		return New(f, "w2B").Attach(context.Background(), ref)
	}

	t.Run("live pane", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("pane", "get", "w2B:p3"), ok(t, "pane_get_working.json"), nil) // Attach
		f.On(cmd("pane", "get", "w2B:p3"), ok(t, "pane_get_working.json"), nil) // State
		f.On(cmd("tab", "close"), ok(t, "tab_close.json"), nil)
		s, err := attach(f, ref)
		if err != nil {
			t.Fatalf("Attach: %v", err)
		}
		if s.Ref() != ref {
			t.Errorf("ref = %+v, want %+v", s.Ref(), ref)
		}
		if st, err := s.State(context.Background()); err != nil || st != backend.Working {
			t.Errorf("State = %v, %v; want working", st, err)
		}
		if err := s.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := argv(f); got[len(got)-1][2] != "w2B:t3" {
			t.Errorf("close used %q, want the stored tab", got[len(got)-1])
		}
	})
	t.Run("pane gone", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("pane", "get"), fail(t, "error_pane_not_found.json", 1), nil)
		if _, err := attach(f, ref); !errors.Is(err, backend.ErrSessionGone) {
			t.Fatalf("err = %v, want ErrSessionGone", err)
		}
	})
	t.Run("malformed ref never reaches herdr", func(t *testing.T) {
		// state.json can be edited by a session: an ID that reads as an
		// option, or an empty one, is refused before any herdr call.
		for _, bad := range []backend.SessionRef{
			{Backend: Name, TabID: "--all", PaneID: "w2B:p3", Agent: "igris-t-01"},
			{Backend: Name, TabID: "w2B:t3", PaneID: "-w2B:p3", Agent: "igris-t-01"},
			{Backend: Name, TabID: "w2B:t3", PaneID: "w2B:p3", Agent: "Igris T"},
			{Backend: Name, TabID: "w2B:t3", PaneID: "w2B:p3", Agent: "--help"},
			{Backend: Name, TabID: "", PaneID: "w2B:p3", Agent: "igris-t-01"},
			{Backend: Name, TabID: "w2B:t3", PaneID: "w2B:p3 x", Agent: "igris-t-01"},
		} {
			f := &runner.Fake{}
			_, err := attach(f, bad)
			if err == nil || !strings.Contains(err.Error(), "state.json looks damaged") {
				t.Errorf("Attach(%+v) err = %v, want a damaged-state error", bad, err)
			}
			if calls := argv(f); len(calls) != 0 {
				t.Errorf("Attach(%+v) ran herdr: %v", bad, calls)
			}
		}
	})
	t.Run("claude exited", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("pane", "get"), shellPane, nil)
		if _, err := attach(f, ref); !errors.Is(err, backend.ErrSessionGone) {
			t.Fatalf("err = %v, want ErrSessionGone", err)
		}
	})
	t.Run("other herdr failure is not gone", func(t *testing.T) {
		f := &runner.Fake{}
		f.On(cmd("pane", "get"), runner.Result{Stderr: []byte("boom"), ExitCode: 1}, nil)
		_, err := attach(f, ref)
		if err == nil || errors.Is(err, backend.ErrSessionGone) {
			t.Fatalf("err = %v, want a plain error", err)
		}
	})
	t.Run("bad refs", func(t *testing.T) {
		for _, bad := range []backend.SessionRef{
			{Backend: "fake", TabID: "t", PaneID: "p", Agent: "a"},
			{Backend: Name, PaneID: "p", Agent: "a"},
			{Backend: Name, TabID: "t", Agent: "a"},
			{Backend: Name, TabID: "t", PaneID: "p"},
		} {
			f := &runner.Fake{}
			if _, err := attach(f, bad); err == nil || len(f.Calls()) != 0 {
				t.Errorf("Attach(%+v) = %v with %d calls; want an error without calling herdr", bad, err, len(f.Calls()))
			}
		}
	})
}

// After an igris restart the engine hands a held first prompt back with
// HoldPrompt: it waits through the startup prompt and goes out once the
// agent is idle, like a prompt held since OpenSession.
func TestHoldPromptAfterAttach(t *testing.T) {
	ctx := context.Background()
	f := &runner.Fake{}
	f.On(cmd("pane", "get", "w2B:p3"), ok(t, "pane_get_blocked.json"), nil) // Attach
	s, err := New(f, "w2B").Attach(ctx, backend.SessionRef{Backend: Name, TabID: "w2B:t3", PaneID: "w2B:p3", Agent: "igris-t-01"})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	h, isHolder := s.(backend.PromptHolder)
	if !isHolder {
		t.Fatal("herdr session is not a backend.PromptHolder")
	}
	h.HoldPrompt("task\nprompt")
	if !h.PromptPending() {
		t.Fatal("PromptPending = false after HoldPrompt")
	}

	f.On(cmd("pane", "get"), ok(t, "pane_get_blocked.json"), nil)
	if st, err := s.State(ctx); err != nil || st != backend.Blocked || len(prompts(f)) != 0 {
		t.Fatalf("State = %s, %v, prompts %q; want blocked and nothing sent", st, err, prompts(f))
	}
	f.On(cmd("pane", "get"), ok(t, "pane_get_idle.json"), nil)
	f.On(cmd("agent", "prompt", "igris-t-01", "task\nprompt"), ok(t, "agent_prompt_nowait.json"), nil)
	if st, err := s.State(ctx); err != nil || st != backend.Working {
		t.Fatalf("State = %s, %v; want working after delivery", st, err)
	}
	if h.PromptPending() || !slices.Equal(prompts(f), []string{"task\nprompt"}) {
		t.Errorf("pending %v, prompts %q; want delivered once", h.PromptPending(), prompts(f))
	}
}
