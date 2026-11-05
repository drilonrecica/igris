package plan

import "strings"

// Parallelism describes how much of a phase's remaining work arise would run
// at once (SPEC §14 `phases`). It is a read-only view that agrees with the
// scheduler (§5.1): only a phase marked parallel (§3.8) has tasks running
// together, and tasks sharing a Group (§3.8) never do.
type Parallelism struct {
	// CriticalPath is the longest chain of unfinished tasks, each depending
	// on the one before it, in dependency order. Empty when nothing is left.
	CriticalPath []string
	// Width is the size of the largest set of tasks that would run together:
	// 1 in a phase that isn't parallel or whose tasks all wait on each other
	// or share a Group. 0 when nothing is left.
	Width int
	// Together holds the sets of two or more tasks that arise would start
	// together once the level before is done, in level then file order;
	// always empty in a phase without the parallel marker.
	Together [][]string
}

// ParallelismOf analyses the unfinished tasks of ph (not done, not skipped)
// that in accepts; a nil in accepts every task. Only dependencies between
// those tasks count: a finished dependency is met, and one in another phase
// (or outside the slice) is not this phase's to schedule. A task's level is
// one more than the deepest of the dependencies it waits on in the phase,
// so the tasks of one level depend on none of each other. In a parallel
// phase a level is split into rounds, each holding at most one task of a
// Group (compared without case, as the scheduler does); a round of two or
// more is a Together set, and Width is the largest round.
func (ph *Phase) ParallelismOf(in func(*Task) bool) Parallelism {
	var tasks []*Task
	member := map[string]*Task{}
	for _, t := range ph.Tasks {
		if t.Status.Satisfied() || (in != nil && !in(t)) {
			continue
		}
		tasks = append(tasks, t)
		member[t.ID] = t
	}
	level := make(map[string]int, len(tasks))
	via := make(map[string]string, len(tasks)) // the dependency that sets the level
	var depth func(t *Task) int
	depth = func(t *Task) int {
		if l, ok := level[t.ID]; ok {
			return l
		}
		level[t.ID] = 1 // a validated plan has no cycle; this only guards one
		best := 0
		for _, id := range t.Deps {
			if member[id] == nil || id == t.ID {
				continue
			}
			if l := depth(member[id]); l > best {
				best, via[t.ID] = l, id
			}
		}
		level[t.ID] = best + 1
		return best + 1
	}
	var out Parallelism
	var deepest *Task
	var byLevel [][]string
	for _, t := range tasks {
		l := depth(t)
		for len(byLevel) < l {
			byLevel = append(byLevel, nil)
		}
		byLevel[l-1] = append(byLevel[l-1], t.ID)
		if deepest == nil || l > level[deepest.ID] {
			deepest = t
		}
	}
	if len(tasks) > 0 {
		out.Width = 1
	}
	for _, ids := range byLevel {
		if !ph.Parallel {
			continue
		}
		for _, set := range rounds(ids, member) {
			out.Width = max(out.Width, len(set))
			if len(set) > 1 {
				out.Together = append(out.Together, set)
			}
		}
	}
	for id := ""; deepest != nil; id = via[id] {
		if id == "" {
			id = deepest.ID
		}
		out.CriticalPath = append([]string{id}, out.CriticalPath...)
		if via[id] == "" {
			break
		}
	}
	return out
}

// rounds splits one level's tasks, in file order, into sets that hold at
// most one task of each Group: the first set takes every task whose Group
// isn't taken yet, the next one the tasks left over, and so on.
func rounds(ids []string, member map[string]*Task) [][]string {
	var out [][]string
	for len(ids) > 0 {
		var set, rest []string
		groups := map[string]bool{}
		for _, id := range ids {
			g := strings.ToLower(member[id].Group)
			if g != "" && groups[g] {
				rest = append(rest, id)
				continue
			}
			groups[g] = g != ""
			set = append(set, id)
		}
		out = append(out, set)
		ids = rest
	}
	return out
}
