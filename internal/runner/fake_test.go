package runner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func cmd(name string, args ...string) Cmd {
	return Cmd{Name: name, Args: args, Timeout: time.Second}
}

func TestFakeScriptedResponses(t *testing.T) {
	f := &Fake{}
	f.On([]string{"git", "status"}, Result{Stdout: []byte("first")}, nil)
	f.On([]string{"git"}, Result{ExitCode: 1}, nil)
	f.On([]string{"git", "status"}, Result{Stdout: []byte("second")}, nil)
	boom := errors.New("boom")
	f.On([]string{"herdr"}, Result{}, boom)

	tests := []struct {
		cmd      Cmd
		stdout   string
		exitCode int
		err      error
	}{
		{cmd: cmd("git", "status", "--short"), stdout: "first"},
		{cmd: cmd("git", "status"), exitCode: 1}, // first unused match wins
		{cmd: cmd("git", "status"), stdout: "second"},
		{cmd: cmd("herdr", "pane", "get"), err: boom},
	}
	for i, tt := range tests {
		res, err := f.Run(context.Background(), tt.cmd)
		if !errors.Is(err, tt.err) {
			t.Fatalf("call %d: err = %v, want %v", i, err, tt.err)
		}
		if string(res.Stdout) != tt.stdout || res.ExitCode != tt.exitCode {
			t.Errorf("call %d: got stdout %q code %d", i, res.Stdout, res.ExitCode)
		}
		if res.Cmd.String() != tt.cmd.String() {
			t.Errorf("call %d: Result.Cmd = %s, want %s", i, res.Cmd, tt.cmd)
		}
	}

	if _, err := f.Run(context.Background(), cmd("git", "status")); err == nil || !strings.Contains(err.Error(), "unexpected command git status") {
		t.Errorf("exhausted responses: err = %v, want unexpected command", err)
	}
	if got := len(f.Calls()); got != 5 {
		t.Errorf("len(Calls()) = %d, want 5", got)
	}
}

func TestFakeFunc(t *testing.T) {
	f := &Fake{}
	f.On([]string{"a"}, Result{Stdout: []byte("scripted")}, nil)
	f.Func(func(c Cmd) (Result, error) {
		return Result{Stdout: []byte("fallback " + c.Name)}, nil
	})
	for _, want := range []string{"scripted", "fallback a", "fallback a"} {
		res, err := f.Run(context.Background(), cmd("a"))
		if err != nil || string(res.Stdout) != want {
			t.Errorf("got %q, %v; want %q", res.Stdout, err, want)
		}
	}
}

func TestFakeValidatesLikeExec(t *testing.T) {
	f := &Fake{}
	f.Func(func(Cmd) (Result, error) { return Result{}, nil })
	if _, err := f.Run(context.Background(), Cmd{Name: "git"}); err == nil {
		t.Error("missing timeout: want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Run(ctx, cmd("git")); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: err = %v, want context.Canceled", err)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("rejected calls were recorded: %v", f.Calls())
	}
}

func TestFakeConcurrent(t *testing.T) {
	f := &Fake{}
	f.Func(func(Cmd) (Result, error) { return Result{}, nil })
	const n = 50
	for range n {
		f.On([]string{"x"}, Result{}, nil)
	}
	var wg sync.WaitGroup
	for range 2 * n {
		wg.Go(func() {
			if _, err := f.Run(context.Background(), cmd("x")); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := len(f.Calls()); got != 2*n {
		t.Errorf("len(Calls()) = %d, want %d", got, 2*n)
	}
}
