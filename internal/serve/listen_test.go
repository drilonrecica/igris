package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

func openDir(t *testing.T, root string) *state.Dir {
	t.Helper()
	d, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// shortEnv chooses .igris/igris.sock whenever it fits, with fallbacks in
// directories of the test's own.
func shortEnv(t *testing.T) env {
	return env{runtimeDir: t.TempDir(), tempDir: t.TempDir(), max: maxSocketPath()}
}

// listenTemp serves s on a socket of a new project in a temp dir and
// returns the project root.
func listenTemp(t *testing.T, s *Server) string {
	t.Helper()
	root := t.TempDir()
	l, err := Listen(openDir(t, root))
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve(l) }()
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return root
}

// A client on the real socket gets the snapshot and the events, and the
// socket is the owner's only.
func TestRealSocket(t *testing.T) {
	var got sender
	s := New(Options{Version: "1.4.0", Sender: &got})
	root := listenTemp(t, s)
	path := SocketPath(root)
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket %s mode = %v, want a 0600 socket", path, fi.Mode())
	}
	c, err := Dial(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(patience))
	r := bufio.NewReader(c)
	line := func() map[string]any {
		t.Helper()
		b, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	_, _ = io.WriteString(c, `{"v":1,"type":"hello","client":"igris 1.4.0"}`+"\n")
	if m := line(); m["type"] != TypeSnapshot || m["server"] != "igris 1.4.0" {
		t.Fatalf("snapshot = %v", m)
	}
	s.Publish(engine.Event{At: t0, Kind: engine.PauseOn})
	if m := line(); m["type"] != "pause_on" {
		t.Errorf("event = %v", m)
	}
	_, _ = io.WriteString(c, `{"v":1,"type":"stop","id":"s"}`+"\n")
	if m := line(); m["type"] != TypeAck || m["id"] != "s" {
		t.Errorf("stop = %v", m)
	}
	if cmds := got.got(); len(cmds) != 1 || cmds[0].Kind != engine.CmdStop {
		t.Errorf("engine got %+v", cmds)
	}
}

// Closing the listener removes the socket; Dial then says no run is live.
func TestListenerCloseRemovesSocket(t *testing.T) {
	root := t.TempDir()
	l, err := listen(openDir(t, root), shortEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(l.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket still there after Close: %v", err)
	}
	if _, err := Dial(context.Background(), root); !errors.Is(err, ErrNotServed) {
		t.Errorf("Dial = %v, want ErrNotServed", err)
	}
}

// Serve started after Shutdown (a serve that fails right after Listen,
// before its Serve goroutine runs) still closes the listener it was
// given, so the socket doesn't outlive serve (V14-G: TestServeYolo was
// flaky on exactly this).
func TestServeAfterShutdownClosesTheListener(t *testing.T) {
	for _, stop := range []string{"Shutdown", "Close"} {
		t.Run(stop, func(t *testing.T) {
			root := t.TempDir()
			l, err := listen(openDir(t, root), shortEnv(t))
			if err != nil {
				t.Fatal(err)
			}
			s := New(Options{Version: "1.4.0"})
			if stop == "Shutdown" {
				err = s.Shutdown(context.Background())
			} else {
				err = s.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Serve(l); err != nil {
				t.Errorf("Serve after %s = %v, want nil, as when closed while it accepts", stop, err)
			}
			if _, err := os.Lstat(l.Path()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("socket still there after Serve returned: %v", err)
			}
		})
	}
}

// A socket nobody listens on is replaced; a live one is refused; a file
// or a symlink in its place is never removed.
func TestListenStaleLiveAndInTheWay(t *testing.T) {
	root := t.TempDir()
	d := openDir(t, root)
	path := state.SocketPath(root)

	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	_ = stale.Close()
	l, err := listen(d, shortEnv(t))
	if err != nil {
		t.Fatalf("a stale socket was not replaced: %v", err)
	}
	if _, err := listen(d, shortEnv(t)); !errors.Is(err, ErrServed) {
		t.Errorf("second listen = %v, want ErrServed", err)
	}
	_ = l.Close()

	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listen(d, shortEnv(t)); err == nil || !strings.Contains(err.Error(), "remove it") {
		t.Errorf("listen over a file = %v, want a refusal saying to remove it", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "x" { //nolint:gosec // the test's own temp dir
		t.Errorf("the file in the way was touched: %q, %v", b, err)
	}
	_ = os.Remove(path)
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := listen(d, shortEnv(t)); err == nil {
		t.Error("listen over a symlink worked")
	}
}

// longRoot makes a project root whose .igris/igris.sock doesn't fit a
// unix socket.
func longRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), strings.Repeat("a", 60), strings.Repeat("b", 60))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

// A root too deep for .igris/igris.sock serves from $XDG_RUNTIME_DIR, else
// from the temp dir, and .igris/igris.sock.path says where.
func TestListenFallback(t *testing.T) {
	for _, tt := range []struct {
		name string
		xdg  bool
		dir  string // the fallback directory's name
	}{
		{"runtime dir", true, "igris"},
		{"temp dir", false, tempDirName()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := longRoot(t)
			e := shortEnv(t)
			base := e.tempDir
			if tt.xdg {
				base = e.runtimeDir
			} else {
				e.runtimeDir = ""
			}
			d := openDir(t, root)
			l, err := listen(d, e)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(base, tt.dir, fallbackName(root))
			if l.Path() != want || len(want) > e.max {
				t.Fatalf("socket at %s, want %s", l.Path(), want)
			}
			if fi, err := os.Lstat(filepath.Dir(want)); err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("fallback directory: %v, %v; want 0700", fi, err)
			}
			if got := socketPath(root); got != want {
				t.Errorf("socketPath = %s, want %s from .igris/igris.sock.path", got, want)
			}
			if fi, err := os.Stat(state.SocketPathFile(root)); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("path file: %v, %v; want 0600", fi, err)
			}
			_ = l.Close()
			if _, err := os.Stat(state.SocketPathFile(root)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("path file left after Close: %v", err)
			}
		})
	}
}

func TestListenNoShortPath(t *testing.T) {
	root := longRoot(t)
	e := env{max: maxSocketPath()} // no runtime or temp dir to fall back to
	if _, err := listen(openDir(t, root), e); err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Errorf("listen = %v, want an error naming what to set", err)
	}
}

// A fallback directory that is a symlink is refused.
func TestListenFallbackDirSymlink(t *testing.T) {
	root := longRoot(t)
	e := shortEnv(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(e.runtimeDir, "igris")); err != nil {
		t.Fatal(err)
	}
	if _, err := listen(openDir(t, root), e); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("listen = %v, want the symlinked directory refused", err)
	}
}

// .igris/igris.sock.path is written by anyone who can write .igris/: a
// path in it that isn't this root's fallback is ignored.
func TestSocketPathIgnoresForeignPathFile(t *testing.T) {
	root := t.TempDir()
	d := openDir(t, root)
	for _, p := range []string{
		"/tmp/evil.sock",
		"relative/igris/" + fallbackName(root),
		"/tmp/igris/../" + fallbackName(root),
		"/tmp/other/" + fallbackName(root),
		"/tmp/igris/" + fallbackName("/somewhere/else"),
	} {
		if err := d.WriteSocketPath(p); err != nil {
			t.Fatal(err)
		}
		if got := socketPath(root); got != state.SocketPath(root) {
			t.Errorf("path file %q: socketPath = %s, want .igris/igris.sock", p, got)
		}
	}
	if err := d.WriteSocketPath("/run/user/1000/igris/" + fallbackName(root)); err != nil {
		t.Fatal(err)
	}
	if got := socketPath(root); got != "/run/user/1000/igris/"+fallbackName(root) {
		t.Errorf("a valid fallback was ignored: %s", got)
	}
}

func TestFallbackName(t *testing.T) {
	// The first 16 hex characters of sha256("/home/owner/project").
	if got := fallbackName("/home/owner/project"); got != "0989a117453d82b0.sock" {
		t.Errorf("fallbackName = %q", got)
	}
}
