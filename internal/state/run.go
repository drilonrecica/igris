package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
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
	// runVersionOptional is igris 0.7–1.4's: the selection is optional in
	// it.
	runVersionOptional = 3
	// runVersion is igris 1.5's: the active list beside the current task,
	// so igris 1.0–1.4, which would see only the first active task, refuse
	// it. A new field gets a new version only when an igris that ignored
	// it would do something wrong.
	runVersion = 4
)

// maxBudgetOverrides bounds state.json's budget_overrides: more than a
// config can set.
const maxBudgetOverrides = 1024

// MaxSlots bounds a parallel phase's slots (SPEC §6.11): an active task's
// slot is from 0 to MaxSlots-1.
const MaxSlots = 8

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
	ConfigHash string     `json:"config_hash"` // hash of the config snapshot
	// Current is the first active task, nil when none is active; it is
	// written as before version 4 so older readers of the file keep
	// working. Activate and Deactivate keep it in step with Active.
	Current *Current `json:"current,omitempty"`
	// Active are the tasks being worked on, in plan order (SPEC §5.1,
	// §6.11); a sequential phase has at most one. Since version 4.
	Active ActiveTasks `json:"active,omitempty"`
	// Worktree is where the run's sessions, verify and commits happen in
	// worktree mode (SPEC §6.8); nil runs them in the project root.
	Worktree *Worktree `json:"worktree,omitempty"`
	// ConfigChanged says igris.toml was edited during the run and differs
	// from the snapshot the run uses; the next run asks the owner before
	// using the file (SPEC §13, V10-05). Cleared when the file is
	// restored.
	ConfigChanged bool `json:"config_changed,omitempty"`
	// BudgetOverrides are the budgets the owner raised or let go on for
	// this run (SPEC §6.9), by budget (run, phase:<id>, rank:<rank>), to
	// the new value or BudgetContinue. Since 1.8; no version change: an
	// igris that ignores it asks again at the original value.
	BudgetOverrides map[string]string `json:"budget_overrides,omitempty"`
	// BatchBase is the run branch's head before the run's first commit
	// under commit = "batch" (SPEC §6.5): the base the batch question's
	// Undo resets to. Kept across a resume of the run, cleared once the
	// question is settled. Since 1.8; no version change: an igris that
	// ignores it never asks.
	BatchBase string `json:"batch_base,omitempty"`
	// CommitOverride is the commit policy the owner's Commit all set for
	// the rest of the run (SPEC §6.5): "auto", or "" for igris.toml's.
	// Since 1.8; no version change.
	CommitOverride string `json:"commit_override,omitempty"`
}

// CommitAll is the only commit policy CommitOverride holds.
const CommitAll = "auto"

// commitSHA is a full SHA-1 or SHA-256 commit hash.
var commitSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidCommit reports whether s is a full commit hash.
func ValidCommit(s string) bool { return commitSHA.MatchString(s) }

// Worktree is a run's git worktree (SPEC §6.8).
type Worktree struct {
	Path   string `json:"path"`   // relative to the project root: .igris/worktrees/<run-id>
	Branch string `json:"branch"` // igris/<run-id>
}

// WorktreesDir is where worktree mode puts its worktrees, relative to the
// project root.
var WorktreesDir = filepath.Join(DirName, "worktrees")

// EnsureWorktreesDir creates root/.igris/worktrees 0700, or tightens an
// existing one, before `git worktree add`: git would make it with the
// umask (0755), and `igris doctor` wants 0700.
func EnsureWorktreesDir(root string) error {
	return mkdirPrivate(filepath.Join(root, WorktreesDir))
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
	PendingPrompt string `json:"pending_prompt,omitempty"`
	// ApprovedPlan is the plan the owner last approved in igris for the
	// task, .igris/plans/<ID>-a<attempt>-<n>.md (SPEC §6.3), for the
	// commit body. Since 1.1; no version change.
	ApprovedPlan string    `json:"approved_plan,omitempty"`
	StartedAt    time.Time `json:"started_at"`

	// The fields only the active list records (SPEC §13), not "current".

	// Attempt is the task's attempt in the run that recorded it (SPEC
	// §13, runs.jsonl); 0 before its first.
	Attempt int `json:"-"`
	// Worktree is the task's own worktree in a parallel phase (SPEC
	// §6.11), .igris/worktrees/<run-id>-<ID> on igris/<run-id>-<ID>.
	Worktree *Worktree `json:"-"`
	// Slot is the task's slot in a parallel phase (SPEC §6.11), from 0 to
	// MaxSlots-1; nil outside one.
	Slot *int `json:"-"`
	// Resolver is set while a resolver session works on the task (SPEC
	// §6.11): the entry's session fields then hold its session. Since
	// 1.8; no version change.
	Resolver *Resolver `json:"-"`
}

// Resolver records a resolver session (SPEC §6.11): why it was opened,
// FailedConflict or FailedVerify.
type Resolver struct {
	Reason string `json:"reason"`
}

// ActiveTasks is the run's active list. Its entries share the record of
// Current but are written as {"task", "attempt", "session",
// "claude_session", "worktree", "slot", …} (SPEC §13).
type ActiveTasks []*Current

// activeEntry is an active task as state.json writes it.
type activeEntry struct {
	Task           string              `json:"task"`
	Attempt        int                 `json:"attempt"`
	Session        *backend.SessionRef `json:"session,omitempty"`
	ClaudeSession  string              `json:"claude_session"`
	Worktree       *Worktree           `json:"worktree,omitempty"`
	Slot           *int                `json:"slot,omitempty"`
	Resolver       *Resolver           `json:"resolver,omitempty"`
	Mode           string              `json:"mode"`
	VerifyAttempts int                 `json:"verify_attempts"`
	PendingPrompt  string              `json:"pending_prompt,omitempty"`
	ApprovedPlan   string              `json:"approved_plan,omitempty"`
	StartedAt      time.Time           `json:"started_at"`
}

// MarshalJSON writes the active list in its own shape.
func (a ActiveTasks) MarshalJSON() ([]byte, error) {
	out := make([]activeEntry, 0, len(a))
	for _, c := range a {
		out = append(out, activeEntry{
			Task: c.TaskID, Attempt: c.Attempt, Session: c.Session, ClaudeSession: c.ClaudeSession,
			Worktree: c.Worktree, Slot: c.Slot, Resolver: c.Resolver, Mode: c.Mode, VerifyAttempts: c.VerifyAttempts,
			PendingPrompt: c.PendingPrompt, ApprovedPlan: c.ApprovedPlan, StartedAt: c.StartedAt,
		})
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads the active list; a null entry stays nil, which
// checkReadBack reports.
func (a *ActiveTasks) UnmarshalJSON(data []byte) error {
	var in []*activeEntry
	if err := json.Unmarshal(data, &in); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			// Named from the run, as for its other fields: active.0.slot.
			te.Field = strings.TrimSuffix("active."+te.Field, ".")
		}
		return err
	}
	out := make(ActiveTasks, 0, len(in))
	for _, e := range in {
		if e == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, &Current{
			TaskID: e.Task, Attempt: e.Attempt, Session: e.Session, ClaudeSession: e.ClaudeSession,
			Worktree: e.Worktree, Slot: e.Slot, Resolver: e.Resolver, Mode: e.Mode, VerifyAttempts: e.VerifyAttempts,
			PendingPrompt: e.PendingPrompt, ApprovedPlan: e.ApprovedPlan, StartedAt: e.StartedAt,
		})
	}
	*a = out
	return nil
}

// ActiveTask returns the record of active task id, or nil.
func (r *Run) ActiveTask(id string) *Current {
	for _, c := range r.Active {
		if c.TaskID == id {
			return c
		}
	}
	return nil
}

// Activate records c as an active task, in place of an earlier record of
// the same task. A new task goes last: the scheduler starts tasks in plan
// order (SPEC §5.1).
func (r *Run) Activate(c *Current) {
	for i, old := range r.Active {
		if old.TaskID == c.TaskID {
			r.Active[i] = c
			r.syncCurrent()
			return
		}
	}
	r.Active = append(r.Active, c)
	r.syncCurrent()
}

// Deactivate forgets active task id, if it is one.
func (r *Run) Deactivate(id string) {
	r.Active = slices.DeleteFunc(r.Active, func(c *Current) bool { return c.TaskID == id })
	if len(r.Active) == 0 {
		r.Active = nil
	}
	r.syncCurrent()
}

// syncCurrent makes Current the first active task.
func (r *Run) syncCurrent() {
	r.Current = nil
	if len(r.Active) > 0 {
		r.Current = r.Active[0]
	}
}

// SaveRun writes r to state.json atomically. A run with a Current but no
// active list has its Current as the one active task.
func (d *Dir) SaveRun(r *Run) error {
	r.Version = runVersion
	if len(r.Active) == 0 && r.Current != nil {
		r.Active = ActiveTasks{r.Current}
	}
	r.syncCurrent()
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
	if r.Version < runVersion {
		// No release before 1.5 wrote an active list: anything under that
		// name is not part of the file.
		r.Active = nil
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
		r.Version = runVersionOptional
	}
	switch r.Version {
	case runVersionOptional:
		// The current task becomes the one active task.
		if r.Current != nil {
			r.Active = ActiveTasks{r.Current}
		}
		r.Version = runVersion
	case runVersion:
		// The active list is what igris uses; the file's current task,
		// checked to be its first, is for older readers.
		r.syncCurrent()
	}
}

// sessionModes are the run modes a recorded session can have (SPEC §7.1);
// "" is a task with no session yet.
var sessionModes = []string{"", "default", "accept", "auto", "plan", "plan:default", "plan:accept", "plan:auto", "plan:yolo", "yolo", "manual"}

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
	if len(r.BudgetOverrides) > maxBudgetOverrides {
		return "too many budget overrides"
	}
	for k, v := range r.BudgetOverrides {
		if !ValidBudgetKey(k) || !ValidBudgetValue(v) {
			return "a budget override is not a budget and a value igris writes"
		}
	}
	switch {
	case r.BatchBase != "" && !ValidCommit(r.BatchBase):
		return "the batch base is not a commit hash"
	case r.CommitOverride != "" && r.CommitOverride != CommitAll:
		return "the commit override is not a commit policy igris writes"
	}
	runID := ""
	if wt := r.Worktree; wt != nil {
		// Only the worktree igris makes: .igris/worktrees/<run-id> on
		// igris/<run-id>, never a path elsewhere or a branch of the owner's.
		id := strings.TrimPrefix(wt.Branch, "igris/")
		if !ValidRunID(id) || wt.Branch != "igris/"+id || wt.Path != filepath.Join(WorktreesDir, id) {
			return "the worktree is not one igris made"
		}
		runID = id
	}
	if r.Version < runVersion {
		return ""
	}
	switch {
	case len(r.Active) == 0 && r.Current != nil,
		len(r.Active) > 0 && (r.Current == nil || r.Active[0] == nil || r.Current.TaskID != r.Active[0].TaskID):
		return "the current task is not the first active task"
	}
	slots := map[int]bool{}
	for i, c := range r.Active {
		if c == nil {
			return fmt.Sprintf("active task %d is not a task record", i+1)
		}
		switch {
		case !plan.ValidID(c.TaskID):
			return fmt.Sprintf("active task %d is not a task ID", i+1)
		case r.Active[:i].index(c.TaskID) >= 0:
			return fmt.Sprintf("the active task %s is listed twice", c.TaskID)
		case !slices.Contains(sessionModes, c.Mode):
			return fmt.Sprintf("the active task %s's mode is not a run mode", c.TaskID)
		case c.VerifyAttempts < 0:
			return fmt.Sprintf("the active task %s's verify attempts are negative", c.TaskID)
		case c.Attempt < 0:
			return fmt.Sprintf("the active task %s's attempt is negative", c.TaskID)
		case c.ApprovedPlan != "" && !ValidPlanFile(c.ApprovedPlan, c.TaskID):
			return fmt.Sprintf("the active task %s's approved plan is not a file igris kept", c.TaskID)
		}
		if s := c.Slot; s != nil {
			if *s < 0 || *s >= MaxSlots || slots[*s] {
				return fmt.Sprintf("the active task %s's slot is not one igris gives", c.TaskID)
			}
			slots[*s] = true
		}
		if rs := c.Resolver; rs != nil && rs.Reason != FailedConflict && rs.Reason != FailedVerify {
			return fmt.Sprintf("the resolver of the active task %s has no reason igris writes", c.TaskID)
		}
		if wt := c.Worktree; wt != nil {
			// Only the task's own worktree in this run:
			// .igris/worktrees/<run-id>-<ID> on igris/<run-id>-<ID>.
			id, ok := strings.CutSuffix(strings.TrimPrefix(wt.Branch, "igris/"), "-"+c.TaskID)
			if !ok || !ValidRunID(id) || (runID != "" && id != runID) ||
				wt.Branch != "igris/"+id+"-"+c.TaskID || wt.Path != filepath.Join(WorktreesDir, id+"-"+c.TaskID) {
				return fmt.Sprintf("the worktree of the active task %s is not one igris made for it", c.TaskID)
			}
			runID = id
		}
	}
	return ""
}

// index is the position of task id in a, or -1.
func (a ActiveTasks) index(id string) int {
	return slices.IndexFunc(a, func(c *Current) bool { return c != nil && c.TaskID == id })
}
