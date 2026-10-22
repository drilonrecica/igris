package plan

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// randomParallelPlan writes a format-3 plan with one parallel phase M1 of
// n tasks (deps only on earlier tasks, so acyclic), random ranks, owners,
// groups and statuses, plus a phase P0 of finished tasks some deps point
// at. Statuses are random, readiness drift included: the scheduler goes by
// the dependencies, never by ready/blocked.
func randomParallelPlan(rng *rand.Rand, n int) string {
	var b strings.Builder
	b.WriteString(format3 + "\n## P0\n\n| ID | Status | Model |\n|---|---|---|\n| p | done | sonnet |\n| q | skipped | sonnet |\n")
	b.WriteString("\n## M1\n\n<!-- igris:parallel -->\n\n| ID | Deps | Status | Model | Owner | Group |\n|---|---|---|---|---|---|\n")
	ranks := []string{"sonnet", "opus", "fable"}
	groups := []string{"", "", "", "g1", "g2"}
	statuses := []string{"ready", "ready", "blocked", "done", "skipped", "in progress"}
	for i := range n {
		id := fmt.Sprintf("t%d", i)
		var deps []string
		for j := range i {
			if rng.Intn(3) == 0 {
				deps = append(deps, fmt.Sprintf("t%d", j))
			}
		}
		if rng.Intn(4) == 0 {
			deps = append(deps, []string{"p", "q"}[rng.Intn(2)])
		}
		owner, rank := "agent", ranks[rng.Intn(len(ranks))]
		switch rng.Intn(8) {
		case 0:
			owner, rank = "user", ""
		case 1:
			owner = "agent + user"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", id, strings.Join(deps, ", "), statuses[rng.Intn(len(statuses))], rank, owner, groups[rng.Intn(len(groups))])
	}
	return b.String()
}

func randomRules(rng *rand.Rand) ParallelRules {
	r := ParallelRules{Max: 1 + rng.Intn(4), ExclusiveOwner: rng.Intn(2) == 0}
	if rng.Intn(2) == 0 {
		r.RankCaps = map[string]int{"fable": 1}
		if rng.Intn(2) == 0 {
			r.RankCaps["opus"] = 1 + rng.Intn(3)
		}
	}
	return r
}

// checkStart fails the test when sel violates SPEC §5.1 for the plan with
// active tasks active: a started task's dependencies are satisfied, Max,
// the rank caps and the Groups are never exceeded beyond what the active
// tasks already exceed (a plan edited by hand), and owner tasks run alone
// when exclusive. It returns the active set after the start.
func checkStart(t *testing.T, p *Plan, active []string, r ParallelRules, sel ParallelSelection) []string {
	t.Helper()
	ph := p.Phase("M1")
	before := slices.Clone(active)
	for _, a := range sel.Adopt {
		if a.Status != InProgress || slices.Contains(before, a.ID) {
			t.Fatalf("adopt %s: status %s, active %v", a.ID, a.Status, before)
		}
		before = append(before, a.ID)
	}
	after := slices.Clone(before)
	for _, s := range sel.Start {
		switch {
		case s.Phase != ph:
			t.Fatalf("start %s of phase %s", s.ID, s.Phase.ID)
		case s.Status.Satisfied() || s.Status == InProgress:
			t.Fatalf("start %s, which is %s", s.ID, s.Status)
		case slices.Contains(before, s.ID):
			t.Fatalf("start %s, which is active", s.ID)
		case len(p.unmet(s)) > 0:
			t.Fatalf("start %s with unmet deps %v", s.ID, p.unmet(s))
		case slices.Contains(after, s.ID):
			t.Fatalf("start %s twice", s.ID)
		}
		after = append(after, s.ID)
	}
	if len(sel.Start) > 0 && len(after) > max(r.Max, len(before)) {
		t.Fatalf("%d active after the start (max %d, %d before)", len(after), r.Max, len(before))
	}
	count := func(ids []string, f func(*Task) bool) int {
		n := 0
		for _, id := range ids {
			if f(p.Task(id)) {
				n++
			}
		}
		return n
	}
	for rank, cap := range r.RankCaps {
		of := func(x *Task) bool { return x.Rank == rank }
		if n := count(after, of); n > max(cap, count(before, of)) {
			t.Fatalf("%d active tasks of rank %s after the start (cap %d)", n, rank, cap)
		}
	}
	for _, s := range sel.Start {
		if s.Group == "" {
			continue
		}
		of := func(x *Task) bool { return strings.EqualFold(x.Group, s.Group) }
		if n := count(after, of); n > 1 {
			t.Fatalf("%d active tasks of group %s after starting %s", n, s.Group, s.ID)
		}
	}
	if r.ExclusiveOwner {
		owners := count(before, isOwnerTask)
		for _, s := range sel.Start {
			switch {
			case owners > 0:
				t.Fatalf("start %s beside an active owner task", s.ID)
			case isOwnerTask(s) && len(before) > 0:
				t.Fatalf("start owner task %s beside %v", s.ID, before)
			case isOwnerTask(s) && len(sel.Start) > 1:
				t.Fatalf("start owner task %s with %v", s.ID, ids(sel.Start))
			}
		}
	}
	switch sel.Outcome {
	case Next:
		if len(sel.Start) == 0 {
			t.Fatal("Next without a task to start")
		}
	case Busy:
		if len(sel.Start) > 0 || len(after) == 0 {
			t.Fatalf("Busy with start %v and %d active", ids(sel.Start), len(after))
		}
	case Complete, Stuck:
		if len(after) > 0 || len(sel.Start) > 0 {
			t.Fatalf("%s with %d active", sel.Outcome, len(after))
		}
		if (sel.Outcome == Stuck) != (len(sel.Waiting) > 0) {
			t.Fatalf("%s with waiting %v", sel.Outcome, sel.Waiting)
		}
	}
	return after
}

// The scheduler never violates the dependencies, Max, the rank caps, the
// Groups or the owner tasks' exclusivity, whatever the plan and the active
// set (SPEC §17).
func TestSelectParallelProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(15_03)) //nolint:gosec // a fixed seed: the same plans every run
	for i := 0; i < 400; i++ {
		text := randomParallelPlan(rng, 1+rng.Intn(8))
		p := Parse("tasks.md", []byte(text), Options{})
		if issues := p.Validate(testRules); len(issues) > 0 {
			t.Fatalf("plan %d invalid: %v\n%s", i, issueMsgs(issues), text)
		}
		r := randomRules(rng)
		// A random active set of the phase's unfinished tasks.
		var active []string
		for _, task := range p.Phase("M1").Tasks {
			if !task.Status.Satisfied() && rng.Intn(3) == 0 {
				active = append(active, task.ID)
			}
		}
		sel, err := p.SelectParallel("M1", nil, active, r)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if t.Failed() {
					t.Logf("plan %d, active %v, rules %+v:\n%s", i, active, r, text)
				}
			}()
			checkStart(t, p, active, r, sel)
		}()
	}
}

// A simulated run of a random parallel phase, finishing random active
// tasks between selections, always completes: no selection violates the
// rules along the way, and the phase is never stuck when every dependency
// is in the plan and finished or finishable.
func TestSelectParallelRunsToCompletion(t *testing.T) {
	rng := rand.New(rand.NewSource(1503)) //nolint:gosec // a fixed seed: the same plans every run
	for i := 0; i < 300; i++ {
		text := randomParallelPlan(rng, 1+rng.Intn(8))
		p := Parse("tasks.md", []byte(text), Options{})
		if issues := p.Validate(testRules); len(issues) > 0 {
			t.Fatalf("plan %d invalid: %v\n%s", i, issueMsgs(issues), text)
		}
		r := randomRules(rng)
		var active []string
		for round := 0; ; round++ {
			if round > 100 {
				t.Fatalf("plan %d: no end after %d rounds\n%s", i, round, text)
			}
			sel, err := p.SelectParallel("M1", nil, active, r)
			if err != nil {
				t.Fatal(err)
			}
			active = checkStart(t, p, active, r, sel)
			if t.Failed() {
				t.Fatalf("plan %d, rules %+v:\n%s", i, r, text)
			}
			for _, s := range sel.Start {
				s.Status = InProgress
			}
			if sel.Outcome == Complete {
				break
			}
			if sel.Outcome == Stuck {
				t.Fatalf("plan %d stuck: %v\n%s", i, sel.Waiting, text)
			}
			// Finish some active tasks, at least one when nothing started.
			var left []string
			for _, id := range active {
				if rng.Intn(2) == 0 || (len(sel.Start) == 0 && len(left) == len(active)-1) {
					p.Task(id).Status = []Status{Done, Skipped}[rng.Intn(2)]
				} else {
					left = append(left, id)
				}
			}
			active = left
		}
		for _, task := range p.Phase("M1").Tasks {
			if !task.Status.Satisfied() {
				t.Fatalf("plan %d complete with %s %s\n%s", i, task.ID, task.Status, text)
			}
		}
	}
}
