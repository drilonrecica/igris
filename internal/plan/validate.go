package plan

import (
	"errors"
	"fmt"
	"github.com/drilonrecica/igris/internal/fsafe"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

var (
	idPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	validModes = []string{"default", "accept", "auto", "plan", "plan:default", "plan:accept", "plan:auto", "plan:yolo", "yolo", "manual"}
)

// Invalid is the error for a plan that fails validation.
type Invalid struct {
	Issues []Issue
	// Newer: the plan is in a format this igris doesn't read.
	Newer bool
}

func (e *Invalid) Error() string {
	var b strings.Builder
	if e.Newer {
		b.WriteString("plan is in a format this igris can't read; upgrade igris:")
	} else {
		fmt.Fprintf(&b, "plan has %d problem(s); fix them (or run `igris adapt`) and try again:", len(e.Issues))
	}
	for _, i := range e.Issues {
		b.WriteString("\n  ")
		b.WriteString(i.Error())
	}
	return b.String()
}

// MaxSize is the most a plan file, and all files of a plan together, may
// hold (SPEC §3.7, §16): the plan is read every poll, and a session can
// replace it.
const MaxSize = 16 << 20

// Load reads and parses the plan at path with the files it includes (SPEC
// §3.7), checked against opts.Root. It fails only if the main plan can't be
// read (also when it is not a regular file, or over MaxSize); a problem with
// an included file is an issue at its include line, so call Validate for
// the plan's problems.
func Load(path string, opts Options) (*Plan, error) {
	data, err := readPlanFile(path)
	if err != nil {
		return nil, err
	}
	return parse(path, data, opts, newIncluder(path, len(data), opts.Root)), nil
}

// readPlanFile reads the plan file as a regular file of at most MaxSize,
// following a symlink (the owner may keep the plan elsewhere).
func readPlanFile(path string) ([]byte, error) {
	data, err := fsafe.ReadFile(path, MaxSize, true)
	switch {
	case errors.Is(err, fsafe.ErrNotRegular):
		return nil, fmt.Errorf("read plan: %w; put the plan file back", err)
	case errors.Is(err, fsafe.ErrTooLarge):
		return nil, fmt.Errorf("read plan: %w; split the plan or remove what is not a plan", err)
	case err != nil:
		return nil, fmt.Errorf("read plan %s: %w", path, err)
	}
	return data, nil
}

// Rules are the config values the plan is validated against.
type Rules struct {
	// Models is the [models] config map used to check ranks.
	Models map[string]string
	// Verify lists the verify profile names defined in config ([verify],
	// and "default" for run.verify) that a Verify cell may name.
	Verify []string
	// Root is the directory Context paths are checked against (SPEC
	// §3.2): the project root for arise and home, the working directory
	// for check, phases and status. "" is the working directory.
	Root string
}

// VerifyNone in a Verify cell (or [phases.<id>] verify) turns verification
// off (SPEC §6.4).
const VerifyNone = "none"

// Validate returns every problem in the plan (SPEC §3), sorted in plan
// order (§3.7).
func (p *Plan) Validate(r Rules) []Issue {
	if p.newerFormat != nil {
		// The rest may follow rules this igris doesn't know: its problems
		// would only mislead.
		return []Issue{*p.newerFormat}
	}
	v := &validator{p: p, models: r.Models, verify: r.Verify, rules: r, first: map[string]*Task{}}
	v.issues = append(v.issues, p.issues...)
	for _, t := range p.Tasks {
		v.checkTask(t)
	}
	v.checkDeps()
	if len(p.Tasks) == 0 && len(v.issues) == 0 {
		// Nothing igris can run: most likely a plan in another format. A
		// misplaced table is reported as such instead.
		for _, nm := range p.nearMiss {
			missing, example := "Status", `"State" = "Status"`
			if nm.hasStatus {
				missing, example = "Model", `"Agent" = "Model"`
			}
			v.addIn(nm.file, nm.line, "no task table found: this table has an ID column but no %s column; rename that column to %s or alias it under [columns] in igris.toml (e.g. %s)", missing, missing, example)
		}
		if len(p.nearMiss) == 0 {
			v.add(0, "no task table found; add a table with ID, Status and Model columns under a ## heading")
		}
	}

	p.sortIssues(v.issues)
	return v.issues
}

// Check validates the plan and returns *Invalid if it has any problem.
func (p *Plan) Check(r Rules) error {
	if issues := p.Validate(r); len(issues) > 0 {
		return &Invalid{Issues: issues, Newer: p.NewerFormat()}
	}
	return nil
}

// validator collects semantic issues without modifying the plan.
type validator struct {
	p      *Plan
	models map[string]string
	verify []string
	rules  Rules
	first  map[string]*Task // task ID -> its first occurrence
	issues []Issue

	root    string // rules.Root with symlinks resolved; see realRoot
	rootErr error
}

func (v *validator) add(line int, format string, a ...any) {
	v.addIn(v.p.Path, line, format, a...)
}

func (v *validator) addIn(file string, line int, format string, a ...any) {
	v.issues = append(v.issues, Issue{File: file, Line: line, Msg: fmt.Sprintf(format, a...)})
}

// task reports a problem at t's row.
func (v *validator) task(t *Task, format string, a ...any) {
	v.addIn(t.Phase.File, t.Line, format, a...)
}

func (v *validator) checkTask(t *Task) {
	switch {
	case t.ID == "":
		v.task(t, "row has no ID; every task needs a unique ID")
	case !idPattern.MatchString(t.ID):
		if bare := strings.Trim(t.ID, "`*_ "); bare != t.ID && idPattern.MatchString(bare) {
			v.task(t, "%q is not a valid task ID; write it as %s, without markdown around it", t.ID, bare)
			break
		}
		v.task(t, "%q is not a valid task ID; use letters, digits, '.', '_' and '-', starting with a letter or digit", t.ID)
	}
	if t.ID != "" {
		first, dup := v.first[t.ID]
		switch {
		case !dup:
			v.first[t.ID] = t
		case first.Phase.File == t.Phase.File:
			v.task(t, "duplicate task ID %s (first at line %d); task IDs must be unique across the plan", t.ID, first.Line)
		default:
			v.task(t, "task %s is also in %s:%d; task IDs must be unique across the plan's files", t.ID, first.Phase.File, first.Line)
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
		v.task(t, "%s: unknown status %q%s; use ready, blocked, in progress, done or skipped", name, t.StatusText, hint)
	}
	if t.Owner == "" {
		v.task(t, "%s: unknown owner %q; use agent, user or agent + user", name, t.OwnerText)
	}
	if t.Mode != "" && !contains(validModes, t.Mode) {
		v.task(t, "%s: unknown mode %q; use default, accept, auto, plan, plan:default, plan:accept, plan:auto, plan:yolo, yolo, manual or —", name, t.Mode)
	}

	switch {
	case t.Owner == OwnerUser && t.Rank != "":
		v.task(t, "%s: user tasks must have Model —, not %q (igris runs no session for them)", name, t.Rank)
	case t.Owner.IsAgent() && t.Rank == "" && t.Status.Satisfied():
		// A finished task never gets a session, so it needs no model (SPEC
		// §3.5). If it goes back to ready or blocked, this check catches it.
	case t.Owner.IsAgent() && t.Rank == "":
		hint := "use Owner user for tasks without a session"
		if t.Phase != nil && !contains(t.Phase.Columns, ColOwner) {
			// Without an Owner column every task is an agent task (SPEC §3.2).
			hint = "this table has no Owner column, so every task is an agent task; add an Owner column with user for tasks without a session"
		}
		v.task(t, "%s: %s task needs a Model (a rank from [models], e.g. sonnet); %s", name, t.Owner, hint)
	case t.Owner.IsAgent() && t.Rank == "?":
		// `igris adapt` leaves "?" where the original plan had no usable model.
		v.task(t, "%s: model not set yet (\"?\"); fill in one of: %s", name, strings.Join(sortedKeys(v.models), ", "))
	case t.Owner.IsAgent():
		if _, ok := v.models[t.Rank]; !ok {
			hint := ""
			if bare := strings.ToLower(strings.Trim(t.Rank, "`*_ ")); bare != t.Rank {
				if _, ok := v.models[bare]; ok {
					hint = fmt.Sprintf(" (did you mean %s?)", bare)
				}
			}
			v.task(t, "%s: unknown model rank %q%s; add it to [models] in igris.toml or use one of: %s", name, t.Rank, hint, strings.Join(sortedKeys(v.models), ", "))
		}
	}

	// Verify, Timeout and Context matter only for a task igris may still
	// run a session for (SPEC §3.2); a user task's are ignored (Hints).
	if t.Owner.IsAgent() && !t.Status.Satisfied() {
		switch {
		case t.Verify == "" || t.Verify == VerifyNone || contains(v.verify, t.Verify):
		case looksLikeCommand(t.Verify):
			v.task(t, "%s: Verify %q names a profile from [verify], never a command; put the command under [verify] in igris.toml and write the profile name here", name, t.Verify)
		default:
			names := append(slices.Sorted(slices.Values(v.verify)), VerifyNone)
			v.task(t, "%s: unknown verify profile %q; define it under [verify] in igris.toml or use one of: %s", name, t.Verify, strings.Join(names, ", "))
		}
		if t.TimeoutText != "" && t.Timeout == 0 {
			v.checkTimeout(t, name, t.TimeoutText)
		}
		v.checkContext(t, name)
	}
	if t.Group != "" && !t.Status.Satisfied() && !idPattern.MatchString(t.Group) {
		v.task(t, "%s: Group %q is not one name; a task is in at most one group, named with letters, digits, '.', '_' and '-'", name, t.Group)
	}

	for _, d := range t.Deps {
		switch {
		case d == t.ID:
			v.task(t, "%s depends on itself; remove it from Deps", name)
		case !idPattern.MatchString(d):
			v.task(t, "%s: Deps entry %q is not a task ID; list task IDs separated by commas", name, d)
		}
	}
}

// durationSyntax is what time.ParseDuration accepts; a cell matching it
// that still doesn't parse overflows.
var durationSyntax = regexp.MustCompile(`^[-+]?((\d+(\.\d*)?|\.\d+)(ns|us|µs|μs|ms|s|m|h))+$`)

// checkTimeout reports why a set Timeout cell gave no duration.
func (v *validator) checkTimeout(t *Task, name, cell string) {
	d, err := time.ParseDuration(cell)
	switch {
	case err == nil && d < MinTimeout:
		v.task(t, "%s: Timeout %q must be at least %s; write e.g. 45m or 1h30m", name, cell, MinTimeout)
	case err != nil && durationSyntax.MatchString(cell):
		v.task(t, "%s: Timeout %q is too large; write e.g. 45m or 1h30m", name, cell)
	default:
		v.task(t, "%s: Timeout %q is not a duration; write e.g. 45m or 1h30m", name, cell)
	}
}

// looksLikeCommand reports whether a Verify cell holds a command (a space,
// a path or a shell character) rather than a profile name.
func looksLikeCommand(cell string) bool {
	return strings.ContainsAny(cell, " \t/;&|<>$()'\"*=~")
}

// checkDeps reports unknown dependencies and every dependency cycle once,
// with its path.
func (v *validator) checkDeps() {
	p := v.p
	for _, t := range p.Tasks {
		for _, d := range t.Deps {
			if p.Task(d) == nil && idPattern.MatchString(d) {
				v.task(t, "%s depends on unknown task %s; check the ID or remove it from Deps", t.ID, d)
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
	v.task(cycle[first], "dependency cycle %s (each task waits on the next); remove one of these dependencies", path)
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
