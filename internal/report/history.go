package report

import (
	"fmt"
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

// HistoryRun is one igris run: the lines with its run ID, or for v0 lines
// run_started up to its run_stopped, or up to the next run_started when it
// has none (SPEC §13).
type HistoryRun struct {
	Run       string    `json:"run,omitempty"` // the run ID; "" for a v0 run
	StartedAt string    `json:"started_at"`    // RFC 3339, UTC
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
	// Note says the log has lines from a newer igris, read best effort.
	Note string `json:"note,omitempty"`
}

// Attempt is one try at a task: task_started (or task_resumed) up to its
// task_done or task_skipped.
type Attempt struct {
	Run          string   `json:"run,omitempty"` // the run ID; "" for a v0 run
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
	Note     string    `json:"note,omitempty"` // as History.Note
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
	id         string
	start, end time.Time
	phases     []string
	stopped    bool
	detail     string
	attempts   []*attempt
	open       map[string]*attempt // the attempts without a result yet, by task
	commits    []string
	errors     []string
	events     []state.Event // the run's lines, in log order, for NewReport
}

// NewHistory summarises the last in.N runs of the log.
func NewHistory(in HistoryInput) History {
	n := in.N
	if n <= 0 {
		n = DefaultHistoryRuns
	}
	recs := readRuns(in.Events)
	h := History{Runs: []HistoryRun{}, Note: newerNote(in.Events)}
	for i := len(recs) - 1; i >= 0 && len(h.Runs) < n; i-- {
		h.Runs = append(h.Runs, recs[i].summary(in.Live && i == len(recs)-1))
	}
	return h
}

// NewTaskHistory lists every attempt of task across the whole log.
func NewTaskHistory(in HistoryInput, task string) TaskHistory {
	h := TaskHistory{Task: textsafe.Line(task), Attempts: []Attempt{}, Note: newerNote(in.Events)}
	recs := readRuns(in.Events)
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		running := r.running(in.Live && i == len(recs)-1)
		for j := len(r.attempts) - 1; j >= 0; j-- {
			if a := r.attempts[j]; a.id == task {
				h.Attempts = append(h.Attempts, a.export(r, running))
			}
		}
	}
	return h
}

// newerNote is the note for a log with lines from a newer igris (a larger
// "v"), "" when there are none.
func newerNote(events []state.Event) string {
	v := 0
	for _, e := range events {
		v = max(v, e.V)
	}
	if v <= state.LogVersion {
		return ""
	}
	return fmt.Sprintf("runs.jsonl has lines from a newer igris (v%d); some details may be missing", v)
}

// readRuns groups events into runs and runs' events into attempts (SPEC
// §13). Lines with a run ID belong to that run. Lines without one (v0) go
// to the run opened by the last run_started without an ID; a run_started
// while such a run is open closes it without a stop. Other lines without
// an ID (before any run, or written outside a run) are dropped. Unknown
// event types and fields are ignored, so a newer log still reads.
func readRuns(events []state.Event) []*runRec {
	var runs []*runRec
	byID := map[string]*runRec{}
	var cur *runRec // the open v0 run
	for _, e := range events {
		var r *runRec
		switch {
		case e.Run != "":
			if e.Type == state.EventRunStarted {
				cur = nil
			}
			if r = byID[e.Run]; r == nil {
				r = &runRec{id: e.Run, start: e.At, open: map[string]*attempt{}}
				byID[e.Run] = r
				runs = append(runs, r)
			}
		case e.Type == state.EventRunStarted:
			cur = &runRec{start: e.At, open: map[string]*attempt{}}
			runs = append(runs, cur)
			r = cur
		default:
			r = cur
		}
		if r == nil {
			continue
		}
		r.add(e)
		r.events = append(r.events, e)
		if e.Type == state.EventRunStopped && r == cur {
			cur = nil
		}
	}
	return runs
}

// add folds one event into the run.
func (r *runRec) add(e state.Event) {
	finish := func(a *attempt, result string) {
		if a == nil {
			a = &attempt{id: e.Task, rank: textsafe.Line(e.Rank), model: textsafe.Line(e.Model)}
			r.attempts = append(r.attempts, a)
		}
		a.end, a.result, a.note = e.At, result, textsafe.Line(e.Detail)
		delete(r.open, a.id)
	}
	e.Task = textsafe.Line(e.Task)
	switch e.Type {
	case state.EventRunStarted:
		r.start, r.phases = e.At, scopePhases(e.Detail)
	case state.EventRunStopped:
		r.end, r.stopped, r.detail = e.At, true, textsafe.Line(e.Detail)
	case state.EventTaskStarted, state.EventTaskResumed:
		// A resume continues the attempt the task had open in this run;
		// across runs it is a new one.
		if a := r.open[e.Task]; a != nil && e.Type == state.EventTaskResumed {
			return
		}
		a := &attempt{id: e.Task, rank: textsafe.Line(e.Rank), model: textsafe.Line(e.Model), start: e.At}
		r.attempts = append(r.attempts, a)
		r.open[e.Task] = a
	case state.EventTaskDone:
		finish(r.open[e.Task], ResultDone)
	case state.EventTaskSkipped:
		finish(r.open[e.Task], ResultSkipped)
	case state.EventTaskReset:
		// `igris reset` ended the attempt without a result.
		if a := r.open[e.Task]; a != nil {
			a.end, a.result = e.At, ResultOpen
			delete(r.open, a.id)
		}
	case state.EventVerifyPassed, state.EventVerifyFailed:
		if a := r.open[e.Task]; a != nil {
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
		r.commits = append(r.commits, textsafe.Line(e.Detail))
	case state.EventError:
		r.errors = append(r.errors, textsafe.Line(e.Detail))
	}
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
		Run: r.id, StartedAt: stamp(r.start), EndedAt: stamp(r.end), DurationS: seconds(r.start, r.end),
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

func (a *attempt) export(r *runRec, running bool) Attempt {
	return Attempt{
		Run: r.id, RunStartedAt: stamp(r.start), StartedAt: stamp(a.start), EndedAt: stamp(a.end),
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
