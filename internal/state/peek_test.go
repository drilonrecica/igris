package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestPeekLog(t *testing.T) {
	good := `{"at":"2026-10-06T12:00:00Z","type":"run_started"}` + "\n" +
		`{"at":"2026-10-06T12:01:00Z","type":"task_started","task":"T1"}` + "\n"
	long := `{"at":"2026-10-06T12:02:00Z","type":"error","detail":"` + strings.Repeat("x", MaxLogLine) + `"}` + "\n"
	tests := []struct {
		name       string
		content    string
		want       int
		unreadable int
	}{
		{"empty log", "\n", 0, 0},
		{"two events", good, 2, 0},
		{"truncated last line", good + `{"at":"2026-10-06T12:0`, 2, 0},
		{"malformed terminated last line", good + "garbage\n", 2, 1},
		{"malformed middle line", "garbage\n" + good, 2, 1},
		// A cut-off line with the next one glued to it (before Append
		// started a new line itself).
		{"glued lines", `{"v":1,"at":"2026-10-08T10:00:00Z","type":"run_sta` + good, 1, 1},
		{"line over 1 MiB", good + long + good, 4, 1},
		{"v1 field of the wrong type", `{"v":1,"at":"2026-10-06T12:00:00Z","type":"task_started","attempt":"x"}` + "\n" + good, 2, 1},
		{"v2 field of another type", `{"v":2,"at":"2026-10-06T12:00:00Z","type":"task_started","task":"T2","attempt":{"n":1}}` + "\n" + good, 3, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeState(t, "runs.jsonl", tt.content)
			got, err := PeekLog(root)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if len(got.Events) != tt.want || got.Unreadable != tt.unreadable {
				t.Errorf("got %d events, %d unreadable; want %d, %d", len(got.Events), got.Unreadable, tt.want, tt.unreadable)
			}
		})
	}
	root := writeState(t, "runs.jsonl", `{"v":2,"at":"2026-10-06T12:00:00Z","type":"task_started","task":"T2","attempt":{"n":1}}`+"\n")
	if got, err := PeekEvents(root); err != nil || len(got) != 1 || got[0].Task != "T2" || got[0].Attempt != 0 || got[0].V != 2 {
		t.Errorf("v2 line = %+v, %v; want its task kept and the attempt dropped", got, err)
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

// Values a run can't have are unknown when read (SPEC §13).
func TestEventChecked(t *testing.T) {
	for _, tc := range []struct {
		in, want Event
	}{
		{Event{DurationMS: -5}, Event{}},
		{Event{DurationMS: 9223372036854775807}, Event{}},
		{Event{DurationMS: maxDurationMS}, Event{DurationMS: maxDurationMS}},
		{Event{DurationMS: maxDurationMS + 1}, Event{}},
		{Event{Attempt: -1, Reason: "nope", Run: "x", Session: "y"}, Event{Reason: ReasonOther}},
	} {
		if got := tc.in.checked(); got != tc.want {
			t.Errorf("%+v checked = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}
