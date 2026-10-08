package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/tui"
)

// TestHelperInterrupt is the child of TestSecondInterruptExits: it waits
// for the first interrupt, says so, and then blocks as a run winding down
// would.
func TestHelperInterrupt(t *testing.T) {
	if os.Getenv("IGRIS_TEST_INTERRUPT") != "1" {
		t.Skip("helper process")
	}
	ctx, cancel, _ := interruptContext()
	defer cancel()
	fmt.Println("ready")
	<-ctx.Done()
	fmt.Println("stopping")
	select {} // the final flush that hangs
}

// The first Ctrl-C stops the run; a second one exits at once, even while
// igris is still sending its last notifications.
func TestSecondInterruptExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperInterrupt$") //nolint:gosec // the test binary itself
	cmd.Env = append(os.Environ(), "IGRIS_TEST_INTERRUPT=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(out)
	expect := func(want string) {
		t.Helper()
		if !lines.Scan() || lines.Text() != want {
			t.Fatalf("child said %q (%v), want %q", lines.Text(), lines.Err(), want)
		}
	}
	expect("ready")
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	expect("stopping")
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil {
		t.Fatalf("child: %v (timed out: %v), want it killed by the second SIGINT", err, ctx.Err() != nil)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("child ended with %v, want killed by SIGINT", exit)
	}
}

// TestHelperSignals is the child of the tests below; IGRIS_TEST_SIGNALS
// picks the path it plays.
func TestHelperSignals(t *testing.T) {
	switch os.Getenv("IGRIS_TEST_SIGNALS") {
	case "arise":
		// The owner quit the TUI; the run winds down on the plain terminal.
		_, cancel, release := interruptContext()
		defer cancel()
		windingDown(release, true, os.Stdout)
		select {} // the run that takes its time to end
	case "home-quit", "home-signal":
		appUI = func(ctx context.Context, o tui.AppOptions) (tui.AppResult, error) {
			fmt.Println("ready")
			if os.Getenv("IGRIS_TEST_SIGNALS") == "home-signal" {
				<-ctx.Done() // a signal ends the app
			}
			o.Ending(true)
			select {} // App waits for its run to end
		}
		runApp(tui.Home{}, os.Stdout, os.Stderr)
	default:
		t.Skip("helper process")
	}
}

// startHelper starts TestHelperSignals as path, with no herdr or tmux to
// find, in an empty directory.
func startHelper(t *testing.T, ctx context.Context, path string) (*exec.Cmd, func(string)) {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperSignals$") //nolint:gosec // the test binary itself
	cmd.Dir = t.TempDir()
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") && !strings.HasPrefix(kv, "TMUX") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "IGRIS_TEST_SIGNALS="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(out)
	return cmd, func(want string) {
		t.Helper()
		if !lines.Scan() || lines.Text() != want {
			t.Fatalf("child said %q (%v), want %q", lines.Text(), lines.Err(), want)
		}
	}
}

// killedBy checks the child ended from sig, not from the test's timeout.
func killedBy(t *testing.T, ctx context.Context, cmd *exec.Cmd, sig syscall.Signal) {
	t.Helper()
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || ctx.Err() != nil {
		t.Fatalf("child: %v (timed out: %v), want it killed by %v", err, ctx.Err() != nil, sig)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != sig {
		t.Errorf("child ended with %v, want killed by %v", exit, sig)
	}
}

// Once the owner has left the TUI (arise's, or home's with a run going),
// the run winds down on the plain terminal: igris says so, and the first
// Ctrl-C (or SIGTERM) exits at once.
func TestSignalAfterTheTUIExits(t *testing.T) {
	for _, tc := range []struct {
		path string
		sig  syscall.Signal
	}{
		{"arise", syscall.SIGINT},
		{"home-quit", syscall.SIGINT},
		{"home-quit", syscall.SIGTERM},
	} {
		t.Run(tc.path+"/"+tc.sig.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd, expect := startHelper(t, ctx, tc.path)
			if tc.path != "arise" {
				expect("ready")
			}
			expect(stoppingText)
			if err := cmd.Process.Signal(tc.sig); err != nil {
				t.Fatal(err)
			}
			killedBy(t, ctx, cmd, tc.sig)
		})
	}
}

// On home, the first Ctrl-C (not only SIGTERM) ends the app and stops its
// run; a second one exits while the run winds down.
func TestHomeSecondInterruptExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd, expect := startHelper(t, ctx, "home-signal")
	expect("ready")
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	expect(stoppingText)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	killedBy(t, ctx, cmd, syscall.SIGINT)
}

// signal.NotifyContext keeps catching its signals until its stop is
// called, so a second Ctrl-C would be swallowed: every command takes
// interruptContext instead.
func TestNoSwallowedInterrupts(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f) //nolint:gosec // this package's own sources
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "signal.NotifyContext") {
			t.Errorf("%s uses signal.NotifyContext; use interruptContext", f)
		}
	}
}
