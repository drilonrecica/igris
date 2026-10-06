package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
)

// runVersion is the state.json format version.
const runVersion = 1

// ErrNoRun is returned by LoadRun when there is no state.json.
var ErrNoRun = errors.New("no previous run")

// Run is the current run, stored in .igris/state.json (SPEC §13).
type Run struct {
	Version    int       `json:"version"`
	StartedAt  time.Time `json:"started_at"`
	Phases     []string  `json:"phases"`            // phases of the run, in order
	Through    string    `json:"through,omitempty"` // --through argument, if any
	ConfigHash string    `json:"config_hash"`       // hash of the config snapshot
	Current    *Current  `json:"current,omitempty"` // nil between tasks
}

// Current is the task the run is working on.
type Current struct {
	TaskID         string              `json:"task_id"`
	Mode           string              `json:"mode"`
	ClaudeSession  string              `json:"claude_session"` // Claude Code session UUID
	Session        *backend.SessionRef `json:"session,omitempty"`
	VerifyAttempts int                 `json:"verify_attempts"` // consecutive verify failures
	StartedAt      time.Time           `json:"started_at"`
}

// SaveRun writes r to state.json atomically.
func (d *Dir) SaveRun(r *Run) error {
	r.Version = runVersion
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("save run state: %w", err)
	}
	return writeFileAtomic(d.runPath(), append(data, '\n'))
}

// LoadRun reads state.json. It returns ErrNoRun if there is none.
func (d *Dir) LoadRun() (*Run, error) {
	data, err := os.ReadFile(d.runPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoRun
	}
	if err != nil {
		return nil, fmt.Errorf("read run state: %w", err)
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("read run state %s: %w; delete the file to start a new run", d.runPath(), err)
	}
	if r.Version != runVersion {
		return nil, fmt.Errorf("read run state %s: unsupported version %d (want %d); delete the file to start a new run", d.runPath(), r.Version, runVersion)
	}
	return &r, nil
}
