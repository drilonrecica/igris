package plan

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// checkGolden compares got with the golden file at path, or rewrites the
// file with -update (review the diff before committing it).
func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("%s differs (run with -update and review the diff):\n--- got ---\n%s--- want ---\n%s", path, got, want)
	}
}

func loadFixture(t *testing.T, path string) *Plan {
	t.Helper()
	p, err := Load(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGoldenInvalid(t *testing.T) {
	files, err := filepath.Glob("testdata/invalid/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("no invalid fixtures found: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".md")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f) //nolint:gosec // test fixture
			if err != nil {
				t.Fatal(err)
			}
			// Issues carry the file name; use the base name so goldens are stable.
			p := Parse(filepath.Base(f), data, Options{})
			issues := p.Validate(testModels)
			if len(issues) == 0 {
				t.Fatal("fixture must be invalid")
			}
			var b strings.Builder
			for _, i := range issues {
				b.WriteString(i.Error() + "\n")
			}
			checkGolden(t, strings.TrimSuffix(f, ".md")+".golden", b.String())
		})
	}
}

func TestGoldenLargeSummary(t *testing.T) {
	p := loadFixture(t, "testdata/large.md")
	if issues := p.Validate(testModels); len(issues) > 0 {
		t.Fatalf("large fixture must be valid: %v", issueMsgs(issues))
	}
	if len(p.Phases) < 10 || len(p.Tasks) < 150 {
		t.Fatalf("large fixture too small: %d phases, %d tasks", len(p.Phases), len(p.Tasks))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "phases %d tasks %d\n", len(p.Phases), len(p.Tasks))
	for _, ph := range p.Phases {
		counts := map[Status]int{}
		for _, t := range ph.Tasks {
			counts[t.Status]++
		}
		sel, err := p.Select(ph.ID)
		if err != nil {
			t.Fatal(err)
		}
		next := ""
		if sel.Task != nil {
			next = " " + sel.Task.ID
		}
		fmt.Fprintf(&b, "%s %q ready=%d blocked=%d in-progress=%d done=%d skipped=%d select=%s%s\n",
			ph.ID, ph.Title, counts[Ready], counts[Blocked], counts[InProgress], counts[Done], counts[Skipped], sel.Outcome, next)
	}
	b.WriteString("drift:\n")
	for _, c := range p.Readiness() {
		b.WriteString("  " + c.String() + "\n")
	}
	checkGolden(t, "testdata/large.golden", b.String())
}

func TestLargeFixtureFeatures(t *testing.T) {
	p := loadFixture(t, "testdata/large.md")
	if p.Task("X-01") != nil {
		t.Error("table inside a code fence must be ignored")
	}
	var pipes, users, agentUser, ranges, cross, modes, extra int
	for _, task := range p.Tasks {
		if strings.Contains(task.Text, "|") {
			pipes++
		}
		switch task.Owner {
		case OwnerUser:
			users++
		case OwnerAgentUser:
			agentUser++
		}
		if strings.ContainsAny(task.DepsText, "….") {
			ranges++
		}
		for _, d := range task.Deps {
			if p.Task(d).Phase != task.Phase {
				cross++
				break
			}
		}
		if task.Mode != "" {
			modes++
		}
		if task.Extra["Spec"] != "" {
			extra++
		}
	}
	for name, n := range map[string]int{"escaped pipes": pipes, "user tasks": users, "agent + user": agentUser, "ranges": ranges, "cross-phase deps": cross, "modes": modes, "extra column": extra} {
		if n == 0 {
			t.Errorf("large fixture has no %s", name)
		}
	}
}

// TestLargeFixtureWriteRoundTrip applies a Sync to the large plan and checks
// that exactly the changed Status cells differ, byte for byte.
func TestLargeFixtureWriteRoundTrip(t *testing.T) {
	orig, err := os.ReadFile("testdata/large.md")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, orig, 0o600); err != nil { //nolint:gosec // temp dir
		t.Fatal(err)
	}
	w := NewWriter(path, Options{}, testModels)
	// F1-13 is blocked behind done tasks, so completing it also changes other cells.
	changes, err := w.Update(context.Background(), func(p *Plan) ([]Change, error) { return p.Sync("F1-13", Done) })
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("expected at least the target change")
	}
	got, err := os.ReadFile(path) //nolint:gosec // temp file
	if err != nil {
		t.Fatal(err)
	}

	oldLines := strings.Split(string(orig), "\n")
	newLines := strings.Split(string(got), "\n")
	if len(oldLines) != len(newLines) {
		t.Fatalf("line count changed: %d -> %d", len(oldLines), len(newLines))
	}
	changed := 0
	for i := range oldLines {
		if oldLines[i] == newLines[i] {
			continue
		}
		changed++
		if !sameExceptStatus(oldLines[i], newLines[i]) {
			t.Errorf("line %d changed outside the Status cell:\n-%s\n+%s", i+1, oldLines[i], newLines[i])
		}
	}
	if changed != len(changes) {
		t.Errorf("%d lines changed, want %d (one per change)", changed, len(changes))
	}
	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Error("trailing newline lost")
	}
}

// sameExceptStatus reports whether two task rows differ only in the Status
// column (the fifth cell of the large fixture's header).
func sameExceptStatus(a, b string) bool {
	esc := strings.NewReplacer(`\|`, "\x00")
	ca, cb := strings.Split(esc.Replace(a), "|"), strings.Split(esc.Replace(b), "|")
	if len(ca) != len(cb) {
		return false
	}
	const statusCell = 5 // leading empty segment + ID, Task, Deps, Spec
	for i := range ca {
		if i != statusCell && ca[i] != cb[i] {
			return false
		}
	}
	return true
}
