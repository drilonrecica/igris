package report

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// ErrNoRuns is NewReport's error for a log with no runs, or no log at all.
var ErrNoRuns = errors.New("no runs recorded yet")

// Report is the result of `igris report`: one run in detail (SPEC §14).
// Every value the log doesn't record is left empty (omitted in JSON), never
// a made-up 0.
type Report struct {
	Project   string           `json:"-"`             // the project directory's name, for the heading
	Run       string           `json:"run,omitempty"` // the run ID; "" for a v0 run
	StartedAt string           `json:"started_at"`
	EndedAt   string           `json:"ended_at,omitempty"`
	DurationS int              `json:"duration_s,omitempty"`
	End       string           `json:"end"` // as HistoryRun.End
	Phases    []string         `json:"phases"`
	Selection *state.Selection `json:"selection,omitempty"` // the slice of a sliced run (SPEC §5.5)
	Done      int              `json:"done"`
	Skipped   int              `json:"skipped"`
	// Unfinished counts the tasks without a result: unfinished or running.
	Unfinished int `json:"unfinished"`
	// NeedsYouS is how long igris waited on the owner; nil for a v0 run.
	NeedsYouS *int           `json:"needs_you_s,omitempty"`
	Commits   []ReportCommit `json:"commits"`
	Errors    []string       `json:"errors"`
	Tasks     []ReportTask   `json:"tasks"`
	Note      string         `json:"note,omitempty"` // as History.Note
}

// ReportCommit is one commit the run made.
type ReportCommit struct {
	Task    string `json:"task,omitempty"`
	SHA     string `json:"sha,omitempty"` // full SHA; "" for a v0 line
	Subject string `json:"subject,omitempty"`
}

// ReportTask is one task of the run, its attempts in the run merged.
type ReportTask struct {
	ID        string `json:"id"`
	Phase     string `json:"phase,omitempty"`
	Title     string `json:"title,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Rank      string `json:"rank,omitempty"`
	Model     string `json:"model,omitempty"`
	Result    string `json:"result"`
	DurationS int    `json:"duration_s,omitempty"`
	// Attempts counts the sessions the task had in the run: starts, resumes
	// and retries.
	Attempts int            `json:"attempts,omitempty"`
	Verify   []ReportVerify `json:"verify"`
	Commit   string         `json:"commit,omitempty"` // full SHA of the task's last commit
	Note     string         `json:"note,omitempty"`   // the done note or skip reason
	Sessions []string       `json:"sessions"`         // Claude session UUIDs, in order
	// Resume is `claude --resume <uuid>` for an agent task's last session.
	Resume    string `json:"resume,omitempty"`
	NeedsYouS *int   `json:"needs_you_s,omitempty"` // nil for a v0 run or a user task
}

// ReportVerify is one verify result.
type ReportVerify struct {
	Profile   string `json:"profile,omitempty"`
	Passed    bool   `json:"passed"`
	DurationS int    `json:"duration_s,omitempty"`
}

// ReportGroup is a phase's tasks of the run, for the report's sections.
type ReportGroup struct {
	Phase string // "" for tasks the log names no phase for (v0 lines)
	Tasks []ReportTask
}

// Groups splits the tasks by phase in log order; the tasks without a known
// phase come last.
func (r Report) Groups() []ReportGroup {
	var out []ReportGroup
	idx := map[string]int{}
	var none []ReportTask
	for _, t := range r.Tasks {
		if t.Phase == "" {
			none = append(none, t)
			continue
		}
		i, ok := idx[t.Phase]
		if !ok {
			i = len(out)
			idx[t.Phase] = i
			out = append(out, ReportGroup{Phase: t.Phase})
		}
		out[i].Tasks = append(out[i].Tasks, t)
	}
	if len(none) > 0 {
		out = append(out, ReportGroup{Tasks: none})
	}
	return out
}

// NewReport summarises the run sel names: "" or "1" for the newest, "2" for
// the one before, …, or a run ID. The caller checks sel's shape (a usage
// error); a run that isn't there is an error saying what to do.
func NewReport(in HistoryInput, project, sel string) (Report, error) {
	recs := readRuns(in.Events)
	if len(recs) == 0 {
		return Report{}, ErrNoRuns
	}
	i := len(recs) - 1
	switch n, err := strconv.Atoi(sel); {
	case sel == "":
	case err == nil:
		if n < 1 || n > len(recs) {
			return Report{}, fmt.Errorf("only %d %s recorded", len(recs), plural(len(recs), "run", "runs"))
		}
		i = len(recs) - n
	default:
		if i = slices.IndexFunc(recs, func(r *runRec) bool { return r.id == sel }); i < 0 {
			return Report{}, fmt.Errorf("no run %s in .igris/runs.jsonl; igris history lists them", textsafe.Line(sel))
		}
	}
	rep := recs[i].report(in.Live && i == len(recs)-1)
	rep.Project, rep.Note = textsafe.Line(project), newerNote(in.Events)
	return rep, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// shaPattern is the shape a commit SHA must have to be shown.
var shaPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// wait is one needs-you interval while the log is read.
type wait struct {
	task, reason string
	start, end   time.Time
}

// taskRec is a ReportTask while the log is read.
type taskRec struct {
	t        ReportTask
	open     bool // has a session or user turn without a result yet
	start    time.Time
	duration time.Duration
	timed    bool // some result had a known duration
}

// report folds the run's lines into a Report.
func (r *runRec) report(live bool) Report {
	sum := r.summary(live)
	out := Report{
		Run: r.id, StartedAt: sum.StartedAt, EndedAt: sum.EndedAt, DurationS: sum.DurationS,
		End: sum.End, Phases: sum.Phases, Selection: scopeSelection(r.startDetail()),
		Commits: []ReportCommit{}, Errors: append([]string{}, r.errors...), Tasks: []ReportTask{},
	}
	var tasks []*taskRec
	byID := map[string]*taskRec{}
	get := func(e state.Event) *taskRec {
		if t := byID[e.Task]; t != nil {
			return t
		}
		t := &taskRec{t: ReportTask{ID: e.Task, Verify: []ReportVerify{}, Sessions: []string{}}}
		byID[e.Task] = t
		tasks = append(tasks, t)
		return t
	}
	var waits []*wait
	closeWaits := func(at time.Time, match func(*wait) bool) {
		for _, w := range waits {
			if w.end.IsZero() && match(w) {
				w.end = at
			}
		}
	}
	for _, e := range r.events {
		e.Task = textsafe.Line(e.Task)
		switch e.Type {
		case state.EventTaskRetried, state.EventTaskDone, state.EventTaskSkipped, state.EventTaskReset:
			closeWaits(e.At, func(w *wait) bool { return w.task == e.Task })
		}
		switch e.Type {
		case state.EventRunStopped:
			if e.DurationMS > 0 {
				out.DurationS = int(e.DurationMS / 1000)
			}
		case state.EventTaskStarted, state.EventTaskResumed:
			t := get(e)
			t.describe(e)
			// As in history: a resume of a task open in this run continues it.
			if !t.open || e.Type == state.EventTaskStarted {
				t.t.Attempts++
				t.open, t.start = true, e.At
			}
			t.session(e.Session)
		case state.EventTaskRetried:
			t := get(e)
			t.describe(e)
			t.t.Attempts++
			t.session(e.Session)
		case state.EventTaskDone, state.EventTaskSkipped:
			t := get(e)
			t.describe(e)
			t.t.Result, t.t.Note = ResultDone, textsafe.Line(e.Detail)
			if e.Type == state.EventTaskSkipped {
				t.t.Result = ResultSkipped
			}
			switch {
			case e.DurationMS > 0:
				t.duration += time.Duration(e.DurationMS) * time.Millisecond
				t.timed = true
			case t.open && !t.start.IsZero() && !e.At.Before(t.start):
				t.duration += e.At.Sub(t.start)
				t.timed = true
			}
			t.open = false
		case state.EventTaskReset:
			if t := byID[e.Task]; t != nil && t.open {
				t.open, t.t.Result = false, ""
			}
		case state.EventVerifyPassed, state.EventVerifyFailed:
			if t := byID[e.Task]; t != nil {
				t.t.Verify = append(t.t.Verify, ReportVerify{
					Profile: verifyProfile(e), Passed: e.Type == state.EventVerifyPassed,
					DurationS: int(e.DurationMS / 1000),
				})
			}
		case state.EventCommitted:
			c := ReportCommit{Task: e.Task, Subject: textsafe.Line(e.Detail)}
			if shaPattern.MatchString(e.Commit) {
				c.SHA = e.Commit
			}
			out.Commits = append(out.Commits, c)
			if t := byID[e.Task]; t != nil && c.SHA != "" {
				t.t.Commit = c.SHA
			}
		case state.EventNeedsYou:
			waits = append(waits, &wait{task: e.Task, reason: e.Reason, start: e.At})
		case state.EventNeedsYouClear:
			for _, w := range waits {
				if w.end.IsZero() && w.task == e.Task && w.reason == e.Reason {
					w.end = e.At
					break
				}
			}
		}
	}
	// Waits still open end with the run: its stop, or its last line.
	end := r.end
	if !r.stopped && len(r.events) > 0 {
		end = r.events[len(r.events)-1].At
	}
	closeWaits(end, func(*wait) bool { return true })

	v1 := r.id != "" // only v1 lines carry a run ID; v0 runs log no waits
	if v1 {
		out.NeedsYouS = ptr(union(waits, func(*wait) bool { return true }))
	}
	running := sum.End == EndRunning
	for _, t := range tasks {
		rt := t.t
		if rt.Result == "" {
			rt.Result = ResultOpen
			if running {
				rt.Result = ResultRunning
			}
		}
		if t.timed {
			rt.DurationS = int(t.duration / time.Second)
		}
		agent := rt.Owner != "user"
		if n := len(rt.Sessions); n > 0 && agent {
			rt.Resume = "claude --resume " + rt.Sessions[n-1]
		}
		if v1 && agent {
			id := rt.ID
			rt.NeedsYouS = ptr(union(waits, func(w *wait) bool { return w.task == id }))
		}
		switch rt.Result {
		case ResultDone:
			out.Done++
		case ResultSkipped:
			out.Skipped++
		default:
			out.Unfinished++
		}
		out.Tasks = append(out.Tasks, rt)
	}
	return out
}

// describe takes the task's details from e where it has them.
func (t *taskRec) describe(e state.Event) {
	set := func(dst *string, v string) {
		if v = textsafe.Line(v); v != "" {
			*dst = v
		}
	}
	set(&t.t.Phase, e.Phase)
	set(&t.t.Title, e.Title)
	set(&t.t.Owner, e.Owner)
	set(&t.t.Rank, e.Rank)
	set(&t.t.Model, e.Model)
}

// session records a Claude session UUID (already shape-checked when read).
func (t *taskRec) session(id string) {
	if id != "" && !slices.Contains(t.t.Sessions, id) {
		t.t.Sessions = append(t.t.Sessions, id)
	}
}

// verifyProfile is the verify line's profile, from the v1 field or the
// detail's "profile <name>" (v0).
func verifyProfile(e state.Event) string {
	if p := textsafe.Line(e.Profile); p != "" {
		return p
	}
	rest, ok := strings.CutPrefix(e.Detail, "profile ")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, ":")
	return textsafe.Line(strings.TrimSpace(name))
}

// startDetail is the run_started detail.
func (r *runRec) startDetail() string {
	for _, e := range r.events {
		if e.Type == state.EventRunStarted {
			return e.Detail
		}
	}
	return ""
}

// scopeSelection reads the selection out of the run_started detail
// ("phase M1; only M1-03, M1-05", "phase M1; from M1-03 until M1-07");
// nil for a whole run.
func scopeSelection(detail string) *state.Selection {
	_, rest, ok := strings.Cut(detail, "; ")
	if !ok {
		return nil
	}
	var sel state.Selection
	if ids, ok := strings.CutPrefix(rest, "only "); ok {
		for _, id := range strings.Split(ids, ", ") {
			if id = textsafe.Line(strings.TrimSpace(id)); id != "" {
				sel.Only = append(sel.Only, id)
			}
		}
	} else {
		f := strings.Fields(textsafe.Line(rest))
		for i := 0; i+1 < len(f); i += 2 {
			switch f[i] {
			case "from":
				sel.From = f[i+1]
			case "until":
				sel.Until = f[i+1]
			}
		}
	}
	if sel.Empty() {
		return nil
	}
	return &sel
}

// union is the whole seconds covered by the matching waits, overlaps
// counted once.
func union(waits []*wait, match func(*wait) bool) int {
	var spans [][2]time.Time
	for _, w := range waits {
		if match(w) && !w.end.IsZero() && w.end.After(w.start) {
			spans = append(spans, [2]time.Time{w.start, w.end})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0].Before(spans[j][0]) })
	var total time.Duration
	var cur [2]time.Time
	for i, s := range spans {
		switch {
		case i == 0:
			cur = s
		case s[0].After(cur[1]):
			total += cur[1].Sub(cur[0])
			cur = s
		case s[1].After(cur[1]):
			cur[1] = s[1]
		}
	}
	if len(spans) > 0 {
		total += cur[1].Sub(cur[0])
	}
	return int(total / time.Second)
}

func ptr(n int) *int { return &n }
