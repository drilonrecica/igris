// Package fsafe reads files that an untrusted party may have replaced: a
// Claude Code session can write the plan, igris.toml and everything under
// .igris/ (SPEC §16). ReadFile never blocks on what it opens, reads only a
// regular file, and only up to a bound, so a FIFO, a device or a huge file
// in a file's place is an error igris reports, never a hang. Open does
// the same for a file read as a stream.
package fsafe

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

var (
	// ErrNotRegular is wrapped when the path is not a regular file (a
	// FIFO, a directory, a device, or a symlink when follow is false).
	ErrNotRegular = errors.New("not a regular file")
	// ErrTooLarge is wrapped when the file holds more than max bytes.
	ErrTooLarge = errors.New("too large")
)

// ReadFile returns the contents of the regular file at path, at most max
// bytes. The file is opened as Open does. A missing file wraps
// fs.ErrNotExist.
func ReadFile(path string, max int64, follow bool) ([]byte, error) {
	f, err := Open(path, follow)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: %w (over %d bytes)", path, ErrTooLarge, max)
	}
	return data, nil
}

// Open opens the regular file at path for reading, for a caller that
// streams it and bounds what it reads itself. The file is opened without
// blocking (a FIFO with no writer would stall an ordinary open) and, with
// follow false, without following a symlink. A missing file wraps
// fs.ErrNotExist.
func Open(path string, follow bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if !follow {
		flags |= syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(path, flags, 0) //nolint:gosec // the caller's path; what is read is checked below
	if err != nil {
		var pe *os.PathError
		if !follow && errors.As(err, &pe) && errors.Is(pe.Err, syscall.ELOOP) {
			return nil, fmt.Errorf("%s: %w (a symbolic link)", path, ErrNotRegular)
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w (%s)", path, ErrNotRegular, info.Mode().Type())
	}
	return f, nil
}
