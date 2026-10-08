package report

import (
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// DefaultHistoryRuns is how many runs `igris history` shows without -n.
const DefaultHistoryRuns = 10

// How a run or a task ended, besides the engine's own outcomes
// (completed, stuck, stopped, error), which are the run_stopped detail.
const (
	EndInterrupted = "interrupted" // no run_stopped: igris died or was killed
	EndRunning     = "running"     // no run_stopped and igris is still alive
	ResultDone     = "done"
	ResultSkipped  = "skipped"
	ResultOpen     = "unfinished" // the run ended before the task did
	ResultRunning  = "running"    // the run is still going and the task has no result yet
)

// HistoryInput is what NewHistory and NewTaskHistory read.
type HistoryInput struct {
	Events []state.Event // from state.PeekEvents, oldest first
	N      int           // runs to show; 0 or less means DefaultHistoryRuns
	// Live says the lock is held by a live igris on this host, so a last run
	// without a stop event is still running rather than interrupted.
	Live bool
}

// TaskRun is one task within a run: its attempts merged.
type TaskRun struct {
	ID           string `json:"id"`
	Rank         string `json:"rank,omitempty"`
	Model        string `json:"model,omitempty"`
	Result       string `json:"result"`
	Attempts     int    `json:"attempts"`
	DurationS    int    `json:"duration_s,omitempty"` // 0 when the log doesn't say
	VerifyFailed int    `json:"verify_failed"`
	VerifyPassed int    `json:"verify_passed"`
}

// HistoryRun is one igris run: run_started up to its run_stopped, or up to
// the next run_started when it has none.
type HistoryRun struct {
	StartedAt string    `json:"started_at"` // RFC 3339, UTC
	EndedAt   string    `json:"ended_at,omitempty"`
	DurationS int       `json:"duration_s,omitempty"`
	Phases    []string  `json:"phases"`
	End       string    `json:"end"` // the run_stopped detail, or EndInterrupted / EndRunning
	Done      int       `json:"done"`
	Skipped   int       `json:"skipped"`
	Tasks     []TaskRun `json:"tasks"`
	Commits   []string  `json:"commits"`
	Errors    []string  `json:"errors"`
}

// History is the result of `igris history`: the last runs, newest first.
type History struct {
	Runs []HistoryRun `json:"runs"`
}

// Attempt is one try at a task: task_started (or task_resumed) up to its
// task_done or task_skipped.
type Attempt struct {
	RunStartedAt string   `json:"run_started_at"`
	StartedAt    string   `json:"started_at,omitempty"`
	EndedAt      string   `json:"ended_at,omitempty"`
	DurationS    int      `json:"duration_s,omitempty"`
	Rank         string   `json:"rank,omitempty"`
	Model        string   `json:"model,omitempty"`
	Result       string   `json:"result"`
	Note         string   `json:"note,omitempty"` // the done note or skip reason
	Verify       []string `json:"verify"`         // one line per verify_failed / verify_passed
}

// TaskHistory is the result of `igris history TASK-ID`: every attempt of the
// task across all runs, newest first.
type TaskHistory struct {
	Task     string    `json:"task"`
	Attempts []Attempt `json:"attempts"`
}

// attempt is an Attempt while the log is read.
type attempt struct {
	id, rank, model, result, note string
	start, end                    time.Time
	verify                        []string
	failed, passed                int
}

// runRec is a HistoryRun while the log is read.
type runRec struct {
	start, end time.Time
	phases     []string
	stopped    bool
	detail     string
	attempts   []*attempt
	commits    []string
	errors     []string
}

// NewHistory summarises the last in.N runs of the log.
func NewHistory(in HistoryInput) History {
	n := in.N
	if n <= 0 {
		n = DefaultHistoryRuns
	}
	recs := readRuns(in.Events)
	h := History{Runs: []HistoryRun{}}
	for i := len(recs) - 1; i >= 0 && len(h.Runs) < n; i-- {
		h.Runs = append(h.Runs, recs[i].summary(in.Live && i == len(recs)-1))
	}
	return h
}

// NewTaskHistory lists every attempt of task across the whole log.
func NewTaskHistory(in HistoryInput, task string) TaskHistory {
	h := TaskHistory{Task: textsafe.Line(task), Attempts: []Attempt{}}
	recs := readRuns(in.Events)
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		running := r.running(in.Live && i == len(recs)-1)
		for j := len(r.attempts) - 1; j >= 0; j-- {
			if a := r.attempts[j]; a.id == task {
				h.Attempts = append(h.Attempts, a.export(r.start, running))
			}
		}
	}
	return h
}

// readRuns groups events into runs and runs' events into attempts. Events
// before the first run_started are dropped; a run_started while a run is
// open closes that run without a stop. Unknown event types are ignored, so
// a newer log still reads.
func readRuns(events []state.Event) []*runRec {
	var runs []*runRec
	var cur *runRec
	open := map[string]*attempt{}
	finish := func(a *attempt, e state.Event, result string) {
		if a == nil {
			a = &attempt{id: e.Task, rank: textsafe.Line(e.Rank), model: textsafe.Line(e.Model)}
			cur.attempts = append(cur.attempts, a)
		}
		a.end, a.result, a.note = e.At, result, textsafe.Line(e.Detail)
		delete(open, a.id)
	}
	for _, e := range events {
		if e.Type == state.EventRunStarted {
			cur = &runRec{start: e.At, phases: scopePhases(e.Detail)}
			runs = append(runs, cur)
			open = map[string]*attempt{}
			continue
		}
		if cur == nil {
			continue
		}
		e.Task = textsafe.Line(e.Task)
		switch e.Type {
		case state.EventRunStopped:
			cur.end, cur.stopped, cur.detail = e.At, true, textsafe.Line(e.Detail)
			cur = nil
		case state.EventTaskStarted, state.EventTaskResumed:
			// A resume continues the attempt the task had open in this
			// run; across runs it is a new one.
			if a := open[e.Task]; a != nil && e.Type == state.EventTaskResumed {
				continue
			}
			a := &attempt{id: e.Task, rank: textsafe.Line(e.Rank), model: textsafe.Line(e.Model), start: e.At}
			cur.attempts = append(cur.attempts, a)
			open[e.Task] = a
		case state.EventTaskDone:
			finish(open[e.Task], e, ResultDone)
		case state.EventTaskSkipped:
			finish(open[e.Task], e, ResultSkipped)
		case state.EventTaskReset:
			// `igris reset` ended the attempt without a result.
			if a := open[e.Task]; a != nil {
				a.end, a.result = e.At, ResultOpen
				delete(open, a.id)
			}
		case state.EventVerifyPassed, state.EventVerifyFailed:
			if a := open[e.Task]; a != nil {
				line := "passed"
				if e.Type == state.EventVerifyFailed {
					a.failed++
					line = "failed"
				} else {
					a.passed++
				}
				if d := textsafe.Line(e.Detail); d != "" {
					line += ": " + d
				}
				a.verify = append(a.verify, line)
			}
		case state.EventCommitted:
			cur.commits = append(cur.commits, textsafe.Line(e.Detail))
		case state.EventError:
			cur.errors = append(cur.errors, textsafe.Line(e.Detail))
		}
	}
	return runs
}

// scopePhases reads the phase IDs out of the run_started detail
// ("phase A, B", or "phase M1; only M1-03, M1-05" for a slice).
func scopePhases(detail string) []string {
	out := []string{}
	detail, _, _ = strings.Cut(detail, "; ")
	for _, p := range strings.Split(strings.TrimPrefix(detail, "phase "), ", ") {
		if p = textsafe.Line(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// running says the run is the live one: no stop event and igris still alive.
func (r *runRec) running(live bool) bool { return live && !r.stopped }

func (r *runRec) summary(live bool) HistoryRun {
	out := HistoryRun{
		StartedAt: stamp(r.start), EndedAt: stamp(r.end), DurationS: seconds(r.start, r.end),
		Phases: r.phases, End: r.detail,
		Tasks: []TaskRun{}, Commits: append([]string{}, r.commits...), Errors: append([]string{}, r.errors...),
	}
	switch {
	case r.stopped && out.End == "":
		out.End = "stopped"
	case r.running(live):
		out.End = EndRunning
	case !r.stopped:
		out.End = EndInterrupted
	}
	byID := map[string]int{}
	for _, a := range r.attempts {
		i, seen := byID[a.id]
		if !seen {
			i = len(out.Tasks)
			byID[a.id] = i
			out.Tasks = append(out.Tasks, TaskRun{ID: a.id, Rank: a.rank, Model: a.model})
		}
		t := &out.Tasks[i]
		t.Attempts++
		t.Result = a.resultOr(out.End == EndRunning)
		t.DurationS += seconds(a.start, a.end)
		t.VerifyFailed += a.failed
		t.VerifyPassed += a.passed
	}
	for _, t := range out.Tasks {
		switch t.Result {
		case ResultDone:
			out.Done++
		case ResultSkipped:
			out.Skipped++
		}
	}
	return out
}

// resultOr is the attempt's result, or what an attempt without one means:
// running while its run is live, unfinished once the run is over.
func (a *attempt) resultOr(running bool) string {
	switch {
	case a.result != "":
		return a.result
	case running:
		return ResultRunning
	}
	return ResultOpen
}

func (a *attempt) export(runStart time.Time, running bool) Attempt {
	return Attempt{
		RunStartedAt: stamp(runStart), StartedAt: stamp(a.start), EndedAt: stamp(a.end),
		DurationS: seconds(a.start, a.end), Rank: a.rank, Model: a.model,
		Result: a.resultOr(running), Note: a.note, Verify: append([]string{}, a.verify...),
	}
}

// seconds is the whole seconds from start to end, 0 when either is unknown
// or the clock went backwards.
func seconds(start, end time.Time) int {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return int(end.Sub(start) / time.Second)
}
