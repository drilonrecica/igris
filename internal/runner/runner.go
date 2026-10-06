// Package runner runs external processes for igris. Every call goes through
// Runner so it can be faked in tests, is passed as argv (never through a
// shell, except Shell for the configured verify command) and has a timeout
// (SPEC §16).
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// ErrTimeout is wrapped by Run's error when a command exceeds its timeout.
var ErrTimeout = errors.New("command timed out")

// waitDelay bounds how long Run waits for output pipes after the process is
// killed, in case a leaked descendant still holds them open.
const waitDelay = 2 * time.Second

// Cmd describes one external process call.
type Cmd struct {
	Name    string        // program, resolved via PATH
	Args    []string      // passed as argv, never through a shell
	Dir     string        // working directory; "" = current directory
	Env     []string      // full environment; nil = inherit igris's
	Stdin   io.Reader     // optional
	Timeout time.Duration // required, > 0
	// Combined captures stderr into Stdout, interleaved as written, so the
	// output reads like a terminal (used for the verify command). Stderr
	// stays empty.
	Combined bool
}

// String renders the command for display and error messages only. It is
// never executed.
func (c Cmd) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	for _, a := range append([]string{c.Name}, c.Args...) {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\r'\"\\$`;&|<>()*?[]{}~#!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Shell builds a `sh -c script` command. It exists only for the verify
// command from the config snapshot (SPEC §6.4); never pass plan text to it.
func Shell(script, dir string, timeout time.Duration) Cmd {
	return Cmd{Name: "sh", Args: []string{"-c", script}, Dir: dir, Timeout: timeout}
}

// Result is the outcome of a command that ran (possibly partially, on
// timeout or cancellation).
type Result struct {
	Cmd      Cmd
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Err returns an *ExitError for a non-zero exit code, nil otherwise.
func (r Result) Err() error {
	if r.ExitCode == 0 {
		return nil
	}
	return &ExitError{Cmd: r.Cmd, Code: r.ExitCode, Stderr: r.Stderr}
}

// ExitError reports a command that exited with a non-zero code.
type ExitError struct {
	Cmd    Cmd
	Code   int
	Stderr []byte
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("%s: exit status %d", e.Cmd, e.Code)
	if tail := stderrTail(e.Stderr); tail != "" {
		msg += ": " + tail
	}
	return msg
}

// stderrTail keeps error messages short: the last few non-empty lines.
func stderrTail(b []byte) string {
	const maxLines = 5
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Runner runs external commands. A non-zero exit is not an error: it is
// reported in Result.ExitCode (see Result.Err). Run returns an error only if
// the command could not be started, timed out or was cancelled.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
}

func validate(c Cmd) error {
	if c.Name == "" {
		return errors.New("run command: empty program name")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("run %s: no timeout set", c)
	}
	return nil
}

// Exec runs commands as real processes.
type Exec struct{}

var (
	_ Runner = Exec{}
	_ Runner = (*Fake)(nil)
)

// Run starts c, waits for it and captures its output. On timeout the whole
// process group is killed so children of `sh -c` don't outlive it.
func (Exec) Run(ctx context.Context, c Cmd) (Result, error) {
	res := Result{Cmd: c, ExitCode: -1}
	if err := validate(c); err != nil {
		return res, err
	}

	runCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	// This is igris's single process-launch seam; callers pass argv, never a
	// shell string built from plan text (AGENTS §6).
	cmd := exec.CommandContext(runCtx, c.Name, c.Args...) //nolint:gosec // G204: see above

	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if c.Combined {
		cmd.Stderr = &stdout
	}
	cmd.WaitDelay = waitDelay
	killGroupOnCancel(cmd)

	err := cmd.Run()
	res.Stdout = stdout.Bytes()
	res.Stderr = stderr.Bytes()

	switch {
	case ctx.Err() != nil:
		return res, fmt.Errorf("run %s: %w", c, ctx.Err())
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return res, fmt.Errorf("run %s: %w after %s; raise the timeout if the command needs longer", c, ErrTimeout, c.Timeout)
	}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &exitErr) && exitErr.Exited():
		res.ExitCode = exitErr.ExitCode()
	case errors.Is(err, exec.ErrNotFound):
		return res, fmt.Errorf("run %s: %q not found in PATH; install it or fix PATH: %w", c, c.Name, err)
	default:
		return res, fmt.Errorf("run %s: %w", c, err)
	}
	return res, nil
}
