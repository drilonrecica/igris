package main

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/serve"
)

// A question of the run (a budget, the commit batch) is not a task in
// `igris status`'s count (SPEC §15.4).
func TestLiveLineCountsTasksOnly(t *testing.T) {
	ask := serve.Event{Task: "M1-03", Question: string(engine.QuestionCommit)}
	budget := &serve.Event{Task: "M1-03", Question: string(engine.QuestionBudget)}
	for _, tt := range []struct {
		name string
		snap serve.Snapshot
		want string
	}{
		{"a task and the run", serve.Snapshot{Questions: []serve.Event{ask}, BudgetQuestion: budget}, "M1-03 · commit? · 1 task and the run need you"},
		{"the run alone", serve.Snapshot{BudgetQuestion: budget}, "! Needs you · budget reached"},
		{"a task alone", serve.Snapshot{Questions: []serve.Event{ask}}, "! Needs you · M1-03 · commit?"},
		{"two tasks", serve.Snapshot{Questions: []serve.Event{ask, {Task: "M1-05", Question: string(engine.QuestionCommit)}}}, "2 tasks need you"},
	} {
		got := liveLine(&tt.snap, nil, nil).String()
		if !strings.Contains(got, tt.want) {
			t.Errorf("%s: %q lacks %q", tt.name, got, tt.want)
		}
	}
}
