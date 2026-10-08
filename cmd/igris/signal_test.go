package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestHelperInterrupt is the child of TestSecondInterruptExits: it waits
// for the first interrupt, says so, and then blocks as a run winding down
// would.
func TestHelperInterrupt(t *testing.T) {
	if os.Getenv("IGRIS_TEST_INTERRUPT") != "1" {
		t.Skip("helper process")
	}
	ctx, cancel := interruptContext()
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
