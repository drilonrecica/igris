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
		data, err := os.ReadFile(fx.src) // read before the subtests change directory
		if err != nil {
			t.Fatal(err)
		}
		for _, args := range cmds {
			args := append([]string(nil), args...)
			goldenDir := mustAbs("testdata/golden")
			name := fx.name + "_" + strings.ReplaceAll(strings.Join(args, "_"), "--", "")
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				t.Chdir(dir)
				ariseRunner = &runner.Fake{}
				//nolint:gosec // a fixture copied into the test's own temp dir
				if err := os.WriteFile(filepath.Join(dir, "tasks.md"), data, 0o600); err != nil {
					t.Fatal(err)
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
