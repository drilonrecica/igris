package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// LockInfo is the content of .igris/igris.lock.
type LockInfo struct {
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	StartedAt time.Time `json:"started_at"`
}

func (i LockInfo) String() string {
	return fmt.Sprintf("pid %d on %s since %s", i.PID, i.Host, i.StartedAt.Format(time.RFC3339))
}

// LockedError reports a lock held by a run that may still be alive.
type LockedError struct {
	Info LockInfo
	Path string // the lock file
	// Remote is set when the lock comes from another host, where igris
	// can't tell whether the run is still alive.
	Remote bool
}

func (e *LockedError) Error() string {
	if e.Remote {
		return fmt.Sprintf("igris is locked by a run on another host (%s); if that run is gone, rerun with --force-unlock", e.Info)
	}
	// A live PID is all igris can check: after a reboot it may belong to
	// another program, and only the owner can tell.
	return fmt.Sprintf("igris is already running in this project (%s); stop it first. If that process is not igris (its PID was reused, e.g. after a reboot), delete %s", e.Info, e.Path)
}

// StaleLockError reports a lock left behind by a run that is no longer
// alive, or a lock file igris can't read.
type StaleLockError struct {
	Info   LockInfo // zero if the file was unreadable
	Reason string
}

func (e *StaleLockError) Error() string {
	return fmt.Sprintf("stale lock %s; rerun with --force-unlock to clear it", e.Reason)
}

// Lock is a held run lock.
type Lock struct {
	path string
	info LockInfo
}

// Info returns the lock's content.
func (l *Lock) Info() LockInfo { return l.info }

// Lock takes the project's run lock so only one igris arise runs at a time
// (SPEC §13). A lock held by a live process on this host is always refused.
// A stale lock (dead process, unreadable file) is refused with a
// *StaleLockError unless force is set; force also clears a lock from
// another host, whose process igris can't check.
func (d *Dir) Lock(force bool) (*Lock, error) {
	host, err := d.hostname()
	if err != nil {
		return nil, fmt.Errorf("lock %s: hostname: %w", d.lockPath(), err)
	}
	info := LockInfo{PID: d.pid, Host: host, StartedAt: d.now().UTC()}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", d.lockPath(), err)
	}

	err = createExclusive(d.lockPath(), data)
	if errors.Is(err, fs.ErrExist) {
		if err := d.checkHeld(host, force); err != nil {
			return nil, err
		}
		// Forced: clear the stale lock and retry once. If another igris
		// takes the lock in between, the exclusive create fails again.
		if err := os.Remove(d.lockPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("clear stale lock %s: %w", d.lockPath(), err)
		}
		err = createExclusive(d.lockPath(), data)
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("lock %s: another igris took the lock while the stale one was cleared", d.lockPath())
		}
	}
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", d.lockPath(), err)
	}
	return &Lock{path: d.lockPath(), info: info}, nil
}

// checkHeld inspects an existing lock file. It returns nil only if the lock
// may be cleared (stale or remote, with force).
func (d *Dir) checkHeld(host string, force bool) error {
	info, err := readLock(d.lockPath())
	if err != nil {
		if force {
			return nil
		}
		return &StaleLockError{Reason: fmt.Sprintf("at %s (%v)", d.lockPath(), err)}
	}
	switch {
	case info.Host != host:
		if force {
			return nil
		}
		return &LockedError{Info: info, Path: d.lockPath(), Remote: true}
	case d.alive(info.PID):
		return &LockedError{Info: info, Path: d.lockPath()}
	case force:
		return nil
	default:
		return &StaleLockError{Info: info, Reason: fmt.Sprintf("from %s (process no longer running)", info)}
	}
}

// Release removes the lock file if it still holds this lock. A lock cleared
// and re-taken by someone else is left alone.
func (l *Lock) Release() error {
	info, err := readLock(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil || info.PID != l.info.PID || info.Host != l.info.Host || !info.StartedAt.Equal(l.info.StartedAt) {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("release lock %s: %w", l.path, err)
	}
	return nil
}

func readLock(path string) (LockInfo, error) {
	var info LockInfo
	data, err := os.ReadFile(path) //nolint:gosec // igris's own lock file
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("unreadable lock file: %w", err)
	}
	if info.PID <= 0 || info.Host == "" {
		return info, errors.New("unreadable lock file: missing pid or host")
	}
	return info, nil
}

// createExclusive creates path holding data, failing with fs.ErrExist if the
// file already exists. The content goes to a temp file that is hard-linked
// into place, so the lock appears complete: another igris never reads a
// half-written lock, takes it for a damaged one and clears it with
// --force-unlock while its owner is alive.
func createExclusive(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := writeAndClose(f, data); err != nil {
		return err
	}
	err = os.Link(tmp, path)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return err
	}
	// No hard links here (some network and FAT file systems): create the
	// file directly, which is exclusive but briefly empty.
	return createDirect(path, data)
}

// createDirect writes data to a new 0600 file at path, failing with
// fs.ErrExist if the file already exists.
func createDirect(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm) //nolint:gosec // igris's own lock file
	if err != nil {
		return err
	}
	if err := writeAndClose(f, data); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// writeAndClose writes data to f as a 0600 file, fsyncs and closes it.
func writeAndClose(f *os.File, data []byte) error {
	if err := f.Chmod(filePerm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
