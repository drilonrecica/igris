package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The wizard's Tasks step (SPEC §15.6, since 1.9) is `arise --only`,
// `--from` and `--until` (§5.5): All tasks, or a picker of the range's
// tasks with work. From goes on to Until, so both can be given, as on the
// command line. The slice is checked by engine.ResolveRange, as `arise`
// checks its flags, and travels in the request like the flags do.

// The Tasks step's choices, in order.
const (
	tasksAll = iota
	tasksOnly
	tasksFrom
	tasksUntil
)

// rangeWork is the tasks of the launch's phase range that are not
// satisfied, in plan order: the tasks a slice can pick from.
func (m *homeScreen) rangeWork() []*plan.Task {
	l, p := m.launch, m.snap.Plan
	if p == nil {
		return nil
	}
	phases, err := p.PhasesThrough(l.req.Phase, l.req.Through)
	if err != nil {
		return nil
	}
	var out []*plan.Task
	for _, ph := range phases {
		for _, t := range ph.Tasks {
			if !t.Status.Satisfied() {
				out = append(out, t)
			}
		}
	}
	return out
}

// tasksStep asks whether to run every task of the range or part of it.
// With a single task with work (or none) there is nothing to choose.
func (m *homeScreen) tasksStep() tea.Cmd {
	l := m.launch
	if len(m.rangeWork()) <= 1 {
		l.req.Selection = state.Selection{}
		return m.modeStep()
	}
	l.step = wizTasks
	scope := l.req.Phase
	if l.req.Through != "" {
		scope += " through " + l.req.Through
	}
	d := &dialog{
		title:  "Tasks",
		detail: "Run every task of " + scope + ", or part of them: only the tasks you pick, or from one task, until one, or both.",
		options: []option{
			{"All tasks", actWizTasks},
			{"Only…", actWizTasks},
			{"From…", actWizTasks},
			{"Until…", actWizTasks},
		},
		cancel: actClose,
	}
	switch sel := l.req.Selection; {
	case len(sel.Only) > 0:
		d.selected = tasksOnly
	case sel.From != "":
		d.selected = tasksFrom
	case sel.Until != "":
		d.selected = tasksUntil
	}
	m.dialog = d
	return nil
}

// taskLabel is a task in a picker: ID, title, status, rank and, when its
// dependencies aren't done, what it waits on.
func taskLabel(p *plan.Plan, t *plan.Task) string {
	out := textsafe.Line(t.ID + " " + t.Title + " · " + t.Status.String() + " · " + rank(t))
	if w := p.WaitingOn(t); w != nil {
		out += " · waits on " + textsafe.Line(strings.Join(w.Unmet, ", "))
	}
	return out
}

// picker is a dialog listing tasks, each picking act, then extra options;
// it remembers the IDs by option in the launch. The one named sel starts
// selected.
func (m *homeScreen) picker(title, detail string, tasks []*plan.Task, sel string, extra ...option) *dialog {
	l := m.launch
	d := &dialog{title: title, detail: detail, cancel: actWizBack}
	l.tasks = l.tasks[:0]
	for _, t := range tasks {
		if t.ID == sel {
			d.selected = len(d.options)
		}
		l.tasks = append(l.tasks, t.ID)
		d.options = append(d.options, option{taskLabel(m.snap.Plan, t), actWizTask})
	}
	d.options = append(d.options, extra...)
	return d
}

// onlyStep picks the tasks for --only: each task is picked or dropped in
// turn; Continue runs the ones picked, in plan order.
func (m *homeScreen) onlyStep() {
	l := m.launch
	l.step = wizOnly
	work := m.rangeWork()
	l.only = map[string]bool{}
	for _, id := range l.req.Selection.Only {
		l.only[id] = true
	}
	d := m.picker("", "enter picks a task or drops it; Continue runs the tasks picked, in plan order.", work, "",
		option{"Continue", actWizTasksOK}, option{"Back", actWizBack})
	for i := range work {
		d.options[i].label = m.onlyMark(l.tasks[i]) + d.options[i].label
	}
	d.title = m.onlyTitle()
	m.dialog = d
}

// onlyMark is a task's box in Only's picker.
func (m *homeScreen) onlyMark(id string) string {
	if m.launch.only[id] {
		return "[x] "
	}
	return "[ ] "
}

// onlyPicked is Only's picks in plan order, as `arise --only` lists them
// once resolved.
func (m *homeScreen) onlyPicked() []string {
	var ids []string
	for _, id := range m.launch.tasks {
		if m.launch.only[id] {
			ids = append(ids, id)
		}
	}
	return ids
}

// onlyTitle says how many tasks Only has picked.
func (m *homeScreen) onlyTitle() string {
	return "Only · " + plural(len(m.launch.only), "task") + " picked"
}

// fromStep picks the task for --from; Until is asked next.
func (m *homeScreen) fromStep() {
	l := m.launch
	l.step = wizFrom
	m.dialog = m.picker("From", "The run starts at the task you pick and goes on in plan order; Until comes next.",
		m.rangeWork(), l.req.Selection.From, option{"Back", actWizBack})
}

// untilStep picks the task for --until: after From's task, To the end of
// the range comes first; without From, every task of the range.
func (m *homeScreen) untilStep() {
	l := m.launch
	l.step = wizUntil
	work := m.rangeWork()
	from := l.req.Selection.From
	detail := "The run stops after the task you pick."
	if at := slices.IndexFunc(work, func(t *plan.Task) bool { return t.ID == from }); at >= 0 {
		work = work[at:]
		detail = "The run starts at " + textsafe.Line(from) + " and stops after the task you pick."
	}
	d := m.picker("Until", detail, work, l.req.Selection.Until, option{"Back", actWizBack})
	if from != "" {
		d.options = append([]option{{"To the end of the range", actWizTask}}, d.options...)
		l.tasks = append([]string{""}, l.tasks...)
		d.selected = max(slices.Index(l.tasks, l.req.Selection.Until), 0) // "" is the end
	}
	m.dialog = d
}

// slicePick runs what the owner picked in the Tasks step or its pickers.
func (m *homeScreen) slicePick(a action) tea.Cmd {
	l, d := m.launch, m.dialog
	switch l.step {
	case wizTasks:
		sel := l.req.Selection
		switch d.selected {
		case tasksAll:
			l.req.Selection = state.Selection{}
			return m.modeStep()
		case tasksOnly:
			m.onlyStep()
		case tasksFrom:
			l.req.Selection = state.Selection{From: sel.From, Until: sel.Until}
			m.fromStep()
		case tasksUntil:
			l.req.Selection = state.Selection{Until: sel.Until}
			m.untilStep()
		}
		return nil
	case wizOnly:
		switch a {
		case actWizTask:
			id := l.tasks[d.selected]
			if l.only[id] {
				delete(l.only, id)
			} else {
				l.only[id] = true
			}
			o := &d.options[d.selected]
			o.label = m.onlyMark(id) + o.label[len("[ ] "):]
			d.title = m.onlyTitle()
		case actWizTasksOK:
			if len(l.only) == 0 {
				// arise's usage error for an empty --only, in the title,
				// which always shows.
				d.title = "Only · pick at least one task"
				return nil
			}
			return m.sliceDone(state.Selection{Only: m.onlyPicked()})
		case actWizBack:
			// The picks stay for the next time Only… is chosen.
			l.req.Selection, l.only = state.Selection{Only: m.onlyPicked()}, nil
			return m.tasksStep()
		}
		return nil
	case wizFrom:
		if a == actWizBack {
			return m.tasksStep()
		}
		l.req.Selection.From = l.tasks[d.selected]
		m.untilStep()
		return nil
	case wizUntil:
		if a == actWizBack {
			if l.req.Selection.From != "" {
				m.fromStep()
				return nil
			}
			return m.tasksStep()
		}
		return m.sliceDone(state.Selection{From: l.req.Selection.From, Until: l.tasks[d.selected]})
	case wizSlice:
		return m.tasksStep()
	}
	return nil
}

// sliceDone checks sel against the plan as `arise` checks its flags
// (engine.ResolveRange) and goes on to the mode; a slice that can't run
// says why, in arise's words, and goes back to the Tasks step.
func (m *homeScreen) sliceDone(sel state.Selection) tea.Cmd {
	l := m.launch
	if _, err := engine.ResolveRange(m.snap.Plan, l.req.Phase, l.req.Through, sel); err != nil {
		l.step = wizSlice
		m.dialog = &dialog{
			title:   "This slice can't run",
			detail:  textsafe.Line(err.Error()),
			options: []option{{"Back", actWizBack}},
			cancel:  actWizBack,
		}
		return nil
	}
	l.req.Selection, l.only = sel, nil
	return m.modeStep()
}
