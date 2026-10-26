package report

import (
	"reflect"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

// The budgets igris.toml sets, and one only the log names, with what the
// run used of each (a phase's: its tasks; a rank's: its tasks) and how
// the run answered them (SPEC §6.9).
func TestBudgets(t *testing.T) {
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	events := []state.Event{
		runStart("r1", day),
		{V: 1, At: day, Type: state.EventTaskStarted, Run: "r1", Task: "A-1", Phase: "A", Rank: "sonnet"},
		{V: 1, At: day, Type: state.EventTaskStarted, Run: "r1", Task: "B-1", Phase: "B", Rank: "opus"},
		usageLine("r1", "A-1", "sonnet", &usage.Counts{Input: 1000, Output: 1_000_000, Messages: 1}, nil, []string{"claude-sonnet-5-5"}, nil, state.AuditOK),
		usageLine("r1", "B-1", "opus", &usage.Counts{Input: 500_000, Output: 500_000, Messages: 1}, nil, []string{"claude-opus-5-5"}, nil, state.AuditOK),
		{V: 1, At: day, Type: state.EventBudgetExceeded, Run: "r1", Budget: "run", Limit: "1.5M", Used: "1.5M"},
		{V: 1, At: day, Type: state.EventBudgetRaised, Run: "r1", Budget: "run", Limit: "3M", Used: "1.5M"},
		{V: 1, At: day, Type: state.EventBudgetExceeded, Run: "r1", Budget: "rank:haiku", Limit: "10k", Used: "12k"},
		{V: 1, At: day, Type: state.EventBudgetRaised, Run: "r1", Budget: "rank:haiku", Limit: "continue", Used: "12k"},
	}
	in := HistoryInput{Events: events, Budgets: config.Budgets{
		Run:    "1.5M",
		Phases: map[string]config.Budget{"a": "$20"},
		Ranks:  map[string]config.Budget{"opus": "2M"},
	}}
	u, err := NewUsage(in, "demo", "", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, b := range u.Budgets {
		lines = append(lines, b.Line())
	}
	want := []string{
		"run 3M · 2M used · 66% · reached, raised to 3M",
		"phase a $20 · $10.00 used · 50%",
		"rank opus 2M · 1M used · 50%",
		"rank haiku 10k · 0 used · 0% · reached, continued",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("budgets =\n%q\nwant\n%q", lines, want)
	}
	if u.Budgets[0].Run != "r1" {
		t.Errorf("run = %q", u.Budgets[0].Run)
	}
	rep, err := NewReport(in, "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Budgets) != 4 || rep.Budgets[0].Run != "" || rep.Budgets[1].Line() != want[1] {
		t.Errorf("report budgets = %+v", rep.Budgets)
	}
	// Without budgets, none.
	in.Budgets = config.Budgets{}
	in.Events = events[:5]
	if u, _ := NewUsage(in, "demo", "", time.Time{}); u.Budgets != nil {
		t.Errorf("budgets without any = %+v", u.Budgets)
	}
}
