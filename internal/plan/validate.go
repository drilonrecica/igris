package plan

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var (
	idPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	validModes = []string{"default", "accept", "auto", "plan", "yolo"}
)

// Invalid is the error for a plan that fails validation.
type Invalid struct {
	Issues []Issue
}

func (e *Invalid) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "plan has %d problem(s); fix them (or run `igris adapt`) and try again:", len(e.Issues))
	for _, i := range e.Issues {
		b.WriteString("\n  ")
		b.WriteString(i.Error())
	}
	return b.String()
}

// Load reads and parses the plan at path. It fails only if the file can't
// be read; call Validate for the plan's problems.
func Load(path string, opts Options) (*Plan, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the owner's plan file
	if err != nil {
		return nil, fmt.Errorf("read plan %s: %w", path, err)
	}
	return Parse(path, data, opts), nil
}

// Validate returns every problem in the plan (SPEC §3), sorted by line.
// models is the [models] config map used to check ranks.
func (p *Plan) Validate(models map[string]string) []Issue {
	v := &validator{p: p, models: models, firstLine: map[string]int{}}
	v.issues = append(v.issues, p.issues...)
	for _, t := range p.Tasks {
		v.checkTask(t)
	}
	v.checkDeps()
	if len(p.Tasks) == 0 && len(v.issues) == 0 {
		// Nothing igris can run: most likely a plan in another format. A
		// misplaced table is reported as such instead.
		v.add(0, "no task table found; add a table with ID, Status and Model columns under a ## heading")
	}

	sort.SliceStable(v.issues, func(i, j int) bool { return v.issues[i].Line < v.issues[j].Line })
	return v.issues
}

// Check validates the plan and returns *Invalid if it has any problem.
func (p *Plan) Check(models map[string]string) error {
	if issues := p.Validate(models); len(issues) > 0 {
		return &Invalid{Issues: issues}
	}
	return nil
}

// validator collects semantic issues without modifying the plan.
type validator struct {
	p         *Plan
	models    map[string]string
	firstLine map[string]int // task ID -> line of its first occurrence
	issues    []Issue
}

func (v *validator) add(line int, format string, a ...any) {
	v.issues = append(v.issues, Issue{File: v.p.Path, Line: line, Msg: fmt.Sprintf(format, a...)})
}

func (v *validator) checkTask(t *Task) {
	at := t.Line
	switch {
	case t.ID == "":
		v.add(at, "row has no ID; every task needs a unique ID")
	case !idPattern.MatchString(t.ID):
		v.add(at, "%q is not a valid task ID; use letters, digits, '.', '_' and '-', starting with a letter or digit", t.ID)
	}
	if t.ID != "" {
		if first, dup := v.firstLine[t.ID]; dup {
			v.add(at, "duplicate task ID %s (first at line %d); task IDs must be unique across the plan", t.ID, first)
		} else {
			v.firstLine[t.ID] = at
		}
	}
	name := t.ID
	if name == "" {
		name = "task"
	}

	if t.Status == StatusUnknown {
		hint := ""
		if s, ok := statusSynonym(t.StatusText); ok {
			hint = fmt.Sprintf(" (did you mean %s?)", s)
		}
		v.add(at, "%s: unknown status %q%s; use ready, blocked, in progress, done or skipped", name, t.StatusText, hint)
	}
	if t.Owner == "" {
		v.add(at, "%s: unknown owner %q; use agent, user or agent + user", name, t.OwnerText)
	}
	if t.Mode != "" && !contains(validModes, t.Mode) {
		v.add(at, "%s: unknown mode %q; use default, accept, auto, plan, yolo or —", name, t.Mode)
	}

	switch {
	case t.Owner == OwnerUser && t.Rank != "":
		v.add(at, "%s: user tasks must have Model —, not %q (igris runs no session for them)", name, t.Rank)
	case t.Owner.IsAgent() && t.Rank == "":
		v.add(at, "%s: %s task needs a Model (a rank from [models], e.g. sonnet); use Owner user for tasks without a session", name, t.Owner)
	case t.Owner.IsAgent() && t.Rank == "?":
		// `igris adapt` leaves "?" where the original plan had no usable model.
		v.add(at, "%s: model not set yet (\"?\"); fill in one of: %s", name, strings.Join(sortedKeys(v.models), ", "))
	case t.Owner.IsAgent():
		if _, ok := v.models[t.Rank]; !ok {
			v.add(at, "%s: unknown model rank %q; add it to [models] in igris.toml or use one of: %s", name, t.Rank, strings.Join(sortedKeys(v.models), ", "))
		}
	}

	for _, d := range t.Deps {
		switch {
		case d == t.ID:
			v.add(at, "%s depends on itself; remove it from Deps", name)
		case !idPattern.MatchString(d):
			v.add(at, "%s: Deps entry %q is not a task ID; list task IDs separated by commas", name, d)
		}
	}
}

// checkDeps reports unknown dependencies and every dependency cycle once,
// with its path.
func (v *validator) checkDeps() {
	p := v.p
	for _, t := range p.Tasks {
		for _, d := range t.Deps {
			if p.Task(d) == nil && idPattern.MatchString(d) {
				v.add(t.Line, "%s depends on unknown task %s; check the ID or remove it from Deps", t.ID, d)
			}
		}
	}

	index := map[*Task]int{}
	for i, t := range p.Tasks {
		index[t] = i
	}
	const (
		white = iota
		grey
		black
	)
	color := map[*Task]int{}
	var stack []*Task
	reported := map[string]bool{}

	var visit func(t *Task)
	visit = func(t *Task) {
		color[t] = grey
		stack = append(stack, t)
		for _, id := range t.Deps {
			d := p.Task(id)
			if d == nil || d == t {
				continue // unknown and self deps are reported above
			}
			switch color[d] {
			case white:
				visit(d)
			case grey:
				v.reportCycle(stack, d, index, reported)
			}
		}
		stack = stack[:len(stack)-1]
		color[t] = black
	}
	for _, t := range p.Tasks {
		if color[t] == white && p.Task(t.ID) == t {
			visit(t)
		}
	}
}

// reportCycle reports the cycle formed by the stack from d to its top,
// rotated to start at the task that comes first in the file.
func (v *validator) reportCycle(stack []*Task, d *Task, index map[*Task]int, reported map[string]bool) {
	start := len(stack) - 1
	for stack[start] != d {
		start--
	}
	cycle := stack[start:]
	first := 0
	for i, t := range cycle {
		if index[t] < index[cycle[first]] {
			first = i
		}
	}
	ids := make([]string, 0, len(cycle)+1)
	for i := range cycle {
		ids = append(ids, cycle[(first+i)%len(cycle)].ID)
	}
	ids = append(ids, ids[0])
	path := strings.Join(ids, " → ")
	if reported[path] {
		return
	}
	reported[path] = true
	v.add(cycle[first].Line, "dependency cycle %s (each task waits on the next); remove one of these dependencies", path)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ValidID reports whether id is a legal task ID (SPEC §3.2). It is also safe
// to use as a file name: it has no path separators and cannot start with a dot.
func ValidID(id string) bool { return idPattern.MatchString(id) }
