package state

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/config"
)

// Budgets (SPEC §6.9): a budget is named run, phase:<id> or rank:<rank>;
// the owner's answer to one reached is a new value as igris.toml writes
// it, or BudgetContinue.

// BudgetContinue is the answer that lets a reached budget go on: it no
// longer holds this run.
const BudgetContinue = "continue"

// maxBudgetName bounds the phase ID or rank in a budget's name.
const maxBudgetName = 64

// ValidBudgetKey reports whether k names a budget: run, phase:<id> or
// rank:<rank>, the name printable without spaces and at most 64 runes. It
// is shown on the terminal and in the web UI.
func ValidBudgetKey(k string) bool {
	if k == "run" {
		return true
	}
	name, ok := strings.CutPrefix(k, "phase:")
	if !ok {
		name, ok = strings.CutPrefix(k, "rank:")
	}
	if !ok || name == "" || utf8.RuneCountInString(name) > maxBudgetName || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) })
}

// ValidBudgetValue reports whether v is a budget's value (a token count or
// an amount, config.Budget) or BudgetContinue.
func ValidBudgetValue(v string) bool {
	if v == BudgetContinue {
		return true
	}
	_, err := config.Budget(v).Limit()
	return err == nil
}

// validUsed reports whether u has the shape of what a budget_* line says
// was used: a short token count (20.4M) or an amount ($41.20).
func validUsed(u string) bool {
	if len(u) > 24 {
		return false
	}
	u = strings.TrimPrefix(u, "$")
	return u != "" && !strings.ContainsFunc(u, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.' && r != ',' && r != 'k' && r != 'M' && r != 'G'
	})
}
