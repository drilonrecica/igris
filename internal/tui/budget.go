package tui

import (
	"slices"

	"github.com/drilonrecica/igris/internal/engine"
)

// Budgets (SPEC §6.9, §15.5): each budget of the run is a line on the
// first card, `budget 14.2M / 20M · 71%`, Ember from 80% and bold from
// 95%; a budget reached is the run's question, answered with Raise (a new
// value for this run), Continue or Stop.

// budgetEvent folds the run's budget events into the view; it reports
// whether ev was one.
func (m *model) budgetEvent(ev engine.Event) bool {
	switch {
	case ev.Kind == engine.BudgetRead:
		m.budgets = slices.Clone(ev.Budgets)
	case ev.Kind == engine.Asked && ev.Question == engine.QuestionBudget:
		e := ev
		m.budgetAsk = &e
		// It opens at once, but never over another question: that one is
		// answered first, and this one comes next.
		if d := m.dialog; d == nil || (d.question == "" && d.answers == "") {
			m.dialog = budgetDialog(ev)
		}
	case ev.Kind == engine.BudgetRaised:
		m.budgetAsk = nil
		if d := m.dialog; d != nil && (d.question == engine.QuestionBudget || d.answers == engine.QuestionBudget) {
			m.dialog = nil
			if q := m.question(); q != nil {
				m.dialog = questionDialog(*q)
			}
		}
	case ev.Kind == engine.BudgetExceeded:
	default:
		return false
	}
	return true
}

// budgetDialog asks what to do about the budget reached (SPEC §15.5):
// Raise, the default, asks for the new value; Continue lets it go on;
// Stop stops the run. It must be answered: esc keeps asking.
func budgetDialog(ev engine.Event) *dialog {
	title := "Budget reached"
	if b := ev.Budget; b != nil {
		title = b.Title()
	}
	return &dialog{
		title:  title,
		detail: ev.Detail,
		options: []option{
			{"Raise…", actBudgetRaise},
			{"Continue (it no longer holds this run)", actBudgetContinue},
			{"Stop igris", actStop},
		},
		cancel:   actClose,
		question: engine.QuestionBudget,
	}
}

// budgetRaiseDialog asks for the budget's new value for this run; igris.toml
// is untouched. Back and esc return to the question.
func budgetRaiseDialog(ev engine.Event) *dialog {
	title, detail := "Raise the budget", "A new value for this run; igris.toml is untouched."
	if b := ev.Budget; b != nil {
		title = "Raise the " + b.Name() + "?"
		detail = b.Used + " used of " + b.Limit + ". " + detail + ` A token count (30M, 500k) or an amount ($60).`
	}
	return &dialog{
		title:   title,
		detail:  detail,
		input:   &field{label: "New value: "},
		inField: true,
		submit:  actBudgetRaiseConfirm,
		options: []option{{"Raise", actBudgetRaiseConfirm}, {"Back", actAnswer}},
		cancel:  actAnswer,
		answers: engine.QuestionBudget,
	}
}

// answerBudget sends the answer to the budget reached: a new value or
// continue. The engine says what it did; a value it refuses comes back as
// a warning and the question stays.
func (m *model) answerBudget(text string) {
	m.dialog = nil
	m.opts.Sender.Send(engine.Command{Kind: engine.CmdBudget, Text: text})
}

// budgetLines are the run's budgets as the first card shows them, painted:
// Ember from 80%, bold from 95%, plain once continued.
func (m *model) budgetLines(w int) []string {
	out := make([]string, 0, len(m.budgets))
	for _, b := range m.budgets {
		t := lookPlain
		switch {
		case b.State == engine.BudgetContinued:
		case b.Percent >= 95:
			t = lookFail
		case b.Percent >= 80:
			t = lookWarn
		}
		out = append(out, fit(m.th.paint(t, b.Line()), w))
	}
	return out
}
