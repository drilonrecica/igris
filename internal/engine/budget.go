package engine

import (
	"context"
	"fmt"
	"maps"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

// Budgets (SPEC §6.9; decisions.md V18-P1). [usage.budget] sets a budget
// for the run, per phase and per rank, in tokens (input + output,
// subagents included, as `igris usage` totals them) or as an estimate in
// dollars by the price table. Igris checks them against this run's
// attempts at every poll, so at every usage read. A budget reached needs
// the owner and holds new starts; every running session keeps running:
// nothing is stopped, killed or switched to another model. The owner
// raises it for this run, lets it go on, or stops the run.

// The states of a BudgetUse besides holding ("").
const (
	// BudgetReached: the budget is reached and waits on the owner.
	BudgetReached = "reached"
	// BudgetContinued: the owner let it go on; it no longer holds this
	// run.
	BudgetContinued = "continued"
)

// BudgetUse is a budget of the run with what it used so far, as a UI shows
// it (SPEC §6.9, §15): `budget 14.2M / 20M · 71%`.
type BudgetUse struct {
	// Budget is run, phase:<id> or rank:<rank>.
	Budget string `json:"budget"`
	// Limit is its value as written in igris.toml, or as the owner raised
	// it for this run.
	Limit string `json:"limit"`
	// Used is what it used: 14.2M, or $12.40 for an amount.
	Used string `json:"used"`
	// Percent is Used of Limit; it may pass 100.
	Percent float64 `json:"percent"`
	// State is "" while the budget holds, BudgetReached or
	// BudgetContinued.
	State string `json:"state,omitempty"`
}

// Name is the budget in words: run budget, phase V18 budget, rank fable
// budget.
func (b BudgetUse) Name() string { return BudgetName(b.Budget) }

// BudgetName is budget key in words: run budget, phase V18 budget, rank
// fable budget.
func BudgetName(key string) string {
	kind, name, ok := strings.Cut(key, ":")
	if !ok {
		return key + " budget"
	}
	return kind + " " + name + " budget"
}

// Line is the budget as a card shows it: `budget 14.2M / 20M · 71%`, with
// the budget's name for any but the run's.
func (b BudgetUse) Line() string {
	what := "budget"
	if b.Budget != "run" {
		what = b.Name()
	}
	s := fmt.Sprintf("%s %s / %s · %d%%", what, b.Used, b.Limit, int(math.Floor(b.Percent)))
	if b.State == BudgetContinued {
		s += " · continued"
	}
	return s
}

// Title is how a reached budget is asked about: `run budget 20M reached`.
func (b BudgetUse) Title() string { return b.Name() + " " + b.Limit + " reached" }

// spend is what tokens cost a budget: their count (input + output) and
// their estimate in dollars, without the tokens of models with no price.
type spend struct {
	tokens int64
	usd    float64
}

func (s *spend) add(o spend) {
	s.tokens, s.usd = satAdd(s.tokens, o.tokens), s.usd+o.usd
}

// spent is what the ended attempts of one task used in this run, with the
// phase and rank its budgets go by.
type spent struct {
	phase, rank string
	spend
}

// budgetWatch is the run's budgets: what the ended attempts used, which
// budgets were reached at which value, and the ones waiting on the owner.
type budgetWatch struct {
	ended map[string]*spent // by task
	// reached keys each budget reached by the value it was reached at:
	// once per budget and value.
	reached map[string]string
	// asks are the reached budgets waiting on the owner, in the order
	// they were reached; asked is the one put to the owner, the first.
	asks  []string
	asked string
	shown []BudgetUse // last sent to the UI
}

// budgetDef is one budget of the config with the value it has for this
// run.
type budgetDef struct {
	key       string
	value     string // as written, or as raised
	limit     config.BudgetLimit
	continued bool
	phase     string // a phase budget's phase ID, matched case-insensitively
	rank      string // a rank budget's rank
}

// counts says whether a task of phase and rank counts for d.
func (d budgetDef) counts(phase, rank string) bool {
	switch {
	case d.phase != "":
		return strings.EqualFold(d.phase, phase)
	case d.rank != "":
		return d.rank == rank
	}
	return true
}

// budgetDefs are the budgets of the config, run first, then the phases'
// and the ranks' in key order, with the owner's overrides for this run.
func (e *Engine) budgetDefs() []budgetDef {
	b := e.cfg.Usage.Budget
	if !e.cfg.Usage.On() || b.IsZero() {
		return nil
	}
	var out []budgetDef
	add := func(key string, v config.Budget, d budgetDef) {
		d.key, d.value = key, string(v)
		if o, ok := e.run.BudgetOverrides[key]; ok {
			if o == state.BudgetContinue {
				d.continued = true
			} else {
				d.value = o
			}
		}
		lim, err := config.Budget(d.value).Limit()
		if err != nil || !state.ValidBudgetKey(key) {
			return // the config and state.json were checked
		}
		d.limit = lim
		out = append(out, d)
	}
	if b.Run != "" {
		add("run", b.Run, budgetDef{})
	}
	for _, id := range slices.Sorted(maps.Keys(b.Phases)) {
		add("phase:"+id, b.Phases[id], budgetDef{phase: id})
	}
	for _, rank := range slices.Sorted(maps.Keys(b.Ranks)) {
		add("rank:"+rank, b.Ranks[rank], budgetDef{rank: rank})
	}
	return out
}

// liveSpend is what l's attempt used so far, as its transcript says.
func (e *Engine) liveSpend(l *launch) spend {
	u := l.usage
	if u == nil || u.r == nil || !u.r.Available() {
		return spend{}
	}
	got := u.r.Usage()
	s := spend{tokens: satAdd(satAdd(sumIn(got.Main), sumIn(got.Sub)), satAdd(got.Main.Output, got.Sub.Output))}
	table := e.cfg.Usage.PriceTable()
	subModels := got.SubModels
	if len(subModels) == 0 {
		subModels = got.Models
	}
	cost := table.Cost(got.Main, got.Models)
	cost.Add(table.Cost(got.Sub, subModels))
	s.usd = cost.USD
	return s
}

// endSpend adds what l's attempt used to its task's: the attempt ended.
func (e *Engine) endSpend(l *launch) {
	s := e.liveSpend(l)
	if s == (spend{}) {
		return
	}
	got := e.budget.ended[l.t.ID]
	if got == nil {
		got = &spent{phase: phaseOf(l.t), rank: l.t.Rank}
		e.budget.ended[l.t.ID] = got
	}
	got.add(s)
}

// budgetUsed is what d used in this run: the ended attempts of its tasks
// and the running ones.
func (e *Engine) budgetUsed(d budgetDef) spend {
	var s spend
	for _, got := range e.budget.ended {
		if d.counts(got.phase, got.rank) {
			s.add(got.spend)
		}
	}
	for _, l := range e.active {
		if d.counts(phaseOf(l.t), l.t.Rank) {
			s.add(e.liveSpend(l))
		}
	}
	return s
}

// use is d with s as a BudgetUse.
func (d budgetDef) use(s spend) BudgetUse {
	u := BudgetUse{Budget: d.key, Limit: d.value}
	if d.limit.Cents > 0 {
		u.Used = fmt.Sprintf("$%.2f", s.usd)
		u.Percent = s.usd * 100 / float64(d.limit.Cents) * 100
	} else {
		u.Used = look.Tokens(s.tokens)
		u.Percent = float64(s.tokens) / float64(d.limit.Tokens) * 100
	}
	if d.continued {
		u.State = BudgetContinued
	}
	return u
}

// over says whether s reached lim.
func over(s spend, lim config.BudgetLimit) bool {
	if lim.Cents > 0 {
		return s.usd*100 >= float64(lim.Cents)
	}
	return s.tokens >= lim.Tokens
}

// checkBudgets checks the run's budgets against what was used (SPEC §6.9):
// one reached is logged, notified and put to the owner, once per budget
// and value, and the UI hears of every change in what they used.
func (e *Engine) checkBudgets(ctx context.Context) {
	defs := e.budgetDefs()
	if len(defs) == 0 && e.budget.shown == nil {
		return
	}
	uses := make([]BudgetUse, 0, len(defs))
	var reached []notify.Message
	for _, d := range defs {
		s := e.budgetUsed(d)
		u := d.use(s)
		switch {
		case d.continued:
		case slices.Contains(e.budget.asks, d.key):
			u.State = BudgetReached
		case over(s, d.limit) && e.budget.reached[d.key] != d.value:
			u.State = BudgetReached
			reached = append(reached, e.reachBudget(u))
		}
		uses = append(uses, u)
	}
	if !slices.Equal(uses, e.budget.shown) {
		e.budget.shown = uses
		e.emitRun(Event{Kind: BudgetRead, Budgets: slices.Clone(uses)})
	}
	e.askBudget()
	// Sent once the question is asked, so ntfy's Continue answers the
	// budget asked (SPEC §10); one reached while another is asked gets
	// Open only.
	for _, m := range reached {
		q := ""
		if m.Question == e.budget.asked {
			q = string(QuestionBudget)
		}
		m.Question = q
		e.notified(e.notifier.Notify(ctx, m))
	}
}

// reachBudget records u reached: the run log's budget_exceeded and the
// event; it returns the urgent budget_exceeded notification, its
// Question the budget's key until checkBudgets sends it. The owner is
// asked once the budgets reached before it are answered.
func (e *Engine) reachBudget(u BudgetUse) notify.Message {
	e.budget.reached[u.Budget] = u.Limit
	e.budget.asks = append(e.budget.asks, u.Budget)
	what := e.budgetWhat(u)
	e.logRun(state.Event{Type: state.EventBudgetExceeded, Budget: u.Budget, Limit: u.Limit, Used: u.Used})
	b := u
	e.emitRun(Event{Kind: BudgetExceeded, Budget: &b, Detail: what})
	return notify.Message{Event: notify.BudgetExceeded, Project: filepath.Base(e.dir.Root()), What: what, RunID: e.runID, At: e.clock.Now(), Question: u.Budget}
}

// budgetWhat says what reaching u means: `run budget 20M reached · 20.4M
// used · M1-04 keeps running`.
func (e *Engine) budgetWhat(u BudgetUse) string {
	var ids []string
	for _, l := range e.active {
		if l.t.Owner.IsAgent() && l.sess != nil && !l.lost {
			ids = append(ids, l.t.ID)
		}
	}
	running := "no session is running"
	switch len(ids) {
	case 0:
	case 1:
		running = ids[0] + " keeps running"
	default:
		running = strings.Join(ids[:len(ids)-1], ", ") + " and " + ids[len(ids)-1] + " keep running"
	}
	return u.Title() + " · " + u.Used + " used · " + running
}

// askBudget puts the first reached budget to the owner, when none is
// asked: Needs you with reason budget, and the question with Raise,
// Continue and Stop (SPEC §6.9, §15.5).
func (e *Engine) askBudget() {
	if e.budget.asked != "" || len(e.budget.asks) == 0 {
		return
	}
	key := e.budget.asks[0]
	i := slices.IndexFunc(e.budget.shown, func(u BudgetUse) bool { return u.Budget == key })
	if i < 0 {
		// Gone from the config snapshot: it can't be; drop it.
		e.budget.asks = e.budget.asks[1:]
		return
	}
	u := e.budget.shown[i]
	e.budget.asked = key
	what := e.budgetWhat(u)
	e.logRun(state.Event{Type: state.EventNeedsYou, Reason: state.ReasonBudget, Detail: u.Title()})
	e.emitRun(Event{Kind: Asked, Question: QuestionBudget, Budget: &u,
		Detail: what + "; no new task starts until you raise it, let it go on, or stop the run"})
}

// budgetHolds says whether a reached budget waits on the owner: no new
// agent task starts meanwhile (SPEC §6.9).
func (e *Engine) budgetHolds() bool { return len(e.budget.asks) > 0 }

// gateBudget holds s's agent tasks while a reached budget waits on the
// owner; user tasks need no session and start.
func (e *Engine) gateBudget(s schedule) schedule {
	if e.budgetHolds() {
		s.start = slices.DeleteFunc(slices.Clone(s.start), func(t *plan.Task) bool { return t.Owner.IsAgent() })
	}
	return s
}

// answerBudget applies the owner's answer to the budget asked (CmdBudget):
// a new value for this run, above what it used, or continue. Recorded in
// state.json (budget_overrides) and the run log (budget_raised); the
// config file is untouched.
func (e *Engine) answerBudget(c Command) {
	key := e.budget.asked
	if key == "" {
		e.reject(c, "no budget is reached")
		return
	}
	var d budgetDef
	found := false
	for _, x := range e.budgetDefs() {
		if x.key == key {
			d, found = x, true
		}
	}
	if !found {
		e.reject(c, "the budget is no longer set")
		return
	}
	used := e.budgetUsed(d)
	text := strings.TrimSpace(c.Text)
	value, done := state.BudgetContinue, BudgetName(key)+" no longer holds this run"
	usedText := d.use(used).Used
	if !strings.EqualFold(text, state.BudgetContinue) {
		lim, err := config.Budget(text).Limit()
		if err != nil {
			e.reject(c, fmt.Sprintf("%q is not a budget: %v", text, err))
			return
		}
		if over(used, lim) {
			nd := d
			nd.limit = lim
			e.reject(c, fmt.Sprintf("%s has used %s already; give a higher value", BudgetName(key), nd.use(used).Used))
			return
		}
		value, done = text, BudgetName(key)+" raised to "+text+" for this run"
	}
	if e.run.BudgetOverrides == nil {
		e.run.BudgetOverrides = map[string]string{}
	}
	e.run.BudgetOverrides[key] = value
	if err := e.dir.SaveRun(e.run); err != nil {
		e.warn(err.Error())
	}
	e.logRun(state.Event{Type: state.EventBudgetRaised, Budget: key, Limit: value, Used: usedText})
	e.logRun(state.Event{Type: state.EventNeedsYouClear, Reason: state.ReasonBudget})
	e.budget.asks = slices.DeleteFunc(e.budget.asks, func(k string) bool { return k == key })
	e.budget.asked = ""
	u := d.use(used)
	u.Limit = value
	if value == state.BudgetContinue {
		u.Limit, u.State = d.value, BudgetContinued
	}
	e.emitRun(Event{Kind: BudgetRaised, Budget: &u, Detail: done})
}

// emitRun emits ev as the run's, not the focused task's.
func (e *Engine) emitRun(ev Event) {
	was := e.task
	e.task = nil
	e.emit(ev)
	e.task = was
}
