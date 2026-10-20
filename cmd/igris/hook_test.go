package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

// `igris hook` records the state, prints nothing and exits 0, and still
// exits 0 on input it can't use (SPEC §6.3).
func TestHookCommand(t *testing.T) {
	root := t.TempDir()
	if _, err := state.Open(root, state.Options{}); err != nil {
		t.Fatal(err)
	}
	const id = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"
	var out, errOut bytes.Buffer
	if code := execHook([]string{"--root", root, "Stop"}, strings.NewReader(`{"session_id":"`+id+`","hook_event_name":"Stop"}`), io.Discard); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if s, ok, err := state.ReadAgentState(root, id); err != nil || !ok || s.State != "idle" {
		t.Errorf("state %+v ok=%v err=%v", s, ok, err)
	}
	if code := run([]string{"hook", "--root", root, "Stop"}, &out, &errOut); code != exitOK {
		t.Errorf("garbage stdin: exit %d", code)
	}
	if code := execHook([]string{"Stop"}, strings.NewReader("{"), io.Discard); code != exitOK {
		t.Errorf("no root: exit %d", code)
	}
	if out.Len()+errOut.Len() != 0 {
		t.Errorf("printed %q %q", out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(root, state.DirName, "agent-state", id+".json")); err != nil {
		t.Error(err)
	}
}

// `igris hook plan-approve --mode M --wait D` waits for igris's decision
// while igris holds the lock, and prints Claude Code's answer (SPEC §6.3).
func TestHookPlanApproveCommand(t *testing.T) {
	root := t.TempDir()
	d, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := d.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	const id = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"
	go func() {
		for i := 0; i < 5000; i++ {
			if r, ok, _ := state.ReadPlanRequest(root, id); ok {
				_ = state.WritePlanDecision(root, id, state.PlanDecision{N: r.N, Behavior: state.PlanAllow})
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	in := `{"session_id":"` + id + `","hook_event_name":"PermissionRequest","tool_name":"ExitPlanMode","tool_input":{"plan":"p"}}`
	var out bytes.Buffer
	if code := execHook([]string{"--root", root, "plan-approve", "--mode", "auto", "--wait", "1m"}, strings.NewReader(in), &out); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), `"behavior":"allow"`) || !strings.Contains(out.String(), `"mode":"auto"`) {
		t.Errorf("printed %q", out.String())
	}
	// A bad wait prints nothing.
	out.Reset()
	if code := execHook([]string{"--root", root, "plan-approve", "--mode", "auto", "--wait", "soon"}, strings.NewReader(in), &out); code != exitOK || out.Len() != 0 {
		t.Errorf("bad wait: exit %d, printed %q", code, out.String())
	}
}
