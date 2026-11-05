package state

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testCells() PlanCells {
	return PlanCells{Run: "20261105-010203-abcd", At: time.Date(2026, 11, 5, 1, 2, 3, 0, time.UTC), Tasks: map[string]PlanCell{
		"M1-03": {Status: "in progress", Rank: "opus", Mode: "auto", Owner: "agent", Deps: "M1-01", TextSHA256: strings.Repeat("a", 64)},
	}}
}

func TestPlanCellsRoundTrip(t *testing.T) {
	root := t.TempDir()
	d, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, why := ReadPlanCells(root); ok || why != "" {
		t.Fatalf("missing file: ok %v why %q; want no baseline, no reason", ok, why)
	}
	if err := d.WritePlanCells(testCells()); err != nil {
		t.Fatal(err)
	}
	got, ok, why := ReadPlanCells(root)
	if !ok {
		t.Fatalf("read back: %s", why)
	}
	want := testCells()
	want.V = PlanCellsVersion
	if got.V != want.V || got.Run != want.Run || !got.At.Equal(want.At) || got.Tasks["M1-03"] != want.Tasks["M1-03"] {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	if fi, err := os.Stat(PlanCellsPath(root)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v", fi, err)
	}
}

// The file is untrusted (SPEC §13): anything not of igris's shape is no
// baseline, with a reason.
func TestPlanCellsUntrusted(t *testing.T) {
	sha := strings.Repeat("a", 64)
	cases := map[string]string{
		"not json":    `{`,
		"newer":       `{"v":2,"run":"20261105-010203-abcd","at":"2026-11-05T01:02:03Z","tasks":{}}`,
		"bad run":     `{"v":1,"run":"x","at":"2026-11-05T01:02:03Z","tasks":{}}`,
		"no time":     `{"v":1,"run":"20261105-010203-abcd","tasks":{}}`,
		"no tasks":    `{"v":1,"run":"20261105-010203-abcd","at":"2026-11-05T01:02:03Z"}`,
		"bad id":      `{"v":1,"run":"20261105-010203-abcd","at":"2026-11-05T01:02:03Z","tasks":{"no id":{"status":"done","text_sha256":"` + sha + `"}}}`,
		"bad status":  `{"v":1,"run":"20261105-010203-abcd","at":"2026-11-05T01:02:03Z","tasks":{"M1-01":{"status":"done (note)","text_sha256":"` + sha + `"}}}`,
		"bad sha":     `{"v":1,"run":"20261105-010203-abcd","at":"2026-11-05T01:02:03Z","tasks":{"M1-01":{"status":"done","text_sha256":"abc"}}}`,
		"huge cell":   `{"v":1,"run":"20261105-010203-abcd","at":"2026-11-05T01:02:03Z","tasks":{"M1-01":{"status":"done","mode":"` + strings.Repeat("x", maxPlanCell+1) + `","text_sha256":"` + sha + `"}}}`,
		"wrong types": `{"v":1,"run":"20261105-010203-abcd","at":"2026-11-05T01:02:03Z","tasks":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, DirName), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(PlanCellsPath(root), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok, why := ReadPlanCells(root); ok || why == "" {
				t.Errorf("ok %v why %q; want no baseline with a reason", ok, why)
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		_ = os.MkdirAll(filepath.Join(root, DirName), 0o700)
		target := filepath.Join(root, "elsewhere.json")
		_ = os.WriteFile(target, []byte(`{}`), 0o600)
		if err := os.Symlink(target, PlanCellsPath(root)); err != nil {
			t.Fatal(err)
		}
		if _, ok, why := ReadPlanCells(root); ok || why == "" {
			t.Errorf("ok %v why %q", ok, why)
		}
	})
	t.Run("fifo", func(t *testing.T) {
		root := t.TempDir()
		_ = os.MkdirAll(filepath.Join(root, DirName), 0o700)
		if err := syscall.Mkfifo(PlanCellsPath(root), 0o600); err != nil {
			t.Skip(err)
		}
		if _, ok, why := ReadPlanCells(root); ok || why == "" {
			t.Errorf("ok %v why %q", ok, why)
		}
	})
}

// WritePlanCells refuses a baseline it couldn't read back.
func TestWritePlanCellsChecks(t *testing.T) {
	d, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	c := testCells()
	c.Run = ""
	if err := d.WritePlanCells(c); err == nil {
		t.Error("a baseline without a run ID was written")
	}
}
