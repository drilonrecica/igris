package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Socket file names in .igris/ (SPEC §13, since 1.4).
const (
	SocketName     = "igris.sock"
	SocketPathName = "igris.sock.path"
)

// SocketPath returns .igris/igris.sock, where `igris serve` listens unless
// that path is too long for a unix socket (SPEC §13).
func SocketPath(root string) string { return filepath.Join(root, DirName, SocketName) }

// SocketPathFile returns .igris/igris.sock.path, which names the socket
// used instead of a path that is too long.
func SocketPathFile(root string) string { return filepath.Join(root, DirName, SocketPathName) }

// WriteSocketPath records sock as the run's socket in .igris/igris.sock.path,
// atomically and 0600.
func (d *Dir) WriteSocketPath(sock string) error {
	return writeFileAtomic(SocketPathFile(d.root), []byte(sock+"\n"))
}

// RemoveSocketPath removes .igris/igris.sock.path; a missing file is not an
// error.
func (d *Dir) RemoveSocketPath() error {
	if err := os.Remove(SocketPathFile(d.root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", SocketPathFile(d.root), err)
	}
	return nil
}
