package tmux

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
)

const uuid = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"

var spec = backend.SessionSpec{
	TaskID:        "P1-01",
	Dir:           "/work/proj",
	Label:         "P1-01 · opus",
	Args:          []string{"--model", "opus", "--session-id", uuid},
	ClaudeSession: uuid,
}

func openSim(t *testing.T, s *sim) *Session {
	t.Helper()
	sess, err := s.backend().OpenSession(context.Background(), spec)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	return sess.(*Session)
}

func TestOpenAndPrompt(t *testing.T) {
	ctx := context.Background()
	s := newSim()
	sess := openSim(t, s)
	ref := sess.Ref()
	if ref != (backend.SessionRef{Backend: Name, TabID: "@1", PaneID: "%1", ClaudeSession: uuid}) {
		t.Errorf("ref %+v", ref)
	}
	p := s.pane("%1")
	if !slices.Equal(p.argv, append([]string{"claude"}, spec.Args...)) || p.name != "P1-01 · opus" {
		t.Errorf("window argv %q name %q", p.argv, p.name)
	}
	if sess.PromptPending() {
		t.Error("prompt pending after a normal start")
	}
	if err := sess.Prompt(ctx, "# Task P1-01\nDo it.\x1b[31m"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.pasted, []string{"# Task P1-01\nDo it."}) {
		t.Errorf("pasted %q", p.pasted)
	}
	if st, err := sess.State(ctx); err != nil || st != backend.Idle {
		t.Errorf("State = %s, %v", st, err)
	}
	s.setHook(uuid, backend.Working)
	if st, _ := sess.State(ctx); st != backend.Working {
		t.Errorf("State = %s, want working", st)
	}
}

// A prompt is not pasted into a question or permission prompt.
func TestPromptBlocked(t *testing.T) {
	s := newSim()
	sess := openSim(t, s)
	s.setHook(uuid, backend.Blocked)
	if err := sess.Prompt(context.Background(), "hi"); !errors.Is(err, ErrAgentBlocked) {
		t.Errorf("Prompt = %v, want ErrAgentBlocked", err)
	}
	if p := s.pane("%1"); len(p.pasted)+len(p.typed) != 0 {
		t.Error("text reached the pane")
	}
}

// Without any hook after the startup wait (folder trust), the first prompt
// is held, the session reads blocked, and the prompt goes out once Claude
// Code is idle (SPEC §11.5).
func TestStartupQuestion(t *testing.T) {
	ctx := context.Background()
	s := newSim()
	s.startHook = ""
	sess := openSim(t, s)
	if st, _ := sess.State(ctx); st != backend.Blocked {
		t.Errorf("State at startup = %s, want blocked", st)
	}
	if err := sess.Prompt(ctx, "task"); err != nil {
		t.Fatal(err)
	}
	if !sess.PromptPending() || len(s.pane("%1").pasted) != 0 {
		t.Fatal("prompt not held")
	}
	s.setHook(uuid, backend.Idle)
	if st, err := sess.State(ctx); err != nil || st != backend.Working {
		t.Errorf("State = %s, %v; want working after delivery", st, err)
	}
	if sess.PromptPending() || !slices.Equal(s.pane("%1").pasted, []string{"task"}) {
		t.Errorf("held prompt not delivered: %q", s.pane("%1").pasted)
	}
}

func TestStateExited(t *testing.T) {
	ctx := context.Background()
	for name, end := range map[string]func(s *sim){
		"window killed": func(s *sim) { s.kill("%1") },
		"pane dead":     func(s *sim) { s.die("%1") },
		"session ended": func(s *sim) { s.setHook(uuid, backend.Exited) },
	} {
		t.Run(name, func(t *testing.T) {
			s := newSim()
			sess := openSim(t, s)
			end(s)
			if st, err := sess.State(ctx); err != nil || st != backend.Exited {
				t.Errorf("State = %s, %v; want exited", st, err)
			}
		})
	}
}

func TestGoneSession(t *testing.T) {
	ctx := context.Background()
	s := newSim()
	sess := openSim(t, s)
	s.kill("%1")
	if err := sess.Prompt(ctx, "x"); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Prompt = %v", err)
	}
	if err := sess.Focus(ctx); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Focus = %v", err)
	}
	if err := sess.Close(ctx); err != nil {
		t.Errorf("Close on a gone window = %v", err)
	}
}

func TestOpenFails(t *testing.T) {
	ctx := context.Background()
	s := newSim()
	s.startHook = ""
	f := &runner.Fake{}
	f.Func(func(c runner.Cmd) (runner.Result, error) {
		res, err := s.run(c)
		if c.Args[0] == "new-window" {
			s.die("%1") // claude exits at once
		}
		return res, err
	})
	b := New(f, true).WithHookStates(s.hookState)
	b.sleep = s.backend().sleep
	if _, err := b.OpenSession(ctx, spec); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("OpenSession = %v", err)
	}
	if len(s.windows) != 0 {
		t.Error("window left behind")
	}
}

func TestAttach(t *testing.T) {
	ctx := context.Background()
	s := newSim()
	ref := openSim(t, s).Ref()
	b := s.backend()
	sess, err := b.Attach(ctx, ref)
	if err != nil || sess.Ref() != ref {
		t.Fatalf("Attach = %v, %v", sess, err)
	}
	// A reattached session gets its held prompt back (SPEC §13).
	sess.(backend.PromptHolder).HoldPrompt("held")
	if st, _ := sess.State(ctx); st != backend.Working || !slices.Equal(s.pane("%1").pasted, []string{"held"}) {
		t.Errorf("held prompt not delivered after attach: %s %q", st, s.pane("%1").pasted)
	}

	for name, bad := range map[string]backend.SessionRef{
		"other backend": {Backend: "herdr", TabID: "@1", PaneID: "%1"},
		"window shape":  {Backend: Name, TabID: "@1;kill-server", PaneID: "%1"},
		"pane shape":    {Backend: Name, TabID: "@1", PaneID: "-t"},
		"uuid shape":    {Backend: Name, TabID: "@1", PaneID: "%1", ClaudeSession: "--model"},
	} {
		f := &runner.Fake{}
		if _, err := New(f, true).Attach(ctx, bad); err == nil || len(f.Calls()) != 0 {
			t.Errorf("%s: Attach = %v after %d calls", name, err, len(f.Calls()))
		}
	}
	s.die("%1")
	if _, err := b.Attach(ctx, ref); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("dead pane: Attach = %v", err)
	}
	s.kill("%1")
	if _, err := b.Attach(ctx, ref); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("gone pane: Attach = %v", err)
	}
}

func TestAvailable(t *testing.T) {
	ctx := context.Background()
	if err := New(&runner.Fake{}, false).Available(ctx); err == nil || !strings.Contains(err.Error(), "inside tmux") {
		t.Errorf("outside tmux: %v", err)
	}
	f := &runner.Fake{}
	f.On(cmd("display-message"), runner.Result{ExitCode: -1}, exec.ErrNotFound)
	if err := New(f, true).Available(ctx); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("not installed: %v", err)
	}
	f = &runner.Fake{}
	f.On(cmd("display-message"), fail(t, "err_no_server.txt"), nil)
	if err := New(f, true).Available(ctx); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("no server: %v", err)
	}
	if err := newSim().backend().Available(ctx); err != nil {
		t.Errorf("inside tmux: %v", err)
	}
	if got := NewFromEnv(&runner.Fake{}, func(string) string { return "/tmp/tmux-1000/default,1,0" }); !got.inside {
		t.Error("NewFromEnv ignored $TMUX")
	}
}

func TestNotify(t *testing.T) {
	s := newSim()
	if err := s.backend().Notify(context.Background(), backend.Notification{Title: "igris: needs you", Body: "P1-01 #(x)"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.toasts, []string{"igris: needs you: P1-01 ##(x)"}) {
		t.Errorf("toasts %q", s.toasts)
	}
}

// TestTailClaudeScreen cuts a Claude Code screen as capture-pane shows it
// (testdata/capture_pane_claude.txt, synthetic): the transcript, blank
// rows, the input box between two rules, the footer, empty rows.
func TestTailClaudeScreen(t *testing.T) {
	s := newSim()
	sess := openSim(t, s)
	f := &runner.Fake{}
	f.On(cmd("capture-pane"), ok(t, "capture_pane_claude.txt"), nil)
	sess.c = NewClient(f)
	got, err := sess.Tail(context.Background(), 60)
	want := []string{
		"❯ Synthetic task prompt for the tail fixture.",
		"● I added the parser and its tests.",
		"  - internal/demo/parse.go",
		"  - internal/demo/parse_test.go",
		"● Bash(igris done T-01)",
		"  ⎿  marked T-01 done",
		"✻ Cooked for 42s",
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Tail = %q, %v; want %q", got, err, want)
	}
}

// TestTail cuts the recorded capture (testdata/capture_pane.txt: six
// history lines, then a 12-row screen with five empty rows at the end).
func TestTail(t *testing.T) {
	ctx := context.Background()
	s := newSim()
	sess := openSim(t, s)
	f := &runner.Fake{}
	f.On(cmd("capture-pane"), ok(t, "capture_pane.txt"), nil)
	f.On(cmd("capture-pane"), fail(t, "err_pane.txt"), nil)
	f.On(cmd("capture-pane"), runner.Result{Stderr: []byte("no server running\n"), ExitCode: 1}, nil)
	sess.c = NewClient(f)

	got, err := sess.Tail(ctx, 4)
	want := []string{"history line 10", "⏺ Synthetic reply: this line is long enough that the 40-column pane wraps it twice over.", "\tindented\twith tabs", "✻ Working… (esc to interrupt)"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Tail = %q, %v; want %q (blank rows dropped)", got, err, want)
	}
	if c := f.Calls()[0]; !slices.Equal(c.Args, []string{"capture-pane", "-p", "-J", "-t", "%1", "-S", "-4"}) {
		t.Errorf("args %q", c.Args)
	}
	if _, err := sess.Tail(ctx, 4); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Tail of a gone pane = %v, want ErrSessionGone", err)
	}
	if _, err := sess.Tail(ctx, 4); err == nil || errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Tail = %v, want a plain error", err)
	}
}

// Submit presses Enter on what sits in the input box, typing nothing.
func TestSubmit(t *testing.T) {
	ctx := context.Background()
	s := newSim()
	sess := openSim(t, s)
	p := s.pane("%1")
	s.mu.Lock()
	p.typed = "pasted, not sent"
	s.mu.Unlock()
	if err := sess.Submit(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.pasted, []string{"pasted, not sent"}) {
		t.Errorf("pasted %q", p.pasted)
	}
	s.kill("%1")
	if err := sess.Submit(ctx); !errors.Is(err, backend.ErrSessionGone) {
		t.Errorf("Submit on a gone pane = %v", err)
	}
}
