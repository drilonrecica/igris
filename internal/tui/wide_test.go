package tui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// updating reports -update. The flag is registered by teatest's golden
// package, which program_test.go imports, so it can't be declared here too.
func updating() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

// demoPlan is a synthetic plan for the layout tests.
const demoPlan = `## M0 — Repository foundation

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M0-01 | **Go module** — module path | — | done | sonnet | agent |
| M0-02 | **Entrypoint** — subcommand dispatch | M0-01 | done | sonnet | agent |
| M0-03 | **Makefile** — fmt, lint, test | M0-01 | in progress | sonnet | agent |
| M0-04 | **Config loader** with a title long enough to be cut in the narrow task pane | M0-01 | ready | opus | agent |
| M0-05 | **Buy the domain** | — | ready | — | user |
| M0-13 | **Static assets** | P0-05 | blocked | sonnet | agent |
| M0-14 | **Old idea** | — | skipped | haiku | agent |

## P0 — Decisions

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| P0-05 | **Create repo** | — | ready | — | user |
`

// withPlan writes demoPlan and loads it into the harness's model.
func (hs *harness) withPlan(text string) {
	hs.t.Helper()
	path := filepath.Join(hs.t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		hs.t.Fatal(err)
	}
	hs.m.opts.PlanPath = path
	hs.m.Update(run(hs.m.loadPlan()))
	if hs.m.plan == nil {
		hs.t.Fatal("plan not loaded")
	}
}

// golden compares got with testdata/name.golden.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if updating() {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // test fixture
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs (go test -update rewrites it):\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// checkFits fails if the view is larger than w×h.
func checkFits(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("%d lines, screen has %d", len(lines), h)
	}
	for i, l := range lines {
		if textWidth(l) > w {
			t.Errorf("line %d is %d wide, screen %d: %q", i, textWidth(l), w, l)
		}
	}
}

// layoutStates puts the harness in each state the layout tests draw.
var layoutStates = []struct {
	name  string
	setup func(hs *harness)
}{
	{"idle", func(hs *harness) {
		hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"}, engine.Event{Kind: engine.PhaseStarted, Phase: "M0"})
	}},
	{"running", func(hs *harness) {
		hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0", Detail: "phase M0"},
			engine.Event{Kind: engine.TaskDone, Phase: "M0", Task: "M0-02", Detail: "subcommand dispatch + tests"},
			started("M0-03"), opened("M0-03"))
		hs.now = t0.Add(4*time.Minute + 12*time.Second)
	}},
	{"needs_you", func(hs *harness) {
		hs.events(started("M0-03"), opened("M0-03"))
		hs.now = t0.Add(4*time.Minute + 12*time.Second)
		hs.events(engine.Event{Kind: engine.NeedsYou, Task: "M0-03", Detail: "the session is idle without a signal", At: t0.Add(3*time.Minute + 27*time.Second)})
	}},
	{"your_turn", func(hs *harness) {
		hs.events(engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: "M0-05", Title: "Buy the domain"},
			engine.Event{Kind: engine.YourTurn, Task: "M0-05", Detail: "**Buy the domain** — pick a registrar, buy it, then press d"})
	}},
	{"dialog", func(hs *harness) {
		hs.events(started("M0-03"), opened("M0-03"), asked(engine.QuestionCommit, `commit the changes of M0-03 as "M0-03: Makefile"?`))
	}},
	{"question_closed", func(hs *harness) {
		hs.events(started("M0-03"), opened("M0-03"), asked(engine.QuestionSessionLost, "the session of M0-03 is gone"))
		hs.key("esc")
	}},
}

func TestWideLayoutGolden(t *testing.T) {
	for _, st := range layoutStates {
		t.Run(st.name, func(t *testing.T) {
			hs := newHarness(t, 120, 30)
			hs.m.loc = time.UTC
			hs.withPlan(demoPlan)
			st.setup(hs)
			view := hs.m.View()
			checkFits(t, view, 120, 30)
			golden(t, "wide_"+st.name, view)
		})
	}
}

func TestWideLayoutFitsAtItsLimits(t *testing.T) {
	for _, size := range [][2]int{{100, wideMinHeight}, {100, 24}, {200, 60}} {
		for _, st := range layoutStates {
			hs := newHarness(t, size[0], size[1])
			hs.withPlan(demoPlan)
			st.setup(hs)
			if !strings.HasPrefix(hs.m.View(), "┌ igris · sinjal") {
				t.Fatalf("%v %s: not the wide layout", size, st.name)
			}
			checkFits(t, hs.m.View(), size[0], size[1])
		}
	}
}

func TestWideCardAnswerReopensTheDialog(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"), asked(engine.QuestionSessionLost, "gone"))
	hs.key("esc")
	if hs.m.dialog != nil || !strings.Contains(hs.m.View(), "[Answer…]") {
		t.Fatalf("no Answer… button after esc:\n%s", hs.m.View())
	}
	hs.click(isAct(actAnswer))
	if hs.m.dialog == nil || hs.m.dialog.question != engine.QuestionSessionLost {
		t.Fatal("Answer… did not reopen the question")
	}
	hs.click(isOption(1))
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdRetry, Continue: true}) {
		t.Errorf("sent %+v", got)
	}
}

func TestWideClicks(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.click(isAct(actPause))
	hs.click(isAct(actDone))
	if got := hs.s.take(); len(got) != 2 || got[0].Kind != engine.CmdPause || got[1].Kind != engine.CmdDone {
		t.Errorf("sent %+v", got)
	}
	// Both the bar and the card offer Open session.
	n := 0
	hs.m.View()
	for _, z := range hs.m.zones.list {
		if z.t.act == actOpen {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d open-session zones, want bar + card", n)
	}
	hs.click(isAct(actOpen))
	if len(hs.focus) != 1 {
		t.Error("open session not focused")
	}
}

func TestWideTaskListFollowsAndScrolls(t *testing.T) {
	var b strings.Builder
	b.WriteString("## L\n\n| ID | Task | Deps | Status | Model | Owner |\n|---|---|---|---|---|---|\n")
	for i := 1; i <= 40; i++ {
		status := "done"
		if i > 30 {
			status = "ready"
		}
		fmt.Fprintf(&b, "| L-%02d | **Task %02d** | — | %s | sonnet | agent |\n", i, i, status)
	}
	hs := newHarness(t, 120, 24)
	hs.withPlan(b.String())
	hs.events(engine.Event{Kind: engine.TaskStarted, Phase: "L", Task: "L-31", Title: "Task 31", Rank: "sonnet", Model: "sonnet", Mode: "default"})
	hs.m.Update(run(hs.m.loadPlan()))
	if v := hs.m.View(); !strings.Contains(v, "L-31") || strings.Contains(v, "L-01 ") {
		t.Fatalf("list doesn't follow the current task:\n%s", v)
	}
	var tasks rect
	for _, z := range hs.m.zones.list {
		if z.t.region == regionTasks {
			tasks = z.r
		}
	}
	for range 20 {
		hs.m.Update(wheel(tasks, true))
	}
	if v := hs.m.View(); !strings.Contains(v, "L-01 ") {
		t.Fatalf("wheel did not scroll the list up:\n%s", v)
	}
	hs.events(engine.Event{Kind: engine.TaskStarted, Phase: "L", Task: "L-32", Title: "Task 32"})
	if hs.m.taskTop != -1 {
		t.Error("a new task does not bring the list back to it")
	}
}

func TestGlyphs(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"))
	glyph := func(id string) string { return hs.m.glyph(hs.m.plan.Task(id)) }
	want := map[string]string{"M0-01": "✓", "M0-03": "●", "M0-04": "·", "M0-13": "⨯", "M0-14": "–"}
	for id, g := range want {
		if got := glyph(id); got != g {
			t.Errorf("%s glyph %q, want %q", id, got, g)
		}
	}
	hs.events(engine.Event{Kind: engine.NeedsYou, Task: "M0-03"})
	if got := glyph("M0-03"); got != "!" {
		t.Errorf("needs-you glyph %q", got)
	}
	if r := rank(hs.m.plan.Task("M0-05")); r != "user" {
		t.Errorf("user task rank %q", r)
	}
}
