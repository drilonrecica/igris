package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/plan"
)

// Side sessions (SPEC §6.10, §13, since 1.2): each open one has a record,
// .igris/side/<id>.json, next to its rules and hooks files. Sessions can
// write .igris/, so records are read bounded, without following a symlink,
// and shape-checked. state.json holds nothing about side sessions.

// SideVersion is the record version igris writes. Records are not frozen
// (SPEC §19): a newer igris may write another.
const SideVersion = 1

// maxSideRecordSize bounds a record file; igris writes ~600 bytes.
const maxSideRecordSize = 4 << 10

// Side session presets (SPEC §6.10).
const (
	SidePresetBlank = "blank" // no first prompt
	SidePresetTask  = "task"  // about this task: the record names the task
	SidePresetIgris = "igris" // igris problem
)

// SideModes are the modes a side session may run in: never yolo nor a
// plan:<mode> value (SPEC §6.10).
var SideModes = []string{"default", "accept", "auto", "plan"}

// SideRecord is one open side session: .igris/side/<id>.json.
type SideRecord struct {
	V             int                `json:"v"`
	ID            string             `json:"id"`
	At            time.Time          `json:"at"` // when it was opened
	Backend       string             `json:"backend"`
	Ref           backend.SessionRef `json:"ref"`
	ClaudeSession string             `json:"claude_session"`
	Model         string             `json:"model"`
	Rank          string             `json:"rank"`
	Mode          string             `json:"mode"`
	Preset        string             `json:"preset"`
	Task          string             `json:"task,omitempty"` // the task, for SidePresetTask
	// Dir is where the session works, relative to the project root: "."
	// or its worktree's path.
	Dir      string    `json:"dir"`
	Worktree *Worktree `json:"worktree,omitempty"` // its own worktree (SideWorktree)
}

// ErrSideNewer is wrapped by a SideProblem for a record a newer igris
// wrote: it is left alone, not reported as damaged.
var ErrSideNewer = errors.New("written by a newer igris")

// SideProblem is a record ReadSideRecords could not use.
type SideProblem struct {
	ID  string // from the file name
	Err error  // wraps ErrSideNewer for a newer record; anything else is damage
}

// SideDir returns root/.igris/side.
func SideDir(root string) string { return filepath.Join(root, DirName, "side") }

// SideWorktree is side session id's own worktree (SPEC §6.10):
// .igris/worktrees/side-<id> on the branch igris/side-<id>.
func SideWorktree(id string) *Worktree {
	return &Worktree{Path: filepath.Join(WorktreesDir, "side-"+id), Branch: "igris/side-" + id}
}

// NewSideID returns a random side session ID: 6 lowercase hex characters.
func NewSideID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate side session ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func sideRecordPath(root, id string) string { return filepath.Join(SideDir(root), id+".json") }

// SideRulesPath and SideHooksPath are side session id's rules and hooks
// files (backend.SideRulesFile under root, and its settings file).
func SideRulesPath(root, id string) string { return filepath.Join(root, backend.SideRulesFile(id)) }
func SideHooksPath(root, id string) string {
	return filepath.Join(SideDir(root), id+".settings.json")
}

// sideDir creates root/.igris/side (0700) if needed and makes sure it is a
// directory itself, not a symlink a session put in its place.
func sideDir(root string) (string, error) {
	dir := SideDir(root)
	if err := mkdirPrivate(dir); err != nil {
		return "", err
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory (a symbolic link?); remove it", dir)
	}
	return dir, nil
}

// WriteSideRules writes side session id's rules file and returns its path.
func WriteSideRules(root, id, content string) (string, error) {
	return writeSideFile(root, id, SideRulesPath(root, id), []byte(content))
}

// WriteSideHooks writes side session id's hooks settings file and returns
// its path.
func WriteSideHooks(root, id string, data []byte) (string, error) {
	return writeSideFile(root, id, SideHooksPath(root, id), data)
}

func writeSideFile(root, id, path string, data []byte) (string, error) {
	if !backend.ValidSideID(id) {
		return "", fmt.Errorf("write side session file: invalid side session ID %q", id)
	}
	if _, err := sideDir(root); err != nil {
		return "", err
	}
	return path, writeFileAtomic(path, data)
}

// WriteSideRecord records the open side session r, atomically.
func WriteSideRecord(root string, r SideRecord) error {
	if err := checkSideRecord(r); err != nil {
		return fmt.Errorf("write side session record: %w", err)
	}
	if _, err := sideDir(root); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("write side session record: %w", err)
	}
	return writeFileAtomic(sideRecordPath(root, r.ID), append(data, '\n'))
}

var sideRecordName = regexp.MustCompile(`^([0-9a-f]{6})\.json$`)

// ReadSideRecords returns the open side sessions recorded under root,
// oldest first, and the records it could not use. No side/ directory is
// no record. It never creates anything.
func ReadSideRecords(root string) ([]SideRecord, []SideProblem, error) {
	dir := SideDir(root)
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read side sessions: %w", err)
	}
	if !fi.IsDir() {
		return nil, nil, fmt.Errorf("read side sessions: %s is not a directory (a symbolic link?); remove it", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read side sessions: %w", err)
	}
	var recs []SideRecord
	var bad []SideProblem
	for _, e := range entries {
		m := sideRecordName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		r, err := readSideRecord(filepath.Join(dir, e.Name()), m[1])
		if err != nil {
			bad = append(bad, SideProblem{ID: m[1], Err: err})
			continue
		}
		recs = append(recs, r)
	}
	slices.SortStableFunc(recs, func(a, b SideRecord) int { return a.At.Compare(b.At) })
	return recs, bad, nil
}

// readSideRecord reads the record at path, which must be side session
// id's.
func readSideRecord(path, id string) (SideRecord, error) {
	data, err := fsafe.ReadFile(path, maxSideRecordSize, false)
	if err != nil {
		return SideRecord{}, err
	}
	var v struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return SideRecord{}, fmt.Errorf("%s: %w", path, err)
	}
	if v.V > SideVersion {
		return SideRecord{}, fmt.Errorf("%s: version %d, %w", path, v.V, ErrSideNewer)
	}
	var r SideRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return SideRecord{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := checkSideRecord(r); err != nil {
		return SideRecord{}, fmt.Errorf("%s: %w", path, err)
	}
	if r.ID != id {
		return SideRecord{}, fmt.Errorf("%s: it records side session %q", path, r.ID)
	}
	return r, nil
}

var (
	sideBackendPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)
	sideRankPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)
	sideModelPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\[\]-]{0,79}$`)
)

// ValidSideModel and ValidSideRank report whether a model or rank may be
// recorded for a side session: neither may read as a command-line option.
func ValidSideModel(s string) bool { return sideModelPattern.MatchString(s) }
func ValidSideRank(s string) bool  { return sideRankPattern.MatchString(s) }

// checkSideRecord checks every value of r that igris uses as a file name,
// a command argument or a path: a session can write the record.
func checkSideRecord(r SideRecord) error {
	switch {
	case r.V != SideVersion:
		return fmt.Errorf("version %d, not %d", r.V, SideVersion)
	case !backend.ValidSideID(r.ID):
		return fmt.Errorf("invalid side session ID %q", r.ID)
	case r.At.IsZero():
		return errors.New("no opening time")
	case !sideBackendPattern.MatchString(r.Backend) || r.Ref.Backend != r.Backend:
		return fmt.Errorf("backend %q and session ref backend %q", r.Backend, r.Ref.Backend)
	case !ValidSessionUUID(r.ClaudeSession) || r.Ref.ClaudeSession != r.ClaudeSession:
		return fmt.Errorf("invalid Claude session %q", r.ClaudeSession)
	case !ValidSideModel(r.Model):
		return fmt.Errorf("invalid model %q", r.Model)
	case !ValidSideRank(r.Rank):
		return fmt.Errorf("invalid rank %q", r.Rank)
	case !slices.Contains(SideModes, r.Mode):
		return fmt.Errorf("mode %q is not one of %s", r.Mode, strings.Join(SideModes, ", "))
	}
	switch r.Preset {
	case SidePresetTask:
		if !plan.ValidID(r.Task) {
			return fmt.Errorf("invalid task ID %q", r.Task)
		}
	case SidePresetBlank, SidePresetIgris:
		if r.Task != "" {
			return fmt.Errorf("preset %s names task %q", r.Preset, r.Task)
		}
	default:
		return fmt.Errorf("unknown preset %q", r.Preset)
	}
	if r.Worktree == nil {
		if r.Dir != "." {
			return fmt.Errorf("dir %q without a worktree, not \".\"", r.Dir)
		}
		return nil
	}
	if wt := SideWorktree(r.ID); *r.Worktree != *wt || r.Dir != wt.Path {
		return fmt.Errorf("worktree %+v and dir %q, not %s on %s", *r.Worktree, r.Dir, wt.Path, wt.Branch)
	}
	return nil
}

// ClaimSideRecord takes side session id's record for this igris: it
// renames the file aside, atomically, reads it and removes it. Of two igris
// processes ending the same session (a run and a read-only home, SPEC
// §6.10) only one claims it; the other gets false, so the session's usage
// is logged once. A record that can't be read once claimed is still
// claimed and removed: the error says why.
func ClaimSideRecord(root, id string) (SideRecord, bool, error) {
	if !backend.ValidSideID(id) {
		return SideRecord{}, false, fmt.Errorf("claim side session record: invalid side session ID %q", id)
	}
	n, err := NewPlanN()
	if err != nil {
		return SideRecord{}, false, err
	}
	claimed := filepath.Join(SideDir(root), "."+id+".claimed-"+n[:8])
	err = os.Rename(sideRecordPath(root, id), claimed)
	if errors.Is(err, fs.ErrNotExist) {
		return SideRecord{}, false, nil
	}
	if err != nil {
		return SideRecord{}, false, fmt.Errorf("claim side session record: %w", err)
	}
	defer func() { _ = os.Remove(claimed) }()
	r, err := readSideRecord(claimed, id)
	if err != nil {
		return SideRecord{}, true, fmt.Errorf("claim side session record: %w", err)
	}
	return r, true, nil
}

// RemoveSideFiles removes side session id's rules and hooks files. Missing
// files are not an error.
func RemoveSideFiles(root, id string) error {
	if !backend.ValidSideID(id) {
		return fmt.Errorf("remove side session files: invalid side session ID %q", id)
	}
	for _, path := range []string{SideRulesPath(root, id), SideHooksPath(root, id)} {
		if err := removeFile(path); err != nil {
			return fmt.Errorf("remove side session files: %w", err)
		}
	}
	return nil
}
