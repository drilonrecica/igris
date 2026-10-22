package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/runner"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// TestReportGoldens pins the text and --json output of check, phases, status
// and arise --dry-run on the fixture plans. The report layer moved out of
// cmd/igris without changing a byte of it; these goldens are what says so.
func TestReportGoldens(t *testing.T) {
	savedRunner := ariseRunner
	t.Cleanup(func() { ariseRunner = savedRunner })
	fixtures := []struct {
		name, src string
		dryPhases []string
	}{
		{"valid", "testdata/plans/valid.md", []string{"P1"}},
		{"invalid", "testdata/plans/invalid.md", []string{"M0"}},
		{"drift", "testdata/plans/drift.md", []string{"A", "B"}},
		{"large", "../../internal/plan/testdata/large.md", []string{"F1"}},
		{"format4", "testdata/plans/format4.md", []string{"A"}},
		{"parallel", "testdata/plans/parallel.md", []string{"B"}},
		// A directory is a split plan: every file in it is copied.
		{"split", "testdata/plans/split", []string{"A", "B"}},
		{"split-invalid", "testdata/plans/split-invalid", []string{"A"}},
	}
	cmds := [][]string{
		{"check"}, {"check", "--json"},
		{"phases"}, {"phases", "--json"},
		{"status"}, {"status", "--json"},
	}
	for _, fx := range fixtures {
		cmds := cmds
		for _, ph := range fx.dryPhases {
			cmds = append(cmds[:len(cmds):len(cmds)], []string{"arise", ph, "--dry-run"})
		}
		files := readFixture(t, fx.src) // read before the subtests change directory
		for _, args := range cmds {
			args := append([]string(nil), args...)
			goldenDir := mustAbs("testdata/golden")
			name := fx.name + "_" + strings.ReplaceAll(strings.Join(args, "_"), "--", "")
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				t.Chdir(dir)
				ariseRunner = &runner.Fake{}
				for name, data := range files {
					path := filepath.Join(dir, name)
					if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
						t.Fatal(err)
					}
					//nolint:gosec // a fixture copied into the test's own temp dir
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				var out, errb bytes.Buffer
				code := run(args, &out, &errb)
				got := fmt.Sprintf("exit: %d\n--- stdout\n%s--- stderr\n%s", code, out.String(), errb.String())
				got = strings.ReplaceAll(got, dir, "<dir>") // t.TempDir differs per run
				golden := filepath.Join(goldenDir, name+".golden")
				if *update {
					_ = os.MkdirAll(filepath.Dir(golden), 0o750)
					if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(golden) //nolint:gosec // a golden under testdata/
				if err != nil {
					t.Fatalf("%v; run `go test ./cmd/igris -run TestReportGoldens -update`", err)
				}
				if got != string(want) {
					t.Errorf("output changed:\n%s\nwant:\n%s", got, want)
				}
			})
		}
	}
}

// readFixture returns a fixture's files by their path in the project: a
// file is tasks.md, a directory gives every file under it.
func readFixture(t *testing.T, src string) map[string][]byte {
	t.Helper()
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		data, err := os.ReadFile(src) //nolint:gosec // a fixture under testdata/
		if err != nil {
			t.Fatal(err)
		}
		return map[string][]byte{"tasks.md": data}
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		files[rel], err = os.ReadFile(path) //nolint:gosec // a fixture under testdata/
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
