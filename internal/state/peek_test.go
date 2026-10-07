package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeState writes .igris/<name> under a fresh root and returns the root.
func writeState(t *testing.T, name, content string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(root, DirName, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestClassifyLock(t *testing.T) {
	alive := func(pid int) bool { return pid == 42 }
	tests := []struct {
		name string
		info LockInfo
		want lockClass
	}{
		{"live", LockInfo{PID: 42, Host: "here"}, lockLive},
		{"dead", LockInfo{PID: 7, Host: "here"}, lockStale},
		{"remote", LockInfo{PID: 42, Host: "there"}, lockRemote},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyLock(tt.info, "here", alive); got != tt.want {
				t.Errorf("classifyLock = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPeekLock(t *testing.T) {
	host := func() (string, error) { return "here", nil }
	alive := func(pid int) bool { return pid == 42 }
	tests := []struct {
		name    string
		content string
		want    LockState // Path and Info.StartedAt are checked separately
	}{
		{"none", "", LockState{}},
		{"live", lockJSON(42, "here"), LockState{Held: true, Alive: true}},
		{"stale", lockJSON(7, "here"), LockState{Held: true, Stale: true, Reason: "process no longer running"}},
		{"remote", lockJSON(42, "there"), LockState{Held: true, Remote: true}},
		{"garbage", "not json", LockState{Held: true, Unreadable: true}},
		{"missing pid", `{"host":"here"}`, LockState{Held: true, Unreadable: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeState(t, "igris.lock", tt.content)
			got, err := peekLock(root, host, alive)
			if err != nil {
				t.Fatal(err)
			}
			if got.Path != filepath.Join(root, DirName, "igris.lock") {
				t.Errorf("Path = %q", got.Path)
			}
			if got.Held != tt.want.Held || got.Alive != tt.want.Alive || got.Remote != tt.want.Remote ||
				got.Stale != tt.want.Stale || got.Unreadable != tt.want.Unreadable {
				t.Errorf("state = %+v, want %+v", got, tt.want)
			}
			if tt.want.Reason != "" && got.Reason != tt.want.Reason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.want.Reason)
			}
			if tt.want.Unreadable && got.Reason == "" {
				t.Error("unreadable lock has no Reason")
			}
			if (tt.want.Alive || tt.want.Stale || tt.want.Remote) && got.Info.PID == 0 {
				t.Error("Info not filled")
			}
		})
	}
}

func TestPeekLockHostnameError(t *testing.T) {
	root := writeState(t, "igris.lock", lockJSON(42, "here"))
	_, err := peekLock(root, func() (string, error) { return "", errors.New("boom") }, func(int) bool { return true })
	if err == nil {
		t.Fatal("want error")
	}
}

func TestPeekLockReadError(t *testing.T) {
	root := writeState(t, "", "")
	if err := os.Mkdir(filepath.Join(root, DirName, "igris.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PeekLock(root); err == nil {
		t.Fatal("want error for a lock path that can't be read")
	}
}

func TestPeekEvents(t *testing.T) {
	good := `{"at":"2026-10-06T12:00:00Z","type":"run_started"}` + "\n" +
		`{"at":"2026-10-06T12:01:00Z","type":"task_started","task":"T1"}` + "\n"
	tests := []struct {
		name    string
		content string
		want    int
		wantErr bool
	}{
		{"empty log", "\n", 0, false},
		{"two events", good, 2, false},
		{"truncated last line", good + `{"at":"2026-10-06T12:0`, 2, false},
		{"malformed terminated last line", good + "garbage\n", 2, false},
		{"malformed middle line", "garbage\n" + good, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeState(t, "runs.jsonl", tt.content)
			got, err := PeekEvents(root)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != tt.want {
				t.Errorf("got %d events, want %d", len(got), tt.want)
			}
		})
	}
}

func TestPeekNeverCreatesState(t *testing.T) {
	root := t.TempDir()
	if st, err := PeekLock(root); err != nil || st.Held {
		t.Errorf("PeekLock = %+v, %v", st, err)
	}
	if _, err := PeekEvents(root); !errors.Is(err, ErrNoLog) {
		t.Errorf("PeekEvents err = %v, want ErrNoLog", err)
	}
	if _, err := os.Stat(filepath.Join(root, DirName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".igris/ was created (stat err = %v)", err)
	}
}

func TestPeekSignals(t *testing.T) {
	root := t.TempDir()
	sigs, bad, err := PeekSignals(root)
	if err != nil || len(sigs) != 0 || len(bad) != 0 {
		t.Fatalf("no .igris: %v %v %v", sigs, bad, err)
	}
	dir := filepath.Join(root, DirName, "signals")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"B-2.json": `{"id":"B-2","action":"skip","note":"why","at":"2026-10-07T09:30:00Z"}`,
		"A-1.json": `{"id":"A-1","action":"done","note":"ok","at":"2026-10-07T09:30:00Z"}`,
		"C-3.json": `not json`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sigs, bad, err = PeekSignals(root)
	if err != nil || len(sigs) != 2 || sigs[0].ID != "A-1" || sigs[1].ID != "B-2" || len(bad) != 1 {
		t.Errorf("sigs %+v, bad %v, err %v", sigs, bad, err)
	}
}
