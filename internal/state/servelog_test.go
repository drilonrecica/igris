package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeLog(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if lines, err := ServeLogTail(root, 3); err != nil || lines != nil {
		t.Errorf("tail of no log = %v, %v", lines, err)
	}
	for _, line := range []string{"one\n", "two\n"} {
		f, err := d.ServeLog()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	fi, err := os.Stat(d.ServeLogPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("serve.log mode %v, %v; want 0600", fi.Mode(), err)
	}
	if got, _ := os.ReadFile(d.ServeLogPath()); string(got) != "one\ntwo\n" {
		t.Errorf("serve.log = %q; a new serve appends", got)
	}
	if lines, err := ServeLogTail(root, 1); err != nil || strings.Join(lines, "|") != "two" {
		t.Errorf("tail 1 = %v, %v", lines, err)
	}
	if lines, err := ServeLogTail(root, 9); err != nil || strings.Join(lines, "|") != "one|two" {
		t.Errorf("tail 9 = %v, %v", lines, err)
	}
}

// A log over its limit is cut to its last limit bytes, from the first
// whole line on, when a serve starts.
func TestServeLogCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.log")
	if err := os.WriteFile(path, []byte("aaaa\nbbbb\ncccc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := openServeLog(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("d\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got, _ := os.ReadFile(path); string(got) != "cccc\nd\n" { //nolint:gosec // test fixture
		t.Errorf("serve.log = %q, want the last whole line and the new one", got)
	}
}

// A symlink in serve.log's place is refused both ways.
func TestServeLogRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, d.ServeLogPath()); err != nil {
		t.Skip(err)
	}
	if f, err := d.ServeLog(); err == nil {
		_ = f.Close()
		t.Error("ServeLog followed a symlink")
	}
	if lines, err := ServeLogTail(root, 1); err == nil {
		t.Errorf("ServeLogTail followed a symlink: %v", lines)
	}
}

// CheckLock says what Lock would do and takes nothing.
func TestCheckLock(t *testing.T) {
	d, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CheckLock(false); err != nil {
		t.Fatalf("CheckLock with no lock file = %v", err)
	}
	lock, err := d.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	var locked *LockedError
	if err := d.CheckLock(false); !errors.As(err, &locked) {
		t.Errorf("CheckLock while held = %v, want a *LockedError", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	if err := d.CheckLock(false); err != nil {
		t.Errorf("CheckLock after release = %v; it must not have taken the lock", err)
	}
	again, err := d.Lock(false)
	if err != nil {
		t.Fatalf("Lock after CheckLock = %v", err)
	}
	_ = again.Release()
}
