package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

const servePlan = `## A

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| A-1 | **One** | — | ready | sonnet | agent |
`

// withServeTerminal says whether serve's stdin is a terminal.
func withServeTerminal(t *testing.T, tty bool) {
	t.Helper()
	saved := serveTerminal
	t.Cleanup(func() { serveTerminal = saved })
	serveTerminal = func() bool { return tty }
}

// stopResult is one `igris stop` run while serve runs.
type stopResult struct {
	code     int
	out, err string
}

// stopAction runs `igris stop` and hands its result to ch.
func stopAction(ch chan<- stopResult) func() {
	return func() {
		var out, errb bytes.Buffer
		code := run([]string{"stop"}, &out, &errb)
		ch <- stopResult{code, out.String(), errb.String()}
	}
}

// noSocket fails when the run's socket outlived serve.
func noSocket(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(state.SocketPath(root)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the socket outlived serve: %v", err)
	}
	if l, err := state.PeekLock(root); err != nil || l.Held {
		t.Errorf("the lock outlived serve: %+v, %v", l, err)
	}
}

// serve runs the phase to its end, logging plain lines and hosting the run
// on the socket while it runs (SPEC §14).
func TestServe(t *testing.T) {
	code, got, root := noTUIRun{cmd: "serve", plan: servePlan, args: []string{"A"}}.run(t)
	if code != exitOK {
		t.Fatalf("exit %d; output:\n%s", code, got)
	}
	for _, w := range []string{
		"serving the run on " + state.SocketPath(root),
		"run started: phase A",
		"A-1 done · did A-1",
		"run stopped: completed",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
	if got := planStatuses(t, root); got != "A-1=done" {
		t.Errorf("statuses = %s", got)
	}
	noSocket(t, root)
}

// igris stop stops the run over the socket and waits until it has; the
// session stays open and serve exits 0.
func TestServeStop(t *testing.T) {
	stopped := make(chan stopResult, 1)
	code, got, root := noTUIRun{
		cmd: "serve", plan: servePlan, args: []string{"A"}, noSignal: true,
		actions: map[string]func(){"A-1 session open": stopAction(stopped)},
	}.run(t)
	if code != exitOK {
		t.Fatalf("serve exit %d; output:\n%s", code, got)
	}
	s := <-stopped
	if s.code != exitOK || !strings.Contains(s.out, "the run stopped") {
		t.Errorf("stop: exit %d, %q %q", s.code, s.out, s.err)
	}
	if !strings.Contains(got, "run stopped: stopped") {
		t.Errorf("output:\n%s", got)
	}
	if got := planStatuses(t, root); got != "A-1=in progress" {
		t.Errorf("statuses = %s", got)
	}
	noSocket(t, root)
}

// A stop while serve waits on a start-up question ends serve: nothing was
// started, which is the owner's stop (exit 0), and stop sees it end.
func TestServeStopBeforeTheRun(t *testing.T) {
	const drift = servePlan + "| A-2 | **Two** | A-1 | ready | sonnet | agent |\n"
	stopped := make(chan stopResult, 1)
	code, got, root := noTUIRun{
		cmd: "serve", plan: drift, args: []string{"A"},
		actions: map[string]func(){"Let igris fix them?": stopAction(stopped)},
	}.run(t)
	if code != exitOK || !strings.Contains(got, "before the run started; nothing was started") {
		t.Fatalf("serve exit %d; output:\n%s", code, got)
	}
	if s := <-stopped; s.code != exitOK {
		t.Errorf("stop: exit %d, %q %q", s.code, s.out, s.err)
	}
	if got := planStatuses(t, root); got != "A-1=ready A-2=ready" {
		t.Errorf("statuses = %s", got)
	}
	noSocket(t, root)
}

// Skip-permissions needs the typed phrase on a terminal: without one
// serve refuses (exit 1), and there is no flag around it (SPEC §7.3).
func TestServeYolo(t *testing.T) {
	t.Run("refused without a terminal", func(t *testing.T) {
		withServeTerminal(t, false)
		code, got, root := noTUIRun{cmd: "serve", plan: servePlan, args: []string{"A", "--mode", "yolo"},
			replies: [][2]string{{"Type \"skip permissions\"", "skip permissions"}}}.run(t)
		if code != exitFail || !strings.Contains(got, "stdin is not a terminal") || strings.Contains(got, "Type \"skip permissions\"") {
			t.Errorf("exit %d; output:\n%s", code, got)
		}
		if got := planStatuses(t, root); got != "A-1=ready" {
			t.Errorf("statuses = %s", got)
		}
		noSocket(t, root)
	})
	t.Run("typed on a terminal", func(t *testing.T) {
		withServeTerminal(t, true)
		code, got, _ := noTUIRun{cmd: "serve", plan: servePlan, args: []string{"A", "--mode", "yolo"},
			replies: [][2]string{{"Type \"skip permissions\"", "skip permissions"}}}.run(t)
		if code != exitOK || !strings.Contains(got, "A-1 session open [SKIP PERMISSIONS]") {
			t.Errorf("exit %d; output:\n%s", code, got)
		}
	})
	t.Run("wrong phrase on a terminal", func(t *testing.T) {
		withServeTerminal(t, true)
		code, got, _ := noTUIRun{cmd: "serve", plan: servePlan, args: []string{"A", "--mode", "yolo"},
			replies: [][2]string{{"Type \"skip permissions\"", "y"}}}.run(t)
		if code != exitFail || !strings.Contains(got, "skip-permissions mode not confirmed") {
			t.Errorf("exit %d; output:\n%s", code, got)
		}
	})
}

// One serve per project: a live run's lock is refused before the socket
// is touched.
func TestServeLockHeld(t *testing.T) {
	code, got, _ := noTUIRun{cmd: "serve", plan: servePlan, args: []string{"A"}, before: func(root string) {
		d, err := state.Open(root, state.Options{})
		if err != nil {
			panic(err)
		}
		if _, err := d.Lock(false); err != nil { // this live process
			panic(err)
		}
	}}.run(t)
	if code != exitFail || !strings.Contains(got, "igris serve:") {
		t.Errorf("exit %d; output:\n%s", code, got)
	}
}

func TestStopWithoutRun(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": servePlan})
	var out, errb bytes.Buffer
	if code := run([]string{"stop"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), serve.ErrNotServed.Error()) {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

func TestServeStopUsage(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": servePlan})
	for _, args := range [][]string{
		{"serve", "--no-tui"}, {"serve", "--dry-run"}, {"serve", "A", "B"}, {"serve", "--mode", "wild"},
		{"serve", "--only", "A-1", "--from", "A-1"}, {"stop", "A"}, {"stop", "--force"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: exit %d, want %d; stderr %q", args, code, exitUsage, errb.String())
		}
	}
}
