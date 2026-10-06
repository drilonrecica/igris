package adapt

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // fixed fixture names
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// taskTexts is each task's ID and description, in file order.
func taskTexts(p *plan.Plan) []string {
	var out []string
	for _, tk := range p.Tasks {
		out = append(out, tk.ID+": "+tk.Text)
	}
	return out
}

// Every task of each original, as the owner wrote it. The originals are
// not canonical, so the parser can't list them; the proposals must.
var wantTasks = map[string][]string{
	"renamed-columns": {"A-1: **Scaffold** the module", "A-2: **Parse** gadget files", "A-3: **Render** gadgets"},
	"prose-deps":      {"G-1: **Lexer**", "G-2: **Parser**", "G-3: **Checker**", "G-4: **Report**"},
	"missing-models":  {"S-1: **Cut** teeth", "S-2: **Polish** rims", "S-3: **Pack**"},
}

// TestScenarios runs adapt end to end on the fake backend for synthetic
// non-canonical plans: the proposal must keep every task and its
// description, and validate (or list exactly the open models).
func TestScenarios(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		deps    map[string][]string // expected Deps per task ID in the proposal; nil to skip
		openIDs []string            // tasks left at Model "?"
	}{
		{
			name:    "renamed columns",
			fixture: "renamed-columns",
		},
		{
			name:    "prose deps",
			fixture: "prose-deps",
			deps: map[string][]string{
				"G-1": nil,
				"G-2": {"G-1"},
				"G-3": {"G-1", "G-2"},
				"G-4": {"G-1", "G-2", "G-3"},
			},
		},
		{
			name:    "missing models",
			fixture: "missing-models",
			openIDs: []string{"S-1", "S-2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := readFixture(t, tt.fixture+".orig.md")
			proposed := readFixture(t, tt.fixture+".proposed.md")
			h := newHarness(t, orig)
			h.plan = filepath.Join(h.root, tt.fixture+".orig.md")
			if err := os.WriteFile(h.plan, []byte(orig), 0o600); err != nil {
				t.Fatal(err)
			}
			h.finishOnPrompt(proposed)

			res, err := h.run(context.Background(), "sonnet")
			if err != nil {
				t.Fatal(err)
			}

			// The prompt carries the plan's problems; the original is untouched.
			prompts := h.be.Prompts(ID)
			if len(prompts) != 1 {
				t.Fatalf("prompts = %d, want 1", len(prompts))
			}
			if !strings.Contains(prompts[0], "`"+tt.fixture+".orig.md`") {
				t.Errorf("prompt does not name the plan:\n%s", prompts[0])
			}
			if data, _ := os.ReadFile(h.plan); string(data) != orig { //nolint:gosec // test temp dir
				t.Error("adapt changed the plan")
			}

			// Every task and description survives the conversion, in order.
			cfg := config.Default()
			cfg.Models["opus"] = "claude-opus-5-5"
			opts := plan.Options{Columns: cfg.Columns}
			propPlan := plan.Parse(res.ProposalPath, res.Proposed, opts)
			if got, want := taskTexts(propPlan), wantTasks[tt.fixture]; !slices.Equal(got, want) {
				t.Errorf("tasks = %q, want %q", got, want)
			}

			for id, want := range tt.deps {
				var got []string
				for _, tk := range propPlan.Tasks {
					if tk.ID == id {
						got = tk.Deps
					}
				}
				if !slices.Equal(got, want) {
					t.Errorf("%s deps = %v, want %v", id, got, want)
				}
			}

			if len(res.Issues) != len(tt.openIDs) {
				t.Fatalf("issues = %v, want %d open models", res.Issues, len(tt.openIDs))
			}
			for i, id := range tt.openIDs {
				msg := res.Issues[i].Msg
				if !strings.Contains(msg, id) || !strings.Contains(msg, `model not set yet ("?")`) {
					t.Errorf("issue %d = %q, want the open model of %s", i, msg, id)
				}
				if res.Issues[i].File != res.ProposalPath {
					t.Errorf("issue file = %s, want the proposal", res.Issues[i].File)
				}
			}
			if len(tt.openIDs) > 0 && !strings.Contains(string(res.Proposed), "## Adapt notes") {
				t.Error("the proposal has no Adapt notes section")
			}

			// Accepting replaces the plan with the proposal and backs up the original.
			backup, err := Accept(h.dir, res, t0.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(backup); string(data) != orig { //nolint:gosec // test temp dir
				t.Error("the backup is not the original")
			}
			after, _ := os.ReadFile(h.plan) //nolint:gosec // test temp dir
			if string(after) != proposed {
				t.Error("the plan is not the proposal after accept")
			}
			left := plan.Parse(h.plan, after, opts).Validate(cfg.Models)
			if len(left) != len(tt.openIDs) {
				t.Errorf("check after accept = %v, want %d open models", left, len(tt.openIDs))
			}
		})
	}
}
