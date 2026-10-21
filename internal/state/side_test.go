package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/fsafe"
)

func sideRecord(id string, at time.Time) SideRecord {
	const uuid = "7c1d2e3f-4a5b-4c6d-8e7f-9a0b1c2d3e4f"
	return SideRecord{
		V: SideVersion, ID: id, At: at, Backend: "tmux",
		Ref:           backend.SessionRef{Backend: "tmux", TabID: "@3", PaneID: "%3", ClaudeSession: uuid},
		ClaudeSession: uuid, Model: "claude-opus-5-5[1m]", Rank: "opus", Mode: "default",
		Preset: SidePresetTask, Task: "M1-03", Dir: ".",
	}
}

func TestSideRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	if recs, bad, err := ReadSideRecords(root); recs != nil || bad != nil || err != nil {
		t.Fatalf("no side/: %v %v %v", recs, bad, err)
	}
	if _, err := os.Stat(filepath.Join(root, DirName)); !os.IsNotExist(err) {
		t.Fatalf("ReadSideRecords created .igris/: %v", err)
	}
	later, earlier := sideRecord("b4e3d2", at.Add(time.Minute)), sideRecord("a3f2c1", at)
	earlier.Preset, earlier.Task, earlier.Mode = SidePresetBlank, "", "manual"
	earlier.Worktree, earlier.Dir = SideWorktree("a3f2c1"), filepath.Join(DirName, "worktrees", "side-a3f2c1")
	for _, r := range []SideRecord{later, earlier} {
		if err := WriteSideRecord(root, r); err != nil {
			t.Fatal(err)
		}
	}
	recs, bad, err := ReadSideRecords(root)
	if err != nil || len(bad) != 0 || len(recs) != 2 {
		t.Fatalf("ReadSideRecords = %d records, %v, %v", len(recs), bad, err)
	}
	if recs[0].ID != "a3f2c1" || *recs[0].Worktree != *SideWorktree("a3f2c1") || recs[1] != later {
		t.Errorf("records %+v, want oldest first as written", recs)
	}
	if *SideWorktree("a3f2c1") != (Worktree{Path: ".igris/worktrees/side-a3f2c1", Branch: "igris/side-a3f2c1"}) {
		t.Errorf("SideWorktree = %+v", *SideWorktree("a3f2c1"))
	}
	for path, want := range map[string]os.FileMode{SideDir(root): 0o700, filepath.Join(SideDir(root), "a3f2c1.json"): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v, %v; want %v", path, fi.Mode().Perm(), err, want)
		}
	}
	data, _ := os.ReadFile(filepath.Join(SideDir(root), "b4e3d2.json")) //nolint:gosec // test temp dir
	for _, key := range []string{`"v":1`, `"id":"b4e3d2"`, `"at":`, `"backend":"tmux"`, `"ref":{`, `"claude_session":`, `"model":`, `"rank":"opus"`, `"mode":"default"`, `"preset":"task"`, `"task":"M1-03"`, `"dir":"."`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("record %s lacks %s", data, key)
		}
	}
}

// Every value igris turns into a file name, an argument or a path is
// checked, on write and on read: a session can write the record.
func TestSideRecordShape(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	tests := map[string]func(r *SideRecord){
		"version":           func(r *SideRecord) { r.V = 0 },
		"id":                func(r *SideRecord) { r.ID = "../x" },
		"upper id":          func(r *SideRecord) { r.ID = "A3F2C1" },
		"no time":           func(r *SideRecord) { r.At = time.Time{} },
		"backend":           func(r *SideRecord) { r.Backend = "-x" },
		"ref backend":       func(r *SideRecord) { r.Ref.Backend = "herdr" },
		"claude session":    func(r *SideRecord) { r.ClaudeSession = "--model"; r.Ref.ClaudeSession = "--model" },
		"ref claude":        func(r *SideRecord) { r.Ref.ClaudeSession = "2b7f3c1e-8d4a-4f6b-9c2e-5a1d0e9f8b7c" },
		"model":             func(r *SideRecord) { r.Model = "--dangerously-skip-permissions" },
		"empty model":       func(r *SideRecord) { r.Model = "" },
		"rank":              func(r *SideRecord) { r.Rank = "-x" },
		"yolo":              func(r *SideRecord) { r.Mode = "yolo" },
		"plan:yolo":         func(r *SideRecord) { r.Mode = "plan:yolo" },
		"plan:accept":       func(r *SideRecord) { r.Mode = "plan:accept" },
		"preset":            func(r *SideRecord) { r.Preset = "other" },
		"task preset no id": func(r *SideRecord) { r.Task = "" },
		"bad task":          func(r *SideRecord) { r.Task = "-rf" },
		"blank with task":   func(r *SideRecord) { r.Preset = SidePresetBlank },
		"dir":               func(r *SideRecord) { r.Dir = "/etc" },
		"worktree elsewhere": func(r *SideRecord) {
			r.Worktree, r.Dir = &Worktree{Path: "..", Branch: "igris/side-a3f2c1"}, ".."
		},
		"worktree branch": func(r *SideRecord) {
			r.Worktree = &Worktree{Path: ".igris/worktrees/side-a3f2c1", Branch: "main"}
			r.Dir = r.Worktree.Path
		},
		"worktree dir": func(r *SideRecord) { r.Worktree = SideWorktree("a3f2c1") },
	}
	if err := checkSideRecord(sideRecord("a3f2c1", at)); err != nil {
		t.Fatalf("the base record: %v", err)
	}
	for name, mutate := range tests {
		r := sideRecord("a3f2c1", at)
		mutate(&r)
		if err := WriteSideRecord(t.TempDir(), r); err == nil {
			t.Errorf("%s: WriteSideRecord accepted %+v", name, r)
		}
		root := t.TempDir()
		mustWrite(t, filepath.Join(SideDir(root), "a3f2c1.json"), mustJSON(t, r))
		recs, bad, err := ReadSideRecords(root)
		if err != nil || len(recs) != 0 || len(bad) != 1 || bad[0].ID != "a3f2c1" || errors.Is(bad[0].Err, ErrSideNewer) {
			t.Errorf("%s: read back %v, %v, %v; want one damaged record", name, recs, bad, err)
		}
	}
}

// What can't be a record is reported, never followed, blocked on or read
// whole; a newer igris's record is left alone; other names are ignored.
func TestReadSideRecordsUntrusted(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	dir := SideDir(root)
	good := sideRecord("a3f2c1", at)
	if err := WriteSideRecord(root, good); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "rec.json")
	mustWrite(t, outside, mustJSON(t, sideRecord("b4e3d2", at)))
	if err := os.Symlink(outside, filepath.Join(dir, "b4e3d2.json")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "c5f4e3.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "d6a5f4.json"), []byte(`{"v":1,"id":"d6a5f4","pad":"`+strings.Repeat("x", 5000)+`"}`))
	mustWrite(t, filepath.Join(dir, "e7b6a5.json"), mustJSON(t, sideRecord("a3f2c1", at))) // names another ID
	mustWrite(t, filepath.Join(dir, "f8c7b6.json"), []byte(`{"v":2,"id":"f8c7b6","new":true}`))
	mustWrite(t, filepath.Join(dir, "a3f2c1.rules.md"), []byte("rules"))
	mustWrite(t, filepath.Join(dir, "notes.json"), []byte("{"))

	recs, bad, err := ReadSideRecords(root)
	if err != nil || len(recs) != 1 || recs[0] != good {
		t.Fatalf("records %v, %v", recs, err)
	}
	why := map[string]error{}
	for _, p := range bad {
		why[p.ID] = p.Err
	}
	for id, want := range map[string]error{"b4e3d2": fsafe.ErrNotRegular, "c5f4e3": fsafe.ErrNotRegular, "d6a5f4": fsafe.ErrTooLarge, "f8c7b6": ErrSideNewer} {
		if !errors.Is(why[id], want) {
			t.Errorf("%s: %v, want %v", id, why[id], want)
		}
	}
	if err := why["e7b6a5"]; err == nil || errors.Is(err, ErrSideNewer) {
		t.Errorf("e7b6a5 holds a3f2c1's record: %v", err)
	}
	if len(bad) != 5 {
		t.Errorf("%d problems, want 5: %v", len(bad), bad)
	}

	// side/ itself replaced by a symlink to the owner's files.
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, SideDir(root2)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSideRecords(root2); err == nil {
		t.Error("read records through a symlinked side/")
	}
	if err := WriteSideRecord(root2, good); err == nil {
		t.Error("wrote a record through a symlinked side/")
	}
	if _, err := WriteSideRules(root2, "a3f2c1", "x"); err == nil {
		t.Error("wrote rules through a symlinked side/")
	}
}

// Of the igris processes that find a session gone, exactly one claims its
// record (SPEC §6.10).
func TestClaimSideRecord(t *testing.T) {
	root := t.TempDir()
	r := sideRecord("a3f2c1", time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC))
	if err := WriteSideRecord(root, r); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	claims := 0
	for range 8 {
		wg.Go(func() {
			got, ok, err := ClaimSideRecord(root, "a3f2c1")
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				claims++
				mu.Unlock()
				if got != r {
					t.Errorf("claimed %+v, want %+v", got, r)
				}
			}
		})
	}
	wg.Wait()
	if claims != 1 {
		t.Errorf("%d claims, want 1", claims)
	}
	if entries, _ := os.ReadDir(SideDir(root)); len(entries) != 0 {
		t.Errorf("left behind %v", entries)
	}
	if _, ok, err := ClaimSideRecord(root, "a3f2c1"); ok || err != nil {
		t.Errorf("claimed a gone record: %v, %v", ok, err)
	}
	if _, _, err := ClaimSideRecord(root, "../x"); err == nil {
		t.Error("claimed an invalid ID")
	}
	// A damaged record is claimed and removed, with the reason.
	mustWrite(t, filepath.Join(SideDir(root), "b4e3d2.json"), []byte("{"))
	if _, ok, err := ClaimSideRecord(root, "b4e3d2"); !ok || err == nil {
		t.Errorf("claim of a damaged record: %v, %v; want claimed with an error", ok, err)
	}
	if entries, _ := os.ReadDir(SideDir(root)); len(entries) != 0 {
		t.Errorf("left behind %v", entries)
	}
}

func TestSideFiles(t *testing.T) {
	root := t.TempDir()
	rules, err := WriteSideRules(root, "a3f2c1", "rules")
	if err != nil || rules != filepath.Join(root, ".igris", "side", "a3f2c1.rules.md") {
		t.Fatalf("WriteSideRules = %s, %v", rules, err)
	}
	if rules != filepath.Join(root, backend.SideRulesFile("a3f2c1")) {
		t.Errorf("rules at %s, not where tmux's binding looks", rules)
	}
	hooks, err := WriteSideHooks(root, "a3f2c1", []byte("{}"))
	if err != nil || hooks != filepath.Join(root, ".igris", "side", "a3f2c1.settings.json") {
		t.Fatalf("WriteSideHooks = %s, %v", hooks, err)
	}
	if fi, err := os.Stat(hooks); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("hooks file: %v, %v", fi.Mode().Perm(), err)
	}
	if _, err := WriteSideRules(root, "--x", "rules"); err == nil {
		t.Error("wrote rules for an invalid ID")
	}
	if err := RemoveSideFiles(root, "a3f2c1"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSideFiles(root, "a3f2c1"); err != nil {
		t.Errorf("removing again: %v", err)
	}
	if entries, _ := os.ReadDir(SideDir(root)); len(entries) != 0 {
		t.Errorf("left behind %v", entries)
	}
}

func TestNewSideID(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		id, err := NewSideID()
		if err != nil || !backend.ValidSideID(id) {
			t.Fatalf("NewSideID = %q, %v", id, err)
		}
		seen[id] = true
	}
	if len(seen) < 45 {
		t.Errorf("only %d distinct IDs in 50", len(seen))
	}
}

// mustWrite writes data to path, making its directory (0700) first.
func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
