package state

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func regEnv(t *testing.T) RegistryEnv {
	t.Helper()
	return RegistryEnv{RuntimeDir: t.TempDir(), TempDir: t.TempDir()}
}

func openState(t *testing.T) *Dir {
	t.Helper()
	d, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func yes(context.Context, string) bool { return true }
func alive(int) bool                   { return true }

func TestRegisterRunWritesAndRemoves(t *testing.T) {
	e, d := regEnv(t), openState(t)
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	remove, err := d.RegisterRun(e, "demo", "/run/x.sock", at)
	if err != nil {
		t.Fatal(err)
	}
	runs, _ := e.RunsDir()
	path := filepath.Join(runs, RootHash(d.Root())+".json")
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("entry: %v, %v", fi, err)
	}
	for _, dir := range []string{runs, filepath.Dir(runs)} {
		if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
			t.Errorf("%s is %v, want 0700", dir, fi.Mode().Perm())
		}
	}
	got, skipped := liveRuns(context.Background(), e, alive, yes)
	if len(skipped) != 0 || len(got) != 1 {
		t.Fatalf("got %v, skipped %v", got, skipped)
	}
	want := RunEntry{V: 1, Root: d.Root(), Project: "demo", PID: os.Getpid(), Started: at, Socket: "/run/x.sock"}
	if got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	if err := remove(); err != nil {
		t.Errorf("second remove: %v", err)
	}
	if got, _ := liveRuns(context.Background(), e, alive, yes); len(got) != 0 {
		t.Errorf("after remove: %v", got)
	}
}

func TestRegistryFallsBackToTempDir(t *testing.T) {
	e := RegistryEnv{RuntimeDir: "relative", TempDir: t.TempDir()}
	runs, err := e.RunsDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(runs, filepath.Join(e.TempDir, "igris-")) || filepath.Base(runs) != "runs" {
		t.Errorf("runs dir %s", runs)
	}
	if _, err := (RegistryEnv{}).RunsDir(); err == nil {
		t.Error("no directory at all: want an error")
	}
}

func TestRegisterRunRefusesUnsafeDirectory(t *testing.T) {
	e, d := regEnv(t), openState(t)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(e.RuntimeDir, "igris")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RegisterRun(e, "p", "/s", time.Now()); err == nil {
		t.Error("a symlinked registry directory was accepted")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("wrote through the symlink: %v", entries)
	}
}

func TestLiveRunsDropsDeadAndBad(t *testing.T) {
	e := regEnv(t)
	runs, _ := e.RunsDir()
	if err := PrivateDir(filepath.Dir(runs), "x"); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(runs, "x"); err != nil {
		t.Fatal(err)
	}
	good, gone, noSock := "/p/good", "/p/gone", "/p/nosock"
	entry := func(root string, pid int, sock string) string {
		return `{"v":1,"root":"` + root + `","project":"p","pid":` + strconv.Itoa(pid) + `,"started":"2026-11-11T09:00:00Z","socket":"` + sock + `"}`
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(runs, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(RootHash(good)+".json", entry(good, 1, "/s/good"))
	write(RootHash(gone)+".json", entry(gone, 2, "/s/gone"))
	write(RootHash(noSock)+".json", entry(noSock, 1, "/s/none"))
	write("garbage.json", "{not json")
	write(RootHash("/p/other")+".json", entry("/p/mismatch", 1, "/s/x"))
	write(RootHash("/p/big")+".json", strings.Repeat("x", 5<<10))
	write(RootHash("/p/v2")+".json", `{"v":2}`)
	write(".tmp-in-flight.tmp", "partial")
	if err := syscall.Mkfifo(filepath.Join(runs, "fifo.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(runs, RootHash(good)+".json"), filepath.Join(runs, "link.json")); err != nil {
		t.Fatal(err)
	}
	isAlive := func(pid int) bool { return pid == 1 }
	answering := func(_ context.Context, sock string) bool { return sock != "/s/none" }

	got, skipped := liveRuns(context.Background(), e, isAlive, answering)
	if len(got) != 1 || got[0].Root != good {
		t.Fatalf("live = %v", got)
	}
	reasons := map[string]string{}
	for _, s := range skipped {
		if s.Reason == "" {
			t.Errorf("%s skipped without a reason", s.Path)
		}
		reasons[filepath.Base(s.Path)] = s.Reason
	}
	for name, sub := range map[string]string{
		RootHash(gone) + ".json":       "gone",
		RootHash(noSock) + ".json":     "socket",
		"garbage.json":                 "not a run entry",
		RootHash("/p/other") + ".json": "does not match",
		RootHash("/p/big") + ".json":   "too large",
		RootHash("/p/v2") + ".json":    "version",
		"fifo.json":                    "regular",
		"link.json":                    "regular",
	} {
		if !strings.Contains(reasons[name], sub) {
			t.Errorf("%s: reason %q, want it to mention %q", name, reasons[name], sub)
		}
	}
	if len(skipped) != 8 {
		t.Errorf("skipped %d, want 8: %v", len(skipped), skipped)
	}
}

func TestLiveRunsNoRegistry(t *testing.T) {
	got, skipped := liveRuns(context.Background(), regEnv(t), alive, yes)
	if got != nil || skipped != nil {
		t.Errorf("got %v, %v", got, skipped)
	}
}

// answers against a real unix socket: up, then closed.
func TestAnswersSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if !answers(context.Background(), sock) {
		t.Error("a listening socket does not answer")
	}
	_ = l.Close()
	if answers(context.Background(), sock) {
		t.Error("a closed socket answers")
	}
}

func TestLiveRunsOrder(t *testing.T) {
	e := regEnv(t)
	t0 := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	for i, root := range []string{t.TempDir(), t.TempDir()} {
		d, err := Open(root, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.RegisterRun(e, "p", "/s", t0.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := liveRuns(context.Background(), e, alive, yes)
	if len(got) != 2 || !got[0].Started.Before(got[1].Started) {
		t.Errorf("not ordered by start: %v", got)
	}
}

// The registry's readers trust only a private directory of this user's:
// under the os.TempDir() fallback another user could have made
// <tempdir>/igris-<uid>/runs first and filled it with entries.
func TestLiveRunsRefusesAnOpenDirectory(t *testing.T) {
	for _, which := range []string{"base", "runs"} {
		t.Run(which, func(t *testing.T) {
			e := regEnv(t)
			d := openState(t)
			if _, err := d.RegisterRun(e, "p", "/tmp/p.sock", time.Now()); err != nil {
				t.Fatal(err)
			}
			if runs, _ := liveRuns(context.Background(), e, alive, yes); len(runs) != 1 {
				t.Fatalf("own entry not read: %v", runs)
			}
			dir, _ := e.RunsDir()
			if which == "base" {
				dir = filepath.Dir(dir)
			}
			if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // the test opens it on purpose
				t.Fatal(err)
			}
			runs, skipped := liveRuns(context.Background(), e, alive, yes)
			if len(runs) != 0 || len(skipped) != 1 || !strings.Contains(skipped[0].Reason, "not a private directory") {
				t.Errorf("open %s directory: runs %v, skipped %v", which, runs, skipped)
			}
		})
	}
}
