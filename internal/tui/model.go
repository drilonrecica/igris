package tui

import (
	"context"
	"strings"
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
	claudeSession                string // the session's Claude UUID, for claude --resume
}

// area is a focus region (SPEC §15.5); tab moves between them.
type area int

const (
	focusBar area = iota // the default
	focusLog
	focusTasks
)

// areaOrder is the tab order: task list, action bar, log.
var areaOrder = []area{focusTasks, focusBar, focusLog}

type logEntry struct {
	at   time.Time
	text string
	look look
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
	page    *page // help, details or the log, under any dialog
	log     []logEntry
	scroll  int // log lines scrolled back from the newest
	ended   bool
	endText string
	plan    *plan.Plan // as last read; nil until loaded
	taskTop int        // first task list line shown; -1 follows the current task
	// autoTaskTop is where following the current task put the list in the
	// last frame; scrolling starts from there.
	autoTaskTop int
	folded      []option // actions behind the bar's More… in the last frame
	loc         *time.Location

	focus area
	// barFocus is the focused bar button, by action so it survives buttons
	// coming and going; actNone means the first one.
	barFocus action
	bar      []action // the bar's buttons in the last frame, in order
	// selID is the selected task in the task list; "" means the current
	// one (or the first).
	selID string
	// overrides are the per-task modes the owner chose, as the engine
	// confirmed them.
	overrides map[string]string

	notice   string // what the last y copied, shown in the header
	noticeAt time.Time

	zones zones // of the last frame
	th    *theme
}

func newModel(ctx context.Context, opts Options) *model {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &model{ctx: ctx, opts: opts, mode: opts.Mode, width: 80, height: 24, taskTop: -1, loc: time.Local, overrides: map[string]string{}, th: &theme{}}
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
			m.addLog(m.opts.Now(), "could not read the plan: "+msg.err.Error(), lookTitle)
			break
		}
		m.plan = msg.p
	case tickMsg:
		if m.notice != "" && m.opts.Now().Sub(m.noticeAt) >= noticeFor {
			m.notice = ""
		}
		return m, tick()
	case focusErrMsg:
		m.addLog(m.opts.Now(), "could not open the session: "+msg.err.Error(), lookTitle)
	case tea.KeyMsg:
		return m, m.key(msg)
	case tea.MouseMsg:
		return m, m.mouse(msg)
	}
	return m, nil
}

// key handles a key press: an open dialog gets it first, then an open
// page, then the focused region; shortcut keys work outside text fields.
func (m *model) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	m.notice = "" // a notice lasts until the next key
	if k == "ctrl+c" || (k == "q" && (m.dialog == nil || !m.dialog.inField)) {
		return m.activate(actQuit)
	}
	if m.dialog != nil {
		a, _ := m.dialog.key(msg)
		return m.pick(a)
	}
	if m.page != nil {
		if m.page.key(k) {
			m.page = nil
		}
		return nil
	}
	switch k {
	case "tab", "shift+tab":
		m.cycleFocus(k == "tab")
	case "enter", " ":
		switch m.focus {
		case focusBar:
			return m.activate(m.focusedButton())
		case focusTasks:
			if i := m.selected(); i >= 0 {
				m.page = m.detailPage(m.phaseTasks()[i].ID)
			}
		case focusLog:
			m.page = m.logPage()
		}
	case "left", "right":
		if m.focus == focusBar {
			m.moveBar(k == "right")
		}
	case "up", "k":
		m.arrow(-1)
	case "down", "j":
		m.arrow(1)
	case "pgup":
		m.pageArea(-1)
	case "pgdown":
		m.pageArea(1)
	default:
		return m.activate(shortcuts[k])
	}
	return nil
}

// arrow moves within the focused region: the task selection, or the log
// one line (by > 0 is down, towards the newest).
func (m *model) arrow(by int) {
	switch m.focus {
	case focusTasks:
		m.moveSel(by)
	case focusLog:
		m.scrollLog(-by)
	}
}

// pageArea scrolls the task list when it has the focus, else the log, by
// a screenful.
func (m *model) pageArea(by int) {
	if m.focus == focusTasks {
		m.scrollTasks(by * max(m.height/3, 1))
		return
	}
	m.scrollLog(-by * m.logRows())
}

// cycleFocus moves the focus to the next (or previous) region.
func (m *model) cycleFocus(next bool) {
	i := 0
	for j, a := range areaOrder {
		if a == m.focus {
			i = j
		}
	}
	if next {
		i++
	} else {
		i += len(areaOrder) - 1
	}
	m.focus = areaOrder[i%len(areaOrder)]
	if m.focus == focusTasks {
		m.taskTop = -1 // bring the selection into view
	}
}

// focusedButton is the action of the focused bar button in the last frame.
func (m *model) focusedButton() action {
	for _, a := range m.bar {
		if a == m.barFocus {
			return a
		}
	}
	if len(m.bar) > 0 {
		return m.bar[0]
	}
	return actNone
}

// moveBar moves the bar focus one button along, wrapping around.
func (m *model) moveBar(right bool) {
	if len(m.bar) == 0 {
		return
	}
	i := 0
	for j, a := range m.bar {
		if a == m.focusedButton() {
			i = j
		}
	}
	if right {
		i++
	} else {
		i += len(m.bar) - 1
	}
	m.barFocus = m.bar[i%len(m.bar)]
}

// selected is the selected task's index in the phase's tasks: the one
// picked, else the current one, else the first; -1 when there are none.
func (m *model) selected() int {
	tasks := m.phaseTasks()
	id := m.selID
	if id == "" && m.cur != nil {
		id = m.cur.id
	}
	for i, t := range tasks {
		if t.ID == id {
			return i
		}
	}
	if len(tasks) > 0 {
		return 0
	}
	return -1
}

// moveSel moves the task selection by tasks, keeping it in the list.
func (m *model) moveSel(by int) {
	tasks := m.phaseTasks()
	i := m.selected()
	if i < 0 {
		return
	}
	i = min(max(i+by, 0), len(tasks)-1)
	m.selID = tasks[i].ID
	m.taskTop = -1
}

// mouse handles clicks and the wheel.
func (m *model) mouse(msg tea.MouseMsg) tea.Cmd {
	t, ok := m.zones.at(msg.X, msg.Y)
	if m.page != nil && m.dialog == nil {
		return m.pageMouse(msg, t, ok)
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp, msg.Button == tea.MouseButtonWheelDown:
		by := 3
		if msg.Button == tea.MouseButtonWheelDown {
			by = -3
		}
		switch m.zones.regionAt(msg.X, msg.Y) {
		case regionLog:
			m.scrollLog(by)
		case regionTasks:
			m.scrollTasks(-by)
		}
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && ok:
		switch {
		case t.act == actTaskRow && m.dialog == nil:
			m.clickTask(t.option)
			return nil
		case t.act == actField && m.dialog != nil:
			m.dialog.inField = true
			return nil
		case t.act == actOption && m.dialog != nil:
			return m.pick(m.dialog.pick(t.option))
		case t.act == actOption || t.act == actField:
			return nil
		}
		if m.dialog != nil {
			return nil // the dialog is modal
		}
		return m.activate(t.act)
	}
	return nil
}

// clickTask selects the i-th task of the phase and opens its details.
func (m *model) clickTask(i int) {
	tasks := m.phaseTasks()
	if i < 0 || i >= len(tasks) {
		return
	}
	m.focus, m.selID = focusTasks, tasks[i].ID
	m.page = m.detailPage(tasks[i].ID)
}

// pageMouse handles the mouse while a page is open: the wheel scrolls it
// wherever the pointer is, and Close closes it.
func (m *model) pageMouse(msg tea.MouseMsg, t target, ok bool) tea.Cmd {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		m.page.scroll(-3)
	case msg.Button == tea.MouseButtonWheelDown:
		m.page.scroll(3)
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress && ok && t.act == actClose:
		m.page = nil
	}
	return nil
}

// pick runs an action chosen in the open dialog. A dialog the TUI opened
// itself (More…, Stop, Skip…) closes once something is picked.
func (m *model) pick(a action) tea.Cmd {
	d := m.dialog
	if a == actSkipConfirm && d != nil && d.input != nil {
		reason := strings.TrimSpace(d.input.value)
		if reason == "" {
			d.inField, d.input.hint = true, "Type a reason to skip."
			return nil
		}
		m.dialog = nil
		if d.answers != "" {
			m.asked = nil // the skip answers the question it came from
		}
		m.opts.Sender.Send(engine.Command{Kind: engine.CmdSkip, Text: reason})
		return nil
	}
	if mode, ok := modeOf(a); ok && d != nil {
		m.dialog = nil
		if mode == engine.ModeYolo {
			m.dialog = yoloDialog(d.task) // never a single click or key
			return nil
		}
		m.opts.Sender.Send(modeCommand(d.task, mode, false))
		return nil
	}
	if a == actYoloConfirm && d != nil && d.input != nil {
		if strings.TrimSpace(d.input.value) != engine.YoloPhrase {
			d.inField, d.input.hint = true, "Type exactly: "+engine.YoloPhrase
			return nil
		}
		m.dialog = nil
		m.opts.Sender.Send(modeCommand(d.task, engine.ModeYolo, true))
		return nil
	}
	if a != actNone && d != nil && d.question == "" {
		m.dialog = nil
	}
	return m.activate(a)
}

// modeCommand sets the run mode, or task's mode when task isn't "".
func modeCommand(task, mode string, typed bool) engine.Command {
	if task == "" {
		return engine.Command{Kind: engine.CmdMode, Text: mode, Yes: typed}
	}
	return engine.Command{Kind: engine.CmdTaskMode, Task: task, Text: mode, Yes: typed}
}

// taskMode is the mode t's next session would run in.
func (m *model) taskMode(t *plan.Task) string { return m.rows().taskMode(t) }

// taskModeTarget is the selected task when Task mode applies to it: nil once
// the run is over, and for user tasks, which have no session.
func (m *model) taskModeTarget() *plan.Task {
	i := m.selected()
	if i < 0 || m.ended {
		return nil
	}
	if t := m.phaseTasks()[i]; t.Owner.IsAgent() {
		return t
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
		if m.opts.Leave != nil {
			return m.opts.Leave
		}
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
	case actMore:
		if len(m.folded) > 0 {
			m.dialog = moreDialog(m.folded)
		}
		return nil
	case actHelp:
		m.page = helpPage(m.th)
		return nil
	case actDetails:
		if i := m.selected(); i >= 0 {
			m.page = m.detailPage(m.phaseTasks()[i].ID)
		}
		return nil
	case actMode:
		if !m.ended {
			m.dialog = modeDialog("", m.mode)
		}
		return nil
	case actTaskMode:
		if t := m.taskModeTarget(); t != nil {
			m.dialog = modeDialog(t.ID, m.taskMode(t))
		}
		return nil
	case actStopAsk:
		if !m.ended {
			m.dialog = stopDialog()
		}
		return nil
	case actSkip:
		if m.cur != nil && !m.ended {
			d := skipDialog(m.cur.id, !m.cur.user)
			if m.dialog != nil && m.dialog.question != "" {
				d.answers = m.dialog.question
			}
			m.dialog = d
		}
		return nil
	case actRetry:
		if m.cur != nil && !m.cur.user && !m.ended {
			m.dialog = retryDialog(m.cur.id)
		}
		return nil
	case actCopy:
		m.copySelected()
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
		m.addLog(ev.At, line, logLook(ev.Kind))
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
			m.cur.session, m.cur.mode, m.cur.claudeSession = ev.Session, ev.Mode, ev.ClaudeSession
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
		if m.dialog != nil && m.dialog.task == ev.Task {
			m.dialog = nil // it asked about a task that is over
		}
	case engine.PauseOn:
		m.paused = true
	case engine.PauseOff:
		m.paused, m.holding = false, false
	case engine.Paused:
		m.holding = true
	case engine.ModeChanged:
		m.mode = ev.Detail
	case engine.TaskModeChanged:
		m.overrides[ev.Task] = ev.Detail
	case engine.RunStopped:
		m.ended = true
		m.endText = "the run stopped: " + ev.Detail
		m.settle()
		m.dialog = nil // nothing left to ask or confirm
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

func (m *model) addLog(at time.Time, text string, l look) {
	m.log = append(m.log, logEntry{at, text, l})
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
