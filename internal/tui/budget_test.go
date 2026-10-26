package tui

// Budgets in the run view (SPEC §6.9, §15.5): the budget lines on the
// first card, the run's question with Raise, Continue and Stop, and a
// restored snapshot.

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

var runReached = engine.BudgetUse{Budget: "run", Limit: "20M", Used: "20.4M", Percent: 102, State: engine.BudgetReached}

func budgetAsked() []engine.Event {
	b := runReached
	return []engine.Event{
		{Kind: engine.BudgetRead, Budgets: []engine.BudgetUse{runReached, {Budget: "rank:opus", Limit: "$40", Used: "$33.10", Percent: 82.75}}},
		{Kind: engine.BudgetExceeded, Budget: &b, Detail: "run budget 20M reached · 20.4M used · M0-03 keeps running"},
		{Kind: engine.Asked, Question: engine.QuestionBudget, Budget: &b, Detail: "run budget 20M reached · 20.4M used · M0-03 keeps running; no new task starts until you raise it, let it go on, or stop the run"},
	}
}

func TestBudgetLinesAndQuestion(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.events(budgetAsked()...)
	if !hs.m.needsOwner() || hs.m.cur.state != stateWorking {
		t.Errorf("needs owner %v, card state %v: the budget is the run's", hs.m.needsOwner(), hs.m.cur.state)
	}
	d := hs.m.dialog
	if d == nil || d.question != engine.QuestionBudget || d.title != "run budget 20M reached" {
		t.Fatalf("dialog = %+v", d)
	}
	view := hs.m.View()
	for _, want := range []string{"Raise…", "Continue (it no longer holds this run)", "Stop igris", "M0-03 keeps running"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	// Esc keeps asking; the card shows the budgets.
	hs.key("esc")
	view = hs.m.View()
	for _, want := range []string{"budget 20.4M / 20M · 102%", "rank opus budget $33.10 / $40 · 82%", "! run budget 20M reached · 20.4M used"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if q := hs.m.question(); q == nil || q.Question != engine.QuestionBudget {
		t.Fatalf("question = %+v", q)
	}
	// Raise: the value is asked for, an empty one refused.
	hs.m.dialog = questionDialog(*hs.m.question())
	hs.key("1")
	if d := hs.m.dialog; d == nil || d.input == nil || !d.inField {
		t.Fatalf("Raise opened %+v", hs.m.dialog)
	}
	hs.key("enter")
	if len(hs.s.take()) != 0 || !strings.Contains(hs.m.View(), "Type the new value") {
		t.Fatalf("an empty value went out:\n%s", hs.m.View())
	}
	hs.keys("3", "0", "M", "enter")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdBudget, Text: "30M"}) {
		t.Fatalf("sent %+v", got)
	}
	raised := runReached
	raised.Limit, raised.State = "30M", ""
	hs.events(engine.Event{Kind: engine.BudgetRaised, Budget: &raised, Detail: "run budget raised to 30M for this run"})
	if hs.m.budgetAsk != nil || hs.m.needsOwner() || hs.m.dialog != nil {
		t.Errorf("after the raise: ask %+v, dialog %+v", hs.m.budgetAsk, hs.m.dialog)
	}
	// Continue.
	hs.events(budgetAsked()[2])
	hs.key("2")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdBudget, Text: state.BudgetContinue}) {
		t.Fatalf("sent %+v", got)
	}
}

// Without a task, the card says what holds; a restored view has the
// budgets and the question.
func TestBudgetRestored(t *testing.T) {
	hs := newHarness(t, 120, 40)
	b := runReached
	hs.m.restore(serve.Snapshot{Run: "r", Phase: "M0", Budgets: []engine.BudgetUse{runReached},
		BudgetQuestion: &serve.Event{Type: string(engine.Asked), Question: string(engine.QuestionBudget), Budget: &b, Detail: "run budget 20M reached · 20.4M used · no session is running"}})
	if hs.m.budgetAsk == nil || hs.m.dialog == nil || hs.m.dialog.question != engine.QuestionBudget {
		t.Fatalf("ask %+v, dialog %+v", hs.m.budgetAsk, hs.m.dialog)
	}
	hs.key("esc")
	view := hs.m.View()
	for _, want := range []string{"held: run budget 20M reached", "budget 20.4M / 20M · 102%"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}
