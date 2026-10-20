package plan

import (
	"reflect"
	"testing"
)

func TestParallelismOf(t *testing.T) {
	mk := func(id string, st Status, deps ...string) *Task { return &Task{ID: id, Status: st, Deps: deps} }
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := &Phase{ID: "P", Tasks: tt.tasks}
			if got := ph.ParallelismOf(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
