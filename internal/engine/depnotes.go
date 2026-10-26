package engine

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/fsafe"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Bounds of the handoff notes in a task prompt (SPEC §6.1, V18-P3).
const (
	maxDepNote     = 500      // bytes of one dep's note
	maxDepPlan     = 4 << 10  // bytes of one dep's approved plan
	maxDepNotes    = 12 << 10 // bytes of the whole section
	maxDepPlanFile = 64 << 10 // the plan file igris reads, at most
	depLeftOut     = "(left out: the notes are over 12 KiB)"
)

// depNotes returns the handoff notes of t's direct deps (SPEC §6.1): for
// each dep that is done or skipped and left a note or a plan, in the order
// of the Deps cell, what the run log and .igris/plans/ record of its latest
// attempt. A dry run reads the owner's project, not its scratch copy. nil
// with dep_notes off or nothing to hand over; a problem is a warning, never
// an error: the prompt goes out without that dep's plan.
func (e *Engine) depNotes(t *plan.Task) []prompt.DepNote {
	notes, problems := e.collectDepNotes(t)
	for _, p := range problems {
		e.warn(p)
	}
	return notes
}

// collectDepNotes is depNotes with its problems returned, for the dry run,
// which shows the notes without repeating the warnings.
func (e *Engine) collectDepNotes(t *plan.Task) (out []prompt.DepNote, problems []string) {
	if !e.cfg.Run.DepNotesOn() || len(t.Deps) == 0 {
		return nil, nil
	}
	skip := func(err error) ([]prompt.DepNote, []string) {
		return nil, []string{fmt.Sprintf("handoff notes of %s's deps: %v; its prompt goes out without them", t.ID, err)}
	}
	p, err := e.loadPlan()
	if err != nil {
		return skip(err)
	}
	root := e.opts.contextRoot
	if root == "" {
		root = e.dir.Root()
	}
	events, err := state.PeekEvents(root)
	if err != nil && !errors.Is(err, state.ErrNoLog) {
		return skip(err)
	}
	latest := latestFinishes(events)
	size := 0
	for _, id := range t.Deps {
		dep := p.Task(id)
		if dep == nil || !dep.Status.Satisfied() {
			continue
		}
		f, ok := latest[id]
		if !ok {
			continue
		}
		n := prompt.DepNote{ID: id, Title: textsafe.Line(dep.Title), Status: dep.Status.String(), Note: cutRunes(textsafe.Line(f.done.Detail), maxDepNote)}
		if n.Note == "" && f.plan == nil {
			continue
		}
		if size+len(n.Note)+len(id)+32 > maxDepNotes {
			n.Note, f.plan = depLeftOut, nil
		}
		size += len(n.Note) + len(id) + 32
		out = append(out, n)
		if f.plan != nil {
			if msg := depPlan(root, &out[len(out)-1], f.plan, &size); msg != "" {
				problems = append(problems, msg)
			}
		}
	}
	return out, problems
}

// depPlan sets n's plan from the plan_approved line ev, if the file is
// unchanged since the approval and fits what is left of the section. It
// returns what it left out and why, if that is worth a warning.
func depPlan(root string, n *prompt.DepNote, ev *state.Event, size *int) string {
	if !state.ValidPlanFile(ev.PlanFile, n.ID) {
		return ""
	}
	data, err := fsafe.ReadFile(filepath.Join(root, ev.PlanFile), maxDepPlanFile, false)
	if err != nil {
		return fmt.Sprintf("the approved plan of %s: %v; it is left out of the prompt", n.ID, err)
	}
	if !planMatches(data, ev.PlanSHA256) {
		return fmt.Sprintf("the approved plan of %s changed since it was approved; it is left out of the prompt", n.ID)
	}
	text := textsafe.Clean(string(data))
	if len(text) > maxDepPlan {
		cut := maxDepPlan
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text, n.PlanCut = text[:cut], true
	}
	if *size+len(text) > maxDepNotes {
		return ""
	}
	*size += len(text)
	n.Plan, n.PlanFile = text, filepath.Join(root, ev.PlanFile)
	return ""
}

// planMatches reports whether the plan file's data is the plan whose sha256
// the log recorded: SavePlan ends the file with a newline the text may not
// have had.
func planMatches(data []byte, want string) bool {
	for _, d := range [][]byte{data, bytes.TrimSuffix(data, []byte("\n"))} {
		sum := sha256.Sum256(d)
		if hex.EncodeToString(sum[:]) == want {
			return true
		}
	}
	return false
}

// finish is the latest done or skip of a task and the plan approved in that
// attempt, if any.
type finish struct {
	done *state.Event
	plan *state.Event
}

// latestFinishes finds, per task, its latest task_done or task_skipped
// after its latest task_reset, in any run.
func latestFinishes(events []state.Event) map[string]finish {
	out := map[string]finish{}
	approved := map[string][]*state.Event{} // plan_approved lines since the task's last reset
	for i := range events {
		ev := &events[i]
		switch ev.Type {
		case state.EventTaskReset:
			delete(out, ev.Task)
			delete(approved, ev.Task)
		case state.EventPlanApproved:
			approved[ev.Task] = append(approved[ev.Task], ev)
		case state.EventTaskDone, state.EventTaskSkipped:
			f := finish{done: ev}
			for _, a := range approved[ev.Task] {
				if a.Run == ev.Run && a.Attempt == ev.Attempt {
					f.plan = a // the latest one of the attempt
				}
			}
			out[ev.Task] = f
		}
	}
	return out
}
