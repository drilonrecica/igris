// Package state manages igris's .igris/ directory in the project root
// (SPEC §13): the run lock, state.json, the runs.jsonl log and the signal
// and prompt directories. Files are written atomically with 0600 permissions
// in 0700 directories (SPEC §16).
package state

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DirName is the state directory's name inside the project root.
const DirName = ".igris"

const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// Options configures Open.
type Options struct {
	// Now returns the current time; nil means time.Now. The engine passes
	// its clock so tests control timestamps.
	Now func() time.Time
}

// Dir is an opened .igris/ directory.
type Dir struct {
	root string // project root
	path string // root/.igris
	now  func() time.Time

	// Lock test hooks; see process_unix.go for the real ones.
	alive    func(pid int) bool
	hostname func() (string, error)
	pid      int
}

// Open creates root/.igris/ and its signals/, prompts/ and adapt/ subdirectories if
// needed and makes sure all of them are 0700.
func Open(root string, opts Options) (*Dir, error) {
	d := &Dir{
		root:     root,
		path:     filepath.Join(root, DirName),
		now:      opts.Now,
		alive:    processAlive,
		hostname: os.Hostname,
		pid:      os.Getpid(),
	}
	if d.now == nil {
		d.now = time.Now
	}
	for _, dir := range []string{d.path, d.SignalsDir(), d.PromptsDir(), d.AdaptDir()} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return nil, fmt.Errorf("create state directory %s: %w", dir, err)
		}
		if err := os.Chmod(dir, dirPerm); err != nil {
			return nil, fmt.Errorf("restrict state directory %s to 0700: %w", dir, err)
		}
	}
	return d, nil
}

// Root returns the project root.
func (d *Dir) Root() string { return d.root }

// Path returns the .igris/ directory.
func (d *Dir) Path() string { return d.path }

// SignalsDir returns the directory holding pending signal files (SPEC §6.2).
func (d *Dir) SignalsDir() string { return filepath.Join(d.path, "signals") }

// PromptsDir returns the directory holding the per-task rules files passed
// to Claude Code (SPEC §6).
func (d *Dir) PromptsDir() string { return filepath.Join(d.path, "prompts") }

// AdaptDir returns the directory holding `igris adapt` proposals and plan
// backups (SPEC §9).
func (d *Dir) AdaptDir() string { return filepath.Join(d.path, "adapt") }

func (d *Dir) lockPath() string   { return filepath.Join(d.path, "igris.lock") }
func (d *Dir) runPath() string    { return filepath.Join(d.path, "state.json") }
func (d *Dir) eventsPath() string { return filepath.Join(d.path, "runs.jsonl") }

// writeFileAtomic replaces path with data: it writes a 0600 temp file in
// the same directory, fsyncs it and renames it over path, so readers see
// either the old or the new content, never a partial file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	fail := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Chmod(filePerm); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, err)
	}
	syncDir(dir)
	return nil
}

// syncDir fsyncs a directory so a rename in it is durable. It is best
// effort: some file systems don't support syncing directories.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // igris's own state directory
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// WriteRules writes content (the igris rules) to .igris/prompts/<ID>.rules.md
// and returns its path, for --append-system-prompt-file (SPEC §6).
func (d *Dir) WriteRules(id, content string) (string, error) {
	if err := checkSignalID(id); err != nil {
		return "", fmt.Errorf("write rules: %w", err)
	}
	path := filepath.Join(d.PromptsDir(), id+".rules.md")
	if err := writeFileAtomic(path, []byte(content)); err != nil {
		return "", err
	}
	return path, nil
}
