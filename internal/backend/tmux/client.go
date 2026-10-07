// Package tmux is the tmux backend (SPEC §11.5, V03-P2): one window per
// task in the tmux server igris runs in, driven through the tmux CLI. The
// agent state comes from Claude Code's hooks (SPEC §6.3).
package tmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Program is the tmux executable, resolved via PATH.
const Program = "tmux"

// DefaultTimeout bounds every tmux call (SPEC §11.5).
const DefaultTimeout = 10 * time.Second

// Error is a tmux call that exited non-zero. tmux reports errors as one
// line of text on stderr, e.g. "can't find pane: %3".
type Error struct {
	Op      string // the subcommand, e.g. "list-panes"
	Message string // tmux's message, cleaned
	Exit    int
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("tmux %s: exit status %d", e.Op, e.Exit)
	}
	return fmt.Sprintf("tmux %s: %s", e.Op, e.Message)
}

// IsGone reports whether err says the pane or window no longer exists
// (V03-P2: "can't find pane: %N", "can't find window: @N").
func IsGone(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return strings.HasPrefix(e.Message, "can't find pane") || strings.HasPrefix(e.Message, "can't find window")
}

// PaneInfo describes one pane (list-panes).
type PaneInfo struct {
	ID         string // %N
	Dead       bool   // the pane's command exited (remain-on-exit keeps it)
	DeadStatus int    // its exit status, when Dead
	Command    string // the pane's current command
}

// Client calls the tmux CLI. The zero value is not usable; call NewClient.
type Client struct {
	run     runner.Runner
	timeout time.Duration
}

// NewClient returns a client that runs tmux through r. tmux finds the
// server igris runs in through $TMUX, which igris inherits.
func NewClient(r runner.Runner) *Client {
	return &Client{run: r, timeout: DefaultTimeout}
}

// exec runs `tmux args...` with stdin and returns its stdout.
func (c *Client) exec(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	op := ""
	if len(args) > 0 {
		op = args[0]
	}
	res, err := c.run.Run(ctx, runner.Cmd{Name: Program, Args: args, Stdin: stdin, Timeout: c.timeout})
	if err != nil {
		return nil, fmt.Errorf("tmux %s: %w", op, err)
	}
	if res.ExitCode != 0 {
		return nil, &Error{Op: op, Message: textsafe.Line(strings.TrimSpace(string(res.Stderr))), Exit: res.ExitCode}
	}
	return res.Stdout, nil
}

// Version returns the server's version, e.g. "3.7c". It fails when no
// server is reachable.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.exec(ctx, nil, "display-message", "-p", "#{version}")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", fmt.Errorf("tmux display-message: no version in the output")
	}
	return v, nil
}

// NewWindow opens a detached window named name in dir running argv
// directly (tmux execs a multi-argument command without a shell, V03-P2)
// and returns its window and pane IDs. name is escaped: -n is a tmux
// format, where #(…) would run a command.
func (c *Client) NewWindow(ctx context.Context, dir, name string, argv []string) (window, pane string, err error) {
	if len(argv) == 0 {
		return "", "", fmt.Errorf("tmux new-window: no command")
	}
	args := []string{"new-window", "-d", "-P", "-F", "#{window_id} #{pane_id}", "-c", dir, "-n", Literal(name), "--"}
	out, err := c.exec(ctx, nil, append(args, argv...)...)
	if err != nil {
		return "", "", err
	}
	f := strings.Fields(string(out))
	if len(f) != 2 || !windowPattern.MatchString(f[0]) || !panePattern.MatchString(f[1]) {
		return "", "", fmt.Errorf("tmux new-window: unexpected output %q", textsafe.Line(string(out)))
	}
	return f[0], f[1], nil
}

// RemainOnExit keeps window's pane after its command exits, so an exited
// Claude Code stays visible and observable as dead.
func (c *Client) RemainOnExit(ctx context.Context, window string) error {
	_, err := c.exec(ctx, nil, "set-option", "-w", "-t", window, "remain-on-exit", "on")
	return err
}

// LoadBuffer stores text in the paste buffer name. The text travels on
// stdin: never as an argument, never through a format.
func (c *Client) LoadBuffer(ctx context.Context, name, text string) error {
	_, err := c.exec(ctx, strings.NewReader(text), "load-buffer", "-b", name, "-")
	return err
}

// PasteBuffer pastes buffer name into pane with bracketed paste (when the
// application asked for it, as Claude Code does) and deletes the buffer.
func (c *Client) PasteBuffer(ctx context.Context, name, pane string) error {
	_, err := c.exec(ctx, nil, "paste-buffer", "-p", "-d", "-b", name, "-t", pane)
	return err
}

// SendEnter presses Enter in pane.
func (c *Client) SendEnter(ctx context.Context, pane string) error {
	_, err := c.exec(ctx, nil, "send-keys", "-t", pane, "Enter")
	return err
}

// Pane describes pane. A pane that no longer exists gives an error for
// which IsGone holds (display-message would exit 0 for it, V03-P2).
func (c *Client) Pane(ctx context.Context, pane string) (PaneInfo, error) {
	out, err := c.exec(ctx, nil, "list-panes", "-t", pane, "-F", "#{pane_id}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_current_command}")
	if err != nil {
		return PaneInfo{}, err
	}
	for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || f[0] != pane {
			continue // other panes of the window
		}
		status, _ := strconv.Atoi(f[2])
		return PaneInfo{ID: f[0], Dead: f[1] == "1", DeadStatus: status, Command: textsafe.Line(f[3])}, nil
	}
	return PaneInfo{}, &Error{Op: "list-panes", Message: "can't find pane: " + pane, Exit: 1}
}

// SelectWindow makes window the current window of its session.
func (c *Client) SelectWindow(ctx context.Context, window string) error {
	_, err := c.exec(ctx, nil, "select-window", "-t", window)
	return err
}

// KillWindow closes window and its pane.
func (c *Client) KillWindow(ctx context.Context, window string) error {
	_, err := c.exec(ctx, nil, "kill-window", "-t", window)
	return err
}

// DisplayMessage shows text on igris's tmux client for 5 seconds. text is
// cleaned and escaped: display-message text is a format.
func (c *Client) DisplayMessage(ctx context.Context, text string) error {
	_, err := c.exec(ctx, nil, "display-message", "-d", "5000", Literal(text))
	return err
}

// Literal makes s safe as a tmux format: control characters are removed
// and every # is doubled, so #{…} and #(…) are shown, never expanded or
// run (V03-P2).
func Literal(s string) string {
	return strings.ReplaceAll(textsafe.Line(s), "#", "##")
}
