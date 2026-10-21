package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/draft"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// A synthetic drafted plan that passes `igris check`.
const draftedPlan = `<!-- igris:format 1 -->
# Photo renamer

## M0 — Foundation

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| M0-01 | **Write the spec** | — | ready | — | user | — |
| M0-02 | **Decide the layout** | M0-01 | blocked | opus | agent + user | plan |
| M0-G | **M0 gate** | M0-01, M0-02 | blocked | sonnet | agent + user | — |

## Draft notes

- M0-02 opus: decides the layout
- M0-G sonnet: runs the checks
`

// draftTestProject sets up an empty directory as the working directory,
// with a fake backend whose draft session writes proposal for the plan at
// planFile (relative to it) and signals done. The review fails the test
// unless a test decides it.
func draftTestProject(t *testing.T, planFile, proposal string) (root string, be *fake.Backend) {
	t.Helper()
	root = t.TempDir()
	t.Chdir(root)
	clock := engine.NewFakeClock(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC))
	be = fake.New()
	be.SetAutoSignal(func(context.Context, string) error {
		d, err := state.Open(root, state.Options{Now: clock.Now})
		if err != nil {
			return err
		}
		if err := os.WriteFile(draft.ProposalPath(d, filepath.Join(root, planFile)), []byte(proposal), 0o600); err != nil {
			return err
		}
		return d.WriteSignal(state.Signal{ID: draft.ID, Action: state.ActionDone, Note: "drafted"})
	})
	savedBackend, savedClock, savedGetenv, savedReview := ariseBackend, adaptClock, ariseGetenv, draftReview
	t.Cleanup(func() {
		ariseBackend, adaptClock, ariseGetenv, draftReview = savedBackend, savedClock, savedGetenv, savedReview
	})
	ariseBackend = func(*config.Config) (backend.Backend, error) { return be, nil }
	adaptClock = clock
	ariseGetenv = func(string) string { return "" }
	draftReview = func(context.Context, tui.ReviewOptions) (bool, error) {
		t.Error("the review was shown")
		return false, nil
	}
	return root, be
}

// decideDraft makes the draft review answer accept and records what it was
// shown.
func decideDraft(accept bool, shown *tui.ReviewOptions) {
	draftReview = func(_ context.Context, o tui.ReviewOptions) (bool, error) {
		*shown = o
		return accept, nil
	}
}

// A goal in an empty directory: no project and no plan yet. Accept writes
// the plan, mode 0644, and no igris.toml (SPEC §9.1).
func TestDraftCommandAccept(t *testing.T) {
	root, be := draftTestProject(t, "tasks.md", draftedPlan)
	var shown tui.ReviewOptions
	decideDraft(true, &shown)

	var out, errb bytes.Buffer
	if got := run([]string{"draft", "goal", "rename photos by date"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s\nstdout: %s", got, errb.String(), out.String())
	}
	if shown.Command != "igris draft" || !shown.NewPlan || shown.PlanPath != "tasks.md" || len(shown.Issues) != 0 ||
		shown.Note != "ranks are suggestions: 2 set, 0 left at ?" || len(shown.Review.Prose) == 0 {
		t.Errorf("review shown %+v", shown)
	}
	opened := be.Opened()
	if len(opened) != 1 || opened[0].Label != "DRAFT · opus" {
		t.Errorf("opened = %+v, want opus, the default draft.model", opened)
	}
	if p := be.Prompts(draft.ID); len(p) != 1 || !strings.Contains(p[0], "rename photos by date") {
		t.Errorf("prompts = %q", p)
	}
	plan := filepath.Join(root, "tasks.md")
	for _, want := range []string{"draft session started (opus)", "draft session done: drafted", "accepted: " + plan + " written; its ranks are suggestions (2 set, 0 left at ?)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out.String())
		}
	}
	info, err := os.Stat(plan)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("plan: %v, %v", info, err)
	}
	if data, _ := os.ReadFile(plan); string(data) != draftedPlan { //nolint:gosec // test temp dir
		t.Errorf("plan = %q, want the proposal", data)
	}
	if _, err := os.Stat(filepath.Join(root, "igris.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Error("draft wrote igris.toml")
	}
	if got := run([]string{"check"}, &out, &errb); got != exitOK {
		t.Errorf("igris check on the accepted plan: exit %d", got)
	}
}

func TestDraftCommandReject(t *testing.T) {
	root, _ := draftTestProject(t, "tasks.md", draftedPlan)
	var shown tui.ReviewOptions
	decideDraft(false, &shown)

	var out, errb bytes.Buffer
	if got := run([]string{"draft", "goal"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if want := "rejected: nothing was written; the proposal stays in " + filepath.Join(root, ".igris", "draft", "tasks.proposed.md"); !strings.Contains(out.String(), want) {
		t.Errorf("stdout = %s, want %q", out.String(), want)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a rejected proposal wrote the plan")
	}
}

// Accepting a proposal with ranks left at ? is the second confirmation's
// business in the review; afterwards igris says what is left to fix.
func TestDraftCommandAcceptWithOpenRanks(t *testing.T) {
	proposal := strings.Replace(draftedPlan, "| blocked | opus |", "| blocked | ? |", 1)
	root, _ := draftTestProject(t, "tasks.md", proposal)
	var shown tui.ReviewOptions
	decideDraft(true, &shown)

	var out, errb bytes.Buffer
	if got := run([]string{"draft", "goal", "x"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if len(shown.Issues) != 1 || !strings.Contains(shown.Issues[0], "model not set yet") || shown.Note != "ranks are suggestions: 1 set, 1 left at ?" {
		t.Errorf("review = %+v", shown)
	}
	plan := filepath.Join(root, "tasks.md")
	if !strings.Contains(out.String(), "still has 1 problem(s)") || !strings.Contains(out.String(), plan+":9: M0-02: model not set yet") {
		t.Errorf("stdout = %s, want the problems against the plan", out.String())
	}
	if got := run([]string{"check"}, &out, &errb); got != exitFail {
		t.Errorf("igris check exit %d: the open rank must keep failing", got)
	}
}

// A plan in the way stops draft before anything is created (SPEC §9.1).
func TestDraftCommandPlanInTheWay(t *testing.T) {
	for _, args := range [][]string{{"draft", "goal"}, {"draft", "spec", "spec.md"}, {"draft", "import", "issues.json"}} {
		root, be := draftTestProject(t, "tasks.md", draftedPlan)
		if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		if got := run(args, &out, &errb); got != exitFail {
			t.Fatalf("%v: exit = %d, want 1", args, got)
		}
		want := "igris draft: tasks.md already exists; add to it with igris draft extend, or draft into a new file with --plan PATH (a format-2 plan can include it)\n"
		if errb.String() != want {
			t.Errorf("%v: stderr = %q, want %q", args, errb.String(), want)
		}
		if len(be.Opened()) != 0 {
			t.Errorf("%v: a session was opened", args)
		}
		if _, err := os.Stat(filepath.Join(root, ".igris")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%v: .igris/ was created", args)
		}
	}
}

// --plan drafts into a new file next to an existing plan; --model wins over
// draft.model, which wins over the default.
func TestDraftCommandPlanAndModelFlags(t *testing.T) {
	root, be := draftTestProject(t, "plans/v2.md", draftedPlan)
	for name, text := range map[string]string{"tasks.md": "mine\n", "igris.toml": "[draft]\nmodel = \"sonnet\"\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "plans"), 0o700); err != nil {
		t.Fatal(err)
	}
	var shown tui.ReviewOptions
	decideDraft(false, &shown)

	var out, errb bytes.Buffer
	if got := run([]string{"draft", "goal", "--plan", "plans/v2.md"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if shown.PlanPath != filepath.Join("plans", "v2.md") || !strings.Contains(out.String(), "v2.proposed.md") {
		t.Errorf("review %+v, stdout %s", shown, out.String())
	}
	if p := be.Prompts(draft.ID); len(p) != 1 || !strings.Contains(p[0], "`plans/v2.md`") {
		t.Errorf("prompt does not name plans/v2.md")
	}
	if got := run([]string{"draft", "goal", "--plan", "plans/v2.md", "--model", "opus"}, &out, &errb); got != exitOK {
		t.Fatalf("exit = %d, stderr: %s", got, errb.String())
	}
	if opened := be.Opened(); len(opened) != 2 || opened[0].Label != "DRAFT · sonnet" || opened[1].Label != "DRAFT · opus" {
		t.Errorf("opened = %+v, want draft.model, then --model", opened)
	}
}

func TestDraftCommandRefusals(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		setup func(t *testing.T, root string)
		want  string
	}{
		{"side session", []string{"draft", "goal"}, func(t *testing.T, _ string) { t.Setenv("IGRIS_SIDE", "a3f2c1") },
			"igris draft: this is a side session; draft plans from igris outside it\n"},
		{"outside the project", []string{"draft", "goal", "--plan", "../elsewhere.md"}, func(*testing.T, string) {}, "is outside the project"},
		{"no such directory", []string{"draft", "goal", "--plan", "plans/v2.md"}, func(*testing.T, string) {},
			"igris draft: the directory of plans/v2.md doesn't exist; create plans first, or pass another --plan\n"},
		{"invalid igris.toml", []string{"draft", "goal"}, func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "igris.toml"), []byte("[draft]\nmodel = \"haiku\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "draft.model"},
		{"spec not built yet", []string{"draft", "spec", "spec.md"}, func(*testing.T, string) {}, "`igris draft spec` is not available in this build yet"},
		{"import not built yet", []string{"draft", "import", "x.csv", "--format", "jira"}, func(*testing.T, string) {}, "`igris draft import` is not available in this build yet"},
		{"extend without a project", []string{"draft", "extend"}, func(*testing.T, string) {}, "no igris project"},
		{"extend not built yet", []string{"draft", "extend", "a phase for docs"}, func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "tasks.md"), []byte(draftedPlan), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "`igris draft extend` is not available in this build yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, be := draftTestProject(t, "tasks.md", draftedPlan)
			tt.setup(t, root)
			var out, errb bytes.Buffer
			if got := run(tt.args, &out, &errb); got != exitFail {
				t.Fatalf("exit = %d, want 1; stderr %q", got, errb.String())
			}
			if !strings.Contains(errb.String(), tt.want) {
				t.Errorf("stderr = %q, want %q", errb.String(), tt.want)
			}
			if len(be.Opened()) != 0 {
				t.Error("a session was opened")
			}
		})
	}
}

// Ctrl-C while the draft session works stops waiting: the owner's choice,
// so exit 0 (SPEC §14), and nothing is written.
func TestDraftCommandCtrlC(t *testing.T) {
	root, be := draftTestProject(t, "tasks.md", draftedPlan)
	ctx, cancel := context.WithCancel(context.Background())
	saved := draftContext
	t.Cleanup(func() { draftContext = saved; cancel() })
	draftContext = func() (context.Context, context.CancelFunc, func()) { return ctx, cancel, func() {} }
	be.SetAutoSignal(func(context.Context, string) error { cancel(); return nil }) // the session never signals
	var out, errb bytes.Buffer
	if code := run([]string{"draft", "goal"}, &out, &errb); code != exitOK {
		t.Fatalf("exit %d, want 0; stderr %q", code, errb.String())
	}
	if !strings.Contains(out.String(), "draft stopped; the DRAFT session stays open") {
		t.Errorf("stdout %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(root, "tasks.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the plan was written")
	}
}

// A file that appears at the plan path during the review is never
// overwritten: Accept fails and the proposal stays (SPEC §9.1).
func TestDraftCommandFileAppearedDuringReview(t *testing.T) {
	root, _ := draftTestProject(t, "tasks.md", draftedPlan)
	plan := filepath.Join(root, "tasks.md")
	draftReview = func(context.Context, tui.ReviewOptions) (bool, error) {
		return true, os.WriteFile(plan, []byte("mine\n"), 0o600)
	}
	var out, errb bytes.Buffer
	if got := run([]string{"draft", "goal"}, &out, &errb); got != exitFail {
		t.Fatalf("exit = %d, want 1; stdout %s", got, out.String())
	}
	if !strings.Contains(errb.String(), "a file appeared there after the review; the proposal stays in") {
		t.Errorf("stderr = %q", errb.String())
	}
	if data, _ := os.ReadFile(plan); string(data) != "mine\n" { //nolint:gosec // test temp dir
		t.Errorf("plan = %q: overwritten", data)
	}
}

// A failing review writes nothing and exits 1.
func TestDraftCommandReviewFails(t *testing.T) {
	root, _ := draftTestProject(t, "tasks.md", draftedPlan)
	draftReview = func(context.Context, tui.ReviewOptions) (bool, error) { return true, errors.New("no terminal") }
	var out, errb bytes.Buffer
	if got := run([]string{"draft", "goal"}, &out, &errb); got != exitFail {
		t.Fatalf("exit = %d, want 1", got)
	}
	if !strings.Contains(errb.String(), "review: no terminal; nothing was written") {
		t.Errorf("stderr = %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(root, "tasks.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the plan was written")
	}
}
