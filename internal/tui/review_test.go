package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/plan"
)

const reviewOld = `# Widgets

| Key | What | State | LLM |
|---|---|---|---|
| W-1 | Parse widgets | todo | sonnet |
`

const reviewNew = `## W — Widgets

| ID | Task | Status | Model |
|---|---|---|---|
| W-1 | Parse widgets | ready | ? |
`

func reviewOpts(issues ...string) ReviewOptions {
	return ReviewOptions{
		PlanPath: "tasks.md",
		Review:   adapt.LineReview([]byte(reviewOld), []byte(reviewNew)),
		Issues:   issues,
	}
}

// reviewHarness drives a review by hand, like harness does the run TUI.
type reviewHarness struct {
	t    *testing.T
	r    *review
	quit bool
}

func newReviewHarness(t *testing.T, w, h int, o ReviewOptions) *reviewHarness {
	t.Helper()
	rh := &reviewHarness{t: t, r: newReview(o)}
	rh.r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return rh
}

func (rh *reviewHarness) result(cmd tea.Cmd) {
	if _, ok := run(cmd).(tea.QuitMsg); ok {
		rh.quit = true
	}
}

func (rh *reviewHarness) keys(ks ...string) {
	for _, k := range ks {
		_, cmd := rh.r.Update(keyMsg(k))
		rh.result(cmd)
	}
}

func (rh *reviewHarness) click(want func(target) bool) {
	rh.t.Helper()
	rh.r.View()
	for _, z := range rh.r.zones.list {
		if want(z.t) {
			_, cmd := rh.r.Update(tea.MouseMsg{X: z.r.x, Y: z.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
			rh.result(cmd)
			return
		}
	}
	rh.t.Fatalf("no zone to click; zones: %+v\n%s", rh.r.zones.list, rh.r.View())
}

func TestReviewDecisions(t *testing.T) {
	tests := []struct {
		name     string
		issues   []string
		keys     []string
		quit     bool
		accepted bool
	}{
		{"enter rejects by default", nil, []string{"enter"}, true, false},
		{"a accepts a valid proposal", nil, []string{"a"}, true, true},
		{"tab then enter accepts", nil, []string{"tab", "enter"}, true, true},
		{"right then left keeps reject", nil, []string{"right", "left", "enter"}, true, false},
		{"r rejects", nil, []string{"r"}, true, false},
		{"esc rejects", nil, []string{"esc"}, true, false},
		{"q rejects", nil, []string{"q"}, true, false},
		{"ctrl+c rejects", nil, []string{"ctrl+c"}, true, false},
		{"scrolling decides nothing", nil, []string{"down", "pgdown", "end", "home", "up"}, false, false},
		{"invalid: a asks first", []string{"x"}, []string{"a"}, false, false},
		{"invalid: the dialog starts on keep reviewing", []string{"x"}, []string{"a", "enter", "enter"}, true, false},
		{"invalid: replace anyway", []string{"x"}, []string{"a", "down", "enter"}, true, true},
		{"invalid: option number", []string{"x"}, []string{"a", "2"}, true, true},
		{"invalid: esc backs out of the dialog", []string{"x"}, []string{"a", "esc"}, false, false},
		{"invalid: ctrl+c in the dialog rejects", []string{"x"}, []string{"a", "ctrl+c"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rh := newReviewHarness(t, 100, 30, reviewOpts(tt.issues...))
			rh.keys(tt.keys...)
			if rh.quit != tt.quit || rh.r.accepted != tt.accepted {
				t.Errorf("quit %v accepted %v, want %v %v", rh.quit, rh.r.accepted, tt.quit, tt.accepted)
			}
		})
	}
}

func TestReviewClicks(t *testing.T) {
	rh := newReviewHarness(t, 100, 30, reviewOpts())
	rh.click(isAct(actAccept))
	if !rh.quit || !rh.r.accepted {
		t.Error("clicking Accept did not accept")
	}

	rh = newReviewHarness(t, 100, 30, reviewOpts())
	rh.click(isAct(actReject))
	if !rh.quit || rh.r.accepted {
		t.Error("clicking Reject did not reject")
	}

	rh = newReviewHarness(t, 100, 30, reviewOpts("p.md:3: W-1: model not set yet"))
	rh.click(isAct(actAccept))
	if rh.quit || rh.r.dialog == nil {
		t.Fatal("Accept on an invalid proposal must ask first")
	}
	rh.click(isOption(1))
	if !rh.quit || !rh.r.accepted {
		t.Error("clicking Replace anyway did not accept")
	}
}

func TestReviewView(t *testing.T) {
	rh := newReviewHarness(t, 60, 20, reviewOpts(`p.md:5: W-1: model not set yet ("?"); fill in one of: opus, sonnet`))
	out := rh.r.View()
	lines := strings.Split(out, "\n")
	if len(lines) != 20 {
		t.Errorf("%d lines, want exactly the 20 of the terminal", len(lines))
	}
	for i, l := range lines {
		if w := textWidth(l); w > 60 {
			t.Errorf("line %d is %d cells wide: %q", i, w, l)
		}
	}
	for _, want := range []string{
		"review tasks.md",
		"⨯ the proposal has 1 problem(s)",
		"+3 −3 lines",
		"⨯ p.md:5: W-1: model not set yet",
		"- | Key | What | State | LLM |",
		"+ | ID | Task | Status | Model |",
		"[›Reject‹]", "[Accept]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}

	rh = newReviewHarness(t, 60, 20, reviewOpts())
	if out := rh.r.View(); !strings.Contains(out, "✓ the proposal passes igris check") || strings.Contains(out, "Problems") {
		t.Errorf("valid proposal view:\n%s", out)
	}
}

func TestReviewFoldsUnchangedLines(t *testing.T) {
	var old, proposed []string
	for i := range 40 {
		old = append(old, fmt.Sprintf("line %d", i))
		proposed = append(proposed, fmt.Sprintf("line %d", i))
	}
	proposed[20] = "changed"
	rh := newReviewHarness(t, 80, 40, ReviewOptions{PlanPath: "p.md", Review: adapt.Review{Prose: adapt.Diff(old, proposed)}})
	out := rh.r.View()
	for _, want := range []string{"⋯ 17 unchanged line(s)", "⋯ 16 unchanged line(s)", "  line 17", "- line 20", "+ changed", "  line 23"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line 16\n") || strings.Contains(out, "line 24") {
		t.Errorf("lines outside the context are shown:\n%s", out)
	}
}

func TestReviewScrollAndWrap(t *testing.T) {
	var proposed []string
	for i := range 100 {
		proposed = append(proposed, fmt.Sprintf("| T-%d | %s |", i, strings.Repeat("x", 70)))
	}
	rh := newReviewHarness(t, 50, 20, ReviewOptions{PlanPath: "p.md", Review: adapt.Review{Prose: adapt.Diff(nil, proposed)}})
	out := rh.r.View()
	if !strings.Contains(out, "1–15 of 200") {
		t.Errorf("want 2 rows per wrapped line and a scroll position:\n%s", out)
	}
	if !strings.Contains(out, "+ | T-0 | xxx") || !strings.Contains(out, "\n+ xxx") {
		t.Errorf("wrapped rows must keep the + marker:\n%s", out)
	}
	rh.keys("end")
	if out := rh.r.View(); !strings.Contains(out, "186–200 of 200") {
		t.Errorf("end did not scroll to the bottom:\n%s", out)
	}
	rh.r.Update(wheel(rect{0, 5, 1, 1}, true))
	if out := rh.r.View(); !strings.Contains(out, "183–197 of 200") {
		t.Errorf("the wheel did not scroll up:\n%s", out)
	}
}

func TestReviewCleansControlCharacters(t *testing.T) {
	rh := newReviewHarness(t, 80, 10, ReviewOptions{PlanPath: "p.md", Review: adapt.Review{Prose: adapt.Diff(nil, []string{"a\x1b[2Jb\tc"})}})
	out := rh.r.View()
	if strings.Contains(out, "\x1b") || !strings.Contains(out, "+ a?[2Jb    c") {
		t.Errorf("control characters reach the terminal: %q", out)
	}
}

func TestReviewNoColorKeepsMarkers(t *testing.T) {
	rh := newReviewHarness(t, 80, 20, reviewOpts("x"))
	rh.r.th = noColorTheme(t)
	out := strip(rh.r.View())
	for _, want := range []string{"⨯ the proposal", "- # Widgets", "+ ## W — Widgets", "[›Reject‹]"} {
		if !strings.Contains(out, want) {
			t.Errorf("NO_COLOR view missing %q:\n%s", want, out)
		}
	}
}

func TestReviewProgram(t *testing.T) {
	tm := teatest.NewTestModel(t, newReview(reviewOpts()), teatest.WithInitialTermSize(80, 24))
	seen(t, tm, "✓ the proposal passes", "[Accept]")
	tm.Send(keyMsg("tab"))
	tm.Send(keyMsg("enter"))
	final := tm.FinalModel(t, teatest.WithFinalTimeout(waitFor)).(*review)
	if !final.accepted {
		t.Error("tab, enter did not accept")
	}
}

// tableReview compares the synthetic reordered-columns plan of the adapt
// tests with its proposal.
func tableReview(t *testing.T) ReviewOptions {
	t.Helper()
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("..", "adapt", "testdata", "reorder", name)) //nolint:gosec // test fixture
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	rv := adapt.Compare(read("original.md"), read("proposed.md"), plan.Options{Columns: map[string]string{"Depends on": "Deps"}})
	if !rv.Tables {
		t.Fatal("the fixture must compare table by table")
	}
	return ReviewOptions{PlanPath: "tasks.md", Review: rv, Issues: []string{`proposed.md:30: L-02: model not set yet ("?")`}}
}

func TestReviewTablesGolden(t *testing.T) {
	for _, sz := range []struct {
		name string
		w, h int
	}{{"wide", 100, 50}, {"narrow", 60, 60}} {
		t.Run(sz.name, func(t *testing.T) {
			rh := newReviewHarness(t, sz.w, sz.h, tableReview(t))
			view := rh.r.View()
			checkFits(t, view, sz.w, sz.h)
			golden(t, "review_tables_"+sz.name, view)
		})
	}
}

func TestReviewTablesLayout(t *testing.T) {
	rh := newReviewHarness(t, 100, 60, tableReview(t))
	out := rh.r.View()
	for _, want := range []string{
		"table change(s) · +5 −1 lines outside the tables",
		"~ phase W1 — Parsing widgets",
		`  ~ W1-03 Deps         "W1-01" → "W1-01, W1-02"`,
		"+ phase W3 — Animation",
		"  + W3-00 Wait for the animation spec from the client",
		"- phase Later",
		"Outside the task tables:",
		"+ Plan for the widget factory. Ship by spring 2027.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("wide view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "| W1-01") {
		t.Errorf("table rows must not be line-diffed:\n%s", out)
	}

	rh = newReviewHarness(t, 60, 60, tableReview(t))
	out = rh.r.View()
	for _, want := range []string{
		`  ~ W1-03 Deps: "W1-01" → "W1-01, W1-02"`,
		"  ~ columns reordered\n",
		`  -   "Status, ID, Model, task, Depends on"`,
		`  +   "ID, Task, Deps, Status, Model"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("narrow view missing %q:\n%s", want, out)
		}
	}
}

func TestReviewTablesWrapKeepsMarkers(t *testing.T) {
	long := strings.Repeat("y", 70)
	o := ReviewOptions{PlanPath: "p.md", Review: adapt.Review{Tables: true, Sections: []adapt.Section{{
		Op: adapt.Mod, Phase: "A", Heading: "A — x",
		Changes: []adapt.Change{{Op: adapt.Mod, What: "A-1 Task", Old: "x", New: long + "\x1b[2J"}, {Op: adapt.Add, What: "A-2", New: long}},
	}}}}
	rh := newReviewHarness(t, 50, 20, o)
	out := rh.r.View()
	for _, want := range []string{`  +   "yyy`, "\n  + yyy", "  + A-2 yyy", "The text outside the task tables is unchanged."} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("control characters reach the terminal: %q", out)
	}
	for i, l := range strings.Split(out, "\n") {
		if w := textWidth(l); w > 50 {
			t.Errorf("line %d is %d cells wide: %q", i, w, l)
		}
	}
}

func TestReviewTablesUnchanged(t *testing.T) {
	rh := newReviewHarness(t, 80, 20, ReviewOptions{PlanPath: "p.md", Review: adapt.Review{Tables: true}})
	if out := rh.r.View(); !strings.Contains(out, "The task tables are unchanged.") {
		t.Errorf("view:\n%s", out)
	}
}

func TestReviewTablesDecisions(t *testing.T) {
	rh := newReviewHarness(t, 100, 30, tableReview(t))
	rh.keys("end", "a")
	if rh.quit || rh.r.dialog == nil {
		t.Fatal("Accept on an invalid proposal must ask first")
	}
	rh.keys("2")
	if !rh.quit || !rh.r.accepted {
		t.Error("replace anyway did not accept")
	}
	rh = newReviewHarness(t, 60, 20, tableReview(t))
	rh.keys("pgdown", "enter")
	if !rh.quit || rh.r.accepted {
		t.Error("enter did not reject")
	}
}
