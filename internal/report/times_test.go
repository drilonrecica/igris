package report

import (
	"reflect"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

func TestSpansNormMinus(t *testing.T) {
	m := func(a, b int) span {
		return span{t0.Add(time.Duration(a) * time.Minute), t0.Add(time.Duration(b) * time.Minute)}
	}
	for _, tc := range []struct {
		name     string
		in, cut  spans
		norm     spans
		minus    spans
		minusMin int
	}{
		{name: "empty"},
		{name: "merge and drop empty", in: spans{m(5, 8), m(0, 3), m(2, 4), m(6, 6), m(8, 9)},
			norm: spans{m(0, 4), m(5, 9)}, minus: spans{m(0, 4), m(5, 9)}, minusMin: 8},
		{name: "cut inside", in: spans{m(0, 10)}, cut: spans{m(2, 3), m(5, 7)},
			norm: spans{m(0, 10)}, minus: spans{m(0, 2), m(3, 5), m(7, 10)}, minusMin: 7},
		{name: "cut over the edges", in: spans{m(2, 4), m(6, 8)}, cut: spans{m(0, 3), m(7, 20)},
			norm: spans{m(2, 4), m(6, 8)}, minus: spans{m(3, 4), m(6, 7)}, minusMin: 2},
		{name: "cut all", in: spans{m(2, 4)}, cut: spans{m(0, 9)}, norm: spans{m(2, 4)}},
	} {
		n := norm(tc.in)
		if !reflect.DeepEqual(n, tc.norm) {
			t.Errorf("%s: norm %v, want %v", tc.name, n, tc.norm)
		}
		got := n.minus(norm(tc.cut))
		if !reflect.DeepEqual(got, tc.minus) || got.total() != time.Duration(tc.minusMin)*time.Minute {
			t.Errorf("%s: minus %v (%v), want %v", tc.name, got, got.total(), tc.minus)
		}
	}
}

func TestReportTimes(t *testing.T) {
	const id = "20261001-090000-3fa2"
	at := func(min int, typ state.EventType, task string) state.Event {
		return state.Event{V: 1, At: t0.Add(time.Duration(min) * time.Minute), Type: typ, Run: id, Task: task}
	}
	start := func(min int, task, phase, rank, owner string) state.Event {
		e := at(min, state.EventTaskStarted, task)
		e.Phase, e.Rank, e.Owner = phase, rank, owner
		return e
	}
	verify := func(min, took int, task string) state.Event {
		e := at(min, state.EventVerifyPassed, task)
		e.DurationMS = int64(took) * 60_000
		return e
	}
	wait := func(min int, typ state.EventType, task string) state.Event {
		e := at(min, typ, task)
		e.Reason = state.ReasonIdle
		return e
	}
	limit := func(min, reset int, task string) state.Event {
		e := at(min, state.EventLimitHit, task)
		e.Window, e.ResetsAt = "five_hour", t0.Add(time.Duration(reset)*time.Minute).Format(time.RFC3339)
		return e
	}
	mins := func(n int) int { return n * 60 }
	for _, tc := range []struct {
		name   string
		events []state.Event
		tasks  map[string]*Times
		run    *TimeGroup
		phases []string
		ranks  []string
	}{
		{
			name: "sequential: needs you before limit before verify",
			events: []state.Event{
				at(0, state.EventRunStarted, ""),
				start(0, "A-1", "A", "opus", "agent"),
				limit(5, 15, "A-1"),                  // 5–15, less the wait
				wait(10, state.EventNeedsYou, "A-1"), // 10–12
				wait(12, state.EventNeedsYouClear, "A-1"),
				verify(20, 4, "A-1"),                 // 16–20
				verify(32, 4, "A-1"),                 // 28–32, two minutes in the wait below
				wait(30, state.EventNeedsYou, "A-1"), // 30–35
				wait(35, state.EventNeedsYouClear, "A-1"),
				at(40, state.EventTaskDone, "A-1"),
				at(40, state.EventRunStopped, ""),
			},
			tasks: map[string]*Times{"A-1": {AgentS: mins(19), VerifyS: mins(6), NeedsYouS: mins(7), LimitS: mins(8)}},
			run: &TimeGroup{Times: Times{AgentS: mins(19), VerifyS: mins(6), NeedsYouS: mins(7), LimitS: mins(8)},
				Wall: Times{AgentS: mins(19), VerifyS: mins(6), NeedsYouS: mins(7), LimitS: mins(8)}, WallS: mins(40)},
			phases: []string{"A"}, ranks: []string{"opus"},
		},
		{
			name: "parallel: counted per task and as wall time; user task left out",
			events: []state.Event{
				at(0, state.EventRunStarted, ""),
				start(0, "P-1", "P", "opus", "agent"),
				start(2, "P-2", "P", "sonnet", "agent"),
				start(3, "U-1", "P", "", "user"),
				wait(4, state.EventNeedsYou, "P-2"), // 4–6
				wait(6, state.EventNeedsYouClear, "P-2"),
				at(8, state.EventTaskDone, "P-2"),
				at(9, state.EventTaskDone, "U-1"),
				at(10, state.EventTaskDone, "P-1"),
				start(12, "Q-1", "Q", "sonnet", "agent"),
				at(15, state.EventTaskDone, "Q-1"),
				at(15, state.EventRunStopped, ""),
			},
			tasks: map[string]*Times{
				"P-1": {AgentS: mins(10)}, "P-2": {AgentS: mins(4), NeedsYouS: mins(2)}, "Q-1": {AgentS: mins(3)},
			},
			run: &TimeGroup{Times: Times{AgentS: mins(17), NeedsYouS: mins(2)},
				Wall: Times{AgentS: mins(13), NeedsYouS: mins(2)}, WallS: mins(13)},
			phases: []string{"P", "Q"}, ranks: []string{"opus", "sonnet"},
		},
		{
			name: "limit wait ends with the attempt, an open task with the run's last line",
			events: []state.Event{
				at(0, state.EventRunStarted, ""),
				start(0, "A-1", "A", "opus", "agent"),
				limit(2, 60, "A-1"),
				at(5, state.EventTaskRetried, "A-1"),
				limit(6, 6, "A-1"), // a reset that isn't after the hit: unknown, no wait
				{V: 1, At: t0.Add(7 * time.Minute), Type: state.EventLimitHit, Run: id, Task: "A-1", ResetsAt: "soon"},
				at(9, state.EventNotification, "A-1"),
			},
			tasks: map[string]*Times{"A-1": {AgentS: mins(6), LimitS: mins(3)}},
			run: &TimeGroup{Times: Times{AgentS: mins(6), LimitS: mins(3)},
				Wall: Times{AgentS: mins(6), LimitS: mins(3)}, WallS: mins(9)},
			phases: []string{"A"}, ranks: []string{"opus"},
		},
		{
			name:   "only a user task",
			events: []state.Event{at(0, state.EventRunStarted, ""), start(0, "U-1", "A", "", "user"), at(4, state.EventTaskDone, "U-1")},
		},
	} {
		in := HistoryInput{Events: tc.events}
		r, err := NewReport(in, "demo", "")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := map[string]*Times{}
		for _, task := range r.Tasks {
			if task.Time != nil {
				got[task.ID] = task.Time
			}
		}
		if len(tc.tasks) == 0 {
			tc.tasks = map[string]*Times{}
		}
		if !reflect.DeepEqual(got, tc.tasks) {
			t.Errorf("%s: tasks %v, want %v", tc.name, got, tc.tasks)
		}
		if tc.run == nil {
			if r.Time != nil {
				t.Errorf("%s: time %+v, want none", tc.name, r.Time)
			}
			continue
		}
		if r.Time == nil || r.Time.Run != *tc.run {
			t.Errorf("%s: run %+v, want %+v", tc.name, r.Time, tc.run)
			continue
		}
		var phases, ranks []string
		for _, g := range r.Time.Phases {
			phases = append(phases, g.Phase)
		}
		for _, g := range r.Time.Ranks {
			ranks = append(ranks, g.Rank)
		}
		if !reflect.DeepEqual(phases, tc.phases) || !reflect.DeepEqual(ranks, tc.ranks) {
			t.Errorf("%s: phases %v ranks %v, want %v %v", tc.name, phases, ranks, tc.phases, tc.ranks)
		}
		// History carries the same breakdown.
		h := NewHistory(in)
		if len(h.Runs) != 1 || !reflect.DeepEqual(h.Runs[0].Time, r.Time) {
			t.Errorf("%s: history time %+v, want %+v", tc.name, h.Runs[0].Time, r.Time)
		}
	}
}

// A v0 run logs no waits: no breakdown.
func TestReportTimesV0(t *testing.T) {
	events := []state.Event{
		ev(0, state.EventRunStarted, "", "phase A"),
		ev(1, state.EventTaskStarted, "A-1", ""),
		ev(5, state.EventTaskDone, "A-1", ""),
		ev(6, state.EventRunStopped, "", "completed"),
	}
	r, err := NewReport(HistoryInput{Events: events}, "demo", "")
	if err != nil || r.Time != nil || r.Tasks[0].Time != nil {
		t.Errorf("v0: time %+v, task %+v, err %v", r.Time, r.Tasks, err)
	}
}
