package state

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ServeLogName is the file in .igris/ that takes the stdout and stderr of
// an `igris serve` started in the background by arise or home (SPEC §13,
// since 1.4).
const ServeLogName = "serve.log"

// ServeLogMax is how much of serve.log is kept when a serve starts.
const ServeLogMax = 16 << 20

// ServeLogPath returns .igris/serve.log.
func (d *Dir) ServeLogPath() string { return filepath.Join(d.path, ServeLogName) }

// ServeLog opens .igris/serve.log for a serve about to start: created 0600,
// cut to its last ServeLogMax bytes, positioned at its end. A symlink in
// its place is refused, as a session could have planted one.
func (d *Dir) ServeLog() (*os.File, error) {
	return openServeLog(d.ServeLogPath(), ServeLogMax)
}

func openServeLog(path string, limit int64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, filePerm) //nolint:gosec // igris's own state directory
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	fail := func(err error) (*os.File, error) {
		_ = f.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	fi, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if !fi.Mode().IsRegular() {
		return fail(fmt.Errorf("not a regular file"))
	}
	if err := f.Chmod(filePerm); err != nil {
		return fail(err)
	}
	if size := fi.Size(); size > limit {
		keep := make([]byte, limit)
		if _, err := f.ReadAt(keep, size-limit); err != nil {
			return fail(err)
		}
		// From the first whole line on.
		if i := bytes.IndexByte(keep, '\n'); i >= 0 && i+1 < len(keep) {
			keep = keep[i+1:]
		}
		if _, err := f.WriteAt(keep, 0); err != nil {
			return fail(err)
		}
		if err := f.Truncate(int64(len(keep))); err != nil {
			return fail(err)
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fail(err)
	}
	return f, nil
}

// ServeLogTail returns the last n non-empty lines of serve.log, raw: the
// caller cleans them before showing them. Nothing (and no error) when the
// file is missing.
func ServeLogTail(root string, n int) ([]string, error) {
	path := filepath.Join(root, DirName, ServeLogName)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0) //nolint:gosec // igris's own state directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("read %s: not a regular file", path)
	}
	const window = 64 << 10
	from := max(fi.Size()-window, 0)
	buf := make([]byte, fi.Size()-from)
	if _, err := f.ReadAt(buf, from); err != nil && err != io.EOF {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(buf), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines[max(len(lines)-n, 0):], nil
}
