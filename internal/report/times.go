package report

import (
	"slices"
	"sort"
	"time"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Times is time by what it was spent on (SPEC §14), in whole seconds:
// igris waiting on the owner (needs you), a session waiting for a usage
// limit to reset (limit), the verify command running (verify), and the
// rest of the task's time, its sessions at work (agent). Each instant of a
// task counts once, in the first of needs you, limit, verify and agent
// that it is in, so a task's four add up to its time in the run. Since 1.8.
type Times struct {
	AgentS    int `json:"agent_s"`
	VerifyS   int `json:"verify_s"`
	NeedsYouS int `json:"needs_you_s"`
	LimitS    int `json:"limit_s"`
}

// TimeGroup is the time of a set of tasks: the run's, a phase's or a
// rank's. Its Times are the tasks' summed, so tasks running side by side
// in a parallel phase each count; Wall is the time at least one of them
// spent on each, and WallS the time at least one of them ran.
type TimeGroup struct {
	Phase string `json:"phase,omitempty"`
	Rank  string `json:"rank,omitempty"`
	Times
	WallS int   `json:"wall_s"`
	Wall  Times `json:"wall"`
}

// TimeBreakdown is a run's time by what it was spent on, for the run's
// agent tasks, each phase (in log order) and each rank (in order of first
// start). A wait on the owner of no task (a changed plan holding the run)
// is in the run's needs-you total only.
type TimeBreakdown struct {
	Run    TimeGroup   `json:"run"`
	Phases []TimeGroup `json:"phases"`
	Ranks  []TimeGroup `json:"ranks"`
}

// span is an interval of the run's time.
type span struct{ from, to time.Time }

// spans is a set of intervals; norm makes it sorted and disjoint.
type spans []span

// norm is the union of s: sorted, overlaps merged, empty spans dropped.
func norm(s ...spans) spans {
	var all spans
	for _, x := range s {
		for _, sp := range x {
			if sp.to.After(sp.from) {
				all = append(all, sp)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].from.Before(all[j].from) })
	var out spans
	for _, sp := range all {
		if n := len(out); n > 0 && !sp.from.After(out[n-1].to) {
			if sp.to.After(out[n-1].to) {
				out[n-1].to = sp.to
			}
			continue
		}
		out = append(out, sp)
	}
	return out
}

// minus is s without o; both normalized.
func (s spans) minus(o spans) spans {
	var out spans
	for _, sp := range s {
		for _, cut := range o {
			if !cut.to.After(sp.from) || !cut.from.Before(sp.to) {
				continue
			}
			if cut.from.After(sp.from) {
				out = append(out, span{sp.from, cut.from})
			}
			sp.from = cut.to
			if !sp.to.After(sp.from) {
				break
			}
		}
		if sp.to.After(sp.from) {
			out = append(out, sp)
		}
	}
	return out
}

func (s spans) total() time.Duration {
	var d time.Duration
	for _, sp := range s {
		d += sp.to.Sub(sp.from)
	}
	return d
}

// taskTime is one agent task's intervals while the log is read.
type taskTime struct {
	id, phase, rank string
	user            bool
	open            bool
	start           time.Time
	ran             spans // from a start or resume to the result or reset
	verify, limit   spans
	waits           spans
	limits          []*span // limit waits not ended yet
	// After classify: what each instant went to, and all of it.
	agent, verifyOnly, limitOnly, needs, all spans
}

// classify splits the task's time: needs you first, then a limit wait,
// then verify, then agent.
func (t *taskTime) classify() {
	t.needs = norm(t.waits)
	held := norm(t.needs, t.limit)
	t.limitOnly = norm(t.limit).minus(t.needs)
	t.verifyOnly = norm(t.verify).minus(held)
	t.agent = norm(t.ran).minus(norm(held, t.verify))
	t.all = norm(t.ran, t.verify, t.limit, t.waits)
}

func (t *taskTime) times() Times {
	return Times{
		AgentS: secs(t.agent.total()), VerifyS: secs(t.verifyOnly.total()),
		NeedsYouS: secs(t.needs.total()), LimitS: secs(t.limitOnly.total()),
	}
}

// times is the run's time breakdown (SPEC §14) from its lines and its
// needs-you waits: each agent task's Times by ID, and the groups. Both are
// nil for a v0 run, which logs no waits, and for a run without an agent
// task.
func (r *runRec) times(waits []*wait) (map[string]*Times, *TimeBreakdown) {
	if r.id == "" {
		return nil, nil
	}
	var tasks []*taskTime
	byID := map[string]*taskTime{}
	get := func(id string) *taskTime {
		if t := byID[id]; t != nil {
			return t
		}
		t := &taskTime{id: id}
		byID[id] = t
		tasks = append(tasks, t)
		return t
	}
	endLimits := func(t *taskTime, at time.Time) {
		for _, l := range t.limits {
			if at.Before(l.to) {
				l.to = at
			}
			t.limit = append(t.limit, *l)
		}
		t.limits = nil
	}
	for _, e := range r.events {
		id := textsafe.Line(e.Task)
		if id == "" {
			continue
		}
		switch e.Type {
		case state.EventTaskStarted, state.EventTaskResumed, state.EventTaskRetried:
			t := get(id)
			if v := textsafe.Line(e.Phase); v != "" {
				t.phase = v
			}
			if v := textsafe.Line(e.Rank); v != "" {
				t.rank = v
			}
			if e.Owner != "" {
				t.user = e.Owner == "user"
			}
			if e.Type == state.EventTaskRetried {
				// A new session: the old one's limit wait is over.
				endLimits(t, e.At)
			} else if !t.open {
				t.open, t.start = true, e.At
			}
		case state.EventTaskDone, state.EventTaskSkipped, state.EventTaskReset:
			t := byID[id]
			if t == nil {
				continue
			}
			switch {
			case t.open:
				t.ran = append(t.ran, span{t.start, e.At})
			case e.DurationMS > 0 && e.Type != state.EventTaskReset:
				t.ran = append(t.ran, span{e.At.Add(-time.Duration(e.DurationMS) * time.Millisecond), e.At})
			}
			t.open = false
			endLimits(t, e.At)
		case state.EventVerifyPassed, state.EventVerifyFailed:
			if t := byID[id]; t != nil && e.DurationMS > 0 {
				t.verify = append(t.verify, span{e.At.Add(-time.Duration(e.DurationMS) * time.Millisecond), e.At})
			}
		case state.EventLimitHit:
			// The session waits until the window resets (SPEC §6.9); an
			// unknown reset is no wait igris can count.
			reset, err := time.Parse(time.RFC3339, e.ResetsAt)
			if t := byID[id]; t != nil && err == nil && reset.After(e.At) {
				t.limits = append(t.limits, &span{e.At, reset})
			}
		}
	}
	end := r.lastAt()
	for _, t := range tasks {
		if t.open && !end.Before(t.start) {
			t.ran = append(t.ran, span{t.start, end})
		}
		endLimits(t, end)
	}
	for _, w := range waits {
		if t := byID[w.task]; t != nil && !w.end.IsZero() {
			t.waits = append(t.waits, span{w.start, w.end})
		}
	}
	tasks = slices.DeleteFunc(tasks, func(t *taskTime) bool { return t.user })
	if len(tasks) == 0 {
		return nil, nil
	}
	per := map[string]*Times{}
	for _, t := range tasks {
		t.classify()
		v := t.times()
		per[t.id] = &v
	}
	out := &TimeBreakdown{Run: group(tasks), Phases: []TimeGroup{}, Ranks: []TimeGroup{}}
	for _, ph := range keys(tasks, func(t *taskTime) string { return t.phase }) {
		g := group(slices.DeleteFunc(slices.Clone(tasks), func(t *taskTime) bool { return t.phase != ph }))
		g.Phase = ph
		out.Phases = append(out.Phases, g)
	}
	for _, rk := range keys(tasks, func(t *taskTime) string { return t.rank }) {
		g := group(slices.DeleteFunc(slices.Clone(tasks), func(t *taskTime) bool { return t.rank != rk }))
		g.Rank = rk
		out.Ranks = append(out.Ranks, g)
	}
	return per, out
}

// keys is the distinct non-empty values of key over tasks, in order.
func keys(tasks []*taskTime, key func(*taskTime) string) []string {
	var out []string
	for _, t := range tasks {
		if k := key(t); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// group sums the tasks' times and measures their wall time. The sums add
// durations, not rounded seconds, so tasks one after another have the same
// time summed and as wall time.
func group(tasks []*taskTime) TimeGroup {
	var sum [4]time.Duration
	var agent, verify, limit, needs, all []spans
	for _, t := range tasks {
		sum[0] += t.agent.total()
		sum[1] += t.verifyOnly.total()
		sum[2] += t.needs.total()
		sum[3] += t.limitOnly.total()
		agent, verify, limit = append(agent, t.agent), append(verify, t.verifyOnly), append(limit, t.limitOnly)
		needs, all = append(needs, t.needs), append(all, t.all)
	}
	return TimeGroup{
		Times: Times{AgentS: secs(sum[0]), VerifyS: secs(sum[1]), NeedsYouS: secs(sum[2]), LimitS: secs(sum[3])},
		Wall: Times{
			AgentS: secs(norm(agent...).total()), VerifyS: secs(norm(verify...).total()),
			NeedsYouS: secs(norm(needs...).total()), LimitS: secs(norm(limit...).total()),
		},
		WallS: secs(norm(all...).total()),
	}
}
