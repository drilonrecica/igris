package plan

// Parallelism describes how much of a phase's remaining work could run at
// once (SPEC §14 `phases`). It is a read-only view of the dependency graph;
// igris runs tasks together only in a phase marked parallel (§3.8).
type Parallelism struct {
	// CriticalPath is the longest chain of unfinished tasks, each depending
	// on the one before it, in dependency order. Empty when nothing is left.
	CriticalPath []string
	// Width is the size of the widest level: the most tasks that could run
	// together. 0 when nothing is left.
	Width int
	// Together holds, per level with two or more tasks, the IDs that would
	// run together once the level before it is done, in file order.
	Together [][]string
}

// ParallelismOf analyses the unfinished tasks of ph (not done, not skipped)
// that in accepts; a nil in accepts every task. Only dependencies between
// those tasks count: a finished dependency is met, and one in another phase
// (or outside the slice) is not this phase's to schedule. A task's level is
// one more than the deepest of the dependencies it waits on in the phase,
// so the tasks of one level depend on none of each other.
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
	for _, ids := range byLevel {
		out.Width = max(out.Width, len(ids))
		if len(ids) > 1 {
			out.Together = append(out.Together, ids)
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
