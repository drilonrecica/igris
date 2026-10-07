package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// maxSignalSize bounds a signal file; a real one is a few hundred bytes.
const maxSignalSize = 64 << 10

// Signal actions (SPEC §6.2).
const (
	ActionDone = "done"
	ActionSkip = "skip"
)

// ConfigFile is the config file that marks a project root, next to DirName.
const ConfigFile = "igris.toml"

// Signal is the content of .igris/signals/<ID>.json, written by
// `igris done` / `igris skip` and consumed by the running igris.
type Signal struct {
	ID     string    `json:"id"`
	Action string    `json:"action"` // ActionDone or ActionSkip
	Note   string    `json:"note"`   // done note, or the skip reason
	At     time.Time `json:"at"`
}

// FindRoot walks up from start to the nearest directory holding igris.toml
// or .igris/ and returns it (SPEC §6.2).
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("find project root from %s: %w", start, err)
	}
	for {
		for _, marker := range []string{ConfigFile, DirName} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s or %s found in %s or any parent; run this from inside the project, or `igris init` to set one up", ConfigFile, DirName, start)
		}
		dir = parent
	}
}

func (d *Dir) signalPath(id string) string {
	return filepath.Join(d.SignalsDir(), id+".json")
}

func checkSignalID(id string) error {
	if !plan.ValidID(id) {
		return fmt.Errorf("invalid task ID %q", id)
	}
	return nil
}

// WriteSignal stores s atomically as signals/<ID>.json, replacing an
// earlier signal for the same task. A zero At is set to the current time.
func (d *Dir) WriteSignal(s Signal) error {
	if err := checkSignalID(s.ID); err != nil {
		return fmt.Errorf("write signal: %w", err)
	}
	if s.Action != ActionDone && s.Action != ActionSkip {
		return fmt.Errorf("write signal %s: unknown action %q", s.ID, s.Action)
	}
	if s.At.IsZero() {
		s.At = d.now()
	}
	s.At = s.At.UTC()
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("write signal %s: %w", s.ID, err)
	}
	return writeFileAtomic(d.signalPath(s.ID), append(data, '\n'))
}

// ReadSignal returns the pending signal for id, or (nil, nil) if there is none.
func (d *Dir) ReadSignal(id string) (*Signal, error) {
	if err := checkSignalID(id); err != nil {
		return nil, fmt.Errorf("read signal: %w", err)
	}
	return readSignalFile(d.signalPath(id), id)
}

// readSignalFile reads one signal. Sessions can write into signals/, so the
// file is only read if it is a regular file of a sane size (a FIFO would
// block igris forever), and its note is cleaned for display.
func readSignalFile(path, id string) (*Signal, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read signal %s: %w", path, err)
	}
	switch {
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("read signal %s: not a regular file (%s); delete it and run the command again", path, info.Mode().Type())
	case info.Size() > maxSignalSize:
		return nil, fmt.Errorf("read signal %s: %d bytes is too large for a signal; delete it and run the command again", path, info.Size())
	}
	data, err := os.ReadFile(path) //nolint:gosec // igris's own state directory; id validated
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read signal %s: %w", path, err)
	}
	s, err := parseSignal(data, id)
	if err != nil {
		return nil, fmt.Errorf("read signal %s: %w; delete the file or run the command again", path, err)
	}
	return s, nil
}

// parseSignal decodes the content of id's signal file. A session could have
// written it, so only a done or skip for id is accepted, and the note is
// cleaned for display.
func parseSignal(data []byte, id string) (*Signal, error) {
	var s Signal
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.ID != id || (s.Action != ActionDone && s.Action != ActionSkip) {
		return nil, fmt.Errorf("unexpected content (id %q, action %q)", s.ID, s.Action)
	}
	s.Note = textsafe.Line(s.Note)
	return &s, nil
}

// ListSignals returns every pending signal, sorted by task ID. Files that
// can't be read as a signal are returned as errors in bad, not dropped, so
// the engine can report them; one bad file never hides the others.
func (d *Dir) ListSignals() (sigs []Signal, bad []error, err error) {
	entries, err := os.ReadDir(d.SignalsDir())
	if err != nil {
		return nil, nil, fmt.Errorf("list signals: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || e.IsDir() || !plan.ValidID(id) { // also skips ".<name>.tmp-*" files
			continue
		}
		s, err := readSignalFile(filepath.Join(d.SignalsDir(), name), id)
		switch {
		case err != nil:
			bad = append(bad, err)
		case s != nil:
			sigs = append(sigs, *s)
		}
	}
	sort.Slice(sigs, func(i, j int) bool { return sigs[i].ID < sigs[j].ID })
	return sigs, bad, nil
}

// RemoveSignal deletes the signal for id; a missing signal is not an error.
func (d *Dir) RemoveSignal(id string) error {
	if err := checkSignalID(id); err != nil {
		return fmt.Errorf("remove signal: %w", err)
	}
	if err := os.Remove(d.signalPath(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove signal %s: %w", id, err)
	}
	return nil
}

// Disposition is what the engine does with a signal (SPEC §6.2).
type Disposition int

const (
	// Stray: the signal isn't for the current task. It is kept and
	// reported, never applied.
	Stray Disposition = iota
	// Apply: a done signal for the current task, or a skip signal for the
	// current user task; proceed with it.
	Apply
	// ConfirmSkip: a skip signal for the current agent task. Mark the task
	// Needs you and apply the skip only after the owner confirms.
	ConfirmSkip
)

func (d Disposition) String() string {
	switch d {
	case Apply:
		return "apply"
	case ConfirmSkip:
		return "confirm-skip"
	default:
		return "stray"
	}
}

// Classify decides what to do with s while currentID is the running task.
// currentOwner is that task's owner; it only matters for skip signals,
// where only agent tasks (including "agent + user") need confirmation
// because the owner never acts through a session for them.
func Classify(s Signal, currentID string, currentOwner plan.Owner) Disposition {
	if s.ID != currentID {
		return Stray
	}
	if s.Action == ActionSkip && currentOwner != plan.OwnerUser {
		return ConfirmSkip
	}
	return Apply
}
