package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
)

// maxAgentStateSize bounds an agent-state file. igris writes ~100 bytes;
// anything larger was not written by `igris hook` (SPEC §6.3).
const maxAgentStateSize = 4 << 10

// ValidSessionUUID reports whether s has the shape of a Claude session UUID
// as igris generates it (engine.NewSessionID). Agent-state files are keyed
// by it.
func ValidSessionUUID(s string) bool { return backend.ValidClaudeSession(s) }

// AgentState is one record written by `igris hook`: the agent state a
// Claude Code hook event maps to (SPEC §6.3).
type AgentState struct {
	State backend.AgentState `json:"state"`
	Event string             `json:"event"`
	At    time.Time          `json:"at"`
}

// AgentStateDir returns root/.igris/agent-state.
func AgentStateDir(root string) string { return filepath.Join(root, DirName, "agent-state") }

// HooksDir returns root/.igris/hooks, where the per-task hooks settings
// files live (SPEC §6.3).
func HooksDir(root string) string { return filepath.Join(root, DirName, "hooks") }

// WriteHooksFile atomically writes the hooks settings file for taskID and
// returns its path. id must be a valid task ID (it becomes a file name).
func (d *Dir) WriteHooksFile(id string, data []byte) (string, error) {
	if err := checkSignalID(id); err != nil {
		return "", fmt.Errorf("write hooks file: %w", err)
	}
	dir := HooksDir(d.root)
	if err := mkdirPrivate(dir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".settings.json")
	return path, writeFileAtomic(path, data)
}

// WriteAgentState records s for the session uuid in root's .igris/. It
// never creates .igris/ itself: a hook firing in a directory igris doesn't
// manage leaves no trace.
func WriteAgentState(root, uuid string, s AgentState) error {
	if !ValidSessionUUID(uuid) {
		return fmt.Errorf("write agent state: invalid session id %q", uuid)
	}
	if _, err := os.Stat(filepath.Join(root, DirName)); err != nil {
		return fmt.Errorf("write agent state: %w", err)
	}
	dir := AgentStateDir(root)
	if err := mkdirPrivate(dir); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("write agent state: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, uuid+".json"), append(data, '\n'))
}

// ReadAgentState returns the last agent state recorded for the session
// uuid, and false if there is none. Sessions can write into .igris/, so the
// file is opened without following symlinks and without blocking (a FIFO
// must not stall igris), read only if it is a regular file of at most
// 4 KiB, and only a known state is accepted.
func ReadAgentState(root, uuid string) (AgentState, bool, error) {
	if !ValidSessionUUID(uuid) {
		return AgentState{}, false, fmt.Errorf("read agent state: invalid session id %q", uuid)
	}
	path := filepath.Join(AgentStateDir(root), uuid+".json")
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0) //nolint:gosec // igris's own state directory; uuid validated
	if errors.Is(err, fs.ErrNotExist) {
		return AgentState{}, false, nil
	}
	if err != nil {
		return AgentState{}, false, fmt.Errorf("read agent state %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return AgentState{}, false, fmt.Errorf("read agent state %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return AgentState{}, false, fmt.Errorf("read agent state %s: not a regular file (%s)", path, info.Mode().Type())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxAgentStateSize+1))
	if err != nil {
		return AgentState{}, false, fmt.Errorf("read agent state %s: %w", path, err)
	}
	s, err := ParseAgentState(data)
	if err != nil {
		return AgentState{}, false, fmt.Errorf("read agent state %s: %w", path, err)
	}
	return s, true, nil
}

// ParseAgentState decodes an agent-state file's content. Only the states
// `igris hook` writes are accepted.
func ParseAgentState(data []byte) (AgentState, error) {
	if len(data) > maxAgentStateSize {
		return AgentState{}, fmt.Errorf("%d bytes is too large for an agent state", len(data))
	}
	var s AgentState
	if err := json.Unmarshal(data, &s); err != nil {
		return AgentState{}, err
	}
	switch s.State {
	case backend.Working, backend.Idle, backend.Blocked, backend.Exited:
	default:
		return AgentState{}, fmt.Errorf("unexpected state %q", s.State)
	}
	if len(s.Event) > 64 {
		s.Event = s.Event[:64]
	}
	return s, nil
}

// HookStates returns a backend.HookStates reading root's agent-state files.
// A file that can't be read counts as no state: it is untrusted input and
// says nothing reliable (SPEC §6.3).
func HookStates(root string) backend.HookStates {
	return func(uuid string) (backend.AgentState, bool) {
		s, ok, err := ReadAgentState(root, uuid)
		if err != nil || !ok {
			return "", false
		}
		return s.State, true
	}
}

// mkdirPrivate creates dir (and missing parents) with 0700 and makes sure
// dir itself is 0700.
func mkdirPrivate(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create state directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, dirPerm); err != nil {
		return fmt.Errorf("restrict state directory %s to 0700: %w", dir, err)
	}
	return nil
}
