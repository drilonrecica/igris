package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// testDir opens a state dir with a fixed clock, host "here" and pid 100.
// Only the pids in alive count as running.
func testDir(t *testing.T, alive ...int) *Dir {
	t.Helper()
	d, err := Open(t.TempDir(), Options{Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	d.hostname = func() (string, error) { return "here", nil }
	d.pid = 100
	d.alive = func(pid int) bool {
		for _, p := range alive {
			if p == pid {
				return true
			}
		}
		return false
	}
	return d
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestOpenCreatesPrivateDirs(t *testing.T) {
	root := t.TempDir()
	// A pre-existing, too-open .igris/ is tightened.
	if err := os.Mkdir(filepath.Join(root, DirName), 0o755); err != nil { //nolint:gosec // the test checks it gets tightened
		t.Fatal(err)
	}
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{d.Path(), d.SignalsDir(), d.PromptsDir(), d.AdaptDir()} {
		if got := perm(t, dir); got != 0o700 {
			t.Errorf("%s perm = %o, want 700", dir, got)
		}
	}
	if d.Root() != root || d.Path() != filepath.Join(root, ".igris") {
		t.Errorf("Root/Path = %s, %s", d.Root(), d.Path())
	}
	// Opening again is fine.
	if _, err := Open(root, Options{}); err != nil {
		t.Errorf("second Open: %v", err)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	for _, content := range []string{"one", "two"} {
		if err := writeFileAtomic(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path) //nolint:gosec // test temp dir
		if err != nil || string(got) != content {
			t.Fatalf("content = %q, %v; want %q", got, err, content)
		}
		if p := perm(t, path); p != 0o600 {
			t.Errorf("perm = %o, want 600", p)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want only f.json (temp files left behind)", len(entries))
	}
}

func writeLock(t *testing.T, d *Dir, content string) {
	t.Helper()
	if err := os.WriteFile(d.lockPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func lockJSON(pid int, host string) string {
	b, _ := json.Marshal(LockInfo{PID: pid, Host: host, StartedAt: t0.Add(-time.Hour)})
	return string(b)
}

func TestLock(t *testing.T) {
	tests := []struct {
		name     string
		existing string // lock file content; "" = none
		alive    []int
		force    bool
		wantErr  any // nil, *LockedError or *StaleLockError
		remote   bool
	}{
		{name: "fresh"},
		{name: "live process", existing: lockJSON(42, "here"), alive: []int{42}, wantErr: &LockedError{}},
		{name: "live process with force", existing: lockJSON(42, "here"), alive: []int{42}, force: true, wantErr: &LockedError{}},
		{name: "dead process", existing: lockJSON(42, "here"), wantErr: &StaleLockError{}},
		{name: "dead process with force", existing: lockJSON(42, "here"), force: true},
		{name: "corrupt file", existing: "{nope", wantErr: &StaleLockError{}},
		{name: "empty file", existing: "{}", wantErr: &StaleLockError{}},
		{name: "corrupt file with force", existing: "{nope", force: true},
		{name: "other host", existing: lockJSON(42, "there"), alive: []int{42}, wantErr: &LockedError{}, remote: true},
		{name: "other host with force", existing: lockJSON(42, "there"), alive: []int{42}, force: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := testDir(t, tt.alive...)
			if tt.existing != "" {
				writeLock(t, d, tt.existing)
			}
			l, err := d.Lock(tt.force)
			switch tt.wantErr.(type) {
			case nil:
				if err != nil {
					t.Fatalf("Lock: %v", err)
				}
				info, rerr := readLock(d.lockPath())
				if rerr != nil || info != (LockInfo{PID: 100, Host: "here", StartedAt: t0}) || l.Info() != info {
					t.Errorf("lock file = %+v, %v; Info = %+v", info, rerr, l.Info())
				}
				if p := perm(t, d.lockPath()); p != 0o600 {
					t.Errorf("lock perm = %o, want 600", p)
				}
			case *LockedError:
				var le *LockedError
				if !errors.As(err, &le) {
					t.Fatalf("Lock error = %v, want *LockedError", err)
				}
				if le.Remote != tt.remote || le.Info.PID != 42 {
					t.Errorf("LockedError = %+v", le)
				}
			case *StaleLockError:
				var se *StaleLockError
				if !errors.As(err, &se) {
					t.Fatalf("Lock error = %v, want *StaleLockError", err)
				}
				if !strings.Contains(err.Error(), "--force-unlock") {
					t.Errorf("error %q doesn't mention --force-unlock", err)
				}
			}
			if tt.wantErr != nil {
				// A refused lock leaves the existing file alone.
				got, _ := os.ReadFile(d.lockPath())
				if string(got) != tt.existing {
					t.Errorf("lock file changed to %q", got)
				}
			}
		})
	}
}

// The lock file is complete the moment it exists, so another igris never
// reads it half-written, and the temp file it came from is cleaned up.
func TestLockFileAppearsComplete(t *testing.T) {
	d := testDir(t)
	if err := createExclusive(d.lockPath(), []byte("full content")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(d.lockPath()); string(got) != "full content" {
		t.Errorf("lock = %q", got)
	}
	if err := createExclusive(d.lockPath(), []byte("second")); !errors.Is(err, fs.ErrExist) {
		t.Errorf("second create err = %v, want ErrExist", err)
	}
	if got, _ := os.ReadFile(d.lockPath()); string(got) != "full content" {
		t.Errorf("lock after refused create = %q", got)
	}
	entries, _ := os.ReadDir(d.Path())
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
	if err := createDirect(d.lockPath(), []byte("x")); !errors.Is(err, fs.ErrExist) {
		t.Errorf("fallback create err = %v, want ErrExist", err)
	}
}

func TestLockedErrorNamesTheFile(t *testing.T) {
	d := testDir(t, 42)
	writeLock(t, d, lockJSON(42, "here"))
	_, err := d.Lock(false)
	if err == nil || !strings.Contains(err.Error(), d.lockPath()) || !strings.Contains(err.Error(), "not igris") {
		t.Errorf("err = %v, want the lock path and the PID-reuse hint", err)
	}
}

func TestLockRelease(t *testing.T) {
	d := testDir(t, 100) // our own pid counts as alive
	l, err := d.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Lock(false); !errors.As(err, new(*LockedError)) {
		t.Fatalf("second Lock = %v, want *LockedError", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.lockPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock file still there after Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Errorf("second Release = %v, want nil", err)
	}

	// A lock re-taken by someone else is not removed by the old holder.
	l2, err := d.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	other := lockJSON(42, "here")
	writeLock(t, d, other)
	if err := l2.Release(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(d.lockPath()); string(got) != other {
		t.Errorf("Release removed or changed someone else's lock: %q", got)
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("own process not alive")
	}
	if processAlive(0) || processAlive(-1) {
		t.Error("pid <= 0 counted as alive")
	}
}

func TestRunRoundTrip(t *testing.T) {
	d := testDir(t)
	if _, err := d.LoadRun(); !errors.Is(err, ErrNoRun) {
		t.Fatalf("LoadRun on empty dir = %v, want ErrNoRun", err)
	}
	want := &Run{
		StartedAt:  t0,
		Phases:     []string{"M1", "M2"},
		Through:    "M2",
		ConfigHash: "abc123",
		Current: &Current{
			TaskID:         "M2-01",
			Mode:           "accept",
			ClaudeSession:  "0b8f5c1e-0000-4000-8000-000000000001",
			Session:        &backend.SessionRef{Backend: "herdr", TabID: "t1", PaneID: "p1", Agent: "igris-m2-01"},
			VerifyAttempts: 2,
			StartedAt:      t0.Add(time.Minute),
		},
	}
	if err := d.SaveRun(want); err != nil {
		t.Fatal(err)
	}
	if p := perm(t, d.runPath()); p != 0o600 {
		t.Errorf("state.json perm = %o, want 600", p)
	}
	got, err := d.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || got.Version != 1 {
		t.Errorf("LoadRun = %+v\nwant %+v", got, want)
	}

	// Between tasks Current is cleared.
	want.Current = nil
	if err := d.SaveRun(want); err != nil {
		t.Fatal(err)
	}
	if got, err := d.LoadRun(); err != nil || got.Current != nil {
		t.Errorf("LoadRun after clearing Current = %+v, %v", got, err)
	}
}

func TestLoadRunErrors(t *testing.T) {
	for name, content := range map[string]string{
		"bad json":    "{",
		"bad version": `{"version": 99}`,
		"no version":  `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			d := testDir(t)
			if err := os.WriteFile(d.runPath(), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := d.LoadRun()
			if err == nil || errors.Is(err, ErrNoRun) || !strings.Contains(err.Error(), "delete the file") {
				t.Errorf("LoadRun = %v, want an error saying what to do", err)
			}
		})
	}
}

func TestRunLog(t *testing.T) {
	d := testDir(t)
	if events, err := d.Events(); err != nil || len(events) != 0 {
		t.Fatalf("Events on empty log = %v, %v", events, err)
	}
	in := []Event{
		{Type: EventRunStarted, Detail: "phases M2"},
		{Type: EventTaskStarted, Task: "M2-01", Rank: "opus", Model: "opus"},
		{At: t0.Add(time.Minute).In(time.FixedZone("CEST", 2*3600)), Type: EventTaskDone, Task: "M2-01", Detail: "note"},
	}
	for _, e := range in {
		if err := d.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if p := perm(t, d.eventsPath()); p != 0o600 {
		t.Errorf("runs.jsonl perm = %o, want 600", p)
	}
	got, err := d.Events()
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{At: t0, Type: EventRunStarted, Detail: "phases M2"},
		{At: t0, Type: EventTaskStarted, Task: "M2-01", Rank: "opus", Model: "opus"},
		{At: t0.Add(time.Minute), Type: EventTaskDone, Task: "M2-01", Detail: "note"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Events = %+v\nwant %+v", got, want)
	}

	data, _ := os.ReadFile(d.eventsPath())
	if n := strings.Count(string(data), "\n"); n != 3 {
		t.Errorf("runs.jsonl has %d lines, want 3:\n%s", n, data)
	}
}

func TestRunLogTruncatedTail(t *testing.T) {
	d := testDir(t)
	if err := d.Append(Event{Type: EventRunStarted}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(d.eventsPath(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"at":"2026-`)
	_ = f.Close()
	events, err := d.Events()
	if err != nil || len(events) != 1 {
		t.Errorf("Events with truncated tail = %v, %v; want the one complete event", events, err)
	}

	// A corrupt line in the middle is an error.
	if err := os.WriteFile(d.eventsPath(), []byte("{bad\n{\"type\":\"error\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Events(); err == nil || !strings.Contains(err.Error(), ":1:") {
		t.Errorf("Events with corrupt line = %v, want error naming line 1", err)
	}
}

func TestWriteAdaptFile(t *testing.T) {
	d, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	path, err := d.WriteAdaptFile("tasks.bak.md", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(d.AdaptDir(), "tasks.bak.md") {
		t.Errorf("path = %s", path)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("stat = %v, %v; want 0600", fi, err)
	}
	for _, bad := range []string{"", "..", "../x.md", "a/b.md"} {
		if _, err := d.WriteAdaptFile(bad, nil); err == nil {
			t.Errorf("WriteAdaptFile(%q) succeeded", bad)
		}
	}
}
