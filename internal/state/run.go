package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// State file format versions. A run with a selection (SPEC §5.5) is saved
// as version 2, so igris v0.3, which knows only 1, refuses it instead of
// resuming it without its slice; a whole-phase run stays readable by v0.3.
const (
	runVersion      = 1
	runVersionSlice = 2
)

// ErrNoRun is returned by LoadRun when there is no state.json.
var ErrNoRun = errors.New("no previous run")

// Run is the current run, stored in .igris/state.json (SPEC §13).
type Run struct {
	Version   int       `json:"version"`
	StartedAt time.Time `json:"started_at"`
	Phases    []string  `json:"phases"`            // phases of the run, in order
	Through   string    `json:"through,omitempty"` // --through argument, if any
	// Selection is the part of the phases the run is limited to (SPEC
	// §5.5); nil runs them whole. A v0.3 state file has none.
	Selection  *Selection `json:"selection,omitempty"`
	ConfigHash string     `json:"config_hash"`       // hash of the config snapshot
	Current    *Current   `json:"current,omitempty"` // nil between tasks
}

// Selection is `arise --only` or `--from`/`--until` (SPEC §5.5): task IDs,
// as the owner named them.
type Selection struct {
	Only  []string `json:"only,omitempty"`
	From  string   `json:"from,omitempty"`
	Until string   `json:"until,omitempty"`
}

// Empty says nothing is selected: the run's phases run whole.
func (s Selection) Empty() bool { return len(s.Only) == 0 && s.From == "" && s.Until == "" }

// String names the selection as run_started does, e.g. "only M1-03,
// M1-05" or "from M1-03 until M2-02"; "" when it is empty.
func (s Selection) String() string {
	if len(s.Only) > 0 {
		return "only " + strings.Join(s.Only, ", ")
	}
	var parts []string
	if s.From != "" {
		parts = append(parts, "from "+s.From)
	}
	if s.Until != "" {
		parts = append(parts, "until "+s.Until)
	}
	return strings.Join(parts, " ")
}

// Current is the task the run is working on.
type Current struct {
	TaskID         string              `json:"task_id"`
	Mode           string              `json:"mode"`
	ClaudeSession  string              `json:"claude_session"` // Claude Code session UUID
	Session        *backend.SessionRef `json:"session,omitempty"`
	VerifyAttempts int                 `json:"verify_attempts"` // consecutive verify failures
	// PendingPrompt is the first prompt while the session holds it back at a
	// startup prompt, so a reattached session still gets it (SPEC §13).
	PendingPrompt string    `json:"pending_prompt,omitempty"`
	StartedAt     time.Time `json:"started_at"`
}

// SaveRun writes r to state.json atomically.
func (d *Dir) SaveRun(r *Run) error {
	r.Version = runVersion
	if r.Selection != nil {
		r.Version = runVersionSlice
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("save run state: %w", err)
	}
	return writeFileAtomic(d.runPath(), append(data, '\n'))
}

// LoadRun reads state.json. It returns ErrNoRun if there is none.
func (d *Dir) LoadRun() (*Run, error) { return loadRun(d.runPath()) }

// PeekRun reads root/.igris/state.json without creating or changing
// anything, e.g. for a dry run. It returns ErrNoRun if there is none.
func PeekRun(root string) (*Run, error) {
	return loadRun(filepath.Join(root, DirName, "state.json"))
}

func loadRun(path string) (*Run, error) {
	data, err := os.ReadFile(path) //nolint:gosec // igris's own state file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoRun
	}
	if err != nil {
		return nil, fmt.Errorf("read run state: %w", err)
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("read run state %s: %w; delete the file to start a new run", path, err)
	}
	if r.Version != runVersion && r.Version != runVersionSlice {
		return nil, fmt.Errorf("read run state %s: unsupported version %d (want %d or %d); delete the file to start a new run", path, r.Version, runVersion, runVersionSlice)
	}
	if r.Selection != nil {
		// The IDs end up in messages and on the terminal: a session could
		// have written anything here.
		ids := slices.Clone(r.Selection.Only)
		for _, id := range []string{r.Selection.From, r.Selection.Until} {
			if id != "" {
				ids = append(ids, id)
			}
		}
		for _, id := range ids {
			if !plan.ValidID(id) {
				return nil, fmt.Errorf("read run state %s: damaged: the selection names %q, which is not a task ID; delete the file to start a new run", path, textsafe.Line(id))
			}
		}
	}
	return &r, nil
}
