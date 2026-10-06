package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const helperEnv = "IGRIS_RUNNER_HELPER"

// TestHelperProcess is not a real test: helper() re-runs the test binary
// into it so the Exec tests don't depend on system tools.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	switch args[0] {
	case "args":
		for _, a := range args[1:] {
			fmt.Println(a)
		}
	case "out": // out STDOUT STDERR CODE
		fmt.Print(args[1])
		fmt.Fprint(os.Stderr, args[2])
		code, _ := strconv.Atoi(args[3])
		os.Exit(code)
	case "pwd":
		wd, _ := os.Getwd()
		fmt.Print(wd)
	case "env":
		fmt.Print(os.Getenv(args[1]))
	case "cat":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "sleep":
		fmt.Print("started")
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func helper(t *testing.T, args ...string) Cmd {
	t.Helper()
	return Cmd{
		Name:    os.Args[0],
		Args:    append([]string{"-test.run=^TestHelperProcess$", "--"}, args...),
		Env:     append(os.Environ(), helperEnv+"=1"),
		Timeout: 10 * time.Second,
	}
}

func TestExecOutputAndExitCode(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		wantErr  bool
		contains string
	}{
		{name: "success", code: 0},
		{name: "failure", code: 3, wantErr: true, contains: "exit status 3: some error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Exec{}.Run(context.Background(), helper(t, "out", "some output", "some error", strconv.Itoa(tt.code)))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := string(res.Stdout); got != "some output" {
				t.Errorf("Stdout = %q", got)
			}
			if got := string(res.Stderr); got != "some error" {
				t.Errorf("Stderr = %q", got)
			}
			if res.ExitCode != tt.code {
				t.Errorf("ExitCode = %d, want %d", res.ExitCode, tt.code)
			}
			exitErr := res.Err()
			if (exitErr != nil) != tt.wantErr {
				t.Fatalf("Err() = %v, wantErr %v", exitErr, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}
			var ee *ExitError
			if !errors.As(exitErr, &ee) || ee.Code != tt.code {
				t.Errorf("Err() = %#v, want *ExitError with code %d", exitErr, tt.code)
			}
			if msg := exitErr.Error(); !strings.Contains(msg, tt.contains) || !strings.Contains(msg, "TestHelperProcess") {
				t.Errorf("Err() message %q lacks command or %q", msg, tt.contains)
			}
		})
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 10}
	for i := range 7 {
		if _, err := fmt.Fprintf(b, "%d2345", i); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(b.Bytes()); got != "5234562345" {
		t.Errorf("Bytes = %q, want the last 10 bytes", got)
	}
	if len(b.buf) > 2*b.max {
		t.Errorf("buffer holds %d bytes, want at most %d", len(b.buf), 2*b.max)
	}
	small := &tailBuffer{max: 10}
	_, _ = small.Write([]byte("abc"))
	if got := string(small.Bytes()); got != "abc" {
		t.Errorf("Bytes = %q", got)
	}
}

func TestExecArgvIsNotShellInterpreted(t *testing.T) {
	args := []string{"$(echo pwned); rm -rf x", "a b", "`id`", "*", "line1\nline2", ""}
	res, err := Exec{}.Run(context.Background(), helper(t, append([]string{"args"}, args...)...))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := strings.Join(args, "\n") + "\n"
	if got := string(res.Stdout); got != want {
		t.Errorf("Stdout = %q, want %q", got, want)
	}
}

func TestExecDirEnvStdin(t *testing.T) {
	dir := t.TempDir()

	c := helper(t, "pwd")
	c.Dir = dir
	res, err := Exec{}.Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run pwd: %v", err)
	}
	if got := string(res.Stdout); got != dir {
		t.Errorf("pwd = %q, want %q", got, dir)
	}

	c = helper(t, "env", "IGRIS_TEST_VAR")
	c.Env = append(c.Env, "IGRIS_TEST_VAR=value")
	res, err = Exec{}.Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run env: %v", err)
	}
	if got := string(res.Stdout); got != "value" {
		t.Errorf("env = %q, want %q", got, "value")
	}

	c = helper(t, "cat")
	c.Stdin = strings.NewReader("from stdin")
	res, err = Exec{}.Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run cat: %v", err)
	}
	if got := string(res.Stdout); got != "from stdin" {
		t.Errorf("cat = %q, want %q", got, "from stdin")
	}
}

func TestExecTimeout(t *testing.T) {
	c := helper(t, "sleep")
	c.Timeout = 200 * time.Millisecond
	res, err := Exec{}.Run(context.Background(), c)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if got := string(res.Stdout); got != "started" {
		t.Errorf("partial Stdout = %q, want %q", got, "started")
	}
}

// A timed-out `sh -c` must take its children down too. If only sh were
// killed, the backgrounded sleep would hold the output pipe open and Run
// would only return after waitDelay.
func TestExecTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	_, err := Exec{}.Run(context.Background(), Shell("sleep 30 & wait", "", 200*time.Millisecond))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed >= waitDelay {
		t.Errorf("Run took %s; child process outlived the timeout", elapsed)
	}
}

func TestExecParentCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Exec{}.Run(ctx, helper(t, "sleep"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, must not be ErrTimeout", err)
	}
}

func TestExecShell(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), Shell("echo out; echo err >&2; exit 4", "", 10*time.Second))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "out\n" || string(res.Stderr) != "err\n" || res.ExitCode != 4 {
		t.Errorf("got stdout %q stderr %q code %d", res.Stdout, res.Stderr, res.ExitCode)
	}
}

func TestExecCombinedOutput(t *testing.T) {
	c := Shell("echo one; echo two >&2; echo three", "", 10*time.Second)
	c.Combined = true
	res, err := Exec{}.Run(context.Background(), c)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "one\ntwo\nthree\n" || len(res.Stderr) != 0 {
		t.Errorf("got stdout %q stderr %q, want all output interleaved in stdout", res.Stdout, res.Stderr)
	}
}

func TestExecInvalidCommands(t *testing.T) {
	tests := []struct {
		name     string
		cmd      Cmd
		contains string
	}{
		{name: "empty name", cmd: Cmd{Timeout: time.Second}, contains: "empty program name"},
		{name: "no timeout", cmd: Cmd{Name: "true"}, contains: "no timeout"},
		{name: "not found", cmd: Cmd{Name: "igris-no-such-binary", Timeout: time.Second}, contains: "not found in PATH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Exec{}.Run(context.Background(), tt.cmd)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("err = %v, want it to contain %q", err, tt.contains)
			}
		})
	}
}

func TestShell(t *testing.T) {
	c := Shell("make test", "/project", time.Minute)
	if c.Name != "sh" || len(c.Args) != 2 || c.Args[0] != "-c" || c.Args[1] != "make test" || c.Dir != "/project" || c.Timeout != time.Minute {
		t.Errorf("Shell = %#v", c)
	}
}

func TestCmdString(t *testing.T) {
	tests := []struct {
		cmd  Cmd
		want string
	}{
		{Cmd{Name: "git", Args: []string{"status"}}, "git status"},
		{Cmd{Name: "git", Args: []string{"commit", "-m", "M0-05: it's done"}}, `git commit -m 'M0-05: it'\''s done'`},
		{Cmd{Name: "herdr", Args: []string{""}}, "herdr ''"},
	}
	for _, tt := range tests {
		if got := tt.cmd.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func TestExitErrorTrimsStderr(t *testing.T) {
	stderr := "1\n2\n3\n4\n5\n6\n7\n"
	err := Result{Cmd: Cmd{Name: "x"}, ExitCode: 1, Stderr: []byte(stderr)}.Err()
	if got, want := err.Error(), "x: exit status 1: 3\n4\n5\n6\n7"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
