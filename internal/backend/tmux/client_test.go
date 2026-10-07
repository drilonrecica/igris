package tmux

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // test fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ok(t *testing.T, name string) runner.Result {
	return runner.Result{Stdout: fixture(t, name)}
}

func fail(t *testing.T, name string) runner.Result {
	return runner.Result{Stderr: fixture(t, name), ExitCode: 1}
}

func cmd(words ...string) []string { return append([]string{Program}, words...) }

func TestClientCommands(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		res  runner.Result
		call func(c *Client) error
		args []string
	}{
		{"new window: argv after --, name escaped", ok(t, "new_window.txt"), func(c *Client) error {
			w, p, err := c.NewWindow(ctx, "/x y/proj", "P1-01 · #(touch pwn) #{pane_id}", []string{"claude", "--model", "opus", "--settings", "/a;b"})
			if err == nil && (w != "@1" || p != "%1") {
				t.Errorf("window %q pane %q", w, p)
			}
			return err
		}, []string{"new-window", "-d", "-P", "-F", "#{window_id} #{pane_id}", "-c", "/x y/proj", "-n", "P1-01 · ##(touch pwn) ##{pane_id}", "--", "claude", "--model", "opus", "--settings", "/a;b"}},
		{"remain on exit", runner.Result{}, func(c *Client) error { return c.RemainOnExit(ctx, "@1") },
			[]string{"set-option", "-w", "-t", "@1", "remain-on-exit", "on"}},
		{"paste", runner.Result{}, func(c *Client) error { return c.PasteBuffer(ctx, "igris-b", "%1") },
			[]string{"paste-buffer", "-p", "-d", "-b", "igris-b", "-t", "%1"}},
		{"enter", runner.Result{}, func(c *Client) error { return c.SendEnter(ctx, "%1") },
			[]string{"send-keys", "-t", "%1", "Enter"}},
		{"select", runner.Result{}, func(c *Client) error { return c.SelectWindow(ctx, "@1") },
			[]string{"select-window", "-t", "@1"}},
		{"kill", runner.Result{}, func(c *Client) error { return c.KillWindow(ctx, "@1") },
			[]string{"kill-window", "-t", "@1"}},
		{"toast escaped", runner.Result{}, func(c *Client) error { return c.DisplayMessage(ctx, "needs you: #(rm -rf ~)\x1b[31m") },
			[]string{"display-message", "-d", "5000", "needs you: ##(rm -rf ~)"}},
		{"version", ok(t, "version.txt"), func(c *Client) error {
			v, err := c.Version(ctx)
			if err == nil && v != "3.7c" {
				t.Errorf("version %q", v)
			}
			return err
		}, []string{"display-message", "-p", "#{version}"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &runner.Fake{}
			f.On([]string{Program}, tt.res, nil)
			if err := tt.call(NewClient(f)); err != nil {
				t.Fatal(err)
			}
			calls := f.Calls()
			if len(calls) != 1 || !slices.Equal(calls[0].Args, tt.args) || calls[0].Timeout != DefaultTimeout {
				t.Errorf("calls %v, want tmux %q", calls, tt.args)
			}
		})
	}
}

// The prompt travels on stdin, never in argv (SPEC §11.5).
func TestLoadBufferUsesStdin(t *testing.T) {
	f := &runner.Fake{}
	f.On(cmd("load-buffer"), runner.Result{}, nil)
	text := "line one\nline two #(touch x)"
	if err := NewClient(f).LoadBuffer(context.Background(), "igris-b", text); err != nil {
		t.Fatal(err)
	}
	c := f.Calls()[0]
	if !slices.Equal(c.Args, []string{"load-buffer", "-b", "igris-b", "-"}) {
		t.Errorf("args %q", c.Args)
	}
	got, _ := io.ReadAll(c.Stdin)
	if string(got) != text {
		t.Errorf("stdin %q, want %q", got, text)
	}
}

func TestPane(t *testing.T) {
	ctx := context.Background()
	f := &runner.Fake{}
	f.On(cmd("list-panes"), ok(t, "list_panes_alive.txt"), nil)
	f.On(cmd("list-panes"), ok(t, "list_panes_dead.txt"), nil)
	f.On(cmd("list-panes"), fail(t, "err_pane.txt"), nil)
	c := NewClient(f)
	if p, err := c.Pane(ctx, "%1"); err != nil || p.Dead || p.Command != "claude" {
		t.Errorf("alive: %+v %v", p, err)
	}
	if p, err := c.Pane(ctx, "%5"); err != nil || !p.Dead || p.DeadStatus != 0 {
		t.Errorf("dead: %+v %v", p, err)
	}
	if _, err := c.Pane(ctx, "%999"); !IsGone(err) {
		t.Errorf("gone: %v", err)
	}
	// A pane missing from the window's list is gone too.
	f.On(cmd("list-panes"), ok(t, "list_panes_alive.txt"), nil)
	if _, err := c.Pane(ctx, "%7"); !IsGone(err) {
		t.Errorf("other pane: %v", err)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	f := &runner.Fake{}
	f.On(cmd("kill-window"), fail(t, "err_window.txt"), nil)
	f.On(cmd("display-message"), fail(t, "err_no_server.txt"), nil)
	f.On(cmd("new-window"), runner.Result{Stdout: []byte("garbage\n")}, nil)
	c := NewClient(f)
	if err := c.KillWindow(ctx, "@999"); !IsGone(err) || !strings.Contains(err.Error(), "can't find window: @999") {
		t.Errorf("kill: %v", err)
	}
	_, err := c.Version(ctx)
	var e *Error
	if !errors.As(err, &e) || IsGone(err) || !strings.Contains(e.Message, "error connecting") {
		t.Errorf("version: %v", err)
	}
	if _, _, err := c.NewWindow(ctx, "/p", "x", []string{"claude"}); err == nil {
		t.Error("accepted garbage window IDs")
	}
}

func TestLiteral(t *testing.T) {
	for in, want := range map[string]string{
		"plain":            "plain",
		"#{pane_id}":       "##{pane_id}",
		"#(cmd)":           "##(cmd)",
		"a\nb\x1b[1mc":     "a bc",
		"already ## twice": "already #### twice",
	} {
		if got := Literal(in); got != want {
			t.Errorf("Literal(%q) = %q, want %q", in, got, want)
		}
	}
}
