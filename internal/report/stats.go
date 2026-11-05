package report

import (
	"sort"
)

// RankStats is the plan's health for the tasks of one rank over the runs
// `igris history --stats` reads (SPEC §14).
type RankStats struct {
	Rank  string `json:"rank"`
	Tasks int    `json:"tasks"` // task runs: a task run in two runs counts twice
	// Attempts is the mean attempts per task run, retries included.
	Attempts float64 `json:"attempts"`
	// VerifyRuns is the verify results (failed + passed) and VerifyFailed
	// those that failed; VerifyFailRate is VerifyFailed over VerifyRuns,
	// left out when the tasks never ran verify.
	VerifyRuns     int      `json:"verify_runs"`
	VerifyFailed   int      `json:"verify_failed"`
	VerifyFailRate *float64 `json:"verify_fail_rate,omitempty"`
	// AgentS and NeedsYouS are the mean per task run over the runs that
	// record times (v1, SPEC §13); left out when none of them does.
	AgentS    *int `json:"agent_s,omitempty"`
	NeedsYouS *int `json:"needs_you_s,omitempty"`
}

// RewriteCandidate is a task that took 2 or more attempts, or failed
// verify, in every run that ran it: the task text, not the agent, is
// probably what to fix.
type RewriteCandidate struct {
	Task         string `json:"task"`
	Phase        string `json:"phase,omitempty"`
	Rank         string `json:"rank,omitempty"`
	Runs         int    `json:"runs"`
	Attempts     int    `json:"attempts"` // over those runs
	VerifyFailed int    `json:"verify_failed"`
}

// Stats is the result of `igris history --stats`.
type Stats struct {
	V       int                `json:"v"`
	Runs    int                `json:"runs"` // runs read
	Ranks   []RankStats        `json:"ranks"`
	Rewrite []RewriteCandidate `json:"rewrite"`
	Note    string             `json:"note,omitempty"` // as History.Note
}

// NewStats summarises the plan's health over the last in.N runs of the log
// (all of them when N is 0 or less): per rank, then the rewrite candidates.
// A task still running, and a user task (no rank), take no part.
func NewStats(in HistoryInput) Stats {
	out := Stats{V: JSONVersion, Ranks: []RankStats{}, Rewrite: []RewriteCandidate{}, Note: logNote(in)}
	recs := readRuns(in.Events)
	if in.N > 0 && len(recs) > in.N {
		recs = recs[len(recs)-in.N:]
	}
	out.Runs = len(recs)

	type rankAcc struct {
		RankStats
		attempts, timed, agent, needs int
	}
	var ranks []*rankAcc
	byRank := map[string]*rankAcc{}
	type taskAcc struct {
		RewriteCandidate
		allBad bool
	}
	var tasks []*taskAcc
	byTask := map[string]*taskAcc{}

	for i, r := range recs {
		sum := r.summary(in.Live && i == len(recs)-1)
		phase := map[string]string{}
		for _, a := range r.attempts {
			if a.phase != "" {
				phase[a.id] = a.phase
			}
		}
		for _, t := range sum.Tasks {
			if t.Rank == "" || t.Result == ResultRunning {
				continue
			}
			a := byRank[t.Rank]
			if a == nil {
				a = &rankAcc{RankStats: RankStats{Rank: t.Rank}}
				byRank[t.Rank] = a
				ranks = append(ranks, a)
			}
			a.Tasks++
			a.attempts += t.Attempts
			a.VerifyFailed += t.VerifyFailed
			a.VerifyRuns += t.VerifyFailed + t.VerifyPassed
			if t.Time != nil {
				a.timed++
				a.agent += t.Time.AgentS
				a.needs += t.Time.NeedsYouS
			}

			c := byTask[t.ID]
			if c == nil {
				c = &taskAcc{RewriteCandidate: RewriteCandidate{Task: t.ID}, allBad: true}
				byTask[t.ID] = c
				tasks = append(tasks, c)
			}
			c.Runs++
			c.Attempts += t.Attempts
			c.VerifyFailed += t.VerifyFailed
			c.Rank = t.Rank // the newest run's
			if p := phase[t.ID]; p != "" {
				c.Phase = p
			}
			c.allBad = c.allBad && (t.Attempts >= 2 || t.VerifyFailed > 0)
		}
	}

	for _, a := range ranks {
		s := a.RankStats
		s.Attempts = float64(a.attempts) / float64(a.Tasks)
		if a.VerifyRuns > 0 {
			rate := float64(a.VerifyFailed) / float64(a.VerifyRuns)
			s.VerifyFailRate = &rate
		}
		if a.timed > 0 {
			ag, ny := a.agent/a.timed, a.needs/a.timed
			s.AgentS, s.NeedsYouS = &ag, &ny
		}
		out.Ranks = append(out.Ranks, s)
	}
	for _, c := range tasks {
		if c.allBad {
			out.Rewrite = append(out.Rewrite, c.RewriteCandidate)
		}
	}
	sort.SliceStable(out.Rewrite, func(i, j int) bool { return out.Rewrite[i].Attempts > out.Rewrite[j].Attempts })
	return out
}
