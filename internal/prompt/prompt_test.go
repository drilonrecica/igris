package prompt

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/plan"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
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

const testPlan = `## M1 — Widgets

| ID | Task | Deps | Status | Model | Owner | Spec |
|---|---|---|---|---|---|---|
| M1-01 | **Parse widgets** — read them | — | done | sonnet | agent | |
| M1-02 | **Frob widgets** — frob them all, then check | M1-01 | ready | opus | agent | §4.2 |
| M1-03 | **Pick a widget color** — recommend one | M1-01, M1-02 | blocked | opus | agent + user | |
`

func testTask(t *testing.T, id string) *plan.Task {
	t.Helper()
	p := plan.Parse("tasks.md", []byte(testPlan), plan.Options{})
	task := p.Task(id)
	if task == nil {
		t.Fatalf("task %s not in test plan", id)
	}
	return task
}

func TestRenderGolden(t *testing.T) {
	tests := []struct {
		name string
		id   string
		env  Env
	}{
		{"agent-ask", "M1-01", Env{Model: "sonnet", PlanFile: "tasks.md", CommitPolicy: "ask"}},
		{"agent-deps-extra", "M1-02", Env{Model: "opus", PlanFile: "tasks.md", CommitPolicy: "auto"}},
		{"agent-user", "M1-03", Env{Model: "opus", PlanFile: "tasks.md", CommitPolicy: "ask"}},
		{"resumed", "M1-02", Env{Model: "opus", PlanFile: "tasks.md", CommitPolicy: "ask", Resumed: true}},
		{"commit-never", "M1-02", Env{Model: "opus", PlanFile: "tasks.md", CommitPolicy: "never"}},
		{"commit-never-resumed", "M1-02", Env{Model: "opus", PlanFile: "tasks.md", CommitPolicy: "never", Resumed: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(VarsFor(testTask(t, tt.id), tt.env), "")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
				t.Errorf("want exactly one trailing newline, got %q", got[len(got)-3:])
			}
			checkGolden(t, tt.name, got)
		})
	}
}

func TestVarsFor(t *testing.T) {
	v := VarsFor(testTask(t, "M1-02"), Env{Model: "opus", PlanFile: "p.md", CommitPolicy: "ask"})
	if v.Phase != "M1" || v.PhaseTitle != "Widgets" || v.Owner != "agent" || v.Rank != "opus" {
		t.Errorf("vars = %+v", v)
	}
	if v.Title != "Frob widgets" || len(v.Deps) != 1 || v.Deps[0] != "M1-01" || v.Extra["Spec"] != "§4.2" {
		t.Errorf("vars = %+v", v)
	}
	if v.DoneCommand != "igris done M1-02" {
		t.Errorf("DoneCommand = %q", v.DoneCommand)
	}
	v = VarsFor(testTask(t, "M1-02"), Env{DoneCommand: "x done"})
	if v.DoneCommand != "x done" {
		t.Errorf("explicit DoneCommand lost: %q", v.DoneCommand)
	}
}

func TestRenderCustomTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.tmpl")
	if err := os.WriteFile(path, []byte("Do {{.ID}} ({{.Title}}) on {{.Model}}.\n\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Render(VarsFor(testTask(t, "M1-02"), Env{Model: "opus"}), path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Do M1-02 (Frob widgets) on opus.\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderErrors(t *testing.T) {
	write := func(content string) string {
		p := filepath.Join(t.TempDir(), "t.tmpl")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name, path, want string
	}{
		{"missing file", filepath.Join(t.TempDir(), "nope.tmpl"), "prompt_template"},
		{"parse error", write("{{.ID"), "parse prompt template"},
		{"unknown field", write("{{.Titel}}"), "SPEC §6.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Render(VarsFor(testTask(t, "M1-01"), Env{}), tt.path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestRules(t *testing.T) {
	r := Rules()
	if strings.Contains(r, "{{") {
		t.Error("rules must not contain template variables")
	}
	for _, want := range []string{"igris done", "Status column", "Exactly one task"} {
		if !strings.Contains(r, want) {
			t.Errorf("rules missing %q", want)
		}
	}
}

func TestContinue(t *testing.T) {
	got := Continue("M0-03")
	for _, want := range []string{"M0-03", "git status", "igris done M0-03 --note"} {
		if !strings.Contains(got, want) {
			t.Errorf("Continue() = %q, missing %q", got, want)
		}
	}
	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Errorf("Continue() = %q, want a single line", got)
	}
}
