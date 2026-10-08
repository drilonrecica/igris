package report

import (
	"time"

	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// MaxRankDurations is how many of a rank's most recent durations
// RankDurations keeps.
const MaxRankDurations = 20

// RankDurations returns, per rank, how long finished agent tasks took: the
// task_done of a task started (not resumed) in its run, so partial times
// never count, from the log's duration_ms or, for v0 lines, the time from
// task_started to task_done (SPEC §15.3). Verify and the owner's waits are
// included. Skipped and user tasks don't count. Each rank keeps its
// MaxRankDurations most recent durations, oldest first.
func RankDurations(events []state.Event) map[string][]time.Duration {
	out := map[string][]time.Duration{}
	for _, r := range readRuns(events) {
		started := map[string]time.Time{} // agent tasks started in this run
		for _, e := range r.events {
			task := textsafe.Line(e.Task)
			switch e.Type {
			case state.EventTaskStarted:
				if e.Owner == "user" || e.Detail == "user task" {
					delete(started, task)
					continue
				}
				started[task] = e.At
			case state.EventTaskSkipped, state.EventTaskReset:
				delete(started, task) // the attempt ended without a done
			case state.EventTaskDone:
				start, ok := started[task]
				delete(started, task)
				rank := textsafe.Line(e.Rank)
				if !ok || rank == "" {
					continue
				}
				d := time.Duration(e.DurationMS) * time.Millisecond
				if d <= 0 {
					d = e.At.Sub(start)
				}
				if d <= 0 {
					continue
				}
				ds := append(out[rank], d)
				if len(ds) > MaxRankDurations {
					ds = ds[len(ds)-MaxRankDurations:]
				}
				out[rank] = ds
			}
		}
	}
	return out
}
