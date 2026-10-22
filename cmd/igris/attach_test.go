package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/tui"
)

// withTerminal says whether igris runs on a terminal it can draw on.
func withTerminal(t *testing.T, tty bool) {
	t.Helper()
	saved := interactiveTerm
	t.Cleanup(func() { interactiveTerm = saved })
	interactiveTerm = func() bool { return tty }
}

// withAttachUI replaces the attached run view.
func withAttachUI(t *testing.T, ui func(context.Context, tui.Options) error) {
	t.Helper()
	saved := attachUI
	t.Cleanup(func() { attachUI = saved })
	attachUI = ui
}

// attachAction runs `igris attach` and hands its result to ch.
func attachAction(ch chan<- stopResult) func() {
	return func() {
		var out, errb bytes.Buffer
		code := run([]string{"attach"}, &out, &errb)
		ch <- stopResult{code, out.String(), errb.String()}
	}
}

// checkAttached fails unless o is the run view of a served run, started
// from its snapshot.
func checkAttached(t *testing.T, o tui.Options) {
	t.Helper()
	if _, ok := o.Sender.(*serve.Client); !ok || !o.Attached || o.Version != igrisVersion() {
		t.Errorf("options: sender %T, attached %v, version %q", o.Sender, o.Attached, o.Version)
	}
	select {
	case <-o.Feed.Started():
	default:
		t.Error("the view starts without serve's snapshot")
	}
}

// igris attach shows the served run, and its commands reach it: Stop run
// stops it, and attach reports how it ended.
func TestAttachStopsTheRun(t *testing.T) {
	withTerminal(t, true)
	withAttachUI(t, func(_ context.Context, o tui.Options) error {
		checkAttached(t, o)
		o.Sender.Send(engine.Command{Kind: engine.CmdStop})
		select {
		case <-o.Feed.Ended():
		case <-time.After(10 * time.Second):
			t.Error("the run did not end")
		}
		return nil
	})
	attached := make(chan stopResult, 1)
	code, got, _ := noTUIRun{
		cmd: "serve", plan: servePlan, args: []string{"A"}, noSignal: true,
		actions: map[string]func(){"A-1 session open": attachAction(attached)},
	}.run(t)
	if code != exitOK || !strings.Contains(got, "run stopped: stopped") {
		t.Fatalf("serve exit %d; output:\n%s", code, got)
	}
	a := <-attached
	if a.code != exitOK || !strings.Contains(a.out, "stopped") || strings.Contains(a.out, "detached") {
		t.Errorf("attach: exit %d, %q %q", a.code, a.out, a.err)
	}
}

// Leaving the view detaches: the run goes on until igris stop.
func TestAttachDetaches(t *testing.T) {
	withTerminal(t, true)
	withAttachUI(t, func(_ context.Context, o tui.Options) error {
		checkAttached(t, o)
		return nil
	})
	attached, stopped := make(chan stopResult, 1), make(chan stopResult, 1)
	code, got, _ := noTUIRun{
		cmd: "serve", plan: servePlan, args: []string{"A"}, noSignal: true,
		actions: map[string]func(){"A-1 session open": func() {
			attachAction(attached)()
			stopAction(stopped)()
		}},
	}.run(t)
	if code != exitOK {
		t.Fatalf("serve exit %d; output:\n%s", code, got)
	}
	if a := <-attached; a.code != exitOK || !strings.Contains(a.out, "detached; the run goes on") {
		t.Errorf("attach: exit %d, %q %q", a.code, a.out, a.err)
	}
	if s := <-stopped; s.code != exitOK {
		t.Errorf("stop after detaching: exit %d, %q", s.code, s.err)
	}
}

func TestAttachWithoutRun(t *testing.T) {
	withTerminal(t, true)
	withAttachUI(t, func(context.Context, tui.Options) error {
		t.Error("the view opened without a run")
		return nil
	})
	writeProject(t, map[string]string{"tasks.md": servePlan})
	var out, errb bytes.Buffer
	if code := run([]string{"attach"}, &out, &errb); code != exitFail || !strings.Contains(errb.String(), serve.ErrNotServed.Error()) {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

// attach needs a terminal (2) and takes no arguments (2).
func TestAttachUsage(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": servePlan})
	withTerminal(t, false)
	var out, errb bytes.Buffer
	if code := run([]string{"attach"}, &out, &errb); code != exitUsage || !strings.Contains(errb.String(), "needs a terminal") {
		t.Errorf("no terminal: exit %d, stderr %q", code, errb.String())
	}
	withTerminal(t, true)
	for _, args := range [][]string{{"attach", "A"}, {"attach", "--force"}} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: exit %d, want %d; stderr %q", args, code, exitUsage, errb.String())
		}
	}
}
