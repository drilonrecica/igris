package serve

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
)

// The status word and its companion line: the first thing the page says
// (docs/superpowers/specs/2026-10-22-phone-ui-design.md).
func TestUIStatusWord(t *testing.T) {
	now := uiT0.Add(4*time.Minute + 12*time.Second)
	working := Current{Task: "A", State: StateWorking}
	asking := Current{Task: "A", State: StateQuestion}
	ask := Event{Task: "A", Question: string(engine.QuestionCommit), At: uiT0.Add(2 * time.Minute)}
	tests := []struct {
		name             string
		snap             Snapshot
		word, line, tone string
	}{
		{"no run", Snapshot{}, "Idle", "no run yet", "dim"},
		{"started, no task", Snapshot{Run: "r", Started: uiT0}, "Working", "nothing running yet · 4m12s", ""},
		{"working", Snapshot{Run: "r", Started: uiT0, Active: []Current{working}}, "Working", "nothing needs you · 4m12s", ""},
		{"question", Snapshot{Run: "r", Started: uiT0, Active: []Current{asking}, Questions: []Event{ask}}, "Needs you", "1 question · 2m12s", "needs"},
		{"answered, card still asks", Snapshot{Run: "r", Started: uiT0, Active: []Current{asking}}, "Working", "nothing needs you · 4m12s", ""},
		{"two need you", Snapshot{Run: "r", Started: uiT0, Active: []Current{asking, {Task: "B", State: StateYourTurn}}, Questions: []Event{ask}}, "Needs you", "2 tasks need you", "needs"},
		{"one needs you", Snapshot{Run: "r", Started: uiT0, Active: []Current{{Task: "B", State: StateYourTurn}}}, "Needs you", "1 task needs you", "needs"},
		{"reset only", Snapshot{Run: "r", Started: uiT0, Active: []Current{working}, Reset: &Event{Task: "B", Question: string(engine.QuestionConfirmReset), At: uiT0.Add(time.Minute)}}, "Needs you", "1 question · 3m12s", "needs"},
		{"pause waits for the task", Snapshot{Run: "r", Started: uiT0, Paused: true, Active: []Current{working}}, "Working", "pauses after A · 4m12s", ""},
		{"pause waits for the tasks", Snapshot{Run: "r", Started: uiT0, Paused: true, Active: []Current{working, {Task: "B", State: StateWorking}}}, "Working", "pauses after these tasks · 4m12s", ""},
		{"paused between tasks", Snapshot{Run: "r", Started: uiT0, Paused: true}, "Paused", "after this task · 4m12s", "plain"},
		{"holding", Snapshot{Run: "r", Started: uiT0, Paused: true, Holding: true}, "Paused", "holding · 4m12s", "plain"},
		{"needs beats paused", Snapshot{Run: "r", Started: uiT0, Paused: true, Active: []Current{{Task: "A", State: StateLost}}}, "Needs you", "1 task needs you", "needs"},
		{"stopped", Snapshot{Run: "r", Ended: true, End: "stopped"}, "Done", "– stopped", "plain"},
		{"completed", Snapshot{Run: "r", Ended: true, End: "completed"}, "Done", "✓ completed", ""},
		{"failed", Snapshot{Run: "r", Ended: true, End: "failed"}, "Done", "✗ failed", "ember"},
		{"error", Snapshot{Run: "r", Ended: true, End: "error"}, "Done", "✗ failed", "ember"},
		{"stuck", Snapshot{Run: "r", Ended: true, End: "stuck"}, "Done", "▲ stuck", "ember"},
		{"ended unknown", Snapshot{Run: "r", Ended: true}, "Done", "– stopped", "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusOf(tt.snap, now)
			line := got.Line
			if got.Ran != "" {
				line += " · " + got.Ran
			}
			if got.Word != tt.word || line != tt.line || got.Tone != tt.tone {
				t.Errorf("statusOf = %+v; want %s, %q, %s", got, tt.word, tt.line, tt.tone)
			}
			// The clock the line ends with is a span the script keeps
			// ticking from Since (seen frozen at the V17 gate).
			if (got.Ran == "") != got.Since.IsZero() || (got.Ran != "" && got.Ran != look.Duration(now.Sub(got.Since))) {
				t.Errorf("clock %q since %v", got.Ran, got.Since)
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
	active := []Current{{Task: "P1-02", State: StateQuestion}, {Task: "P1-03", State: StateQuestion}}
	got := segmentsOf(ph, active, map[string]bool{"P1-03": true}) // P1-02's answer is in flight
	want := []segment{{"P1-01", "done", "done"}, {"P1-02", "active", "working"}, {"P1-03", "needs", "needs you"}, {"P1-04", "todo", "to do"}, {"P1-05", "skipped", "skipped"}}
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
	if strings.Contains(body, "`") {
		t.Error("the user task's sheet prints literal backticks")
	}
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
// asked for none, a visible keyboard focus, Chakra Petch on buttons, and
// never a bare system-ui (a monospace face on some Linux setups).
func TestUIStylesheetTokens(t *testing.T) {
	css := appCSS(t)
	for _, want := range []string{"@font-face", `font-family: "Chakra Petch"`, "/static/fonts/chakra-petch-latin-700-normal.woff2", "prefers-color-scheme: light", "prefers-reduced-motion", "--plume: #E3172B", ".question.sheet", ".question.sheet.peek", "nav.bar", ".word.needs", "pointer-events: none", "--on-accent: #05070F", ":focus-visible { outline: 2px solid var(--accent)", ".warn::before { content: \"▲ \"; }"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css lacks %s", want)
		}
	}
	if regexp.MustCompile(`font(-family)?:[^;]*\bsystem-ui\b`).MatchString(css) {
		t.Error("app.css uses system-ui")
	}
	buttons := cssRules(css)["button, .btn, details.retry summary"]
	if !strings.Contains(buttons, "font-family: var(--display)") || !strings.Contains(buttons, "font-weight: 700") {
		t.Errorf("buttons are not Chakra Petch 700: %s", buttons)
	}
}

// Several things waiting at once: each entry says which it is and steps
// to the next, the last back to the first, so no link leads nowhere; a
// card whose entry is in the sheet links to it instead of offering its
// actions a second time.
func TestUISheetsCounted(t *testing.T) {
	w := uiWeb(t, "accept", true)
	running(w.srv)
	_, body, _ := w.get("/", "cookie")
	for _, want := range []string{
		`id="q-1" data-key="plan P1-02"`, `href="#q-2">1 of 3 ›`,
		`id="q-2" data-key="commit? P1-03"`, `href="#q-3">2 of 3 ›`,
		`id="q-3" data-key="reset? P1-04"`, `href="#q-1">3 of 3 ›`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if regexp.MustCompile(`<a class="step"[^>]*>`).FindAllString(body, -1) == nil || strings.Contains(body, `<a class="step">`) {
		t.Error("a stepper without a target")
	}
	if strings.Contains(body, "`igris done`") {
		t.Error("the page prints literal backticks")
	}
	for _, card := range []struct{ id, q string }{{"P1-02", "q-1"}, {"P1-03", "q-2"}} {
		c := cardOf(t, body, card.id)
		if !strings.Contains(c, `<a class="to-sheet" href="#`+card.q+`">▸ answer in the sheet</a>`) || !strings.Contains(c, `<span class="glyph needs" aria-hidden="true">!</span>`) {
			t.Errorf("card %s doesn't point to its question:\n%s", card.id, c)
		}
		if strings.Contains(c, "<form") {
			t.Errorf("card %s repeats the sheet's actions:\n%s", card.id, c)
		}
	}
}

// A card with nothing in the sheet keeps its actions, its tokens written
// as §15.4 writes them and its warnings in Ember's words.
func TestUIWorkingCard(t *testing.T) {
	body := uiPages(t)["working.html"]
	c := cardOf(t, body, "P1-03")
	for _, want := range []string{`>Mark done<`, `>Skip<`, `<details class="retry">`, `2.3M in · 41k out`, `<p class="warn">Overdue · `, `<p class="warn">Rank mismatch · `, `<span class="rank rank-sonnet">sonnet</span>`, `<span class="glyph" aria-hidden="true">●</span>`} {
		if !strings.Contains(c, want) {
			t.Errorf("the working card lacks %s:\n%s", want, c)
		}
	}
	if strings.Contains(c, "to-sheet") {
		t.Error("a working card points to the sheet")
	}
}

// cardOf is task id's card on the page.
func cardOf(t *testing.T, body, id string) string {
	t.Helper()
	c := regexp.MustCompile(`(?s)<article class="card[^"]*" id="card-` + regexp.QuoteMeta(id) + `">.*?</article>`).FindString(body)
	if c == "" {
		t.Fatalf("no card %s", id)
	}
	return c
}

// Recent says what happened in plain words, how long ago, and what a
// finished task used.
func TestUIRecent(t *testing.T) {
	now := uiT0.Add(time.Hour)
	tk := &Tokens{In: 2_345_678, Out: 41_234}
	tests := []struct {
		ev   Event
		want recentView
	}{
		{Event{Type: string(engine.TaskDone), Task: "P1-02", Detail: "parser in", Tokens: tk, At: uiT0.Add(35 * time.Minute)}, recentView{Glyph: "✓", Text: "P1-02 done · parser in · 2.3M in · 41k out", Tone: "done", Ago: "25m ago"}},
		{Event{Type: string(engine.TaskStarted), Task: "P1-03", Title: "Writer", At: uiT0}, recentView{Glyph: "●", Text: "P1-03 started · Writer", Ago: "1h ago"}},
		{Event{Type: string(engine.Asked), Task: "P1-03", Question: string(engine.QuestionCommit), At: now.Add(-20 * time.Second)}, recentView{Glyph: "!", Text: "P1-03 asked: commit?", Ago: "20s ago"}},
		{Event{Type: string(engine.Warning), Detail: "ignored mode", At: now}, recentView{Glyph: "▲", Text: "ignored mode", Tone: "alert", Ago: "0s ago"}},
		{Event{Type: string(engine.VerifyFailed), Task: "P1-03", Detail: "exit 1", At: now}, recentView{Glyph: "✗", Text: "P1-03 verify failed · exit 1", Tone: "alert", Ago: "0s ago"}},
		{Event{Type: string(engine.TaskModeChanged), Task: "P1-04", Detail: "auto", At: now}, recentView{Text: "Mode of P1-04's next session: auto", Ago: "0s ago"}},
		{Event{Type: string(engine.RunStopped), Detail: "completed", At: now}, recentView{Glyph: "✓", Text: "Run ended: completed", Ago: "0s ago"}},
		{Event{Type: "something_new", Task: "P1-01", Detail: "x", At: now}, recentView{Text: "Something new P1-01 · x", Ago: "0s ago"}},
	}
	for _, tt := range tests {
		got := recentOf(tt.ev, now)
		tt.want.At = tt.ev.At
		if got != tt.want {
			t.Errorf("recentOf(%s) = %+v, want %+v", tt.ev.Type, got, tt.want)
		}
	}
}

// A finished task's line in the feed carries the tokens its card had, so
// Recent can say them after the card is gone.
func TestFeedKeepsTheTokensOfADoneTask(t *testing.T) {
	s := New(Options{Project: "demo"})
	t.Cleanup(func() { _ = s.Close() })
	for _, ev := range []engine.Event{
		{At: uiT0, Kind: engine.RunStarted, Run: "r", Phase: "P1"},
		{At: uiT0, Kind: engine.TaskStarted, Task: "P1-02", Rank: "opus", Model: "claude-opus-5-5"},
		{At: uiT0, Kind: engine.UsageRead, Task: "P1-02", Tokens: &engine.Tokens{In: 1200, Out: 30}},
		{At: uiT0, Kind: engine.TaskDone, Task: "P1-02"},
	} {
		s.Publish(ev)
	}
	feed := s.Snapshot().Feed
	last := feed[len(feed)-1]
	if last.Type != string(engine.TaskDone) || last.Tokens == nil || last.Tokens.In != 1200 || last.Tokens.Out != 30 {
		t.Errorf("task_done in the feed = %+v", last)
	}
}
