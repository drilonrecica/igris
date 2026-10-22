package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// FuzzParseSignal: a session can write anything into .igris/signals/, so
// arbitrary bytes must give an error or a done/skip for the right task with
// a note that is safe to draw, never a panic.
func FuzzParseSignal(f *testing.F) {
	for _, s := range []string{
		`{"id":"M0-01","action":"done","note":"did it","at":"2026-10-07T10:00:00Z"}`,
		`{"id":"M0-01","action":"skip","note":"not needed"}`,
		`{"id":"M0-01","action":"done","note":"\u001b[31mred\u001b[0m\nline\ttab"}`,
		`{"id":"M0-02","action":"done"}`,
		`{"id":"M0-01","action":"delete"}`,
		`{"id":"M0-01","action":"done","at":"yesterday"}`,
		`[1,2,3]`, `null`, ``, `{"id":1}`,
	} {
		f.Add([]byte(s), "M0-01", false)
	}
	f.Add([]byte(`{"id":"M0-01","action":"reset","force":true}`), "M0-01", true)
	f.Add([]byte(`{"id":"M0-01","action":"reset"}`), "M0-01", false)
	f.Fuzz(func(t *testing.T, data []byte, id string, reset bool) {
		s, err := parseSignal(data, id, reset)
		if err != nil {
			if s != nil {
				t.Fatalf("error %v with a signal %+v", err, s)
			}
			return
		}
		// Each slot holds its own actions only (SPEC §6.2).
		if s.ID != id || (reset && s.Action != ActionReset) || (!reset && s.Action != ActionDone && s.Action != ActionSkip) {
			t.Fatalf("accepted %+v for id %q (reset slot %v)", s, id, reset)
		}
		if textsafe.HasControl(s.Note) || strings.ContainsAny(s.Note, "\n\t") {
			t.Fatalf("note not cleaned: %q", s.Note)
		}
	})
}

// FuzzParseAgentState: sessions can write into .igris/agent-state/, so
// arbitrary bytes must give an error or one of the states `igris hook`
// writes, never a panic (SPEC §6.3).
func FuzzParseAgentState(f *testing.F) {
	for _, s := range []string{
		`{"state":"idle","event":"Stop","at":"2026-10-07T10:00:00Z"}`,
		`{"state":"blocked","event":"PermissionRequest"}`,
		`{"state":"done"}`, `{"state":"--model"}`, `{"state":1}`,
		`[1]`, `null`, ``,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := ParseAgentState(data)
		if err != nil {
			return
		}
		switch s.State {
		case backend.Working, backend.Idle, backend.Blocked, backend.Exited:
		default:
			t.Fatalf("accepted state %q", s.State)
		}
	})
}

// FuzzLoadRun: a session can write state.json (SPEC §16), so arbitrary
// bytes must give an error or a run whose active list holds what igris
// itself would write: the current task first, task IDs, distinct tasks and
// slots in range, never a panic (SPEC §13).
func FuzzLoadRun(f *testing.F) {
	for _, s := range []string{
		`{"version": 3, "phases": ["M1"], "current": {"task_id": "M1-01", "mode": "auto"}}`,
		`{"version": 4, "phases": ["M1"], "current": {"task_id": "M1-01"}, "active": [{"task": "M1-01", "slot": 0}, {"task": "M1-02", "slot": 1}]}`,
		`{"version": 4, "active": [{"task": "M1-01", "worktree": {"path": ".igris/worktrees/20261022-093000-3fa2-M1-01", "branch": "igris/20261022-093000-3fa2-M1-01"}}], "current": {"task_id": "M1-01"}}`,
		`{"version": 4, "active": [null]}`, `{"version": 4, "active": {}}`, `{"version": 5}`, `[]`, ``,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := loadRun(path)
		if err != nil {
			return
		}
		if r.Version != runVersion {
			t.Fatalf("version %d after migration", r.Version)
		}
		if (r.Current == nil) != (len(r.Active) == 0) || (r.Current != nil && r.Current != r.Active[0]) {
			t.Fatalf("current %+v is not the first of %+v", r.Current, r.Active)
		}
		slots := map[int]bool{}
		for i, c := range r.Active {
			if c == nil || !plan.ValidID(c.TaskID) || r.Active[:i].index(c.TaskID) >= 0 {
				t.Fatalf("active entry %d: %+v", i, c)
			}
			if c.Slot != nil {
				if *c.Slot < 0 || *c.Slot >= MaxSlots || slots[*c.Slot] {
					t.Fatalf("slot %d of %s", *c.Slot, c.TaskID)
				}
				slots[*c.Slot] = true
			}
		}
	})
}
