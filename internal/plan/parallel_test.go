package plan

import (
	"reflect"
	"strings"
	"testing"
)

func TestParallelismOf(t *testing.T) {
	mk := func(id string, st Status, deps ...string) *Task { return &Task{ID: id, Status: st, Deps: deps} }
	grp := func(t *Task, g string) *Task { t.Group = g; return t }
	tests := []struct {
		name  string
		tasks []*Task
		in    func(*Task) bool
		want  Parallelism
	}{
		{"empty", nil, nil, Parallelism{}},
		{"all finished", []*Task{mk("A", Done), mk("B", Skipped, "A")}, nil, Parallelism{}},
		{"chain", []*Task{mk("A", Ready), mk("B", Blocked, "A"), mk("C", Blocked, "B")}, nil,
			Parallelism{CriticalPath: []string{"A", "B", "C"}, Width: 1}},
		{"diamond", []*Task{mk("A", Ready), mk("B", Blocked, "A"), mk("C", Blocked, "A"), mk("D", Blocked, "B", "C")}, nil,
			Parallelism{CriticalPath: []string{"A", "B", "D"}, Width: 2, Together: [][]string{{"B", "C"}}}},
		{"finished and outside deps are met", []*Task{mk("A", Done), mk("B", Ready, "A"), mk("C", Ready, "A", "X-1")}, nil,
			Parallelism{CriticalPath: []string{"B"}, Width: 2, Together: [][]string{{"B", "C"}}}},
		{"forward dep", []*Task{mk("A", Blocked, "B"), mk("B", Ready)}, nil,
			Parallelism{CriticalPath: []string{"B", "A"}, Width: 1}},
		{"slice", []*Task{mk("A", Ready), mk("B", Ready), mk("C", Blocked, "A")}, func(t *Task) bool { return t.ID != "B" },
			Parallelism{CriticalPath: []string{"A", "C"}, Width: 1}},
		{"group: one slot", []*Task{mk("A", Ready), grp(mk("B", Ready), "db"), grp(mk("C", Ready), "DB"), mk("D", Ready)}, nil,
			Parallelism{CriticalPath: []string{"A"}, Width: 3, Together: [][]string{{"A", "B", "D"}}}},
		{"group: every task in one", []*Task{grp(mk("A", Ready), "g"), grp(mk("B", Ready), "g")}, nil,
			Parallelism{CriticalPath: []string{"A"}, Width: 1}},
		{"group: leftovers form the next round", []*Task{grp(mk("A", Ready), "g"), grp(mk("B", Ready), "g"), mk("C", Ready), mk("D", Ready)}, nil,
			Parallelism{CriticalPath: []string{"A"}, Width: 3, Together: [][]string{{"A", "C", "D"}}}},
		{"no marker: width 1, critical path only", []*Task{mk("A", Ready), mk("B", Ready), mk("C", Blocked, "A")}, nil,
			Parallelism{CriticalPath: []string{"A", "C"}, Width: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := &Phase{ID: "P", Tasks: tt.tasks, Parallel: !strings.HasPrefix(tt.name, "no marker")}
			if got := ph.ParallelismOf(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParallelismWith(t *testing.T) {
	p := Parallelism{Together: [][]string{{"B-1", "B-3"}, {"B-4", "B-5", "B-6"}}}
	for _, c := range []struct {
		id   string
		want []string
	}{
		{"B-1", []string{"B-3"}},
		{"B-3", []string{"B-1"}},
		{"B-5", []string{"B-4", "B-6"}},
		{"B-2", nil},
	} {
		if got := p.With(c.id); !reflect.DeepEqual(got, c.want) {
			t.Errorf("With(%s) = %v, want %v", c.id, got, c.want)
		}
	}
	if got := (Parallelism{}).With("B-1"); got != nil {
		t.Errorf("a sequential phase's With = %v, want nil", got)
	}
}
