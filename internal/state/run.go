package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// State file format versions (SPEC §13, §19). igris writes runVersion and
// reads every older one, migrating it on read; a newer one is refused.
const (
	// runVersionWhole is igris v0.2–v0.6's file for a run of whole phases.
	runVersionWhole = 1
	// runVersionSlice is v0.4–v0.6's file for a run with a selection (SPEC
	// §5.5), so v0.3, which knows only 1, refused it instead of running
	// the whole phases.
	runVersionSlice = 2
	// runVersion is igris 1.x's: the selection is optional in it. A new
	// field gets a new version only when an igris that ignored it would do
	// something wrong.
	runVersion = 3
)

var (
	// ErrNoRun is returned by LoadRun when there is no state.json.
	ErrNoRun = errors.New("no previous run")
	// ErrRunDamaged is wrapped by LoadRun's error for a state.json that
	// isn't JSON, has no valid version, or holds a value of the wrong type
	// or shape.
	ErrRunDamaged = errors.New("damaged")
	// ErrRunNewer is wrapped by LoadRun's error for a state.json with a
	// version this igris doesn't know.
	ErrRunNewer = errors.New("written by a newer igris")
)

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
	// Worktree is where the run's sessions, verify and commits happen in
	// worktree mode (SPEC §6.8); nil runs them in the project root.
	Worktree *Worktree `json:"worktree,omitempty"`
	// ConfigChanged says igris.toml was edited during the run and differs
	// from the snapshot the run uses; the next run asks the owner before
	// using the file (SPEC §13, V10-05). Cleared when the file is
	// restored.
	ConfigChanged bool `json:"config_changed,omitempty"`
}

// Worktree is a run's git worktree (SPEC §6.8).
type Worktree struct {
	Path   string `json:"path"`   // relative to the project root: .igris/worktrees/<run-id>
	Branch string `json:"branch"` // igris/<run-id>
}

// WorktreesDir is where worktree mode puts its worktrees, relative to the
// project root.
var WorktreesDir = filepath.Join(DirName, "worktrees")

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
	PendingPrompt string `json:"pending_prompt,omitempty"`
	// ApprovedPlan is the plan the owner last approved in igris for the
	// task, .igris/plans/<ID>-a<attempt>-<n>.md (SPEC §6.3), for the
	// commit body. Since 1.1; no version change.
	ApprovedPlan string    `json:"approved_plan,omitempty"`
	StartedAt    time.Time `json:"started_at"`
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
func (d *Dir) LoadRun() (*Run, error) { return loadRun(d.runPath()) }

// PeekRun reads root/.igris/state.json without creating or changing
// anything, e.g. for a dry run. It returns ErrNoRun if there is none.
func PeekRun(root string) (*Run, error) {
	return loadRun(filepath.Join(root, DirName, "state.json"))
}

// maxRunSize bounds state.json: a session can write it (SPEC §16).
const maxRunSize = 1 << 20

func loadRun(path string) (*Run, error) {
	damaged := func(why string) error {
		return fmt.Errorf("read run state %s: %w: %s; delete the file to start a new run", path, ErrRunDamaged, why)
	}
	data, err := fsafe.ReadFile(path, maxRunSize, false)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNoRun
	case errors.Is(err, fsafe.ErrNotRegular), errors.Is(err, fsafe.ErrTooLarge):
		return nil, damaged(err.Error())
	case err != nil:
		return nil, fmt.Errorf("read run state: %w", err)
	}
	// The version first, alone: a newer igris may have changed the shape
	// of the rest, which is not damage.
	var probe struct{ Version json.RawMessage }
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, damaged("not a JSON object")
	}
	var v int
	if err := json.Unmarshal(probe.Version, &v); err != nil || v < 1 {
		return nil, damaged("no valid version")
	}
	if v > runVersion {
		return nil, fmt.Errorf("read run state %s: %w (version %d); upgrade igris, or delete the file to start a new run", path, ErrRunNewer, v)
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field != "" {
			return nil, damaged(fmt.Sprintf("%s has the wrong type", textsafe.Line(te.Field)))
		}
		return nil, damaged("not a run")
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
				return nil, damaged(fmt.Sprintf("the selection names %q, which is not a task ID", textsafe.Line(id)))
			}
		}
	}
	if why := checkReadBack(&r); why != "" {
		return nil, damaged(why)
	}
	migrateRun(&r)
	return &r, nil
}

// migrateRun brings a run read from an older state file up to runVersion,
// one version at a time; the next SaveRun writes it as runVersion.
func migrateRun(r *Run) {
	switch r.Version {
	case runVersionWhole, runVersionSlice:
		// Both have version 3's fields: 1 never has a selection, and a
		// session ref written before v0.3 has no claude_session, which
		// version 3 allows too.
		r.Version = runVersion
	}
}

// sessionModes are the run modes a recorded session can have (SPEC §7.1);
// "" is a task with no session yet.
var sessionModes = []string{"", "default", "accept", "auto", "plan", "yolo"}

// checkReadBack checks what igris uses from state.json besides the
// selection: a session can write the file, and these become paths, git
// arguments, the reattach's mode check and text on the terminal (SPEC
// §13, V06-07). It returns what is wrong, or "".
func checkReadBack(r *Run) string {
	if c := r.Current; c != nil {
		switch {
		case !plan.ValidID(c.TaskID):
			return "the current task is not a task ID"
		case !slices.Contains(sessionModes, c.Mode):
			return "the current task's mode is not a run mode"
		case c.VerifyAttempts < 0:
			return "the current task's verify attempts are negative"
		case c.ApprovedPlan != "" && !ValidPlanFile(c.ApprovedPlan, c.TaskID):
			return "the current task's approved plan is not a file igris kept"
		}
	}
	if wt := r.Worktree; wt != nil {
		// Only the worktree igris makes: .igris/worktrees/<run-id> on
		// igris/<run-id>, never a path elsewhere or a branch of the owner's.
		id := strings.TrimPrefix(wt.Branch, "igris/")
		if !ValidRunID(id) || wt.Branch != "igris/"+id || wt.Path != filepath.Join(WorktreesDir, id) {
			return "the worktree is not one igris made"
		}
	}
	return ""
}
