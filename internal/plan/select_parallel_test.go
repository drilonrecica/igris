package plan

import (
	"reflect"
	"strings"
	"testing"
)

// parPlan builds a format-3 plan from "PHASE: ID status rank owner group
// deps…" lines; a "-" cell is empty. A phase named with a trailing "*" is
// marked parallel. Statuses write "_" for a space (in_progress).
func parPlan(t *testing.T, spec string) *Plan {
	t.Helper()
	var b strings.Builder
	b.WriteString(format3)
	cur := ""
	for _, ln := range strings.Split(strings.TrimSpace(spec), "\n") {
		ph, rest, _ := strings.Cut(strings.TrimSpace(ln), ":")
		f := strings.Fields(rest)
		if ph != cur {
			cur = ph
			b.WriteString("\n## " + strings.TrimSuffix(ph, "*") + "\n\n")
			if strings.HasSuffix(ph, "*") {
				b.WriteString("<!-- igris:parallel -->\n\n")
			}
			b.WriteString("| ID | Deps | Status | Model | Owner | Group |\n|---|---|---|---|---|---|\n")
		}
		cell := func(s string) string {
			if s == "-" {
				return ""
			}
			return s
		}
		status := strings.ReplaceAll(f[1], "_", " ")
		b.WriteString("| " + f[0] + " | " + strings.Join(f[5:], ", ") + " | " + status + " | " + cell(f[2]) + " | " + cell(f[3]) + " | " + cell(f[4]) + " |\n")
	}
	p := Parse("tasks.md", []byte(b.String()), Options{})
	if issues := p.Validate(testRules); len(issues) > 0 {
		t.Fatalf("invalid test plan: %v", issueMsgs(issues))
	}
	return p
}

func ids(tasks []*Task) []string {
	var out []string
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

// SelectParallel applies SPEC §5.1's parallel rule: up to Max active tasks,
// the candidates in file order, each held by its rank's cap, its Group and
// the owner tasks' exclusivity.
func TestSelectParallel(t *testing.T) {
	const three = `
M1*: a ready sonnet agent -
M1*: b ready sonnet agent -
M1*: c ready sonnet agent -`
	rules := func(max int) ParallelRules { return ParallelRules{Max: max, ExclusiveOwner: true} }
	tests := []struct {
		name    string
		plan    string
		active  []string
		rules   ParallelRules
		outcome Outcome
		start   []string
		adopt   []string
		waiting []string
	}{
		{"up to max", three, nil, rules(2), Next, []string{"a", "b"}, nil, nil},
		{"the rest once a slot frees", three, []string{"a"}, rules(2), Next, []string{"b"}, nil, nil},
		{"full", three, []string{"a", "b"}, rules(2), Busy, nil, nil, nil},
		{"over max after it was lowered", three, []string{"a", "b", "c"}, rules(1), Busy, nil, nil, nil},
		{"max 1 runs one at a time", three, nil, rules(1), Next, []string{"a"}, nil, nil},
		{"max 3 runs all", three, nil, rules(3), Next, []string{"a", "b", "c"}, nil, nil},
		{"deps hold a task back", `
M1*: a ready sonnet agent -
M1*: b blocked sonnet agent - a
M1*: c ready sonnet agent -`, nil, rules(3), Next, []string{"a", "c"}, nil, nil},
		{"a dep satisfied elsewhere", `
P0: x done sonnet agent -
M1*: a blocked sonnet agent - x
M1*: b blocked sonnet agent - y
M1*: c ready sonnet agent -
M2: y ready sonnet agent -`, nil, rules(3), Next, []string{"a", "c"}, nil, nil},
		{"in progress is active, counts, and is adopted", `
M1*: a in_progress sonnet agent -
M1*: b ready sonnet agent -
M1*: c ready sonnet agent -`, nil, rules(2), Next, []string{"b"}, []string{"a"}, nil},
		{"in progress already active is not adopted twice", `
M1*: a in_progress sonnet agent -
M1*: b ready sonnet agent -`, []string{"a"}, rules(2), Next, []string{"b"}, nil, nil},
		{"complete", `
M1*: a done sonnet agent -
M1*: b skipped sonnet agent -`, nil, rules(2), Complete, nil, nil, nil},
		{"complete only once nothing is active", `
M1*: a done sonnet agent -
M1*: b done sonnet agent -`, []string{"b"}, rules(2), Busy, nil, nil, nil},
		{"stuck: none active, none may start", `
M1*: a blocked sonnet agent - z
M1*: b done sonnet agent -
M2: z ready sonnet agent -`, nil, rules(2), Stuck, nil, nil, []string{"a waits on z (ready, phase M2)"}},
		{"not stuck while a task is active", `
M1*: a in_progress sonnet agent -
M1*: b blocked sonnet agent - a`, []string{"a"}, rules(2), Busy, nil, nil, nil},
		{"rank cap", `
M1*: a ready fable agent -
M1*: b ready fable agent -
M1*: c ready sonnet agent -`, nil, ParallelRules{Max: 3, RankCaps: map[string]int{"fable": 1}}, Next, []string{"a", "c"}, nil, nil},
		{"rank cap counts the active", `
M1*: a in_progress fable agent -
M1*: b ready fable agent -
M1*: c ready sonnet agent -`, []string{"a"}, ParallelRules{Max: 3, RankCaps: map[string]int{"fable": 1}}, Next, []string{"c"}, nil, nil},
		{"rank cap above max changes nothing", `
M1*: a ready fable agent -
M1*: b ready fable agent -
M1*: c ready fable agent -`, nil, ParallelRules{Max: 2, RankCaps: map[string]int{"fable": 8}}, Next, []string{"a", "b"}, nil, nil},
		{"unlisted ranks are held by max only", `
M1*: a ready fable agent -
M1*: b ready fable agent -
M1*: c ready opus agent -
M1*: d ready opus agent -`, nil, ParallelRules{Max: 4, RankCaps: map[string]int{"fable": 1}}, Next, []string{"a", "c", "d"}, nil, nil},
		{"a group never runs together", `
M1*: a ready sonnet agent db
M1*: b ready sonnet agent DB
M1*: c ready sonnet agent cache
M1*: d ready sonnet agent -`, nil, rules(4), Next, []string{"a", "c", "d"}, nil, nil},
		{"a group held by an active task", `
M1*: a in_progress sonnet agent db
M1*: b ready sonnet agent db
M1*: c ready sonnet agent -`, []string{"a"}, rules(3), Next, []string{"c"}, nil, nil},
		{"a group whatever the deps say", `
M1*: a done sonnet agent db
M1*: b ready sonnet agent db
M1*: c ready sonnet agent db`, nil, rules(3), Next, []string{"b"}, nil, nil},
		{"owner task starts alone", `
M1*: a ready - user -
M1*: b ready sonnet agent -`, nil, rules(2), Next, []string{"a"}, nil, nil},
		{"owner task waits for the active, holding the rest back", `
M1*: a in_progress sonnet agent -
M1*: b ready fable agent+user -
M1*: c ready sonnet agent -`, []string{"a"}, rules(3), Busy, nil, nil, nil},
		{"nothing starts beside an active owner task", `
M1*: a in_progress - user -
M1*: b ready sonnet agent -`, []string{"a"}, rules(3), Busy, nil, nil, nil},
		{"candidates before an owner task start", `
M1*: a ready sonnet agent -
M1*: b ready - user -
M1*: c ready sonnet agent -`, nil, rules(3), Next, []string{"a"}, nil, nil},
		{"owner tasks like the others when not exclusive", `
M1*: a ready sonnet agent -
M1*: b ready - user -
M1*: c ready sonnet agent -`, nil, ParallelRules{Max: 3}, Next, []string{"a", "b", "c"}, nil, nil},
		{"a slice selects among its tasks", `
M1*: a ready sonnet agent -
M1*: b ready sonnet agent -
M1*: c ready sonnet agent -`, nil, rules(3), Next, []string{"a", "c"}, nil, nil},
		{"stuck in a slice: its tasks wait on tasks outside", `
M1*: a blocked sonnet agent - b
M1*: b ready sonnet agent -`, nil, rules(3), Stuck, nil, nil, []string{"a waits on b (ready, phase M1)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := parPlan(t, tt.plan)
			var in func(*Task) bool
			if strings.Contains(tt.name, "slice") {
				in = func(t *Task) bool { return t.ID != "b" }
			}
			sel, err := p.SelectParallel("M1", in, tt.active, tt.rules)
			if err != nil {
				t.Fatal(err)
			}
			var waiting []string
			for _, w := range sel.Waiting {
				waiting = append(waiting, w.String())
			}
			if sel.Outcome != tt.outcome || !reflect.DeepEqual(ids(sel.Start), tt.start) || !reflect.DeepEqual(ids(sel.Adopt), tt.adopt) || !reflect.DeepEqual(waiting, tt.waiting) {
				t.Errorf("got %s start=%v adopt=%v waiting=%v, want %s start=%v adopt=%v waiting=%v",
					sel.Outcome, ids(sel.Start), ids(sel.Adopt), waiting, tt.outcome, tt.start, tt.adopt, tt.waiting)
			}
		})
	}
}

func TestSelectParallelUnknownPhase(t *testing.T) {
	p := parPlan(t, "M1*: a ready sonnet agent -")
	if _, err := p.SelectParallel("M9", nil, nil, ParallelRules{Max: 2}); err == nil || !strings.Contains(err.Error(), "unknown phase") {
		t.Fatalf("err = %v, want unknown phase", err)
	}
}
