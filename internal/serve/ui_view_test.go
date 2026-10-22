package serve

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// The status word and its companion line: the first thing the page says
// (docs/superpowers/specs/2026-10-22-phone-ui-design.md).
func TestUIStatusWord(t *testing.T) {
	now := uiT0.Add(4*time.Minute + 12*time.Second)
	working := Current{Task: "A", State: StateWorking}
	asking := Current{Task: "A", State: StateQuestion}
	ask := Event{Task: "A", Question: string(engine.QuestionCommit), At: uiT0.Add(2 * time.Minute)}
	tests := []struct {
		name       string
		snap       Snapshot
		word, line string
	}{
		{"no run", Snapshot{}, "Waiting", "no run yet"},
		{"started, no task", Snapshot{Run: "r", Started: uiT0}, "Waiting", "nothing running yet · 4m12s"},
		{"working", Snapshot{Run: "r", Started: uiT0, Active: []Current{working}}, "Working", "nothing needs you · 4m12s"},
		{"question", Snapshot{Run: "r", Started: uiT0, Active: []Current{asking}, Questions: []Event{ask}}, "Needs you", "1 question · 2m12s"},
		{"two need you", Snapshot{Run: "r", Started: uiT0, Active: []Current{asking, {Task: "B", State: StateYourTurn}}, Questions: []Event{ask}}, "Needs you", "2 tasks need you"},
		{"reset only", Snapshot{Run: "r", Started: uiT0, Active: []Current{working}, Reset: &Event{Task: "B", Question: string(engine.QuestionConfirmReset), At: uiT0.Add(time.Minute)}}, "Needs you", "1 question · 3m12s"},
		{"paused beats working", Snapshot{Run: "r", Started: uiT0, Paused: true, Active: []Current{working}}, "Paused", "after this task · 4m12s"},
		{"holding", Snapshot{Run: "r", Started: uiT0, Paused: true, Holding: true}, "Paused", "holding · 4m12s"},
		{"needs beats paused", Snapshot{Run: "r", Started: uiT0, Paused: true, Active: []Current{{Task: "A", State: StateLost}}}, "Needs you", "1 task needs you"},
		{"ended", Snapshot{Run: "r", Ended: true, End: "stopped"}, "Stopped", "ended: stopped"},
		{"completed", Snapshot{Run: "r", Ended: true, End: "completed"}, "Done", "ended: completed"},
		{"failed", Snapshot{Run: "r", Ended: true, End: "failed"}, "Failed", "ended: failed"},
		{"stuck", Snapshot{Run: "r", Ended: true, End: "stuck"}, "Stuck", "ended: stuck"},
		{"ended unknown", Snapshot{Run: "r", Ended: true}, "Stopped", "ended: unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			word, line := statusOf(tt.snap, now)
			if word != tt.word || line != tt.line {
				t.Errorf("statusOf = %q, %q; want %q, %q", word, line, tt.word, tt.line)
			}
		})
	}
}

// The phase bar: one segment per task of the run's phase, in plan order.
func TestUISegments(t *testing.T) {
	ph := &Phase{ID: "P1", Tasks: []Task{
		{ID: "P1-01", Status: "done"}, {ID: "P1-02", Status: "in progress"}, {ID: "P1-03", Status: "in progress"},
		{ID: "P1-04", Status: "blocked"}, {ID: "P1-05", Status: "skipped"},
	}}
	active := []Current{{Task: "P1-02", State: StateWorking}, {Task: "P1-03", State: StateQuestion}}
	got := segmentsOf(ph, active, map[string]bool{"P1-03": true})
	want := []segment{{"P1-01", "done"}, {"P1-02", "active"}, {"P1-03", "needs"}, {"P1-04", "todo"}, {"P1-05", "skipped"}}
	if len(got) != len(want) {
		t.Fatalf("segments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("segment %d = %v, want %v", i, got[i], want[i])
		}
	}
	if s := segmentsOf(nil, nil, nil); s != nil {
		t.Errorf("no phase: segments = %v", s)
	}
}

// Needs you never shows without something to tap: every state that waits
// on the owner has a sheet, with the actions that apply.
func TestUISheets(t *testing.T) {
	w := uiWeb(t, "accept", true)
	running(w.srv)
	_, body, _ := w.get("/", "cookie")
	if n := strings.Count(body, `<section class="question`); n != 3 {
		t.Errorf("running page has %d question sections, want 3 (plan, commit, reset)", n)
	}

	w = uiWeb(t, "accept", true)
	for _, ev := range []engine.Event{
		{At: uiT0, Kind: engine.RunStarted, Run: "r2", Phase: "P1"},
		{At: uiT0.Add(time.Minute), Kind: engine.TaskStarted, Task: "P1-05", Title: "Owner check"},
		{At: uiT0.Add(time.Minute), Kind: engine.YourTurn, Task: "P1-05", Detail: "check it by hand"},
	} {
		w.srv.Publish(ev)
	}
	_, body, _ = w.get("/", "cookie")
	if !strings.Contains(body, "Your turn: P1-05") || strings.Count(body, `<section class="question`) != 1 {
		t.Errorf("a user task's turn has no sheet:\n%s", body)
	}
	if !strings.Contains(body, ">Mark done<") || !strings.Contains(body, ">Skip<") || strings.Contains(body, "Retry") {
		t.Errorf("the user task's sheet offers the wrong actions:\n%s", body)
	}

	w = uiWeb(t, "accept", true)
	for _, ev := range []engine.Event{
		{At: uiT0, Kind: engine.RunStarted, Run: "r3", Phase: "P1"},
		{At: uiT0.Add(time.Minute), Kind: engine.TaskStarted, Task: "P1-03", Title: "Writer", Rank: "sonnet", Model: "claude-sonnet-5-5"},
		{At: uiT0.Add(2 * time.Minute), Kind: engine.NeedsYou, Task: "P1-03", Detail: "waiting for a permission or an answer"},
	} {
		w.srv.Publish(ev)
	}
	_, body, _ = w.get("/", "cookie")
	if strings.Count(body, `<section class="question`) != 1 || !strings.Contains(body, "waiting for a permission or an answer") {
		t.Errorf("a blocked session has no sheet:\n%s", body)
	}
	for _, label := range []string{">Mark done<", ">Skip<", ">Stop igris<"} {
		if !strings.Contains(body, label) {
			t.Errorf("the blocked sheet lacks %s", label)
		}
	}
}

// The stylesheet carries the brand: its fonts served by igris, the dark
// and light palettes by the phone's setting, no motion for those who
// asked for none, and never a bare system-ui (a monospace face on some
// Linux setups).
func TestUIStylesheetTokens(t *testing.T) {
	b, err := uiFiles.ReadFile("ui/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
	for _, want := range []string{"@font-face", `font-family: "Chakra Petch"`, "/static/fonts/chakra-petch-latin-700-normal.woff2", "prefers-color-scheme: light", "prefers-reduced-motion", "--plume: #E3172B", ".question.sheet", "nav.bar", ".word.needs"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	if regexp.MustCompile(`font(-family)?:[^;]*\bsystem-ui\b`).MatchString(css) {
		t.Error("app.css uses system-ui")
	}
}
