package threatmodel

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const mapHeading = "## Rule → test map"

// testRef is a test the map names: `internal/engine.TestName`.
var testRef = regexp.MustCompile("`((?:internal|cmd|tools)/[a-z0-9_/]+)\\.(Test[A-Za-z0-9_]+)`")

// mapRows returns the rows of the rule → test map: each row's cells.
func mapRows(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile("../../docs/threat-model.md")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(data), "\n"+mapHeading+"\n")
	if !ok {
		t.Fatalf("docs/threat-model.md has no %q section", mapHeading)
	}
	if next := strings.Index(after, "\n## "); next >= 0 {
		after = after[:next]
	}
	var rows [][]string
	for _, line := range strings.Split(after, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "|---") || strings.HasPrefix(line, "| ID ") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), " | ")
		rows = append(rows, cells)
	}
	return rows
}

// Every rule names at least one test that exists, or is an accepted risk.
// A renamed or deleted test fails here, so the map can't rot.
func TestMapNamesRealTests(t *testing.T) {
	rows := mapRows(t)
	if len(rows) < 20 {
		t.Fatalf("the map has %d rows; expected the whole rule list", len(rows))
	}
	seen := map[string]bool{}
	for _, cells := range rows {
		id := cells[0]
		if seen[id] {
			t.Errorf("rule %s is listed twice", id)
		}
		seen[id] = true
		tests := cells[len(cells)-1]
		refs := testRef.FindAllStringSubmatch(tests, -1)
		if len(refs) == 0 && !strings.Contains(tests, "accepted") {
			t.Errorf("rule %s names no test and isn't an accepted risk: %q", id, tests)
		}
		for _, ref := range refs {
			if !testExists(t, ref[1], ref[2]) {
				t.Errorf("rule %s: %s.%s doesn't exist", id, ref[1], ref[2])
			}
		}
	}
}

// testExists reports whether package dir pkg has `func name(` in a test
// file.
func testExists(t *testing.T, pkg, name string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("../..", pkg, "*_test.go"))
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

// igris never sets ANTHROPIC_API_KEY (SPEC §7.4, §16): no source file
// writes it into an environment, its own or a child's. The name appears
// only where igris reads it to warn.
func TestNeverSetsAPIKey(t *testing.T) {
	setting := regexp.MustCompile(`APIKeyVar\s*\+\s*"="|ANTHROPIC_API_KEY=|Setenv\(\s*(?:checks\.)?APIKeyVar|Setenv\(\s*"ANTHROPIC_API_KEY"`)
	for _, dir := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, err := os.ReadFile(path) //nolint:gosec // the repository's own sources
			if err != nil {
				return err
			}
			if loc := setting.FindIndex(data); loc != nil {
				t.Errorf("%s sets ANTHROPIC_API_KEY: %q", path, data[loc[0]:loc[1]])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
