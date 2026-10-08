package report

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

func TestNewReportSelectsRun(t *testing.T) {
	const id = "20261001-090000-3fa2"
	events := []state.Event{
		ev(0, state.EventRunStarted, "", "phase A"),
		ev(1, state.EventRunStopped, "", "completed"),
		{V: 1, At: t0.Add(10 * time.Minute), Type: state.EventRunStarted, Run: id, Detail: "phase B"},
		{V: 1, At: t0.Add(11 * time.Minute), Type: state.EventTaskStarted, Run: id, Task: "B-1", Owner: "agent"},
	}
	for _, tc := range []struct {
		sel, want, err string
		live           bool
		result         string
	}{
		{sel: "", want: id, result: ResultOpen},
		{sel: "1", want: id, live: true, result: ResultRunning},
		{sel: "2", want: ""},
		{sel: id, want: id, result: ResultOpen},
		{sel: "3", err: "only 2 runs recorded"},
		{sel: "20991231-000000-0000", err: "no run 20991231-000000-0000 in .igris/runs.jsonl; igris history lists them"},
	} {
		r, err := NewReport(HistoryInput{Events: events, Live: tc.live}, "demo", tc.sel)
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%q: err %v, want %q", tc.sel, err, tc.err)
			}
			continue
		}
		if err != nil || r.Run != tc.want || r.Project != "demo" {
			t.Errorf("%q: run %q, err %v", tc.sel, r.Run, err)
			continue
		}
		if tc.result != "" && (len(r.Tasks) != 1 || r.Tasks[0].Result != tc.result) {
			t.Errorf("%q: tasks %+v, want result %s", tc.sel, r.Tasks, tc.result)
		}
	}
	if _, err := NewReport(HistoryInput{}, "demo", ""); !errors.Is(err, ErrNoRuns) {
		t.Errorf("no runs: %v", err)
	}
	if _, err := NewReport(HistoryInput{Events: events[:2]}, "demo", "2"); err == nil || err.Error() != "only 1 run recorded" {
		t.Errorf("one run: %v", err)
	}
}

func TestReportNeedsYouUnion(t *testing.T) {
	const id = "20261001-090000-3fa2"
	at := func(min int, typ state.EventType, task, reason string) state.Event {
		return state.Event{V: 1, At: t0.Add(time.Duration(min) * time.Minute), Type: typ, Run: id, Task: task, Reason: reason, Owner: "agent"}
	}
	events := []state.Event{
		at(0, state.EventRunStarted, "", ""),
		at(1, state.EventTaskStarted, "A-1", ""),
		at(2, state.EventNeedsYou, "A-1", state.ReasonIdle),
		at(3, state.EventNeedsYou, "A-1", state.ReasonTaskOverdue), // overlaps: counted once
		at(4, state.EventNeedsYouClear, "A-1", state.ReasonIdle),
		at(5, state.EventNeedsYouClear, "A-1", state.ReasonBlocked), // not open: ignored
		at(6, state.EventTaskRetried, "A-1", ""),                    // ends the overdue wait
		at(7, state.EventNeedsYou, "", state.ReasonPlanChanged),     // run-wide
		at(8, state.EventTaskDone, "A-1", ""),                       // not this wait's task
		at(9, state.EventNeedsYouClear, "", state.ReasonPlanChanged),
		at(10, state.EventNeedsYou, "A-2", state.ReasonCommit), // A-2 never started: run-only
		at(12, state.EventRunStopped, "", ""),
	}
	r, err := NewReport(HistoryInput{Events: events}, "demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.NeedsYouS == nil || *r.NeedsYouS != (4+2+2)*60 {
		t.Errorf("run needs you = %v, want 480", r.NeedsYouS)
	}
	if len(r.Tasks) != 1 || r.Tasks[0].NeedsYouS == nil || *r.Tasks[0].NeedsYouS != 4*60 || r.Tasks[0].Attempts != 2 {
		t.Errorf("tasks = %+v", r.Tasks)
	}
}

func TestScopeSelection(t *testing.T) {
	for _, tc := range []struct {
		detail string
		want   *state.Selection
	}{
		{"phase A", nil},
		{"phase A, B; only A-1, B-2", &state.Selection{Only: []string{"A-1", "B-2"}}},
		{"phase A; from A-3 until A-7", &state.Selection{From: "A-3", Until: "A-7"}},
		{"phase A; until A-7", &state.Selection{Until: "A-7"}},
		{"phase A; nonsense", nil},
	} {
		if got := scopeSelection(tc.detail); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %+v, want %+v", tc.detail, got, tc.want)
		}
	}
}

func TestVerifyProfile(t *testing.T) {
	for _, tc := range []struct {
		e    state.Event
		want string
	}{
		{state.Event{Profile: "fast", Detail: "profile slow"}, "fast"},
		{state.Event{Detail: "profile fast: attempt 1 of 3: exit status 2"}, "fast"},
		{state.Event{Detail: "profile full"}, "full"},
		{state.Event{Detail: "attempt 1 of 3: no"}, ""},
	} {
		if got := verifyProfile(tc.e); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.e, got, tc.want)
		}
	}
}
