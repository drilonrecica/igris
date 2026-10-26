package serve

// Budgets on the socket and the web UI (SPEC §6.9, §13, §15.5, §15.8): the
// budgets and the budget question in the snapshot, the `budget` command,
// the question's sheet (Raise, Continue, Stop) and the budget lines.

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
)

var reachedRun = engine.BudgetUse{Budget: "run", Limit: "20M", Used: "20.4M", Percent: 102, State: engine.BudgetReached}

func budgetEvents(at time.Time) []engine.Event {
	b := reachedRun
	return []engine.Event{
		{At: at, Kind: engine.RunStarted, Run: "r1", Phase: "P1", Detail: "phase P1"},
		{At: at.Add(time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Rank: "sonnet", Model: "claude-sonnet-5-5", Mode: "accept"},
		{At: at.Add(time.Minute), Kind: engine.BudgetRead, Phase: "P1", Budgets: []engine.BudgetUse{
			reachedRun, {Budget: "rank:opus", Limit: "$40", Used: "$33.10", Percent: 82.75}}},
		{At: at.Add(time.Minute), Kind: engine.BudgetExceeded, Phase: "P1", Budget: &b, Detail: "run budget 20M reached · 20.4M used · P1-03 keeps running"},
		{At: at.Add(time.Minute), Kind: engine.Asked, Phase: "P1", Question: engine.QuestionBudget, Budget: &b,
			Detail: "run budget 20M reached · 20.4M used · P1-03 keeps running; no new task starts until you raise it, let it go on, or stop the run"},
	}
}

// The snapshot carries the budgets and the budget question, which is the
// run's: the single card doesn't take it.
func TestSnapshotBudgets(t *testing.T) {
	s := New(Options{Version: "1.8.0", Mode: "auto"})
	for _, ev := range budgetEvents(uiT0) {
		s.Publish(ev)
	}
	got := snap(t, s)
	if len(got.Budgets) != 2 || got.Budgets[0] != reachedRun || got.Budgets[1].Line() != "rank opus budget $33.10 / $40 · 82%" {
		t.Errorf("budgets = %+v", got.Budgets)
	}
	q := got.BudgetQuestion
	if q == nil || q.Question != string(engine.QuestionBudget) || q.Task != "" || q.Budget == nil || q.Budget.Title() != "run budget 20M reached" {
		t.Fatalf("budget question = %+v", q)
	}
	if c := got.Current; c == nil || c.State != StateWorking || got.Question != nil {
		t.Errorf("current = %+v, question = %+v", c, got.Question)
	}
	if got := statusOf(got, uiT0.Add(2*time.Minute)); got.Word != look.NeedsYou.String() || got.Line != "1 question" {
		t.Errorf("status = %+v", got)
	}
	raised := reachedRun
	raised.Limit, raised.State = "30M", ""
	s.Publish(engine.Event{At: uiT0.Add(2 * time.Minute), Kind: engine.BudgetRaised, Budget: &raised, Detail: "run budget raised to 30M for this run"})
	if q := snap(t, s).BudgetQuestion; q != nil {
		t.Errorf("after the raise: %+v", q)
	}
}

// Budgets cross the socket both ways; one of a bad shape is dropped.
func TestWireBudgets(t *testing.T) {
	ev := budgetEvents(uiT0)[2]
	w := fromEngine(ev, "r")
	back, ok := w.Engine()
	if !ok || len(back.Budgets) != 2 || back.Budgets[0] != reachedRun {
		t.Fatalf("back = %+v", back.Budgets)
	}
	w.Budgets[0].Budget, w.Budgets[1].Limit = "\x1b[31mrun", "lots"
	if back, _ := w.Engine(); len(back.Budgets) != 0 {
		t.Errorf("bad budgets kept: %+v", back.Budgets)
	}
	r := Request{V: Protocol, Type: CmdBudget, ID: "1", Text: "30M"}
	if c, err := r.command(); err != nil || c.Kind != engine.CmdBudget || c.Text != "30M" {
		t.Errorf("command = %+v, %v", c, err)
	}
	r.Text = " "
	if _, err := r.command(); err == nil {
		t.Error("a budget command without text was taken")
	}
}

// The page shows the budgets, Ember from 80% and bold from 95%, and the
// budget question with Raise (a field), Continue and Stop.
func TestUIBudgetPage(t *testing.T) {
	w := uiWeb(t, "plan", true)
	for _, ev := range budgetEvents(uiT0) {
		w.srv.Publish(ev)
	}
	code, body, _ := w.get("/", "cookie")
	if code != 200 {
		t.Fatalf("GET / = %d", code)
	}
	for _, want := range []string{
		`<p class="meta budget ember strong">budget 20.4M / 20M · 102%</p>`,
		`<p class="meta budget ember">rank opus budget $33.10 / $40 · 82%</p>`,
		"<h2>run budget 20M reached</h2>",
		`name="type" value="budget"`, ">Raise<", `name="text" value="continue"`, ">Continue (it no longer holds this run)<", ">Stop igris<",
		"Budget reached: raise it, continue or stop",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
	code, _, _ = w.postForm("/act", url.Values{"type": {"budget"}, "text": {"30M"}})
	if code != 303 {
		t.Errorf("POST /act = %d", code)
	}
	if cmds := w.send.got(); len(cmds) != 1 || cmds[0].Kind != engine.CmdBudget || cmds[0].Text != "30M" {
		t.Errorf("engine got %+v, want the budget answer", cmds)
	}
}
