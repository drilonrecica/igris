package serve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// snap decodes what a new client would get.
func snap(t *testing.T, s *Server) Snapshot {
	t.Helper()
	p := attach(t, s)
	p.send(`{"v":1,"type":"hello"}`)
	_ = p.conn.SetReadDeadline(time.Now().Add(patience))
	line, err := p.r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var out Snapshot
	if err := json.Unmarshal(line, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The snapshot follows the run as the run view does: the current task's
// state, the questions, pause, modes, counters.
func TestSnapshotTracksTheRun(t *testing.T) {
	s := New(Options{Version: "1.4.0", Mode: "auto"})
	at := func(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }
	ref := &backend.SessionRef{Backend: "fake", PaneID: "p1"}
	for _, ev := range []engine.Event{
		{At: at(0), Kind: engine.RunStarted, Run: "20261022-090000-abcd", Phase: "M1", Slice: []string{"M1-01", "M1-02"}},
		{At: at(0), Kind: engine.WorktreeReady},
		{At: at(1), Kind: engine.TaskStarted, Phase: "M1", Task: "M1-01", Title: "Config", Rank: "sonnet", Model: "sonnet-model", Mode: "auto"},
		{At: at(2), Kind: engine.SessionOpened, Phase: "M1", Task: "M1-01", Session: ref, ClaudeSession: "uuid-1", Mode: "auto"},
		{At: at(3), Kind: engine.UsageRead, Task: "M1-01", Tokens: &engine.Tokens{In: 1200, Out: 30, Models: []string{"claude-sonnet-5-5"}}},
		{At: at(4), Kind: engine.QuotaRead, Quota: &state.Quota{At: at(4), FiveHour: &state.QuotaWindow{Used: 40}}},
		{At: at(5), Kind: engine.NeedsYou, Task: "M1-01", Detail: "idle"},
		{At: at(6), Kind: engine.PauseOn},
		{At: at(7), Kind: engine.ModeChanged, Detail: "accept"},
		{At: at(8), Kind: engine.TaskModeChanged, Task: "M1-02", Detail: "plan"},
		{At: at(9), Kind: engine.Asked, Task: "M1-01", Question: engine.QuestionCommit, Detail: "commit M1-01?"},
		{At: at(10), Kind: engine.Asked, Task: "M1-02", Question: engine.QuestionConfirmReset, Detail: "reset M1-02?"},
	} {
		s.Publish(ev)
	}
	got := snap(t, s)
	if got.Run != "20261022-090000-abcd" || got.Phase != "M1" || strings.Join(got.Slice, ",") != "M1-01,M1-02" ||
		got.Mode != "accept" || got.Overrides["M1-02"] != "plan" || !got.Paused || !got.Worktree || got.Ended {
		t.Errorf("run state = %+v", got)
	}
	c := got.Current
	if c == nil || c.Task != "M1-01" || c.Model != "sonnet-model" || c.User || c.State != StateQuestion || !c.Since.Equal(at(9)) ||
		c.Session == nil || c.Session.PaneID != "p1" || c.ClaudeSession != "uuid-1" || c.Tokens == nil || c.Tokens.In != 1200 || !c.Started.Equal(at(1)) {
		t.Errorf("current = %+v", c)
	}
	if got.Question == nil || got.Question.Question != "commit" || got.Reset == nil || got.Reset.Task != "M1-02" {
		t.Errorf("questions = %+v, %+v", got.Question, got.Reset)
	}
	if got.Quota == nil || got.Quota.FiveHour == nil || got.Quota.FiveHour.Used != 40 {
		t.Errorf("quota = %+v", got.Quota)
	}
	for _, ev := range got.Feed {
		if ev.Type == EventTypeTokens || ev.Type == "quota" {
			t.Errorf("the feed holds a counter: %+v", ev)
		}
		if ev.Run != "20261022-090000-abcd" {
			t.Errorf("feed event without the run: %+v", ev)
		}
	}
	if n := len(got.Feed); n != 10 {
		t.Errorf("feed has %d events, want 10", n)
	}

	// The commit is answered, the reset dropped, the task ends.
	for _, ev := range []engine.Event{
		{At: at(11), Kind: engine.Committed, Task: "M1-01"},
		{At: at(12), Kind: engine.ResetDropped, Task: "M1-02"},
		{At: at(13), Kind: engine.TaskDone, Task: "M1-01"},
		{At: at(14), Kind: engine.PauseOff},
		{At: at(15), Kind: engine.RunStopped, Detail: "completed"},
	} {
		s.Publish(ev)
	}
	got = snap(t, s)
	if got.Current != nil || got.Question != nil || got.Reset != nil || got.Paused || !got.Ended || got.End != "completed" {
		t.Errorf("after the run = %+v", got)
	}
}

func TestSnapshotStates(t *testing.T) {
	start := engine.Event{At: t0, Kind: engine.TaskStarted, Task: "T-1", Model: "m"}
	tests := []struct {
		name   string
		events []engine.Event
		state  string
	}{
		{"started", nil, StateWorking},
		{"verifying", []engine.Event{{Kind: engine.VerifyStarted}}, StateVerifying},
		{"verify failed", []engine.Event{{Kind: engine.VerifyStarted}, {Kind: engine.VerifyFailed}}, StateWorking},
		{"waiting", []engine.Event{{Kind: engine.AgentWaiting}}, StateWaiting},
		{"waiting over", []engine.Event{{Kind: engine.AgentWaiting}, {Kind: engine.AgentWaitingOver}}, StateWorking},
		{"waited too long", []engine.Event{{Kind: engine.AgentWaiting}, {Kind: engine.NeedsYou}}, StateNeedsYou},
		{"lost", []engine.Event{{Kind: engine.SessionLost}}, StateLost},
		{"retrying", []engine.Event{{Kind: engine.SessionLost}, {Kind: engine.Retrying}}, StateWorking},
		{"plan shown", []engine.Event{{Kind: engine.Asked, Question: engine.QuestionPlanApproval, Plan: "do it"}}, StateQuestion},
		{"plan closed", []engine.Event{{Kind: engine.Asked, Question: engine.QuestionPlanApproval}, {Kind: engine.PlanClosed}}, StateNeedsYou},
		{"needs you clear", []engine.Event{{Kind: engine.NeedsYou}, {Kind: engine.NeedsYouClear}}, StateWorking},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(Options{})
			s.Publish(start)
			for _, ev := range tt.events {
				ev.At = t0.Add(time.Second)
				s.Publish(ev)
			}
			got := snap(t, s)
			if got.Current == nil || got.Current.State != tt.state {
				t.Errorf("current = %+v, want state %s", got.Current, tt.state)
			}
		})
	}
	s := New(Options{})
	s.Publish(engine.Event{At: t0, Kind: engine.TaskStarted, Task: "U-1"})
	s.Publish(engine.Event{At: t0, Kind: engine.YourTurn, Task: "U-1"})
	if c := snap(t, s).Current; c == nil || !c.User || c.State != StateYourTurn {
		t.Errorf("user task = %+v", c)
	}
}

// The feed keeps the newest events, oldest first, without plan text.
func TestSnapshotFeedIsBounded(t *testing.T) {
	s := New(Options{})
	for i := range feedLen + 50 {
		s.Publish(engine.Event{At: t0, Kind: engine.Warning, Detail: fmt.Sprint(i)})
	}
	s.Publish(engine.Event{At: t0, Kind: engine.Asked, Question: engine.QuestionPlanApproval, Plan: "secret plan text"})
	got := snap(t, s)
	if len(got.Feed) != feedLen || got.Feed[0].Detail != "51" || got.Feed[feedLen-1].Type != "asked" {
		t.Fatalf("feed = %d events from %q to %q", len(got.Feed), got.Feed[0].Detail, got.Feed[len(got.Feed)-1].Type)
	}
	if got.Feed[feedLen-1].Plan != "" {
		t.Error("the feed holds plan text")
	}
}

func TestSnapshotPhases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.md")
	const text = "## M1 — First\n\n| ID | Task | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|---|\n" +
		"| M1-01 | **Config \x1b[31mloader** | — | done | sonnet | agent | auto |\n" +
		"| M1-02 | **Docs** | M1-01 | ready | — | user | |\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Plan: func() (*plan.Plan, error) { return plan.Load(path, plan.Options{}) }})
	got := snap(t, s)
	if len(got.Phases) != 1 || got.Phases[0].ID != "M1" || len(got.Phases[0].Tasks) != 2 {
		t.Fatalf("phases = %+v (%s)", got.Phases, got.PlanError)
	}
	t1, t2 := got.Phases[0].Tasks[0], got.Phases[0].Tasks[1]
	if t1.ID != "M1-01" || t1.Title != "Config loader" || t1.Status != "done" || t1.Rank != "sonnet" || t1.Owner != "agent" || t1.Mode != "auto" {
		t.Errorf("task 1 = %+v", t1)
	}
	if t2.Status != "ready" || t2.Owner != "user" || t2.Rank != "" {
		t.Errorf("task 2 = %+v", t2)
	}

	s = New(Options{Plan: func() (*plan.Plan, error) { return nil, fmt.Errorf("read tasks.md: \x1b[2Jgone") }})
	if got := snap(t, s); got.Phases != nil || got.PlanError != "read tasks.md: gone" {
		t.Errorf("unreadable plan: %+v", got)
	}
}

// The event line keeps the run log's envelope and field names (SPEC §13,
// §19): changing one breaks every 1.x client.
func TestEventEnvelope(t *testing.T) {
	ev := engine.Event{
		At: time.Date(2026, 10, 22, 9, 0, 1, 0, time.FixedZone("x", 3600)), Kind: engine.TaskDone,
		Phase: "M1", Task: "M1-01", Title: "Config", Rank: "sonnet", Model: "claude-sonnet-5-5", Mode: "auto", Verify: "fast", Detail: "tests added",
		Changes: []plan.Change{{ID: "M1-01", From: plan.InProgress, To: plan.Done}, {ID: "M1-02", From: plan.Blocked, To: plan.Ready}},
	}
	b, err := encodeEvent(fromEngine(ev, "20261022-090000-abcd"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"v":1,"at":"2026-10-22T08:00:01Z","type":"task_done","run":"20261022-090000-abcd","phase":"M1","task":"M1-01","title":"Config","rank":"sonnet","model":"claude-sonnet-5-5","mode":"auto","profile":"fast","detail":"tests added","changes":[{"id":"M1-01","from":"in progress","to":"done"},{"id":"M1-02","from":"blocked","to":"ready"}]}` + "\n"
	if string(b) != want {
		t.Errorf("event line\n got %s\nwant %s", b, want)
	}

	tok, err := encodeEvent(fromEngine(engine.Event{At: t0, Kind: engine.UsageRead, Tokens: &engine.Tokens{In: 5, Out: 1, Unavailable: true}}, ""))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"v":1,"at":"2026-10-22T09:00:00Z","type":"tokens","tokens":{"in":5,"out":1,"unavailable":true}}` + "\n"; string(tok) != want {
		t.Errorf("tokens line\n got %s\nwant %s", tok, want)
	}
}

// No event line is over MaxLine, whatever the engine hands over.
func TestEventLinesAreBounded(t *testing.T) {
	huge := strings.Repeat("p", 3*MaxLine)
	ev := engine.Event{At: t0, Kind: engine.Asked, Question: engine.QuestionPlanApproval, Plan: huge, Detail: huge,
		Output: strings.Split(strings.Repeat("o\n", 5000), "\n")}
	for range 3000 {
		ev.Slice = append(ev.Slice, "M1-"+strings.Repeat("9", 300))
	}
	b, err := encodeEvent(fromEngine(ev, "r"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > MaxLine+1 {
		t.Errorf("line of %d bytes", len(b))
	}
	var back Event
	if err := json.Unmarshal(b, &back); err != nil || back.Type != "asked" || back.Question != "plan_approval" {
		t.Errorf("decoded %+v, %v", back.Type, err)
	}
	if len(back.Output) > maxOutput || len(back.Detail) > maxDetail {
		t.Errorf("output %d lines, detail %d bytes", len(back.Output), len(back.Detail))
	}
}

func TestCut(t *testing.T) {
	for _, tt := range []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"abcdefghij", 6, "abc…"},
		{"ééééé", 6, "é…"}, // never inside a rune
	} {
		if got := cut(tt.s, tt.n); got != tt.want || len(got) > tt.n {
			t.Errorf("cut(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}
