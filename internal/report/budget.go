package report

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/price"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

// Budget is a budget of a run and what the run used of it (SPEC §6.9,
// since 1.8): the budgets igris.toml sets now, and any the run's log says
// it reached or raised.
type Budget struct {
	Run string `json:"run,omitempty"`
	// Budget is run, phase:<id> or rank:<rank>.
	Budget string `json:"budget"`
	// Limit is the value as igris.toml writes it, or as the run last
	// raised it.
	Limit   string  `json:"limit"`
	Used    string  `json:"used"`
	Percent float64 `json:"percent"`
	// Reached says the run reached it; Raised is the owner's last answer
	// then: a new value, or "continue".
	Reached bool   `json:"reached,omitempty"`
	Raised  string `json:"raised,omitempty"`
}

// Line is the budget as the CLI prints it: `run 20M · 14.2M used · 71%`,
// then `reached, raised to 30M` or `reached, continued`.
func (b Budget) Line() string {
	s := fmt.Sprintf("%s %s · %s used · %d%%", strings.Replace(b.Budget, ":", " ", 1), b.Limit, b.Used, int(b.Percent))
	switch {
	case b.Raised == state.BudgetContinue:
		s += " · reached, continued"
	case b.Raised != "":
		s += " · reached, raised to " + b.Raised
	case b.Reached:
		s += " · reached"
	}
	return s
}

// budgetSpend is what a budget counts: tokens (input + output) and their
// estimate without the tokens of models with no price.
type budgetSpend struct {
	tokens int64
	usd    float64
}

// budgets are the budgets of run r: those of bs, then those only its log
// names, with what r's attempts used (a phase's: its tasks; a rank's: its
// tasks) and whether it reached, raised or continued them.
func (r *runRec) budgets(bs config.Budgets, prices price.Table) []Budget {
	type entry struct {
		key, limit string
		reached    bool
		raised     string
	}
	var order []string
	entries := map[string]*entry{}
	add := func(key, limit string) *entry {
		e := entries[key]
		if e == nil {
			e = &entry{key: key, limit: limit}
			entries[key] = e
			order = append(order, key)
		}
		return e
	}
	if bs.Run != "" {
		add("run", string(bs.Run))
	}
	for _, id := range slices.Sorted(maps.Keys(bs.Phases)) {
		add("phase:"+id, string(bs.Phases[id]))
	}
	for _, rank := range slices.Sorted(maps.Keys(bs.Ranks)) {
		add("rank:"+rank, string(bs.Ranks[rank]))
	}
	phases := map[string]string{} // task → phase
	for _, e := range r.events {
		switch e.Type {
		case state.EventTaskStarted, state.EventTaskResumed:
			if e.Phase != "" {
				phases[e.Task] = e.Phase
			}
		case state.EventBudgetExceeded:
			if e.Budget != "" && e.Limit != "" {
				add(e.Budget, e.Limit).reached = true
			}
		case state.EventBudgetRaised:
			if e.Budget != "" && e.Limit != "" {
				x := add(e.Budget, e.Limit)
				x.reached, x.raised = true, e.Limit
				if e.Limit != state.BudgetContinue {
					x.limit = e.Limit
				}
			}
		}
	}
	var out []Budget
	for _, key := range order {
		x := entries[key]
		lim, err := config.Budget(x.limit).Limit()
		if err != nil {
			continue
		}
		kind, name, _ := strings.Cut(key, ":")
		var s budgetSpend
		for _, e := range r.events {
			if e.Type != state.EventUsage || e.Usage == nil {
				continue
			}
			switch kind {
			case "phase":
				if !strings.EqualFold(phases[e.Task], name) {
					continue
				}
			case "rank":
				if e.Rank != name {
					continue
				}
			}
			main, sub := *e.Usage, usage.Counts{}
			if e.Sub != nil {
				sub = *e.Sub
			}
			var t Tokens
			t.add(main)
			t.add(sub)
			s.tokens = satAdd(s.tokens, t.Total())
			s.usd += attemptCost(prices, main, sub, e.Models, e.SubModels).USD
		}
		bu := Budget{Run: r.id, Budget: key, Limit: x.limit, Reached: x.reached, Raised: x.raised}
		if lim.Cents > 0 {
			bu.Used = fmt.Sprintf("$%.2f", s.usd)
			bu.Percent = s.usd * 100 / float64(lim.Cents) * 100
		} else {
			bu.Used = look.Tokens(s.tokens)
			bu.Percent = float64(s.tokens) / float64(lim.Tokens) * 100
		}
		bu.Percent = math.Round(bu.Percent*100) / 100
		out = append(out, bu)
	}
	return out
}
