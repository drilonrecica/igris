package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
)

// maxLog caps the log kept in memory; the full record is .igris/runs.jsonl.
const maxLog = 500

// taskState is what the current task is doing, as far as the owner cares.
type taskState int

const (
	stateWorking   taskState = iota
	stateNeedsYou            // the session waits on the owner
	stateYourTurn            // a user task
	stateVerifying           // the verify command runs
	stateQuestion            // igris asked the owner something
	stateLost                // the session is gone
)

// current is the task the run works on.
type current struct {
	id, title, rank, model, mode string
	user                         bool
	started                      time.Time
	state                        taskState
	since                        time.Time // when state began
	detail                       string    // why it needs the owner
	session                      *backend.SessionRef
}

type logEntry struct {
	at   time.Time
	text string
}

// model is the TUI's state. Only Update changes it; View only records the
// zones of the frame it draws.
type model struct {
	ctx  context.Context
	opts Options

	width, height int

	phase   string
	mode    string
	paused  bool // pause-after-task is on
	holding bool // the run holds because of the pause
	cur     *current
	asked   *engine.Event // the question waiting for an answer
	dialog  *dialog
	log     []logEntry
	scroll  int // log lines scrolled back from the newest
	ended   bool
	endText string
	plan    *plan.Plan // as last read; nil until loaded
	taskTop int        // first task list line shown; -1 follows the current task
	// autoTaskTop is where following the current task put the list in the
	// last frame; scrolling starts from there.
	autoTaskTop int
	loc         *time.Location

	zones zones // of the last frame
}

func newModel(ctx context.Context, opts Options) *model {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &model{ctx: ctx, opts: opts, mode: opts.Mode, width: 80, height: 24, taskTop: -1, loc: time.Local}
}

// Messages besides the feed's batches.
type (
	tickMsg     struct{}
	focusErrMsg struct{ err error }
	planMsg     struct {
		p   *plan.Plan
		err error
	}
)

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.listen(false), tick(), m.loadPlan())
}

// loadPlan reads the plan for the task list. The engine writes it
// atomically, so a read never sees half a write.
func (m *model) loadPlan() tea.Cmd {
	if m.opts.PlanPath == "" {
		return nil
	}
	path, opts := m.opts.PlanPath, m.opts.PlanOptions
	return func() tea.Msg {
		p, err := plan.Load(path, opts)
		return planMsg{p, err}
	}
}

// changesPlan reports whether ev means the plan's statuses or the phase
// shown changed.
func changesPlan(ev engine.Event) bool {
	switch ev.Kind {
	case engine.PhaseStarted, engine.PhaseDone, engine.TaskStarted, engine.TaskResumed, engine.TaskDone, engine.TaskSkipped:
		return true
	}
	return len(ev.Changes) > 0
}

// listen waits for the next batch from the feed.
func (m *model) listen(endSeen bool) tea.Cmd {
	return func() tea.Msg {
		b, ok := m.opts.Feed.next(m.ctx, endSeen)
		if !ok {
			return nil
		}
		return b
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case batch:
		reload := false
		for _, ev := range msg.events {
			m.event(ev)
			reload = reload || changesPlan(ev)
		}
		if msg.ended {
			m.ended = true
			if m.endText == "" {
				m.endText = "the run ended"
			}
		}
		if reload {
			return m, tea.Batch(m.listen(m.ended), m.loadPlan())
		}
		return m, m.listen(m.ended)
	case planMsg:
		if msg.err != nil {
			m.addLog(m.opts.Now(), "could not read the plan: "+msg.err.Error())
			break
		}
		m.plan = msg.p
	case tickMsg:
		return m, tick()
	case focusErrMsg:
		m.addLog(m.opts.Now(), "could not open the session: "+msg.err.Error())
	case tea.KeyMsg:
		return m, m.key(msg.String())
	case tea.MouseMsg:
		return m, m.mouse(msg)
	}
	return m, nil
}

// key handles a key press: an open dialog gets it first.
func (m *model) key(k string) tea.Cmd {
	if m.dialog != nil {
		if k == "ctrl+c" || k == "q" {
			return m.activate(actQuit)
		}
		a, _ := m.dialog.key(k)
		return m.activate(a)
	}
	switch k {
	case "pgup":
		m.scrollLog(m.logRows())
		return nil
	case "pgdown":
		m.scrollLog(-m.logRows())
		return nil
	}
	return m.activate(shortcuts[k])
}

// mouse handles clicks and the wheel.
func (m *model) mouse(msg tea.MouseMsg) tea.Cmd {
	t, ok := m.zones.at(msg.X, msg.Y)
	switch {
	case msg.Button == tea.MouseButtonWheelUp, msg.Button == tea.MouseButtonWheelDown:
		by := 3
		if msg.Button == tea.MouseButtonWheelDown {
			by = -3
		}
		switch {
		case ok && t.region == regionLog:
			m.scrollLog(by)
		case ok && t.region == regionTasks:
			m.scrollTasks(-by)
		}
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && ok:
		if t.act == actOption {
			if m.dialog == nil {
				return nil
			}
			return m.activate(m.dialog.pick(t.option))
		}
		if m.dialog != nil {
			return nil // the dialog is modal
		}
		return m.activate(t.act)
	}
	return nil
}

// activate runs an action, whether it came from a key, a click or a
// dialog.
func (m *model) activate(a action) tea.Cmd {
	switch a {
	case actNone, actOption:
		return nil
	case actQuit:
		return tea.Quit
	case actOpen:
		return m.focusSession()
	case actClose:
		m.dialog = nil
		return nil
	case actAnswer:
		if m.asked != nil {
			m.dialog = questionDialog(*m.asked)
		}
		return nil
	case actDone:
		if m.cur == nil {
			return nil
		}
	}
	if c, ok := a.command(); ok {
		m.opts.Sender.Send(c)
	}
	if m.dialog != nil && m.dialog.question != "" {
		// The engine has its answer; the events that follow say what it did.
		m.dialog, m.asked = nil, nil
	}
	return nil
}

// focusSession brings the current session's pane to the front.
func (m *model) focusSession() tea.Cmd {
	if m.cur == nil || m.cur.session == nil || m.opts.Focus == nil {
		return nil
	}
	ref, focus, ctx := *m.cur.session, m.opts.Focus, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := focus(ctx, ref); err != nil {
			return focusErrMsg{err}
		}
		return nil
	}
}

// event applies one engine event.
func (m *model) event(ev engine.Event) {
	for _, line := range logLines(ev) {
		m.addLog(ev.At, line)
	}
	if ev.Phase != "" {
		m.phase = ev.Phase
	}
	switch ev.Kind {
	case engine.TaskStarted, engine.TaskResumed:
		if m.cur == nil || m.cur.id != ev.Task {
			m.cur = &current{id: ev.Task, started: ev.At}
		}
		m.cur.title, m.cur.rank, m.cur.model, m.cur.mode = ev.Title, ev.Rank, ev.Model, ev.Mode
		m.cur.user = ev.Model == ""
		m.setState(stateWorking, ev)
		m.holding = false
		m.taskTop = -1
		m.settle()
	case engine.SessionOpened:
		if m.cur != nil {
			m.cur.session, m.cur.mode = ev.Session, ev.Mode
		}
	case engine.YourTurn:
		m.setState(stateYourTurn, ev)
	case engine.NeedsYou:
		m.setState(stateNeedsYou, ev)
	case engine.NeedsYouClear, engine.VerifyFailed:
		m.setState(stateWorking, ev)
	case engine.VerifyStarted:
		m.setState(stateVerifying, ev)
	case engine.SessionLost:
		m.setState(stateLost, ev)
		if m.cur != nil {
			m.cur.session = nil
		}
	case engine.Asked:
		e := ev
		m.asked = &e
		m.dialog = questionDialog(ev)
		m.setState(stateQuestion, ev)
	case engine.Retrying:
		if m.cur != nil {
			m.cur.session = nil
		}
		m.setState(stateWorking, ev)
		m.settle()
	case engine.Committed, engine.NotCommitted:
		m.settle()
	case engine.TaskDone, engine.TaskSkipped:
		m.cur = nil
		m.settle()
	case engine.PauseOn:
		m.paused = true
	case engine.PauseOff:
		m.paused, m.holding = false, false
	case engine.Paused:
		m.holding = true
	case engine.ModeChanged:
		m.mode = ev.Detail
	case engine.RunStopped:
		m.ended = true
		m.endText = "the run stopped: " + ev.Detail
		m.settle()
	}
}

func (m *model) setState(s taskState, ev engine.Event) {
	if m.cur == nil {
		return
	}
	m.cur.state, m.cur.since, m.cur.detail = s, ev.At, ev.Detail
}

// settle drops a pending engine question: a later event answered it.
func (m *model) settle() {
	m.asked = nil
	if m.dialog != nil && m.dialog.question != "" {
		m.dialog = nil
	}
}

func (m *model) addLog(at time.Time, text string) {
	m.log = append(m.log, logEntry{at, text})
	if len(m.log) > maxLog {
		m.log = m.log[len(m.log)-maxLog:]
	}
	if m.scroll > 0 {
		m.scroll++ // keep the lines the owner scrolled to in place
	}
}

func (m *model) scrollLog(by int) {
	m.scroll = min(max(m.scroll+by, 0), max(len(m.log)-1, 0))
}

// scrollTasks moves the task list by lines; it stops following the current
// task until the next one starts.
func (m *model) scrollTasks(by int) {
	if m.taskTop < 0 {
		m.taskTop = m.autoTaskTop
	}
	m.taskTop = max(m.taskTop+by, 0)
}
