package state

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
)

const testUUID = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c"

func TestAgentStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadAgentState(root, testUUID); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if err := WriteAgentState(root, testUUID, AgentState{State: backend.Blocked, Event: "PermissionRequest", At: at}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadAgentState(root, testUUID)
	if err != nil || !ok || got.State != backend.Blocked || !got.At.Equal(at) {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	info, err := os.Stat(filepath.Join(AgentStateDir(root), testUUID+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, err %v", info.Mode(), err)
	}
	dirInfo, _ := os.Stat(AgentStateDir(root))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", dirInfo.Mode())
	}
}

// Sessions can write into .igris/: only a small regular file with a known
// state is accepted, and a FIFO never blocks.
func TestReadAgentStateUntrusted(t *testing.T) {
	setup := map[string]func(t *testing.T, path string){
		"fifo": func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skip("mkfifo:", err)
			}
		},
		"symlink": func(t *testing.T, path string) {
			target := filepath.Join(filepath.Dir(path), "target")
			_ = os.WriteFile(target, []byte(`{"state":"idle"}`), 0o600)
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
		"huge": func(t *testing.T, path string) {
			_ = os.WriteFile(path, []byte(`{"state":"idle","event":"`+strings.Repeat("x", 5000)+`"}`), 0o600)
		},
		"unknown state": func(t *testing.T, path string) {
			_ = os.WriteFile(path, []byte(`{"state":"done"}`), 0o600)
		},
		"garbage": func(t *testing.T, path string) {
			_ = os.WriteFile(path, []byte("\x00\x01"), 0o600)
		},
	}
	for name, fn := range setup {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := AgentStateDir(root)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			fn(t, filepath.Join(dir, testUUID+".json"))
			if s, ok, err := ReadAgentState(root, testUUID); err == nil || ok {
				t.Errorf("accepted: %+v ok=%v", s, ok)
			}
		})
	}
	if _, _, err := ReadAgentState(t.TempDir(), "../../etc/passwd"); err == nil {
		t.Error("accepted a path as session id")
	}
}

func TestWriteHooksFile(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	path, err := d.WriteHooksFile("P1-01", []byte(`{"hooks":{}}`))
	if err != nil || path != filepath.Join(HooksDir(root), "P1-01.settings.json") {
		t.Fatalf("path %q err %v", path, err)
	}
	if _, err := d.WriteHooksFile("../x", nil); err == nil {
		t.Error("accepted a path as task id")
	}
}

// PeekAgentState gives the whole record, and counts an unreadable file as
// no record.
func TestPeekAgentState(t *testing.T) {
	root := t.TempDir()
	if _, err := Open(root, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := PeekAgentState(root, testUUID); ok {
		t.Fatal("missing file: ok")
	}
	if _, ok := PeekAgentState(root, "not-a-uuid"); ok {
		t.Fatal("invalid uuid: ok")
	}
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if err := WriteAgentState(root, testUUID, AgentState{State: backend.Idle, Event: "SessionStart", At: at}); err != nil {
		t.Fatal(err)
	}
	got, ok := PeekAgentState(root, testUUID)
	if !ok || got.State != backend.Idle || got.Event != "SessionStart" || !got.At.Equal(at) {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	path := filepath.Join(AgentStateDir(root), testUUID+".json")
	if err := os.WriteFile(path, []byte(`{"state":"bogus","event":"SessionStart"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := PeekAgentState(root, testUUID); ok {
		t.Fatalf("bad state: got %+v ok", got)
	}
}
