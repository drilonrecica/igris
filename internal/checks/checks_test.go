package checks

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
)

// driftPlan has a hint (a "Depends" column next to Deps-less table B) and
// drift (a-2 is blocked although a-1 is done).
const driftPlan = "## A — First\n\n" +
	"| ID | Deps | Status | Model |\n|---|---|---|---|\n" +
	"| a-1 | — | done | sonnet |\n" +
	"| a-2 | a-1 | blocked | sonnet |\n\n" +
	"## B — Second\n\n" +
	"| ID | Depends | Status | Model |\n|---|---|---|---|\n" +
	"| b-1 | a-2 | ready | sonnet |\n"

func writePlan(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func ids(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// With every input set, Run gives every check in the list's order.
func TestRunOrder(t *testing.T) {
	r := &runner.Fake{}
	r.On([]string{"git", "rev-parse", "--is-inside-work-tree"}, runner.Result{Stdout: []byte("true\n")}, nil)
	r.On([]string{"git", "status", "--porcelain"}, runner.Result{}, nil)
	h := fakeHerdr{}
	versions := func(context.Context, runner.Runner) []Result {
		return []Result{{ID: IDClaude, Level: OK, Message: "Claude Code 2.1.292"}, {ID: IDHerdr, Level: OK, Message: "herdr 0.9.1"}, {ID: IDTmux, Level: Warn, Message: "tmux not found in PATH"}}
	}
	got := Run(context.Background(), Options{
		Root: writePlan(t, driftPlan), Runner: r, Getenv: func(string) string { return "" },
		Versions: versions, BackendName: IDHerdr, Backend: h, Integration: h, Config: config.Default(),
	})
	// Only the chosen backend's tool is checked: no tmux row on herdr.
	want := slices.DeleteFunc(slices.Clone(order), func(id string) bool { return id == IDTmux })
	if !slices.Equal(slices.Compact(ids(got)), want) {
		t.Errorf("ids = %v, want %v", ids(got), want)
	}
	got = Run(context.Background(), Options{IDs: []string{IDClaude, IDHerdr, IDTmux}, Versions: versions, BackendName: IDTmux})
	if !slices.Equal(ids(got), []string{IDClaude, IDTmux}) {
		t.Errorf("tmux backend: ids = %v", ids(got))
	}
	if got := Run(context.Background(), Options{IDs: []string{IDClaude, IDHerdr, IDTmux}, Versions: versions}); !slices.Equal(ids(got), []string{IDClaude}) {
		t.Errorf("no backend chosen: ids = %v", ids(got))
	}
	// IDs given in another order don't change the order of the results.
	got = Run(context.Background(), Options{IDs: []string{IDDrift, IDAPIKey}, Getenv: func(string) string { return "" }, Root: writePlan(t, driftPlan), Config: config.Default()})
	if !slices.Equal(ids(got), []string{IDAPIKey, IDDrift}) {
		t.Errorf("ids = %v", ids(got))
	}
	// Pick reorders.
	if p := Pick(got, IDDrift, IDAPIKey); !slices.Equal(ids(p), []string{IDDrift, IDAPIKey}) {
		t.Errorf("pick = %v", ids(p))
	}
}

// A check without its input gives no result.
func TestRunSkipsChecksWithoutInput(t *testing.T) {
	if got := Run(context.Background(), Options{IDs: []string{IDClaude, IDHerdr, IDTmux, IDAPIKey, IDBackend, IDHerdrIntegration, IDGit, IDConfig, IDPlanHints, IDDrift}}); len(got) != 0 {
		t.Errorf("results = %+v", got)
	}
}

func TestPlanChecks(t *testing.T) {
	root := writePlan(t, driftPlan)
	got := Problems(Run(context.Background(), Options{IDs: []string{IDPlanHints, IDDrift}, Root: root, Config: config.Default()}))
	if len(got) != 2 {
		t.Fatalf("problems = %+v", got)
	}
	hint, drift := got[0], got[1]
	path := filepath.Join(root, "tasks.md")
	if hint.ID != IDPlanHints || hint.File != path || hint.Line == 0 || !strings.Contains(hint.Message, `column "Depends" looks like dependencies`) {
		t.Errorf("hint = %+v", hint)
	}
	if hint.String() != (plan.Issue{File: path, Line: hint.Line, Msg: hint.Message}).Error() {
		t.Errorf("hint line = %q", hint.String())
	}
	want := Result{ID: IDDrift, Level: Warn, Confirm: true, File: path, Line: drift.Line, Task: "a-2", From: "blocked", To: "ready",
		Message: "a-2 is blocked but all its dependencies are satisfied; igris will set it to ready"}
	if drift != want || drift.Line == 0 {
		t.Errorf("drift = %+v, want %+v", drift, want)
	}
	if got := DriftLine(drift); got != (plan.Change{ID: "a-2", From: plan.Blocked, To: plan.Ready}).String() {
		t.Errorf("drift line = %q", got)
	}

	// A clean plan: one OK result each.
	clean := Run(context.Background(), Options{IDs: []string{IDPlanHints, IDDrift}, Root: writePlan(t, "## A — First\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| a | — | ready | sonnet |\n"), Config: config.Default()})
	if len(clean) != 2 || len(Problems(clean)) != 0 {
		t.Errorf("clean plan = %+v", clean)
	}

	// An invalid or missing plan: nothing (whoever loads it reports it).
	cycle := "## A — First\n\n| ID | Deps | Status | Model |\n|---|---|---|---|\n| a | b | blocked | sonnet |\n| b | a | blocked | sonnet |\n"
	for name, root := range map[string]string{"invalid": writePlan(t, cycle), "missing": t.TempDir()} {
		if got := Run(context.Background(), Options{IDs: []string{IDPlanHints, IDDrift}, Root: root, Config: config.Default()}); len(got) != 0 {
			t.Errorf("%s plan: %+v", name, got)
		}
	}
}

// A plan passed in is used as is, not loaded again.
func TestPlanGiven(t *testing.T) {
	p := plan.Parse("other.md", []byte(driftPlan), plan.Options{})
	got := Problems(Run(context.Background(), Options{IDs: []string{IDDrift}, Root: t.TempDir(), Config: config.Default(), Plan: p}))
	if len(got) != 1 || got[0].File != "other.md" {
		t.Errorf("drift = %+v", got)
	}
}

func TestConfigWarnings(t *testing.T) {
	cfg := config.Default()
	if got := Run(context.Background(), Options{IDs: []string{IDConfig}, Config: cfg}); len(got) != 1 || got[0].Level != OK {
		t.Errorf("default config = %+v", got)
	}
	cfg.Claude.Command = "/opt/claude"
	got := Run(context.Background(), Options{IDs: []string{IDConfig}, Config: cfg})
	if len(got) != 1 || got[0].Level != Warn || !strings.HasPrefix(got[0].String(), `igris.toml: claude.command = "/opt/claude" is ignored`) {
		t.Errorf("claude.command = %+v", got)
	}
}

func TestProject(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "igris.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := ParentConfig(sub)
	if parent != filepath.Join(root, "igris.toml") || ParentConfig(root) != "" {
		t.Fatalf("ParentConfig = %q, %q", parent, ParentConfig(root))
	}
	tests := []struct {
		name     string
		o        Options
		level    Level
		wantMsg  string
		wantNext string
	}{
		{"has igris.toml", Options{}, OK, "igris.toml found", ""},
		{"no igris.toml", Options{NoConfig: true}, OK, "no igris.toml here; using the defaults (`igris init` creates one)", "igris init"},
		{"parent igris.toml", Options{NoConfig: true, ParentConfig: parent}, Warn, ParentHint(parent), "cd " + root},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.o.IDs = []string{IDProject}
			got := Run(context.Background(), tt.o)
			if len(got) != 1 || got[0].Level != tt.level || got[0].Message != tt.wantMsg || got[0].Next != tt.wantNext {
				t.Errorf("got %+v", got)
			}
		})
	}
}

// Untrusted text (plan cells, command output) comes out clean.
func TestResultsAreSafe(t *testing.T) {
	versions := func(context.Context, runner.Runner) []Result {
		return []Result{{ID: IDClaude, Level: Warn, Message: "claude\x1b]0;evil\x07 1.0\nnext"}}
	}
	got := Run(context.Background(), Options{IDs: []string{IDClaude}, Versions: versions})
	if len(got) != 1 || got[0].Message != "claude 1.0 next" {
		t.Errorf("got %q", got[0].Message)
	}
	p := plan.Parse("tasks\x1b[2J.md", []byte(driftPlan), plan.Options{})
	rs := Run(context.Background(), Options{IDs: []string{IDPlanHints, IDDrift}, Config: config.Default(), Plan: p})
	if len(Problems(rs)) == 0 {
		t.Fatalf("no drift found: %+v", rs)
	}
	for _, r := range rs {
		if strings.ContainsRune(r.Message+r.File, '\x1b') {
			t.Errorf("unsafe result %+v", r)
		}
	}
}

func TestResultString(t *testing.T) {
	for _, tt := range []struct {
		r    Result
		want string
	}{
		{Result{Message: "m"}, "m"},
		{Result{File: "f", Message: "m"}, "f: m"},
		{Result{File: "f", Line: 3, Message: "m"}, "f:3: m"},
	} {
		if got := tt.r.String(); got != tt.want {
			t.Errorf("%+v = %q, want %q", tt.r, got, tt.want)
		}
	}
}
