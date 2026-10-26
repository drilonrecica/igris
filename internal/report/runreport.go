package report

import (
	"errors"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/price"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/usage"
)

// ErrNoRuns is NewReport's error for a log with no runs, or no log at all.
var ErrNoRuns = errors.New("no runs recorded yet")

// Report is the result of `igris report`: one run in detail (SPEC §14).
// Every value the log doesn't record is left empty (omitted in JSON), never
// a made-up 0.
type Report struct {
	V         int              `json:"v"`
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
	// Time is the run's agent tasks' time by what it was spent on, for the
	// run, each phase and each rank; nil for a v0 run or a run without an
	// agent task (SPEC §14). Since 1.8.
	Time *TimeBreakdown `json:"time,omitempty"`
	// Estimate is the price estimate of the tasks' tokens (SPEC §6.9); nil
	// when no attempt's usage was read. Since 1.8.
	Estimate *Estimate `json:"estimate,omitempty"`
	Note     string    `json:"note,omitempty"` // as History.Note
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
	// Audit is the rank audit over the task's attempts (SPEC §6.9): its
	// worst result, mismatch before unavailable, unchecked and ok; "" when
	// the log has none. Since 1.1.
	Audit string `json:"audit,omitempty"`
	// Usage is the tokens of the task's attempts whose usage was read,
	// subagents included, Sub their part of them, Models the main
	// thread's model ids; nil/empty when no attempt's usage was read
	// (SPEC §6.9). Since 1.1.
	Usage  *Tokens  `json:"usage,omitempty"`
	Sub    *Tokens  `json:"sub,omitempty"`
	Models []string `json:"models,omitempty"`
	// Time is the task's time by what it was spent on; nil for a v0 run
	// or a user task (SPEC §14). Since 1.8.
	Time *Times `json:"time,omitempty"`
	// Estimate is the price estimate of Usage; nil without it. Since 1.8.
	Estimate *Estimate `json:"estimate,omitempty"`
}

// RankMismatch reports that some attempt of the task ran on a model its
// rank doesn't resolve to: the report marks it.
func (t ReportTask) RankMismatch() bool { return t.Audit == state.AuditMismatch }

// tokens counts a usage line's tokens into the task; a line without any
// (usage unavailable) adds nothing.
func (t *taskRec) tokens(e state.Event, prices price.Table) {
	if e.Usage == nil {
		return
	}
	var sub usage.Counts
	if e.Sub != nil {
		sub = *e.Sub
	}
	if t.t.Usage == nil {
		t.t.Usage = &Tokens{}
	}
	t.t.Usage.add(*e.Usage)
	t.t.Usage.add(sub)
	t.cost.Add(attemptCost(prices, *e.Usage, sub, e.Models, e.SubModels))
	if !sub.IsZero() {
		if t.t.Sub == nil {
			t.t.Sub = &Tokens{}
		}
		t.t.Sub.add(sub)
	}
	t.t.Models = mergeModels(t.t.Models, e.Models)
}

// auditOrder ranks the audit results, the one to show last.
var auditOrder = []string{state.AuditOK, state.AuditUnchecked, state.AuditUnavailable, state.AuditMismatch}

// audit keeps the worse of the task's audit so far and a, a usage line's
// audit (already checked when read).
func (t *taskRec) audit(a string) {
	if slices.Index(auditOrder, a) > slices.Index(auditOrder, t.t.Audit) {
		t.t.Audit = a
	}
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
	r, err := pickRun(recs, sel)
	if err != nil {
		return Report{}, err
	}
	rep := r.report(in.Live && r == recs[len(recs)-1], in.Prices)
	rep.Project, rep.Note = textsafe.Line(project), logNote(in)
	return rep, nil
}

// isIndex says s is a run index: digits only, never a run ID.
func isIndex(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
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
	timed    bool      // some result had a known duration
	cost     price.Sum // the estimate of the task's tokens
	// latest is the Claude session of the task's latest session line
	// (started, resumed, retried); "" when that line has none or an
	// invalid one, so Resume never points at an older conversation.
	latest string
}

// report folds the run's lines into a Report.
func (r *runRec) report(live bool, prices price.Table) Report {
	sum := r.summary(live)
	out := Report{
		V:   JSONVersion,
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
	waits := r.waits()
	for _, e := range r.events {
		e.Task = textsafe.Line(e.Task)
		switch e.Type {
		case state.EventRunStopped:
			if e.DurationMS > 0 {
				out.DurationS = msSecs(e.DurationMS)
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
					DurationS: msSecs(e.DurationMS),
				})
			}
		case state.EventCommitted:
			c := ReportCommit{Task: e.Task, Subject: textsafe.Line(e.Detail)}
			if shaPattern.MatchString(e.Commit) {
				c.SHA = e.Commit
			}
			out.Commits = append(out.Commits, c)
			if t := byID[e.Task]; t != nil {
				// The task's last commit, also when its SHA is unknown.
				t.t.Commit = c.SHA
			}
		case state.EventUsage:
			if t := byID[e.Task]; t != nil {
				t.audit(e.Audit)
				t.tokens(e, prices)
			}
		case state.EventRankMismatch:
			// Also when the run stopped before the attempt's usage line.
			if t := byID[e.Task]; t != nil {
				t.audit(state.AuditMismatch)
			}
		}
	}
	v1 := r.id != "" // only v1 lines carry a run ID; v0 runs log no waits
	if v1 {
		out.NeedsYouS = ptr(union(waits, func(*wait) bool { return true }))
	}
	perTask, breakdown := r.times(waits)
	out.Time = breakdown
	running := sum.End == EndRunning
	var total price.Sum
	priced := false
	for _, t := range tasks {
		rt := t.t
		if rt.Result == "" {
			rt.Result = ResultOpen
			if running {
				rt.Result = ResultRunning
			}
		}
		if t.timed {
			rt.DurationS = secs(t.duration)
		}
		agent := rt.Owner != "user"
		if t.latest != "" && agent {
			rt.Resume = "claude --resume " + t.latest
		}
		if v1 && agent {
			id := rt.ID
			rt.NeedsYouS = ptr(union(waits, func(w *wait) bool { return w.task == id }))
		}
		rt.Time = perTask[rt.ID]
		if rt.Usage != nil {
			e := NewEstimate(t.cost)
			rt.Estimate = &e
			total.Add(t.cost)
			priced = true
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
	if priced {
		e := NewEstimate(total)
		out.Estimate = &e
	}
	return out
}

// waits are the run's needs-you intervals (SPEC §13), each closed: by its
// needs_you_clear, by the task's next retry, result or reset, or by the
// run's end (its stop, or its last line).
func (r *runRec) waits() []*wait {
	var waits []*wait
	closeWaits := func(at time.Time, match func(*wait) bool) {
		for _, w := range waits {
			if w.end.IsZero() && match(w) {
				w.end = at
			}
		}
	}
	for _, e := range r.events {
		task := textsafe.Line(e.Task)
		switch e.Type {
		case state.EventTaskRetried, state.EventTaskDone, state.EventTaskSkipped, state.EventTaskReset:
			closeWaits(e.At, func(w *wait) bool { return w.task == task })
		case state.EventNeedsYou:
			waits = append(waits, &wait{task: task, reason: e.Reason, start: e.At})
		case state.EventNeedsYouClear:
			// Every open wait of the task for the reason: older igris
			// logged a repeated wait twice and cleared it once.
			closeWaits(e.At, func(w *wait) bool { return w.task == task && w.reason == e.Reason })
		}
	}
	closeWaits(r.lastAt(), func(*wait) bool { return true })
	return waits
}

// lastAt is when the run ended as far as the log says: its stop, or its
// last line when it has none.
func (r *runRec) lastAt() time.Time {
	if !r.stopped && len(r.events) > 0 {
		return r.events[len(r.events)-1].At
	}
	return r.end
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

// session records the Claude session UUID of a session line (already
// shape-checked when read; "" when it has none).
func (t *taskRec) session(id string) {
	t.latest = id
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
	return secs(total)
}

func ptr(n int) *int { return &n }
