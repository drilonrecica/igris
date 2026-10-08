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
	ActionDone  = "done"
	ActionSkip  = "skip"
	ActionReset = "reset"
)

// validAction reports whether a is a signal action igris knows.
func validAction(a string) bool { return a == ActionDone || a == ActionSkip || a == ActionReset }

// ConfigFile is the config file that marks a project root, next to DirName.
const ConfigFile = "igris.toml"

// Signal is the content of a signal file, written by `igris done` /
// `igris skip` / `igris reset` and consumed by the running igris. A task
// has two slots (SPEC §6.2): .igris/signals/<ID>.json for done and skip,
// and <ID>.reset.json for reset, so neither replaces or removes the other.
type Signal struct {
	ID     string    `json:"id"`
	Action string    `json:"action"` // ActionDone, ActionSkip or ActionReset
	Note   string    `json:"note"`   // done note, or the skip reason
	At     time.Time `json:"at"`
	// Force is a reset's --force: a done or skipped task is reset too.
	Force bool `json:"force,omitempty"`
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

// resetSuffix names the reset slot of a task: <ID>.reset.json.
const resetSuffix = ".reset.json"

func (d *Dir) signalPath(id string) string {
	return filepath.Join(d.SignalsDir(), id+".json")
}

func (d *Dir) resetPath(id string) string {
	return filepath.Join(d.SignalsDir(), id+resetSuffix)
}

// slotPath is the file a signal with action is stored in.
func (d *Dir) slotPath(id, action string) string {
	if action == ActionReset {
		return d.resetPath(id)
	}
	return d.signalPath(id)
}

func checkSignalID(id string) error {
	if !plan.ValidID(id) {
		return fmt.Errorf("invalid task ID %q", id)
	}
	return nil
}

// WriteSignal stores s atomically in its slot: signals/<ID>.json for done
// and skip, replacing an earlier one of them, or signals/<ID>.reset.json
// for a reset, replacing an earlier reset. A zero At is set to the current
// time.
func (d *Dir) WriteSignal(s Signal) error {
	if err := checkSignalID(s.ID); err != nil {
		return fmt.Errorf("write signal: %w", err)
	}
	if !validAction(s.Action) {
		return fmt.Errorf("write signal %s: unknown action %q", s.ID, s.Action)
	}
	if s.At.IsZero() {
		s.At = d.now()
	}
	s.At = s.At.UTC()
	var v any = s
	if s.Action == ActionReset {
		// A reset always says whether it was forced (SPEC §6.2).
		v = struct {
			Signal
			Force bool `json:"force"`
		}{s, s.Force}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("write signal %s: %w", s.ID, err)
	}
	return writeFileAtomic(d.slotPath(s.ID, s.Action), append(data, '\n'))
}

// ReadSignal returns the pending done or skip signal for id, or (nil, nil)
// if there is none.
func (d *Dir) ReadSignal(id string) (*Signal, error) {
	if err := checkSignalID(id); err != nil {
		return nil, fmt.Errorf("read signal: %w", err)
	}
	return readSignalFile(d.signalPath(id), id, false)
}

// ReadReset returns the pending reset signal for id, or (nil, nil) if there
// is none.
func (d *Dir) ReadReset(id string) (*Signal, error) {
	if err := checkSignalID(id); err != nil {
		return nil, fmt.Errorf("read signal: %w", err)
	}
	return readSignalFile(d.resetPath(id), id, true)
}

// readSignalFile reads one signal from the reset slot (reset) or the done
// slot. Sessions can write into signals/, so the file is only read if it is
// a regular file of a sane size (a FIFO would block igris forever), and its
// note is cleaned for display.
func readSignalFile(path, id string, reset bool) (*Signal, error) {
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
	s, err := parseSignal(data, id, reset)
	if err != nil {
		return nil, fmt.Errorf("read signal %s: %w; delete the file or run the command again", path, err)
	}
	return s, nil
}

// parseSignal decodes the content of one of id's signal files. A session
// could have written it, so only a reset for id is accepted from the reset
// slot and only a done or skip for id from the done slot, and the note is
// cleaned for display.
func parseSignal(data []byte, id string, reset bool) (*Signal, error) {
	var s Signal
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.ID != id || !validAction(s.Action) || (s.Action == ActionReset) != reset {
		return nil, fmt.Errorf("unexpected content (id %q, action %q)", s.ID, s.Action)
	}
	s.Note = textsafe.Line(s.Note)
	return &s, nil
}

// ListSignals returns every pending signal of both slots, sorted by task
// ID (a task's reset after its done or skip). Files that
// can't be read as a signal are returned as errors in bad, not dropped, so
// the engine can report them; one bad file never hides the others.
func (d *Dir) ListSignals() (sigs []Signal, bad []error, err error) {
	return listSignals(d.SignalsDir())
}

// PeekSignals is ListSignals for root/.igris/signals without creating or
// changing anything. A missing directory means no signals, not an error.
func PeekSignals(root string) (sigs []Signal, bad []error, err error) {
	sigs, bad, err = listSignals(filepath.Join(root, DirName, "signals"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	return sigs, bad, err
}

func listSignals(dir string) (sigs []Signal, bad []error, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("list signals: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || e.IsDir() || !plan.ValidID(id) { // also skips ".<name>.tmp-*" files
			continue
		}
		path := filepath.Join(dir, name)
		var (
			s    *Signal
			serr error
		)
		if base, isReset := strings.CutSuffix(name, resetSuffix); isReset && plan.ValidID(base) {
			s, serr = readSignalFile(path, base, true)
			if serr != nil {
				// A task whose own ID ends in ".reset" has its done slot here.
				if plain, perr := readSignalFile(path, id, false); perr == nil {
					s, serr = plain, nil
				}
			}
		} else {
			s, serr = readSignalFile(path, id, false)
		}
		switch {
		case serr != nil:
			bad = append(bad, serr)
		case s != nil:
			sigs = append(sigs, *s)
		}
	}
	sort.Slice(sigs, func(i, j int) bool {
		if sigs[i].ID != sigs[j].ID {
			return sigs[i].ID < sigs[j].ID
		}
		return sigs[i].Action != ActionReset && sigs[j].Action == ActionReset
	})
	return sigs, bad, nil
}

// RemoveSignal deletes the done or skip signal for id; a missing signal is
// not an error. A pending reset is kept.
func (d *Dir) RemoveSignal(id string) error {
	if err := checkSignalID(id); err != nil {
		return fmt.Errorf("remove signal: %w", err)
	}
	return removeSlot(d.signalPath(id), id)
}

// RemoveReset deletes the reset signal for id; a missing one is not an
// error.
func (d *Dir) RemoveReset(id string) error {
	if err := checkSignalID(id); err != nil {
		return fmt.Errorf("remove signal: %w", err)
	}
	return removeSlot(d.resetPath(id), id)
}

func removeSlot(path, id string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
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
	// Reset: a reset signal, for any task. The engine puts the task back
	// to ready/blocked at its next poll; the time rule doesn't apply.
	Reset
)

func (d Disposition) String() string {
	switch d {
	case Apply:
		return "apply"
	case ConfirmSkip:
		return "confirm-skip"
	case Reset:
		return "reset"
	default:
		return "stray"
	}
}

// Classify decides what to do with s while currentID is the running task.
// currentOwner is that task's owner; it only matters for skip signals,
// where only agent tasks (including "agent + user") need confirmation
// because the owner never acts through a session for them. A reset signal
// is a Reset whichever task it names.
func Classify(s Signal, currentID string, currentOwner plan.Owner) Disposition {
	if s.Action == ActionReset {
		return Reset
	}
	if s.ID != currentID {
		return Stray
	}
	if s.Action == ActionSkip && currentOwner != plan.OwnerUser {
		return ConfirmSkip
	}
	return Apply
}
