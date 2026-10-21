package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/state"
)

var (
	// ErrServed is returned by Listen when a live serve already answers on
	// the project's socket.
	ErrServed = errors.New("a run is already served in this project")
	// ErrNotServed is returned by Dial when no run answers on the
	// project's socket.
	ErrNotServed = errors.New("no run is live in this project")
)

// maxSocketPath is the longest unix socket path the system takes:
// sun_path is 108 bytes on Linux and 104 on macOS and the BSDs, the
// terminating NUL included.
func maxSocketPath() int {
	if runtime.GOOS == "linux" {
		return 107
	}
	return 103
}

// fallbackName is the socket's file name outside .igris/: the first 16 hex
// characters of the sha256 of the project root (SPEC §13).
func fallbackName(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:8]) + ".sock"
}

// tempDirName is the per-user directory under os.TempDir().
func tempDirName() string { return "igris-" + strconv.Itoa(os.Getuid()) }

// env is what the path choice reads from the environment; tests replace it.
type env struct {
	runtimeDir string // $XDG_RUNTIME_DIR
	tempDir    string // os.TempDir()
	max        int    // maxSocketPath()
}

func systemEnv() env {
	return env{runtimeDir: os.Getenv("XDG_RUNTIME_DIR"), tempDir: os.TempDir(), max: maxSocketPath()}
}

// candidates are the socket paths for root in order of preference, each
// with the directory Listen creates for it ("" for .igris/, which exists).
func (e env) candidates(root string) []struct{ path, dir string } {
	var out []struct{ path, dir string }
	out = append(out, struct{ path, dir string }{state.SocketPath(root), ""})
	name := fallbackName(root)
	if e.runtimeDir != "" && filepath.IsAbs(e.runtimeDir) {
		d := filepath.Join(e.runtimeDir, "igris")
		out = append(out, struct{ path, dir string }{filepath.Join(d, name), d})
	}
	if e.tempDir != "" && filepath.IsAbs(e.tempDir) {
		d := filepath.Join(e.tempDir, tempDirName())
		out = append(out, struct{ path, dir string }{filepath.Join(d, name), d})
	}
	return out
}

// Listener is the run's socket. Closing it removes the socket and
// .igris/igris.sock.path.
type Listener struct {
	net.Listener
	path string
	dir  *state.Dir
}

// Path returns the socket's path.
func (l *Listener) Path() string { return l.path }

// Close stops listening and removes the socket and its path file.
func (l *Listener) Close() error {
	err := l.Listener.Close()
	if rerr := os.Remove(l.path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) && err == nil {
		err = rerr
	}
	if rerr := l.dir.RemoveSocketPath(); rerr != nil && err == nil {
		err = rerr
	}
	return err
}

// Listen creates the run's socket for the project of dir (SPEC §13):
// .igris/igris.sock, 0600, or when that path is too long the fallback
// under $XDG_RUNTIME_DIR or os.TempDir(), recorded in
// .igris/igris.sock.path. A stale socket (no listener) is replaced; a
// live one is ErrServed; anything else in its place is refused. The
// caller holds the run lock, so no other serve of this project races it.
func Listen(dir *state.Dir) (*Listener, error) { return listen(dir, systemEnv()) }

func listen(dir *state.Dir, e env) (*Listener, error) {
	root := dir.Root()
	var path, sub string
	for _, c := range e.candidates(root) {
		if len(c.path) <= e.max {
			path, sub = c.path, c.dir
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("no socket path short enough for %s (at most %d bytes): set XDG_RUNTIME_DIR or TMPDIR to a shorter directory", root, e.max)
	}
	if sub != "" {
		if err := privateDir(sub); err != nil {
			return nil, err
		}
	}
	if err := clearStale(path); err != nil {
		return nil, err
	}
	nl, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	l := &Listener{Listener: nl, path: path, dir: dir}
	// The directory (0700) keeps others out between the bind and here.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("restrict %s to 0600: %w", path, err)
	}
	if sub != "" {
		err = dir.WriteSocketPath(path)
	} else {
		err = dir.RemoveSocketPath() // an earlier run's fallback
	}
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// privateDir creates dir 0700 if needed and checks that it is a directory
// of this user's, not a symlink, which nobody else can enter.
func privateDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create the socket directory %s: %w", dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("socket directory %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("socket directory %s is not a directory; remove it", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("socket directory %s belongs to another user; remove it or set XDG_RUNTIME_DIR", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory: 0700 is the owner's only
			return fmt.Errorf("restrict the socket directory %s to 0700: %w", dir, err)
		}
	}
	return nil
}

// clearStale makes room for the socket at path: nothing there, or a socket
// no listener answers on, which is removed.
func clearStale(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("check %s: %w", path, err)
	case fi.Mode()&fs.ModeSocket == 0:
		return fmt.Errorf("%s is in the way of the run's socket and is not one; remove it", path)
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = c.Close()
		return fmt.Errorf("%w (%s answers)", ErrServed, path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the stale socket %s: %w", path, err)
	}
	return nil
}

// SocketPath returns the socket of the project at root as serve made it:
// the path .igris/igris.sock.path names when it names the fallback for
// this root, else .igris/igris.sock. The path file is untrusted (a
// session can write .igris/), so any other path in it is ignored.
func SocketPath(root string) string { return socketPath(root) }

func socketPath(root string) string {
	data, err := fsafe.ReadFile(state.SocketPathFile(root), 4<<10, false)
	if err == nil {
		if p := strings.TrimSpace(string(data)); validFallback(root, p) {
			return p
		}
	}
	return state.SocketPath(root)
}

// validFallback reports whether p is a fallback socket path for root:
// absolute and clean, named after root's hash, in an igris/ or
// igris-<uid>/ directory.
func validFallback(root, p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || filepath.Base(p) != fallbackName(root) {
		return false
	}
	d := filepath.Base(filepath.Dir(p))
	return d == "igris" || d == tempDirName()
}

// Dial connects to the live run of the project at root. With no run
// listening it returns ErrNotServed.
func Dial(ctx context.Context, root string) (net.Conn, error) {
	path := socketPath(root)
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("%w (nothing answers on %s)", ErrNotServed, path)
		}
		return nil, fmt.Errorf("connect to %s: %w", path, err)
	}
	return c, nil
}
