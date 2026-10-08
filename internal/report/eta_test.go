package report

import (
	"reflect"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

func TestRankDurations(t *testing.T) {
	const id = "20261001-090000-3fa2"
	v1 := func(min int, typ state.EventType, task, rank string, ms int64) state.Event {
		return state.Event{V: 1, At: t0.Add(time.Duration(min) * time.Minute), Type: typ, Run: id, Task: task, Rank: rank, Model: rank, DurationMS: ms, Owner: "agent"}
	}
	events := []state.Event{
		// v0: timestamps
		ev(0, state.EventRunStarted, "", "phase A"),
		ev(1, state.EventTaskStarted, "A-1", ""),
		ev(5, state.EventTaskDone, "A-1", ""), // 4m
		ev(6, state.EventTaskStarted, "A-2", "user task"),
		ev(9, state.EventTaskDone, "A-2", ""), // a user task: not counted
		ev(10, state.EventTaskStarted, "A-3", ""),
		ev(11, state.EventTaskSkipped, "A-3", ""), // skipped: not counted
		ev(12, state.EventTaskStarted, "A-4", ""),
		ev(13, state.EventRunStopped, "", "stopped"),
		// v1: duration_ms wins; a resumed task's partial time never counts
		v1(20, state.EventRunStarted, "", "", 0),
		v1(21, state.EventTaskResumed, "A-4", "sonnet", 0),
		v1(25, state.EventTaskDone, "A-4", "sonnet", 240000),
		v1(26, state.EventTaskStarted, "A-5", "sonnet", 0),
		v1(27, state.EventTaskRetried, "A-5", "sonnet", 0),
		v1(30, state.EventTaskDone, "A-5", "sonnet", 7*60000),
		v1(31, state.EventTaskStarted, "A-6", "opus", 0),
		v1(32, state.EventTaskReset, "A-6", "opus", 0),
		v1(40, state.EventTaskDone, "A-6", "opus", 60000), // reset ended the attempt
	}
	got := RankDurations(events)
	want := map[string][]time.Duration{"sonnet": {4 * time.Minute, 7 * time.Minute}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RankDurations = %v, want %v", got, want)
	}

	// Only the most recent MaxRankDurations per rank.
	var many []state.Event
	many = append(many, ev(0, state.EventRunStarted, "", "phase A"))
	for i := range MaxRankDurations + 5 {
		s := ev(2*i, state.EventTaskStarted, "A-1", "")
		d := ev(2*i, state.EventTaskDone, "A-1", "")
		d.DurationMS = int64(i+1) * 1000
		many = append(many, s, d)
	}
	ds := RankDurations(many)["sonnet"]
	if len(ds) != MaxRankDurations || ds[0] != 6*time.Second || ds[len(ds)-1] != (MaxRankDurations+5)*time.Second {
		t.Errorf("kept %v", ds)
	}
}
