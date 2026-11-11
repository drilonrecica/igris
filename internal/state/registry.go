package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/drilonrecica/igris/internal/fsafe"
)

// The run registry (SPEC §13, since 1.11): while its socket is up, an
// `igris serve` writes one small file naming its run to a per-user directory
// outside any project, so a tool that serves several projects (`igris hub`)
// finds the live runs without scanning the disk.

const (
	// RunRegistryVersion is the "v" of a registry entry.
	RunRegistryVersion = 1
	// maxRunEntry bounds a registry entry when it is read back.
	maxRunEntry = 4 << 10
	// answerWait is how long LiveRuns waits for a run's socket.
	answerWait = 500 * time.Millisecond
)

// RootHash is the first 16 hex characters of the sha256 of the project root:
// the name of the run's fallback socket and of its registry entry.
func RootHash(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:8])
}

// RunEntry is one registry file: a live run's `igris serve`.
type RunEntry struct {
	V       int       `json:"v"`
	Root    string    `json:"root"`
	Project string    `json:"project"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Socket  string    `json:"socket"`
}

// SkippedEntry is a registry file LiveRuns did not take, with the reason.
type SkippedEntry struct {
	Path   string
	Reason string
}

// RegistryEnv is what the registry's location reads from the environment;
// tests replace it.
type RegistryEnv struct {
	RuntimeDir string // $XDG_RUNTIME_DIR
	TempDir    string // os.TempDir()
}

// SystemRegistryEnv reads the real environment.
func SystemRegistryEnv() RegistryEnv {
	return RegistryEnv{RuntimeDir: os.Getenv("XDG_RUNTIME_DIR"), TempDir: os.TempDir()}
}

// base is the igris directory runs/ and hub.json live in: under
// $XDG_RUNTIME_DIR when that is absolute, else <tempdir>/igris-<uid>.
func (e RegistryEnv) base() (string, error) {
	if e.RuntimeDir != "" && filepath.IsAbs(e.RuntimeDir) {
		return filepath.Join(e.RuntimeDir, "igris"), nil
	}
	if e.TempDir != "" && filepath.IsAbs(e.TempDir) {
		return filepath.Join(e.TempDir, "igris-"+strconv.Itoa(os.Getuid())), nil
	}
	return "", errors.New("no directory for the run registry: set XDG_RUNTIME_DIR or TMPDIR to an absolute path")
}

// RunsDir returns the registry directory (not created).
func (e RegistryEnv) RunsDir() (string, error) {
	b, err := e.base()
	if err != nil {
		return "", err
	}
	return filepath.Join(b, "runs"), nil
}

// PrivateDir creates dir 0700 if needed and checks that it is a directory
// of this user's, not a symlink, which nobody else can enter. what names it
// in errors ("socket directory").
func PrivateDir(dir, what string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create the %s %s: %w", what, dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("%s %s: %w", what, dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s %s is not a directory; remove it", what, dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s %s belongs to another user; remove it or set XDG_RUNTIME_DIR", what, dir)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory: 0700 is the owner's only
			return fmt.Errorf("restrict the %s %s to 0700: %w", what, dir, err)
		}
	}
	return nil
}

// RegisterRun writes the registry entry of the run in dir, atomically, and
// returns the function that removes it (a missing file is not an error). The
// directories are created 0700 and checked as the socket's are. Call it once
// the socket is up.
func (d *Dir) RegisterRun(e RegistryEnv, project, socket string, now time.Time) (remove func() error, err error) {
	runs, err := e.RunsDir()
	if err != nil {
		return nil, err
	}
	if err := PrivateDir(filepath.Dir(runs), "run registry directory"); err != nil {
		return nil, err
	}
	if err := PrivateDir(runs, "run registry directory"); err != nil {
		return nil, err
	}
	entry := RunEntry{V: RunRegistryVersion, Root: d.root, Project: project, PID: os.Getpid(), Started: now.UTC(), Socket: socket}
	data, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("encode the run registry entry: %w", err)
	}
	path := filepath.Join(runs, RootHash(d.root)+".json")
	if err := writeFileAtomic(path, append(data, '\n')); err != nil {
		return nil, err
	}
	return func() error {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}, nil
}

// LiveRuns returns the registered runs that are alive, ordered by start time
// then root: each entry is read as a regular file of at most 4 KiB and
// shape-checked, its pid must exist and its socket answer. An entry that
// fails is skipped, with the reason, never fatal; a crashed serve's entry
// is one of them (it is left in place: the next serve of the project
// overwrites it). No registry directory is no runs.
func LiveRuns(ctx context.Context) ([]RunEntry, []SkippedEntry) {
	return liveRuns(ctx, SystemRegistryEnv(), processAlive, answers)
}

func liveRuns(ctx context.Context, e RegistryEnv, alive func(int) bool, answering func(context.Context, string) bool) ([]RunEntry, []SkippedEntry) {
	dir, err := e.RunsDir()
	if err != nil {
		return nil, nil
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []SkippedEntry{{Path: dir, Reason: err.Error()}}
	}
	var runs []RunEntry
	var skipped []SkippedEntry
	for _, n := range names {
		path := filepath.Join(dir, n.Name())
		if filepath.Ext(n.Name()) != ".json" {
			continue // a temp file of a write in flight
		}
		if ctx.Err() != nil {
			break
		}
		run, reason := readRunEntry(path, n.Name())
		if reason == "" && !alive(run.PID) {
			reason = fmt.Sprintf("process %d is gone", run.PID)
		}
		if reason == "" && !answering(ctx, run.Socket) {
			reason = "its socket does not answer"
		}
		if reason != "" {
			skipped = append(skipped, SkippedEntry{Path: path, Reason: reason})
			continue
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].Started.Equal(runs[j].Started) {
			return runs[i].Started.Before(runs[j].Started)
		}
		return runs[i].Root < runs[j].Root
	})
	return runs, skipped
}

// readRunEntry reads and shape-checks one entry; reason is "" when it is
// good. The file is named after its root's hash, so a copy under another
// name is refused.
func readRunEntry(path, name string) (RunEntry, string) {
	data, err := fsafe.ReadFile(path, maxRunEntry, false)
	if err != nil {
		return RunEntry{}, err.Error()
	}
	var r RunEntry
	if err := json.Unmarshal(data, &r); err != nil {
		return RunEntry{}, "not a run entry: " + err.Error()
	}
	switch {
	case r.V != RunRegistryVersion:
		return RunEntry{}, fmt.Sprintf("version %d is not understood", r.V)
	case !filepath.IsAbs(r.Root) || filepath.Clean(r.Root) != r.Root:
		return RunEntry{}, "root is not a clean absolute path"
	case name != RootHash(r.Root)+".json":
		return RunEntry{}, "file name does not match the root"
	case r.PID <= 0:
		return RunEntry{}, "pid is not positive"
	case !filepath.IsAbs(r.Socket) || filepath.Clean(r.Socket) != r.Socket:
		return RunEntry{}, "socket is not a clean absolute path"
	case r.Started.IsZero():
		return RunEntry{}, "started is missing"
	}
	return r, ""
}

// answers says whether a listener answers on the unix socket at path
// within a short wait: it connects and closes at once, which serve takes
// for a client that left without a word (the check serve.Answers makes for
// a project's own socket).
func answers(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, answerWait)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}
