package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// copyRunFixture puts testdata/run/name in a project's .igris/state.json
// and returns the project root.
func copyRunFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "run", name)) //nolint:gosec // a fixture in the repo
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

// Every state file an igris release wrote loads, migrated to version 3; a
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
		}},
		{file: "not-json.json", wantErr: ErrRunDamaged, wantMsg: "not a JSON object"},
		{file: "no-version.json", wantErr: ErrRunDamaged, wantMsg: "no valid version"},
		{file: "version-string.json", wantErr: ErrRunDamaged, wantMsg: "no valid version"},
		{file: "bad-field-type.json", wantErr: ErrRunDamaged, wantMsg: "damaged: phases has the wrong type"},
		{file: "bad-task-id.json", wantErr: ErrRunDamaged, wantMsg: "not a task ID"},
		// A newer igris may change the shape: it is never called damage.
		{file: "v4.json", wantErr: ErrRunNewer, wantMsg: "(version 4); upgrade igris"},
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

// A migrated run is saved as version 3 and reads back the same.
func TestMigratedRunSavesAsVersion3(t *testing.T) {
	for _, file := range []string{"v1-v0.2.json", "v2-v0.4-slice.json", "v1-v0.6-worktree.json"} {
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
			if data := readFile(t, d.runPath()); !strings.Contains(data, `"version": 3`) {
				t.Errorf("saved %s", data)
			}
			again, err := d.LoadRun()
			if err != nil || !reflect.DeepEqual(again, r) {
				t.Errorf("reloaded %+v, %v\nwant %+v", again, err, r)
			}
		})
	}
}
