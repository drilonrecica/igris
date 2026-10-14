package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/tools/testmap"
)

const mapHeading = "## Surface → test map"

// frozen maps each "Frozen:" item of SPEC §19, by the bold words it starts
// with, to its row in the map.
var frozen = map[string]string{
	"The plan format, format 1": "C1",
	"`igris.toml`":              "C3",
	"The CLI":                   "C4",
	"`--json` output":           "C6",
	"The session contract":      "C7",
	"`.igris/`":                 "C8",
	"Template variables":        "C10",
	"The generic webhook":       "C11",
	"The task-hook environment": "C12",
}

// Every row names tests that exist, and every surface SPEC §19 freezes has
// its row: a frozen surface can't go untested, and a renamed or deleted
// test fails here.
func TestEverySurfaceHasATest(t *testing.T) {
	rows := testmap.Rows(t, "docs/compatibility.md", mapHeading)
	ids := map[string]bool{}
	for i, cells := range rows {
		id := cells[0]
		if want := fmt.Sprintf("C%d", i+1); id != want {
			t.Errorf("row %d is %s, want %s (rows in order, none twice)", i+1, id, want)
		}
		ids[id] = true
		refs := testmap.Ref.FindAllStringSubmatch(cells[len(cells)-1], -1)
		if len(refs) == 0 {
			t.Errorf("%s names no test", id)
		}
		for _, ref := range refs {
			if !testmap.TestExists(t, ref[1], ref[2]) {
				t.Errorf("%s: %s.%s doesn't exist", id, ref[1], ref[2])
			}
		}
	}
	items := frozenItems(t)
	if len(items) != len(frozen) {
		t.Errorf("SPEC §19 freezes %d surfaces, the test knows %d: give the new one a row in docs/compatibility.md and add it here\n%q", len(items), len(frozen), items)
	}
	for _, item := range items {
		id, ok := frozen[item]
		switch {
		case !ok:
			t.Errorf("SPEC §19 freezes %q, which has no row here", item)
		case !ids[id]:
			t.Errorf("SPEC §19's %q maps to %s, which docs/compatibility.md doesn't have", item, id)
		}
	}
}

// frozenItems returns the bold lead of each bullet under SPEC §19's
// "**Frozen:**".
func frozenItems(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testmap.Root(t), "SPEC.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, s, ok := strings.Cut(string(data), "\n## 19. Compatibility\n")
	if !ok {
		t.Fatal("SPEC.md has no §19")
	}
	_, s, ok = strings.Cut(s, "**Frozen:**\n")
	end := strings.Index(s, "\n\n")
	if !ok || end < 0 {
		t.Fatal("SPEC §19 has no Frozen list")
	}
	var out []string
	for _, line := range strings.Split(s[:end], "\n") {
		lead, rest, ok := strings.Cut(strings.TrimPrefix(line, "- **"), "**")
		if !ok || rest == "" {
			t.Fatalf("SPEC §19: a Frozen bullet without a bold lead: %q", line)
		}
		out = append(out, lead)
	}
	return out
}
