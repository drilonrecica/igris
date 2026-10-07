package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if code := execHook([]string{"--root", root, "Stop"}, strings.NewReader(`{"session_id":"`+id+`","hook_event_name":"Stop"}`)); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if s, ok, err := state.ReadAgentState(root, id); err != nil || !ok || s.State != "idle" {
		t.Errorf("state %+v ok=%v err=%v", s, ok, err)
	}
	if code := run([]string{"hook", "--root", root, "Stop"}, &out, &errOut); code != exitOK {
		t.Errorf("garbage stdin: exit %d", code)
	}
	if code := execHook([]string{"Stop"}, strings.NewReader("{")); code != exitOK {
		t.Errorf("no root: exit %d", code)
	}
	if out.Len()+errOut.Len() != 0 {
		t.Errorf("printed %q %q", out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(root, state.DirName, "agent-state", id+".json")); err != nil {
		t.Error(err)
	}
}
