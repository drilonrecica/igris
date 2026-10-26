package serve

// Fewer interruptions on the socket and the web UI (SPEC §6.5, §10,
// §13, §15.8, V18-P3): the batch question in the snapshot and on the
// sheet, Undo's second confirmation, Commit all, a blocked session's
// cause in its words, and /?task= opening that task's question.

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

func batchEvents(at time.Time, undo bool) []engine.Event {
	return []engine.Event{
		{At: at, Kind: engine.RunStarted, Run: "r1", Phase: "P1", Detail: "phase P1"},
		{At: at.Add(time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Rank: "sonnet", Model: "claude-sonnet-5-5", Mode: "accept"},
		{At: at.Add(time.Hour), Kind: engine.Asked, Phase: "P1", Question: engine.QuestionCommitBatch, Undo: undo,
			Detail: "Keep the 2 commits of this run?", Output: []string{"P1-01: Reader", "P1-02: Parser"}},
	}
}

// The batch question is the run's: in the snapshot on its own, with the
// subjects, and gone once the run ends.
func TestSnapshotBatchQuestion(t *testing.T) {
	s := New(Options{Version: "1.8.0", Mode: "auto"})
	for _, ev := range batchEvents(uiT0, true) {
		s.Publish(ev)
	}
	got := snap(t, s)
	q := got.BatchQuestion
	if q == nil || q.Question != string(engine.QuestionCommitBatch) || q.Task != "" || !q.Undo || len(q.Output) != 2 {
		t.Fatalf("batch question = %+v", q)
	}
	if got.Question != nil || got.Current == nil || got.Current.State != StateWorking {
		t.Errorf("the card took the run's question: %+v, %+v", got.Question, got.Current)
	}
	s.Publish(engine.Event{At: uiT0.Add(2 * time.Hour), Kind: engine.RunStopped, Detail: "completed"})
	if q := snap(t, s).BatchQuestion; q != nil {
		t.Errorf("after the end: %+v", q)
	}
}

// The batch answer, Commit all and a blocked session's cause cross the
// socket; a cause or tool of a bad shape is dropped.
func TestWireInterruptions(t *testing.T) {
	for _, r := range []Request{
		{V: Protocol, Type: CmdAnswer, ID: "1", Question: "commit_batch", Yes: true},
		{V: Protocol, Type: CmdAnswer, ID: "1", Task: "P1-03", Yes: true, Text: "all"},
	} {
		c, err := r.command()
		if err != nil || c.Kind != engine.CmdAnswer || string(c.Question) != r.Question || c.Text != r.Text {
			t.Errorf("command(%+v) = %+v, %v", r, c, err)
		}
	}
	ny := engine.Event{At: uiT0, Kind: engine.NeedsYou, Task: "P1-03", Reason: state.ReasonBlocked, Cause: state.CausePermission, Tool: "Bash"}
	w := fromEngine(ny, "r")
	if back, ok := w.Engine(); !ok || back.Cause != state.CausePermission || back.Tool != "Bash" {
		t.Errorf("back = %+v", back)
	}
	w.Tool = "rm -rf /"
	if back, _ := w.Engine(); back.Cause != state.CausePermission || back.Tool != "" {
		t.Errorf("a bad tool kept: %+v", back)
	}
	w.Cause = "\x1b[31m"
	if back, _ := w.Engine(); back.Cause != "" || back.Tool != "" {
		t.Errorf("a bad cause kept: %+v", back)
	}
	b := fromEngine(batchEvents(uiT0, true)[2], "r")
	if back, ok := b.Engine(); !ok || !back.Undo || back.Question != engine.QuestionCommitBatch {
		t.Errorf("batch question back = %+v", back)
	}
	for _, k := range []engine.EventKind{engine.CommitPolicy, engine.CommitsUndone} {
		if _, ok := fromEngine(engine.Event{Kind: k, Detail: "x"}, "r").Engine(); !ok {
			t.Errorf("%s is unknown to the client", k)
		}
	}
}

// The page asks the batch question with Keep and Undo; Undo takes a
// second confirmation, like Stop.
func TestUIBatchQuestion(t *testing.T) {
	w := uiWeb(t, "plan", true)
	for _, ev := range batchEvents(uiT0, true) {
		w.srv.Publish(ev)
	}
	code, body, _ := w.get("/", "cookie")
	if code != 200 {
		t.Fatalf("GET / = %d", code)
	}
	for _, want := range []string{
		"<h2>Keep the 2 commits of this run?</h2>", "P1-01: Reader", ">Keep<",
		`name="question" value="commit_batch"`, ">Undo (git reset --soft; the changes stay)<", "Undo the run&#39;s commits?",
		"The run ended: keep its commits?",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
	undo := url.Values{"type": {"answer"}, "question": {"commit_batch"}}
	code, body, _ = w.postForm("/act", undo)
	if code != 200 || !strings.Contains(body, "Undo the commits") || len(w.send.got()) != 0 {
		t.Fatalf("Undo without the confirmation = %d, sent %+v", code, w.send.got())
	}
	undo.Set(confirmedField, "1")
	if code, _, _ = w.postForm("/act", undo); code != 303 {
		t.Errorf("confirmed Undo = %d", code)
	}
	want := engine.Command{Kind: engine.CmdAnswer, Question: engine.QuestionCommitBatch}
	if got := w.send.got(); len(got) != 1 || got[0] != want {
		t.Errorf("engine got %+v, want %+v", got, want)
	}
}

// When base..HEAD isn't the run's commits alone, the page offers Keep
// only.
func TestUIBatchQuestionKeepOnly(t *testing.T) {
	w := uiWeb(t, "plan", true)
	for _, ev := range batchEvents(uiT0, false) {
		w.srv.Publish(ev)
	}
	_, body, _ := w.get("/", "cookie")
	if !strings.Contains(body, ">Keep<") || strings.Contains(body, ">Undo") {
		t.Errorf("page:\n%s", body)
	}
}

// The commit question's third answer, Commit all, sends text all.
func TestUICommitAll(t *testing.T) {
	w := uiWeb(t, "plan", true)
	w.srv.Publish(engine.Event{At: uiT0, Kind: engine.RunStarted, Run: "r1", Phase: "P1"})
	w.srv.Publish(engine.Event{At: uiT0, Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Model: "m"})
	w.srv.Publish(engine.Event{At: uiT0, Kind: engine.Asked, Phase: "P1", Task: "P1-03", Question: engine.QuestionCommit, Detail: "commit?"})
	_, body, _ := w.get("/", "cookie")
	if !strings.Contains(body, ">Commit all (no more commit questions this run)<") || !strings.Contains(body, `name="text" value="all"`) {
		t.Fatalf("page:\n%s", body)
	}
	w.postForm("/act", url.Values{"type": {"answer"}, "task": {"P1-03"}, "yes": {"1"}, "text": {"all"}})
	want := engine.Command{Kind: engine.CmdAnswer, Task: "P1-03", Yes: true, Text: "all"}
	if got := w.send.got(); len(got) != 1 || got[0] != want {
		t.Errorf("engine got %+v, want %+v", got, want)
	}
}

// A blocked card's sheet and its Recent line say why, in §10's words.
func TestUIBlockedSaysWhy(t *testing.T) {
	w := uiWeb(t, "plan", true)
	w.srv.Publish(engine.Event{At: uiT0, Kind: engine.RunStarted, Run: "r1", Phase: "P1"})
	w.srv.Publish(engine.Event{At: uiT0, Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Model: "m"})
	w.srv.Publish(engine.Event{At: uiT0, Kind: engine.NeedsYou, Phase: "P1", Task: "P1-03", Reason: state.ReasonBlocked,
		Cause: state.CausePermission, Tool: "Bash", Detail: "the agent is blocked and has not run `igris done` for 30s"})
	_, body, _ := w.get("/", "cookie")
	for _, want := range []string{
		`<span class="what">waits for a permission (Bash)</span>`,
		"P1-03 needs you · waits for a permission (Bash)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
}

// /?task=<ID>, ntfy's Open, puts that task's question on the sheet; an ID
// without one, or none, leaves the first.
func TestUIOpenTask(t *testing.T) {
	w := uiWeb(t, "plan", true)
	w.srv.Publish(engine.Event{At: uiT0, Kind: engine.RunStarted, Run: "r1", Phase: "P1"})
	for _, id := range []string{"P1-02", "P1-03"} {
		w.srv.Publish(engine.Event{At: uiT0, Kind: engine.TaskStarted, Phase: "P1", Task: id, Title: "T", Model: "m", Slot: new(int)})
		w.srv.Publish(engine.Event{At: uiT0, Kind: engine.Asked, Phase: "P1", Task: id, Question: engine.QuestionCommit, Detail: "commit?"})
	}
	sheet := func(path string) string {
		_, body, _ := w.get(path, "cookie")
		i := strings.Index(body, `<section class="question sheet"`)
		if i < 0 {
			t.Fatalf("%s: no sheet:\n%s", path, body)
		}
		_, task, _ := strings.Cut(body[i:], `data-task="`)
		return task[:5]
	}
	for path, want := range map[string]string{"/?task=P1-03": "P1-03", "/?task=P9-99": "P1-02", "/": "P1-02", "/?task=%3Cx%3E": "P1-02"} {
		if got := sheet(path); got != want {
			t.Errorf("%s: the sheet is %s's, want %s's", path, got, want)
		}
	}
}
