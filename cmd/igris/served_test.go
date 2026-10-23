package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// End to end on the in-process serve (SPEC §14): arise starts igris serve
// and attaches, the owner leaves (detached, exit 0), the run goes on in
// serve, `igris attach` shows it and stops it, and serve ends with the
// run: lock and socket gone, serve.log tells the story.
func TestAriseServeAttachStop(t *testing.T) {
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
	var shown tui.Options
	code, output, root := noTUIRun{
		plan: oneTaskPlan, args: []string{"A"}, noSignal: true,
		ui: func(_ context.Context, o tui.Options) error {
			shown = o
			// The owner leaves once the task runs. The run starts after
			// this view has its snapshot, so a stop sent sooner ends
			// serve before the run started, which is another test.
			root := filepath.Dir(o.PlanPath)
			for deadline := time.Now().Add(10 * time.Second); planStatuses(t, root) != "A-1=in progress"; {
				if time.Now().After(deadline) {
					t.Error("A-1 never started")
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			return nil
		},
	}.run(t)
	if code != exitOK || !strings.Contains(output, "detached; the run goes on in igris serve") {
		t.Fatalf("arise exit %d; output:\n%s", code, output)
	}
	if !shown.Attached || shown.Version != igrisVersion() {
		t.Errorf("arise's view: attached %v, version %q", shown.Attached, shown.Version)
	}
	var out, errb bytes.Buffer
	if code := run([]string{"attach"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "stopped") {
		t.Fatalf("attach: exit %d, %q %q", code, out.String(), errb.String())
	}
	noSocket(t, root)
	lines, err := state.ServeLogTail(root, 50)
	if err != nil {
		t.Fatal(err)
	}
	log := strings.Join(lines, "\n")
	for _, want := range []string{"serving the run on", "Arise. phase A", "run stopped · stopped"} {
		if !strings.Contains(log, want) {
			t.Errorf("serve.log lacks %q:\n%s", want, log)
		}
	}
	if got := planStatuses(t, root); got != "A-1=in progress" {
		t.Errorf("statuses = %s, want the task left in progress for the next arise", got)
	}
}

// Bare igris reports a detached run as arise does.
func TestBareIgrisDetachedSaysTheRunGoesOn(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": dryPlan})
	fakeApp(t, true, tui.AppResult{Detached: true}, nil)
	var out, errb bytes.Buffer
	if code := run(nil, &out, &errb); code != exitOK || !strings.Contains(out.String(), "detached; the run goes on in igris serve") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}
