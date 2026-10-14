// Package testmap reads the "→ test" maps in igris's docs (the threat
// model's rule → test map, the compatibility promise's surface → test map)
// for the tests that keep them true: every row names tests that exist.
package testmap

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Ref matches a test a map names: `internal/engine.TestName`.
var Ref = regexp.MustCompile("`((?:internal|cmd|tools)/[a-z0-9_/]+)\\.(Test[A-Za-z0-9_]+)`")

// Rows returns the cells of each row of the table under heading in the
// markdown file at path, which is relative to the repository root. The
// header row (first cell "ID") and the separator are left out.
func Rows(t *testing.T, path, heading string) [][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(Root(t), path)) //nolint:gosec // the repository's own docs
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(data), "\n"+heading+"\n")
	if !ok {
		t.Fatalf("%s has no %q section", path, heading)
	}
	if next := strings.Index(after, "\n## "); next >= 0 {
		after = after[:next]
	}
	var rows [][]string
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "| ID ") {
			continue
		}
		rows = append(rows, strings.Split(strings.Trim(line, "| "), " | "))
	}
	return rows
}

// TestExists reports whether package directory pkg (relative to the
// repository root) has `func name(` in a test file.
func TestExists(t *testing.T, pkg, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(Root(t), pkg, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // the repository's own test files
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\nfunc "+name+"(") {
			return true
		}
	}
	return false
}

// Root is the repository root: the nearest directory up from the test's
// working directory that has go.mod.
func Root(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
}
