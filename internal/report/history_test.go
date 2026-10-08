package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/state"
)

func ev(min int, typ state.EventType, task, detail string) state.Event {
	return state.Event{At: t0.Add(time.Duration(min) * time.Minute), Type: typ, Task: task, Rank: "sonnet", Model: "sonnet", Detail: detail}
}

func TestHistoryRunBoundaries(t *testing.T) {
	events := []state.Event{
		ev(0, state.EventTaskDone, "X-0", ""), // before any run: dropped
		ev(1, state.EventRunStarted, "", "phase A"),
		ev(2, state.EventTaskStarted, "A-1", ""),
		ev(3, state.EventVerifyFailed, "A-1", "attempt 1 of 3: no"),
		ev(5, state.EventTaskDone, "A-1", "ok"),
		ev(6, state.EventTaskStarted, "A-2", ""),
		// no stop: the next start closes this run as interrupted
		ev(10, state.EventRunStarted, "", "phase A, B"),
		ev(11, state.EventTaskResumed, "A-2", ""),
		ev(12, state.EventTaskResumed, "A-2", ""), // already open: same attempt
		ev(13, state.EventTaskSkipped, "A-2", "n/a"),
		ev(14, state.EventRunStopped, "", "completed"),
		ev(20, state.EventRunStarted, "", "phase C"),
		ev(21, state.EventTaskStarted, "C-1", ""),
	}
	h := NewHistory(HistoryInput{Events: events, Live: true})
	if len(h.Runs) != 3 {
		t.Fatalf("runs = %d", len(h.Runs))
	}
	last, mid, first := h.Runs[0], h.Runs[1], h.Runs[2]
	if last.End != EndRunning || last.Tasks[0].Result != ResultRunning {
		t.Errorf("last = %+v", last)
	}
	if mid.End != "completed" || mid.Skipped != 1 || mid.Tasks[0].Attempts != 1 || mid.Tasks[0].DurationS != 120 || len(mid.Phases) != 2 {
		t.Errorf("mid = %+v", mid)
	}
	if first.End != EndInterrupted || first.Done != 1 || first.Tasks[0].VerifyFailed != 1 || first.Tasks[0].DurationS != 180 || first.Tasks[1].Result != ResultOpen {
		t.Errorf("first = %+v", first)
	}
	if got := NewHistory(HistoryInput{Events: events, N: 2}); len(got.Runs) != 2 {
		t.Errorf("-n 2 gave %d runs", len(got.Runs))
	}
	got := NewHistory(HistoryInput{Events: events})
	if got.Runs[0].End != EndInterrupted || got.Runs[0].Tasks[0].Result != ResultOpen {
		t.Errorf("not live: %+v", got.Runs[0])
	}
}

func TestTaskHistoryRunningVersusInterrupted(t *testing.T) {
	events := []state.Event{
		ev(1, state.EventRunStarted, "", "phase A"),
		ev(2, state.EventTaskStarted, "A-1", ""), // interrupted: the next run started
		ev(10, state.EventRunStarted, "", "phase A"),
		ev(11, state.EventTaskResumed, "A-1", ""),
	}
	for _, c := range []struct {
		name        string
		live        bool
		last, older string
	}{
		{"live last run", true, ResultRunning, ResultOpen},
		{"interrupted last run", false, ResultOpen, ResultOpen},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := NewTaskHistory(HistoryInput{Events: events, Live: c.live}, "A-1")
			if len(h.Attempts) != 2 || h.Attempts[0].Result != c.last || h.Attempts[1].Result != c.older {
				t.Errorf("attempts = %+v", h.Attempts)
			}
		})
	}
}

func TestTaskHistoryAcrossRuns(t *testing.T) {
	events := []state.Event{
		ev(1, state.EventRunStarted, "", "phase A"),
		ev(2, state.EventTaskStarted, "A-1", ""),
		ev(3, state.EventVerifyFailed, "A-1", "attempt 1 of 2: bad\x1b[2J"),
		ev(4, state.EventRunStopped, "", "stopped"),
		ev(10, state.EventRunStarted, "", "phase A"),
		ev(11, state.EventTaskResumed, "A-1", ""),
		ev(12, state.EventVerifyPassed, "A-1", ""),
		ev(13, state.EventTaskDone, "A-1", "fine"),
	}
	h := NewTaskHistory(HistoryInput{Events: events, N: 1}, "A-1") // -n doesn't limit attempts
	if len(h.Attempts) != 2 {
		t.Fatalf("attempts = %+v", h.Attempts)
	}
	if a := h.Attempts[0]; a.Result != ResultDone || a.Note != "fine" || a.DurationS != 120 || len(a.Verify) != 1 {
		t.Errorf("newest = %+v", a)
	}
	if a := h.Attempts[1]; a.Result != ResultOpen || strings.Contains(a.Verify[0], "\x1b") {
		t.Errorf("oldest = %+v", a)
	}
	if got := NewTaskHistory(HistoryInput{Events: events}, "Z-1"); got.Attempts == nil || len(got.Attempts) != 0 {
		t.Errorf("unknown task = %#v", got.Attempts)
	}
}

func TestHistoryJSONNeverNull(t *testing.T) {
	out, _ := json.Marshal(NewHistory(HistoryInput{}))
	if string(out) != `{"runs":[]}` {
		t.Errorf("empty = %s", out)
	}
	h := NewHistory(HistoryInput{Events: []state.Event{ev(0, state.EventRunStarted, "", "")}})
	out, _ = json.Marshal(h)
	if !strings.Contains(string(out), `"phases":[]`) || !strings.Contains(string(out), `"commits":[]`) || !strings.Contains(string(out), `"tasks":[]`) {
		t.Errorf("json = %s", out)
	}
}

// `igris reset` ends the task's attempt in the live run: it is unfinished,
// not running, and the next start is a new attempt.
func TestHistoryResetEndsTheAttempt(t *testing.T) {
	events := []state.Event{
		ev(1, state.EventRunStarted, "", "phase A"),
		ev(2, state.EventTaskStarted, "A-1", ""),
		ev(4, state.EventTaskReset, "A-1", "in progress"),
		ev(6, state.EventTaskStarted, "A-1", ""),
	}
	h := NewTaskHistory(HistoryInput{Events: events, Live: true}, "A-1")
	if len(h.Attempts) != 2 || h.Attempts[1].Result != ResultOpen || h.Attempts[1].DurationS != 120 || h.Attempts[0].Result != ResultRunning {
		t.Errorf("attempts = %+v", h.Attempts)
	}
}
