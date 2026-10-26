package engine

// Budgets (SPEC §6.9; decisions.md V18-P1), end to end on the fake backend
// and the fake clock: a budget reached while a session runs needs the owner
// and holds new starts, the session keeps running, and the owner raises
// it, lets it go on or stops the run. Nothing is stopped or switched.

import (
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

// budgetLines returns the run log's budget_* lines as "type budget limit
// used".
func (h *harness) budgetLines() string {
	var out []string
	for _, e := range h.logEvents() {
		switch e.Type {
		case state.EventBudgetExceeded, state.EventBudgetRaised:
			out = append(out, strings.Join([]string{string(e.Type), e.Budget, e.Limit, e.Used}, " "))
		}
	}
	return strings.Join(out, "; ")
}

// spendA1 makes A-1 use 712 tokens at once and 712 more 15 s later, and
// signal done a minute after it opened: 1.4k tokens, about $0.0125 on
// sonnet.
func (h *harness) spendA1() {
	dir := h.reportTranscripts()
	h.autoSignalExcept("A-1")
	prev := h.onEvent
	h.onEvent = func(ev Event) {
		prev(ev)
		if ev.Kind != SessionOpened || ev.Task != "A-1" {
			return
		}
		s := ev.ClaudeSession
		respond(h.t, dir, s, ev.At.Add(time.Second), "msg_1", "claude-sonnet-5-5", 2, 600, 1, false)
		h.clock.At(ev.At.Sub(t0)+15*time.Second, func() {
			respond(h.t, dir, s, h.clock.Now(), "msg_2", "claude-sonnet-5-5", 2, 600, 1, false)
		})
		h.signalAt(ev.At.Sub(t0)+time.Minute, "A-1")
	}
}

// A run budget reached mid-task: the run log, the urgent notification and
// Needs you, A-1 keeps running and is done, A-2 waits until the owner
// raises the budget, which state.json keeps for the run.
func TestBudgetReachedThenRaised(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn+"\n[usage.budget]\nrun = \"1k\"\n")
	h.spendA1()
	var raisedAt time.Time
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionBudget {
			h.clock.At(ev.At.Sub(t0)+5*time.Minute, func() {
				raisedAt = h.clock.Now()
				h.eng.Send(Command{Kind: CmdBudget, Text: "2k"})
			})
		}
	})
	if res, err := h.run(); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got, want := h.budgetLines(), "budget_exceeded run 1k 1.4k; budget_raised run 2k 1.4k"; got != want {
		t.Errorf("budget lines = %q, want %q", got, want)
	}
	// The run's wait, not a task's (A-1's idle waits are its own).
	var waits []string
	for _, w := range strings.Split(h.waits(), ", ") {
		if strings.HasSuffix(w, " budget") {
			waits = append(waits, w)
		}
	}
	if got, want := strings.Join(waits, ", "), "#0 budget, clear #0 budget"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
	asked := h.event(Asked, "")
	if asked.Question != QuestionBudget || asked.Task != "" || asked.Budget == nil || asked.Budget.Title() != "run budget 1k reached" {
		t.Errorf("asked = %+v (budget %+v)", asked, asked.Budget)
	}
	if !strings.HasPrefix(asked.Detail, "run budget 1k reached · 1.4k used · A-1 keeps running") {
		t.Errorf("asked detail = %q", asked.Detail)
	}
	want := "request: run budget 1k reached · 1.4k used · A-1 keeps running"
	if got := h.toasts(); len(got) < 1 || got[0] != want {
		t.Errorf("toasts = %q, want first %q", got, want)
	}
	// A-1 ran to its done; A-2 started only after the raise.
	if h.count(Retrying) != 0 || h.count(SessionLost) != 0 {
		t.Errorf("events: %s", h.kinds())
	}
	if d := h.event(TaskDone, "A-1").At.Sub(h.event(BudgetExceeded, "").At); d <= 0 {
		t.Errorf("A-1 done %s after the budget was reached", d)
	}
	if started := h.event(TaskStarted, "A-2").At; started.Before(raisedAt) {
		t.Errorf("A-2 started at %s, before the raise at %s", started, raisedAt)
	}
	if ev := h.event(BudgetRaised, ""); ev.Budget == nil || ev.Budget.Limit != "2k" || ev.Detail != "run budget raised to 2k for this run" {
		t.Errorf("budget_raised = %+v (budget %+v)", ev, ev.Budget)
	}
	// The last budgets the UI heard of: 1.4k of 2k.
	var last Event
	for _, ev := range h.events {
		if ev.Kind == BudgetRead {
			last = ev
		}
	}
	if len(last.Budgets) != 1 || last.Budgets[0].Line() != "budget 1.4k / 2k · 71%" || last.Budgets[0].State != "" {
		t.Errorf("last budgets = %+v", last.Budgets)
	}
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	if run.BudgetOverrides["run"] != "2k" {
		t.Errorf("budget_overrides = %v", run.BudgetOverrides)
	}
	if strings.Contains(h.read(state.ConfigFile), "2k") {
		t.Error("igris.toml was changed")
	}
}

// A phase budget in dollars: a value not above what it used is refused,
// continue lets it go on and it isn't asked again; a rank budget counts
// only its rank's tasks.
func TestBudgetContinue(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn+"\n[usage.budget.phases]\nA = \"$0.01\"\n\n[usage.budget.ranks]\nopus = \"1k\"\n")
	h.spendA1()
	h.on(func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionBudget {
			h.eng.Send(Command{Kind: CmdBudget, Text: "lots"})
			h.eng.Send(Command{Kind: CmdBudget, Text: "$0.01"})
			h.eng.Send(Command{Kind: CmdBudget, Text: "Continue"})
		}
	})
	if res, err := h.run(); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got, want := h.budgetLines(), "budget_exceeded phase:A $0.01 $0.01; budget_raised phase:A continue $0.01"; got != want {
		t.Errorf("budget lines = %q, want %q", got, want)
	}
	var warns []string
	for _, ev := range h.events {
		if ev.Kind == Warning && strings.Contains(ev.Detail, "budget answer") {
			warns = append(warns, ev.Detail)
		}
	}
	if len(warns) != 2 || !strings.Contains(warns[0], `"lots" is not a budget`) || !strings.Contains(warns[1], "has used $0.01 already") {
		t.Errorf("warnings = %q", warns)
	}
	if ev := h.event(BudgetRaised, ""); ev.Budget == nil || ev.Budget.State != BudgetContinued || ev.Budget.Line() != "phase A budget $0.01 / $0.01 · 120% · continued" {
		t.Errorf("budget_raised = %+v (budget %+v)", ev, ev.Budget)
	}
	if h.count(Asked) != 1 {
		t.Errorf("asked %d times: %s", h.count(Asked), h.kinds())
	}
}

// Stop while a budget waits on the owner: the run stops, nothing new
// starts, and the session that ran is left as it is.
func TestBudgetStop(t *testing.T) {
	h := newHarness(t, chainPlan, usageOn+"\n[usage.budget]\nrun = 1000\n")
	h.spendA1()
	h.on(func(ev Event) {
		if ev.Kind == TaskDone && ev.Task == "A-1" {
			h.clock.At(ev.At.Sub(t0)+time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
		}
	})
	res, err := h.run()
	if err != nil || res.Outcome != Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if got := h.opened(); got != "A-1" {
		t.Errorf("opened = %q", got)
	}
	if got := h.budgetLines(); got != "budget_exceeded run 1000 1.4k" {
		t.Errorf("budget lines = %q", got)
	}
	if got := h.statuses(); !strings.HasPrefix(got, "A-1=done A-2=ready") {
		t.Errorf("statuses = %s", got)
	}
}

// No answer without a budget reached.
func TestBudgetAnswerWithoutABudgetReached(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.on(func(ev Event) {
		if ev.Kind == RunStarted {
			h.eng.Send(Command{Kind: CmdBudget, Text: "continue"})
		}
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, ev := range h.events {
		if ev.Kind == Warning && ev.Detail == "ignored budget answer: no budget is reached" {
			found = true
		}
	}
	if !found || h.count(BudgetRead) != 0 {
		t.Errorf("events: %s", h.kinds())
	}
}
