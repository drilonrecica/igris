package report

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/usage"
)

// ErrNoRunsSince is NewUsage's error for a log with no run since the given
// time; the caller names the time.
var ErrNoRunsSince = errors.New("no run since")

// TopTasks is how many tasks `igris usage` lists as the top consumers.
const TopTasks = 5

// unknownModel is the model of usage that recorded no model id.
const unknownModel = "unknown"

// Tokens are summed token counts. In is input + cache read + cache write,
// the figure the CLI and the run card lead with; the four counts are all
// kept (SPEC §14).
type Tokens struct {
	In         int64 `json:"in"`
	Output     int64 `json:"output"`
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Messages   int64 `json:"messages"`
}

func (t *Tokens) add(c usage.Counts) {
	t.Input, t.Output = satAdd(t.Input, c.Input), satAdd(t.Output, c.Output)
	t.CacheRead, t.CacheWrite = satAdd(t.CacheRead, c.CacheRead), satAdd(t.CacheWrite, c.CacheWrite)
	t.Messages = satAdd(t.Messages, c.Messages)
	t.In = satAdd(satAdd(t.Input, t.CacheRead), t.CacheWrite)
}

// Total is in + out, what the top consumers are ranked by.
func (t Tokens) Total() int64 { return satAdd(t.In, t.Output) }

func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// UsageRun is one run the report covers.
type UsageRun struct {
	Run       string `json:"run,omitempty"` // "" for a v0 run, which logs no usage
	StartedAt string `json:"started_at"`
	End       string `json:"end"` // as HistoryRun.End
}

// UsageRow is what one task, model or rank used. Tokens include the
// subagents', Sub is their part of them.
type UsageRow struct {
	// Attempts counts the attempts whose usage was read.
	Attempts int `json:"attempts"`
	Tokens
	Sub Tokens `json:"sub"`
}

// add counts an attempt's main thread and subagents.
func (r *UsageRow) add(main, sub usage.Counts) {
	r.Tokens.add(main)
	r.addSub(sub)
}

// addSub counts subagent tokens.
func (r *UsageRow) addSub(sub usage.Counts) {
	r.Tokens.add(sub)
	r.Sub.add(sub)
}

// UsageTask is a task's usage over the runs covered.
type UsageTask struct {
	ID    string `json:"id"`
	Rank  string `json:"rank,omitempty"`
	Model string `json:"model,omitempty"` // the model the rank resolved to
	UsageRow
	// Unavailable counts the attempts whose usage could not be read.
	Unavailable int      `json:"unavailable,omitempty"`
	Models      []string `json:"models"`               // the main thread's model ids
	SubModels   []string `json:"sub_models,omitempty"` // the subagents'
	Audit       string   `json:"audit,omitempty"`      // the worst audit of its attempts
}

// UsageModel is a model's usage: the tokens of the attempts whose main
// thread (or subagents) ran on that model. An attempt that ran on several
// is counted under each.
type UsageModel struct {
	Model string `json:"model"`
	UsageRow
}

// UsageRank is a rank's usage.
type UsageRank struct {
	Rank string `json:"rank"`
	UsageRow
}

// UsageMismatch is an attempt that ran on a model its rank doesn't
// resolve to.
type UsageMismatch struct {
	Run    string   `json:"run,omitempty"`
	Task   string   `json:"task"`
	Rank   string   `json:"rank,omitempty"`
	Model  string   `json:"model,omitempty"` // what the rank resolved to
	Models []string `json:"models"`          // what the main thread ran on
}

// UsageAudit is the rank audit over the attempts covered (SPEC §6.9).
type UsageAudit struct {
	OK          int             `json:"ok"`
	Unchecked   int             `json:"unchecked"`
	Unavailable int             `json:"unavailable"`
	Mismatch    int             `json:"mismatch"`
	Mismatches  []UsageMismatch `json:"mismatches"`
}

// Usage is the result of `igris usage`: the token usage and the rank audit
// of one run, or of every run since a time (SPEC §14). The figures are the
// tokens recorded in the session transcripts, never a cost.
type Usage struct {
	V       int        `json:"v"`
	Project string     `json:"-"`
	Runs    []UsageRun `json:"runs"` // oldest first
	// Totals are the usage of the attempts that could be read, subagents
	// included; Attempts counts them and Unavailable the others, which are
	// left out of the totals.
	Totals      UsageRow     `json:"totals"`
	Unavailable int          `json:"unavailable"`
	Tasks       []UsageTask  `json:"tasks"`  // in first-seen order
	Models      []UsageModel `json:"models"` // most tokens first
	Ranks       []UsageRank  `json:"ranks"`  // most tokens first
	Top         []UsageTask  `json:"top"`    // TopTasks tasks by in + out
	Audit       UsageAudit   `json:"audit"`
	// Quota is the subscription quota last recorded by igris's status line
	// (SPEC §6.3), nil when none is: the caller fills it in.
	Quota *state.Quota `json:"quota"`
	Note  string       `json:"note,omitempty"` // as History.Note
	// NoUsage says why (as a clause after "No usage was recorded:") the runs covered have no usage lines at all, so the
	// zeros are not a measurement; empty when any attempt logged usage.
	NoUsage string `json:"no_usage,omitempty"`
}

// NewUsage reports the usage of the run sel names (as for NewReport), or
// when since is not zero of every run that started at or after it. The
// caller checks sel's shape and that sel and since aren't both given.
func NewUsage(in HistoryInput, project, sel string, since time.Time) (Usage, error) {
	recs := readRuns(in.Events)
	if len(recs) == 0 {
		return Usage{}, ErrNoRuns
	}
	var picked []*runRec
	if since.IsZero() {
		r, err := pickRun(recs, sel)
		if err != nil {
			return Usage{}, err
		}
		picked = []*runRec{r}
	} else {
		for _, r := range recs {
			if !r.start.Before(since) {
				picked = append(picked, r)
			}
		}
		if len(picked) == 0 {
			return Usage{}, ErrNoRunsSince
		}
	}
	u := Usage{
		V: JSONVersion, Project: textsafe.Line(project), Runs: []UsageRun{},
		Tasks: []UsageTask{}, Models: []UsageModel{}, Ranks: []UsageRank{}, Top: []UsageTask{},
		Audit: UsageAudit{Mismatches: []UsageMismatch{}}, Note: logNote(in),
	}
	b := usageBuilder{u: &u, tasks: map[string]*UsageTask{}, models: map[string]*UsageModel{}, ranks: map[string]*UsageRank{}}
	for _, r := range picked {
		sum := r.summary(in.Live && r == recs[len(recs)-1])
		u.Runs = append(u.Runs, UsageRun{Run: r.id, StartedAt: sum.StartedAt, End: sum.End})
		for _, e := range r.events {
			b.event(r.id, e)
		}
	}
	b.finish()
	if u.Totals.Attempts == 0 && u.Unavailable == 0 {
		u.NoUsage = noUsageReason(in.UsageOff)
	}
	return u, nil
}

// noUsageReason says why a run has no usage lines.
func noUsageReason(off bool) string {
	if off {
		return "[usage] enabled = false turns it off in this project"
	}
	return "the runs were made before igris 1.1, or with [usage] enabled = false"
}

// pickRun finds the run sel names among recs, oldest first: "" or "1" the
// newest, "2" the one before, …, or a run ID.
func pickRun(recs []*runRec, sel string) (*runRec, error) {
	i := len(recs) - 1
	switch {
	case sel == "":
	case isIndex(sel):
		n, err := strconv.Atoi(sel) // too large for an int is past the end too
		if err != nil || n < 1 || n > len(recs) {
			return nil, fmt.Errorf("only %d %s recorded", len(recs), plural(len(recs), "run", "runs"))
		}
		i = len(recs) - n
	default:
		if i = slices.IndexFunc(recs, func(r *runRec) bool { return r.id == sel }); i < 0 {
			return nil, fmt.Errorf("no run %s in .igris/runs.jsonl; igris history lists them", textsafe.Line(sel))
		}
	}
	return recs[i], nil
}

// usageBuilder folds usage and rank_mismatch lines into a Usage.
type usageBuilder struct {
	u      *Usage
	order  []string // task IDs, first seen first
	tasks  map[string]*UsageTask
	models map[string]*UsageModel
	ranks  map[string]*UsageRank
}

func (b *usageBuilder) event(run string, e state.Event) {
	switch e.Type {
	case state.EventUsage:
		b.attempt(e)
	case state.EventRankMismatch:
		m := UsageMismatch{
			Run: run, Task: textsafe.Line(e.Task), Rank: textsafe.Line(e.Rank), Model: textsafe.Line(e.Model),
			Models: cleanModels(e.Models),
		}
		// Logged once per attempt, with the ids seen so far.
		b.u.Audit.Mismatches = append(b.u.Audit.Mismatches, m)
	}
}

// task returns the row of the task e names, creating it.
func (b *usageBuilder) task(e state.Event) *UsageTask {
	id := textsafe.Line(e.Task)
	t := b.tasks[id]
	if t == nil {
		t = &UsageTask{ID: id, Models: []string{}}
		b.tasks[id] = t
		b.order = append(b.order, id)
	}
	if r := textsafe.Line(e.Rank); r != "" {
		t.Rank = r
	}
	if m := textsafe.Line(e.Model); m != "" {
		t.Model = m
	}
	return t
}

// attempt counts one usage line: an attempt that ended.
func (b *usageBuilder) attempt(e state.Event) {
	t := b.task(e)
	switch e.Audit {
	case state.AuditOK:
		b.u.Audit.OK++
	case state.AuditUnchecked:
		b.u.Audit.Unchecked++
	case state.AuditUnavailable:
		b.u.Audit.Unavailable++
	case state.AuditMismatch:
		b.u.Audit.Mismatch++
	}
	if slices.Index(auditOrder, e.Audit) > slices.Index(auditOrder, t.Audit) {
		t.Audit = e.Audit
	}
	if e.Usage == nil {
		t.Unavailable++
		b.u.Unavailable++
		return
	}
	main, sub := *e.Usage, usage.Counts{}
	if e.Sub != nil {
		sub = *e.Sub
	}
	t.Attempts++
	t.add(main, sub)
	t.Models = mergeModels(t.Models, e.Models)
	t.SubModels = mergeModels(t.SubModels, e.SubModels)
	b.u.Totals.Attempts++
	b.u.Totals.add(main, sub)

	// An attempt that ran on several models is counted under each of them:
	// the transcript doesn't split its tokens, so every row gets all of
	// the main thread's, and rows can add up to more than the totals.
	counted := map[*UsageModel]bool{}
	for _, m := range b.modelRows(e.Models) {
		m.Attempts++
		m.Tokens.add(main)
		counted[m] = true
	}
	if !sub.IsZero() {
		for _, s := range b.modelRows(e.SubModels) {
			if !counted[s] {
				s.Attempts++
				counted[s] = true
			}
			s.addSub(sub)
		}
	}
	rank := textsafe.Line(e.Rank)
	if rank == "" {
		rank = unknownModel
	}
	r := b.ranks[rank]
	if r == nil {
		r = &UsageRank{Rank: rank}
		b.ranks[rank] = r
	}
	r.Attempts++
	r.add(main, sub)
}

// modelRows returns the row of each model id in ids, creating them; the
// row of "unknown" when ids names none.
func (b *usageBuilder) modelRows(ids []string) []*UsageModel {
	ids = cleanModels(ids)
	if len(ids) == 0 {
		ids = []string{unknownModel}
	}
	var rows []*UsageModel
	for _, id := range ids {
		m := b.models[id]
		if m == nil {
			m = &UsageModel{Model: id}
			b.models[id] = m
		}
		rows = append(rows, m)
	}
	return rows
}

// finish orders the rows and picks the top consumers.
func (b *usageBuilder) finish() {
	u := b.u
	for _, id := range b.order {
		u.Tasks = append(u.Tasks, *b.tasks[id])
	}
	for _, m := range b.models {
		u.Models = append(u.Models, *m)
	}
	sort.SliceStable(u.Models, func(i, j int) bool {
		if a, c := u.Models[i].Total(), u.Models[j].Total(); a != c {
			return a > c
		}
		return u.Models[i].Model < u.Models[j].Model
	})
	for _, r := range b.ranks {
		u.Ranks = append(u.Ranks, *r)
	}
	sort.SliceStable(u.Ranks, func(i, j int) bool {
		if a, c := u.Ranks[i].Total(), u.Ranks[j].Total(); a != c {
			return a > c
		}
		return u.Ranks[i].Rank < u.Ranks[j].Rank
	})
	for _, t := range u.Tasks {
		if t.Total() > 0 {
			u.Top = append(u.Top, t)
		}
	}
	sort.SliceStable(u.Top, func(i, j int) bool { return u.Top[i].Total() > u.Top[j].Total() })
	if len(u.Top) > TopTasks {
		u.Top = u.Top[:TopTasks]
	}
	u.Audit.Mismatch = max(u.Audit.Mismatch, len(u.Audit.Mismatches))
}

// cleanModels returns ids cleaned for the terminal, without blanks.
func cleanModels(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if id = textsafe.Line(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// mergeModels adds the ids in add to list, keeping first-seen order.
func mergeModels(list, add []string) []string {
	for _, id := range cleanModels(add) {
		if !slices.Contains(list, id) {
			list = append(list, id)
		}
	}
	return list
}
