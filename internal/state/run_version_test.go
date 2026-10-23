package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// copyRunFixture puts a state file in a project's .igris/state.json and
// returns the project root: testdata/run/name for the damaged and newer
// ones, else the compatibility suite's, shaped like what each release wrote.
func copyRunFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "run", name)
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join("..", "..", "testdata", "compat", "state", name)
	}
	data, err := os.ReadFile(path) //nolint:gosec // a fixture in the repo
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // a fixture copied into the test's own temp dir
	if err := os.WriteFile(filepath.Join(root, DirName, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Every state file an igris release wrote loads, migrated to version 4; a
// damaged one and one from a newer igris are told apart (SPEC §13, §19).
func TestLoadRunVersions(t *testing.T) {
	tests := []struct {
		file    string
		wantErr error  // nil: loads
		wantMsg string // part of the error message
		check   func(t *testing.T, r *Run)
	}{
		{file: "v1-v0.2.json", check: func(t *testing.T, r *Run) {
			if r.Through != "M2" || r.Current == nil || r.Current.Session.ClaudeSession != "" || r.Current.VerifyAttempts != 1 {
				t.Errorf("run = %+v, current %+v", r, r.Current)
			}
		}},
		{file: "v1-v0.3.json", check: func(t *testing.T, r *Run) {
			if c := r.Current; c == nil || c.Session.PaneID != "%3" || c.PendingPrompt == "" || c.Session.ClaudeSession == "" {
				t.Errorf("current = %+v", r.Current)
			}
		}},
		{file: "v2-v0.4-slice.json", check: func(t *testing.T, r *Run) {
			if want := (&Selection{From: "M1-03", Until: "M2-02"}); !reflect.DeepEqual(r.Selection, want) {
				t.Errorf("selection = %+v", r.Selection)
			}
		}},
		{file: "v1-v0.6-worktree.json", check: func(t *testing.T, r *Run) {
			if r.Worktree == nil || r.Worktree.Branch != "igris/20261014-093000-3fa2" {
				t.Errorf("worktree = %+v", r.Worktree)
			}
		}},
		{file: "v3.json", check: func(t *testing.T, r *Run) {
			if r.Selection == nil || len(r.Selection.Only) != 2 || r.Current == nil || r.Current.Mode != "yolo" {
				t.Errorf("run = %+v", r)
			}
			// The current task became the one active task.
			if len(r.Active) != 1 || r.Active[0] != r.Current {
				t.Errorf("active = %+v, current %p", r.Active, r.Current)
			}
		}},
		{file: "v4.json", check: func(t *testing.T, r *Run) {
			c := r.Current
			if len(r.Active) != 1 || r.Active[0] != c || c.TaskID != "M1-05" || c.Attempt != 1 || c.VerifyAttempts != 1 ||
				c.ApprovedPlan == "" || c.Session == nil || c.Session.PaneID != "%7" || c.Slot != nil || c.Worktree != nil {
				t.Errorf("active = %+v", r.Active)
			}
		}},
		{file: "v4-parallel.json", check: func(t *testing.T, r *Run) {
			if len(r.Active) != 2 || r.Current != r.Active[0] {
				t.Fatalf("active = %+v", r.Active)
			}
			a, b := r.Active[0], r.Active[1]
			if a.TaskID != "M1-03" || a.Slot == nil || *a.Slot != 0 || a.PendingPrompt == "" ||
				a.Worktree == nil || a.Worktree.Branch != "igris/20261022-093000-3fa2-M1-03" {
				t.Errorf("first = %+v", a)
			}
			if b.TaskID != "M1-04" || b.Slot == nil || *b.Slot != 1 || b.Attempt != 2 || b.VerifyAttempts != 2 ||
				b.Session == nil || b.Session.PaneID != "%5" || b.Worktree == nil || b.Worktree.Path != ".igris/worktrees/20261022-093000-3fa2-M1-04" {
				t.Errorf("second = %+v", b)
			}
		}},
		{file: "not-json.json", wantErr: ErrRunDamaged, wantMsg: "not a JSON object"},
		{file: "no-version.json", wantErr: ErrRunDamaged, wantMsg: "no valid version"},
		{file: "version-string.json", wantErr: ErrRunDamaged, wantMsg: "no valid version"},
		{file: "bad-field-type.json", wantErr: ErrRunDamaged, wantMsg: "damaged: phases has the wrong type"},
		{file: "bad-task-id.json", wantErr: ErrRunDamaged, wantMsg: "not a task ID"},
		{file: "v4-current-not-first.json", wantErr: ErrRunDamaged, wantMsg: "the current task is not the first active task"},
		{file: "v4-current-not-active.json", wantErr: ErrRunDamaged, wantMsg: "the current task is not the first active task"},
		{file: "v4-null-entry.json", wantErr: ErrRunDamaged, wantMsg: "active task 2 is not a task record"},
		{file: "v4-task-twice.json", wantErr: ErrRunDamaged, wantMsg: "M1-03 is listed twice"},
		{file: "v4-mode.json", wantErr: ErrRunDamaged, wantMsg: "M1-03's mode is not a run mode"},
		{file: "v4-attempt-negative.json", wantErr: ErrRunDamaged, wantMsg: "M1-03's attempt is negative"},
		{file: "v4-approved-plan.json", wantErr: ErrRunDamaged, wantMsg: "M1-03's approved plan is not a file igris kept"},
		{file: "v4-slot-twice.json", wantErr: ErrRunDamaged, wantMsg: "M1-04's slot is not one igris gives"},
		{file: "v4-slot-range.json", wantErr: ErrRunDamaged, wantMsg: "M1-03's slot is not one igris gives"},
		{file: "v4-slot-type.json", wantErr: ErrRunDamaged, wantMsg: "damaged: active.0.slot has the wrong type"},
		{file: "v4-worktree-path.json", wantErr: ErrRunDamaged, wantMsg: "the worktree of the active task M1-03 is not one igris made for it"},
		{file: "v4-worktree-other-task.json", wantErr: ErrRunDamaged, wantMsg: "the worktree of the active task M1-03 is not one igris made for it"},
		{file: "v4-worktree-other-run.json", wantErr: ErrRunDamaged, wantMsg: "the worktree of the active task M1-03 is not one igris made for it"},
		// A newer igris may change the shape: it is never called damage.
		{file: "v5.json", wantErr: ErrRunNewer, wantMsg: "(version 5); upgrade igris"},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			r, err := PeekRun(copyRunFixture(t, tt.file))
			if tt.wantErr == nil {
				if err != nil {
					t.Fatal(err)
				}
				if r.Version != runVersion {
					t.Errorf("version %d, want %d after migration", r.Version, runVersion)
				}
				tt.check(t, r)
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) || !strings.Contains(err.Error(), "delete the file to start a new run") {
				t.Errorf("err = %v, want it to say %q and how to go on", err, tt.wantMsg)
			}
			if errors.Is(err, ErrRunDamaged) && errors.Is(err, ErrRunNewer) {
				t.Errorf("err is both damaged and newer: %v", err)
			}
		})
	}
}

// A migrated run is saved as version 4, its current task also as the one
// active task, and reads back the same.
func TestMigratedRunSavesAsVersion4(t *testing.T) {
	for _, file := range []string{"v1-v0.2.json", "v2-v0.4-slice.json", "v1-v0.6-worktree.json", "v3.json", "v4.json", "v4-parallel.json"} {
		t.Run(file, func(t *testing.T) {
			root := copyRunFixture(t, file)
			d, err := Open(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			r, err := d.LoadRun()
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SaveRun(r); err != nil {
				t.Fatal(err)
			}
			data := readFile(t, d.runPath())
			if !strings.Contains(data, `"version": 4`) || (r.Current != nil && !strings.Contains(data, `"active": [`)) {
				t.Errorf("saved %s", data)
			}
			again, err := d.LoadRun()
			if err != nil || !reflect.DeepEqual(again, r) {
				t.Errorf("reloaded %+v, %v\nwant %+v", again, err, r)
			}
		})
	}
}

func TestEnsureWorktreesDir(t *testing.T) {
	perm := func(t *testing.T, root string) os.FileMode {
		t.Helper()
		fi, err := os.Stat(filepath.Join(root, WorktreesDir))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	t.Run("fresh project", func(t *testing.T) {
		root := t.TempDir()
		old := syscall.Umask(0o022)
		defer syscall.Umask(old)
		if err := EnsureWorktreesDir(root); err != nil {
			t.Fatal(err)
		}
		if got := perm(t, root); got != 0o700 {
			t.Errorf("fresh .igris/worktrees is %o, want 700", got)
		}
	})
	t.Run("0755 leftover", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, WorktreesDir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // the 0755 git leaves
			t.Fatal(err)
		}
		if err := EnsureWorktreesDir(root); err != nil {
			t.Fatal(err)
		}
		if got := perm(t, root); got != 0o700 {
			t.Errorf("leftover .igris/worktrees is %o, want 700", got)
		}
	})
}
