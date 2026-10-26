package state

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestValidBudget(t *testing.T) {
	for k, want := range map[string]bool{
		"run": true, "phase:V18": true, "rank:fable": true, "phase:": false, "rank:a b": false,
		"phase:\x1b[31m": false, "budget": false, "rank:" + strings.Repeat("x", 65): false,
	} {
		if got := ValidBudgetKey(k); got != want {
			t.Errorf("ValidBudgetKey(%q) = %v", k, got)
		}
	}
	for v, want := range map[string]bool{"continue": true, "20M": true, "$40": true, "1500000": true, "lots": false, "$0": false, "": false} {
		if got := ValidBudgetValue(v); got != want {
			t.Errorf("ValidBudgetValue(%q) = %v", v, got)
		}
	}
}

// budget_overrides round-trip through state.json; a bad one is damage.
func TestRunBudgetOverrides(t *testing.T) {
	d := testDir(t)
	r := &Run{Phases: []string{"A"}, ConfigHash: "h", BudgetOverrides: map[string]string{"run": "30M", "rank:opus": BudgetContinue}}
	if err := d.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	got, err := d.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.BudgetOverrides) != 2 || got.BudgetOverrides["run"] != "30M" || got.BudgetOverrides["rank:opus"] != BudgetContinue {
		t.Errorf("overrides = %v", got.BudgetOverrides)
	}
	data, err := os.ReadFile(d.runPath())
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(data), `"30M"`, `"$(rm -rf)"`, 1)
	if err := os.WriteFile(d.runPath(), []byte(bad), 0o600); err != nil { //nolint:gosec // the test's own temp dir
		t.Fatal(err)
	}
	if _, err := d.LoadRun(); !errors.Is(err, ErrRunDamaged) {
		t.Errorf("a bad override: %v", err)
	}
}

// A budget line's values of a bad shape are dropped when read back.
func TestBudgetLineChecked(t *testing.T) {
	e := Event{Type: EventBudgetExceeded, Budget: "run\x1b[2J", Limit: "lots", Used: "20.4M"}.checked()
	if e.Budget != "" || e.Limit != "" || e.Used != "20.4M" {
		t.Errorf("checked = %+v", e)
	}
	e = Event{Type: EventBudgetRaised, Budget: "phase:V18", Limit: "continue", Used: "$41.20"}.checked()
	if e.Budget != "phase:V18" || e.Limit != "continue" || e.Used != "$41.20" {
		t.Errorf("checked = %+v", e)
	}
}
