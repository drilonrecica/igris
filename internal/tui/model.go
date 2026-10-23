package tui

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/side"
	"github.com/drilonrecica/igris/internal/state"
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
	stateWaiting             // the agent waits on background work Claude Code resumes it for
)

// current is an active task, as its card shows it (SPEC §15.3).
type current struct {
	id, title, rank, model, mode string
	slot                         *int // the task's slot in a parallel phase (SPEC §6.11)
	user                         bool
	started                      time.Time
	state                        taskState
	since                        time.Time      // when state began
	detail                       string         // why it needs the owner
	reason                       string         // the run log's reason of Needs you (state.Reason*); "" before 1.7
	overdue                      string         // the overdue detail while the attempt runs past its Timeout
	rankMismatch                 string         // the rank audit's detail once the attempt ran on another model (SPEC §6.9)
	tokens                       *engine.Tokens // the attempt's token usage so far (SPEC §6.9, §15.1); nil until read
	waiting                      bool           // the agent waits on background work (SPEC §6.3)
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
	tone tone
	// arise marks the Arise. line (SPEC §15.1), the one line that isn't
	// an event: its task count is filled in once the plan is read.
	arise bool
}

// model is the TUI's state. Only Update changes it; View only records the
// zones of the frame it draws.
type model struct {
	ctx  context.Context
	opts Options

	width, height int

	// quota is the subscription quota the status line recorded (SPEC §6.3,
	// §15.1); nil until one is, and always without [usage] statusline.
	quota *state.Quota

	phase string
	// slice holds the IDs of a sliced run's tasks (SPEC §5.5), from
	// RunStarted; nil when the run takes its phases whole.
	slice map[string]bool
	// ariseTasks is the Arise. line's task count, once ariseCounted.
	ariseTasks   int
	ariseCounted bool
	mode         string
	paused       bool // pause-after-task is on
	holding      bool // the run holds because of the pause
	// cards are the active tasks in plan order (SPEC §15.1): one in a
	// sequential phase, up to max in a parallel one. cur is the selected
	// card, which o, d, s, r, t, M and y act on; nil with none.
	cards []*current
	cur   *current
	// asks are the questions waiting for an answer, one per task at most:
	// Needs you is one queue in plan order (SPEC §15.1).
	asks map[string]*engine.Event
	// resetAsk is the reset request waiting for the owner's answer
	// (SPEC §6.2); it can come while another question waits.
	resetAsk *engine.Event
	dialog   *dialog
	page     *page // help, details or the log, under any dialog
	log      []logEntry
	scroll   int // log lines scrolled back from the newest
	ended    bool
	endText  string
	// sum is what the run's events add up to, for the end-of-run summary
	// (summary.go).
	sum     summary
	plan    *plan.Plan // as last read; nil until loaded
	taskTop int        // first task list line shown; -1 follows the current task
	// autoTaskTop is where following the current task put the list in the
	// last frame; scrolling starts from there.
	autoTaskTop int
	folded      []button // actions behind the bar's More… in the last frame
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

	// tails are each card's session's last lines, cleaned, by task
	// (tail.go); tailBusy says a refresh is in flight, tailGen counts the
	// session changes so a late result for an earlier one is dropped.
	tails    map[string][]string
	tailBusy bool
	tailGen  int

	// side is the side sessions (side.go); nil without Options.Side.
	// worktree says the run works in a worktree of its own (SPEC §6.8),
	// so a side session in the project root shares no files with it.
	side     *sideUI
	worktree bool

	// lost says the connection to serve is lost until a fresh snapshot
	// (an attached view, attached.go); servedBy names the igris serving
	// the run when it is another version; leaveOnEnd says Stop run was
	// picked, so the view closes once the run has stopped.
	lost       bool
	servedBy   string
	leaveOnEnd bool
	// backend is the backend serve's sessions run on, from its snapshot;
	// "" means Options.Backend.
	backend string

	zones zones // of the last frame
	th    *theme
}

func newModel(ctx context.Context, opts Options) *model {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	m := &model{
		ctx: ctx, opts: opts, mode: opts.Mode, width: 80, height: 24, taskTop: -1, loc: time.Local,
		overrides: map[string]string{}, asks: map[string]*engine.Event{}, tails: map[string][]string{}, th: &theme{},
	}
	if opts.Side != nil {
		m.side = newSideUI(ctx, opts.Side, opts.Now, nil)
	}
	return m
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
	return tea.Batch(m.listen(false), tick(), m.loadPlan(), m.tailTick(), m.side.list())
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
	case engine.PhaseStarted, engine.PhaseDone, engine.TaskStarted, engine.TaskResumed, engine.TaskDone, engine.TaskSkipped, engine.TaskReset:
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
		reload := msg.snap != nil
		if msg.snap != nil {
			m.restore(*msg.snap)
		}
		for _, ev := range msg.events {
			m.event(ev)
			reload = reload || changesPlan(ev)
		}
		if msg.lost {
			m.connLost()
		}
		if msg.ended {
			m.ended = true
			m.lost = false // nothing is left to reconnect to
			if m.endText == "" {
				m.endText = "the run ended"
			}
			if m.sum.end.IsZero() {
				// No RunStopped came: the feed's result says how it ended.
				m.sum.end = m.opts.Now()
				m.sum.outcome = resultOutcome(m.opts.Feed.Result())
				m.sum.need(false, m.sum.end)
				m.endFocus()
			}
		}
		if m.leaveOnEnd && m.ended {
			return m, m.leave() // Stop run: it has stopped
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
		m.sortCards()
		m.refreshArise()
	case tickMsg:
		if m.notice != "" && m.opts.Now().Sub(m.noticeAt) >= noticeFor {
			m.notice = ""
		}
		return m, tick()
	case tailTickMsg:
		if msg.m == m {
			return m, m.refreshTail()
		}
	case tailMsg:
		if msg.m == m {
			m.gotTail(msg)
		}
	case focusErrMsg:
		m.addLog(m.opts.Now(), "could not open the session: "+msg.err.Error(), lookTitle)
	case sideMsg:
		if msg.ui != m.side {
			break
		}
		d, ok, cmd, lines := m.side.result(msg, m.dialog)
		if ok {
			m.dialog = d
		}
		for _, l := range lines {
			m.addLog(m.opts.Now(), l, lookTitle)
		}
		return m, cmd
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
	if k == "ctrl+c" {
		return m.activate(actDetach) // leaves at once: an attached run goes on
	}
	if m.page != nil && m.dialog == nil {
		// q leaves the screen it is on (SPEC §15.5): the page, here.
		if k == "q" || m.page.key(k) {
			m.page = nil
		}
		return nil
	}
	if k == "q" && (m.dialog == nil || !m.dialog.inField) {
		return m.activate(actQuit)
	}
	if m.dialog != nil {
		if m.side.owns(m.dialog) {
			if d, cmd, ok := m.side.key(msg); ok {
				m.dialog = d
				return cmd
			}
		}
		a, _ := m.dialog.key(msg)
		return m.pick(a)
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

// cycleFocus moves the focus to the next (or previous) region; the
// summary has only its bar.
func (m *model) cycleFocus(next bool) {
	if m.ended {
		m.focus = focusBar
		return
	}
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
		case t.act == actCardPick && m.dialog == nil:
			m.pickCard(t.option)
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
	if m.side.owns(d) {
		var cmd tea.Cmd
		m.dialog, cmd = m.side.pick(a)
		return cmd
	}
	if a == actSkipConfirm && d != nil && d.input != nil {
		reason := strings.TrimSpace(d.input.value)
		if reason == "" {
			d.inField, d.input.hint = true, "Type a reason to skip."
			return nil
		}
		m.dialog = nil
		m.opts.Sender.Send(engine.Command{Kind: engine.CmdSkip, Text: reason, Task: d.task})
		if d.answers != "" {
			m.answered(d.task) // the skip answers the question it came from
		}
		return nil
	}
	if a == actPlanRejectConfirm && d != nil && d.input != nil {
		feedback := strings.TrimSpace(d.input.value)
		if feedback == "" {
			d.inField, d.input.hint = true, "Type what the session should change."
			return nil
		}
		m.dialog = nil
		m.opts.Sender.Send(engine.Command{Kind: engine.CmdPlan, Plan: state.PlanDeny, Text: feedback, Task: d.task})
		m.answered(d.task)
		return nil
	}
	if a == actDoneConfirm && d != nil && d.input != nil {
		m.dialog = nil
		m.opts.Sender.Send(engine.Command{Kind: engine.CmdDone, Text: strings.TrimSpace(d.input.value), Task: d.task})
		return nil
	}
	if mode, ok := modeOf(a); ok && d != nil {
		m.dialog = nil
		if engine.SkipsPermissions(mode) {
			m.dialog = yoloDialog(d.task, mode) // never a single click or key
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
		m.opts.Sender.Send(modeCommand(d.task, d.mode, true))
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
	if m.lost && !whileLost(a) {
		return nil // nothing reaches the run until the view reconnects
	}
	switch a {
	case actNone, actOption:
		return nil
	case actQuit:
		if m.opts.Attached && !m.ended && !m.lost {
			m.dialog = detachDialog(m.opts.Leave != nil)
			return nil
		}
		return m.leave()
	case actDetach:
		return m.leave()
	case actStopLeave:
		m.leaveOnEnd = true
		m.opts.Sender.Send(engine.Command{Kind: engine.CmdStop})
		return nil
	case actOpen:
		return m.focusSession(m.cur)
	case actClose:
		m.dialog = nil
		return nil
	case actAnswer:
		if q := m.question(); q != nil {
			m.dialog = questionDialog(*q)
		}
		return nil
	case actAnswerCard:
		// The selected card's own question, else the first in line.
		q := m.question()
		if m.cur != nil && m.cardAsk(m.cur) != nil {
			q = m.cardAsk(m.cur)
		}
		if q != nil {
			m.dialog = questionDialog(*q)
		}
		return nil
	case actCardPrev, actCardNext:
		by := 1
		if a == actCardPrev {
			by = -1
		}
		m.moveCard(by)
		return nil
	case actMore:
		if len(m.folded) > 0 {
			m.dialog = moreDialog(m.folded)
		}
		return nil
	case actHelp:
		m.page = helpPage(m.th, m.opts.Attached)
		return nil
	case actHistory:
		if m.ended && m.opts.History != nil {
			return m.opts.History // the History page, over home (SPEC §15.3)
		}
		return nil
	case actSide:
		if m.side != nil {
			var cmd tea.Cmd
			m.dialog, cmd = m.side.open(m.sideAt())
			return cmd
		}
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
		if c := m.target(); c != nil && !m.ended {
			// After a lost session or a failed before_task hook there is
			// no session left to close.
			d := skipDialog(c.id, !c.user && c.session != nil)
			if m.dialog != nil && m.dialog.question != "" {
				d.answers = m.dialog.question
			}
			m.dialog = d
		}
		return nil
	case actPlanReject:
		if m.dialog != nil && m.dialog.question == engine.QuestionPlanApproval {
			m.dialog = planRejectDialog(m.dialog.task)
		}
		return nil
	case actRetry:
		if c := m.target(); c != nil && !c.user && !m.ended {
			m.dialog = retryDialog(c.id)
		}
		return nil
	case actCopy:
		m.copySelected()
		return nil
	case actDone:
		c := m.target()
		if c == nil {
			return nil
		}
		if c.user && !m.ended {
			m.dialog = doneDialog(c.id) // the owner's own work: a note may say how
			return nil
		}
	}
	resetQ := m.dialog != nil && m.dialog.question == engine.QuestionConfirmReset
	target := m.target()
	if c, ok := a.command(); ok {
		switch {
		case resetQ && c.Kind == engine.CmdAnswer:
			c.Question, c.Task = engine.QuestionConfirmReset, m.dialog.task
		case c.Kind != engine.CmdPause && c.Kind != engine.CmdStop && target != nil:
			// Addressed to its task: with several active, the
			// question's or the selected card's (SPEC §15.1).
			c.Task = target.id
		}
		m.opts.Sender.Send(c)
	}
	switch {
	case resetQ:
		// The other question, if one waits, comes back.
		m.dialog, m.resetAsk = nil, nil
		if q := m.firstAsk(); q != nil {
			m.dialog = questionDialog(*q)
		}
	case m.dialog != nil && m.dialog.question != "":
		// The engine has its answer; the events that follow say what it
		// did. The next question in line comes up.
		m.dialog = nil
		if target != nil {
			m.answered(target.id)
		}
	}
	if a == actPlanPane {
		return m.focusSession(target) // Claude Code's own plan dialog waits there
	}
	return nil
}

// target is the task an action is for: the open question's (or the
// dialog's that came from one), else the selected card's; nil when there
// is neither.
func (m *model) target() *current {
	if d := m.dialog; d != nil && (d.question != "" || d.answers != "") && d.task != "" {
		if c := m.card(d.task); c != nil {
			return c
		}
		return &current{id: d.task} // a task no card shows (anymore)
	}
	return m.cur
}

// answered forgets task id's question once the owner answered it, and
// brings up the next one in line unless a dialog is open (SPEC §15.1).
func (m *model) answered(id string) {
	delete(m.asks, id)
	if m.dialog == nil {
		if q := m.question(); q != nil {
			m.dialog = questionDialog(*q)
		}
	}
}

// sideAt is what a side session opened now can be about (SPEC §15.7): the
// selected task, else the current one, and the run going in this tree.
func (m *model) sideAt() sideAt {
	at := sideAt{place: sessionPlace(m.backendName())}
	if i := m.selected(); i >= 0 {
		at.task = m.phaseTasks()[i].ID
	} else if m.cur != nil {
		at.task = m.cur.id
	}
	if m.ended {
		return at
	}
	at.run = &SideRun{Worktree: m.worktree}
	for _, c := range m.cards {
		at.run.Parallel = at.run.Parallel || c.slot != nil
	}
	at.state = &side.RunInfo{Phase: m.phase}
	if c := m.cur; c != nil {
		at.running = m.card(at.task) != nil
		at.state.Task, at.state.Agent = c.id, m.stateText(c)
		if !c.user {
			at.run.Task = c.id // a user task has no session working on it
		}
	}
	return at
}

// backendName is the run's backend: the served run's, else the one this
// view was given (a view pushed by home has the config's, maybe `auto`).
func (m *model) backendName() string {
	if m.backend != "" {
		return m.backend
	}
	return m.opts.Backend
}

// sessionPlace is what a session opens in on backend: a tmux window or a
// herdr tab.
func sessionPlace(backend string) string {
	if backend == "tmux" {
		return "tmux window"
	}
	return "herdr tab"
}

// canOpen says whether Open session would do something for c: its
// session exists and this igris can reach its backend. An attached client
// outside herdr and tmux (ssh from a phone) can't, so it isn't offered
// (SPEC §15.3).
func (m *model) canOpen(c *current) bool {
	return c != nil && c.session != nil && m.opts.Focus != nil
}

// focusSession brings c's session's pane to the front.
func (m *model) focusSession(c *current) tea.Cmd {
	if !m.canOpen(c) {
		return nil
	}
	ref, id, focus, ctx := *c.session, c.id, m.opts.Focus, m.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := focus(ctx, ref, id); err != nil {
			return focusErrMsg{err}
		}
		return nil
	}
}

// event applies one engine event. A task's events change its own card.
func (m *model) event(ev engine.Event) {
	before := m.sessions()
	defer func() {
		if !maps.Equal(before, m.sessions()) {
			m.resetTail() // another session, or none, for some card
		}
	}()
	m.logEvent(ev)
	if ev.Phase != "" {
		m.phase = ev.Phase
	}
	if ev.Task == "" && len(m.cards) == 1 {
		ev.Task = m.cards[0].id // a task's event, from a sequential run
	}
	c := m.card(ev.Task)
	needed := c != nil && m.needs(c)
	defer func() {
		// The card that needs you is selected when Needs you starts, unless
		// the selected card itself needs you (SPEC §15.1).
		if c != nil && !needed && m.needs(c) && slices.Contains(m.cards, c) && (m.cur == nil || !m.needs(m.cur)) {
			m.selectCard(c)
		}
		m.sum.note(ev)
		m.sum.need(m.needsOwner() && !m.ended, ev.At)
	}()
	switch ev.Kind {
	case engine.RunStarted:
		m.slice = nil
		if ev.Slice != nil {
			m.slice = map[string]bool{}
			for _, id := range ev.Slice {
				m.slice[id] = true
			}
		}
		m.refreshArise() // the slice's count, now that it is known
	case engine.TaskStarted, engine.TaskResumed:
		if c == nil {
			c = &current{id: ev.Task, started: ev.At}
			m.addCard(c)
		}
		c.title, c.rank, c.model, c.mode, c.slot = ev.Title, ev.Rank, ev.Model, ev.Mode, ev.Slot
		c.user = ev.Model == ""
		if ev.Session != nil { // a resume reattached it
			c.session, c.claudeSession = ev.Session, ev.ClaudeSession
		}
		setState(c, stateWorking, ev)
		m.holding = false
		if c == m.cur {
			m.taskTop = -1
		}
		m.settle(ev.Task)
	case engine.SessionOpened:
		if c != nil {
			m.sum.fold(c.tokens) // the attempt before this one is over
			c.session, c.mode, c.claudeSession = ev.Session, ev.Mode, ev.ClaudeSession
			c.overdue, c.rankMismatch, c.tokens = "", "", nil // a new attempt
		}
	case engine.UsageRead:
		if c != nil {
			c.tokens = ev.Tokens
		}
	case engine.QuotaRead:
		m.quota = ev.Quota
	case engine.TaskOverdue:
		if c != nil {
			c.overdue = ev.Detail
		}
	case engine.RankMismatch:
		if c != nil {
			c.rankMismatch = ev.Detail
		}
	case engine.YourTurn:
		setState(c, stateYourTurn, ev)
	case engine.AgentWaiting:
		setState(c, stateWaiting, ev)
		if c != nil {
			c.waiting = true
		}
	case engine.AgentWaitingOver:
		if c != nil {
			c.waiting = false
			if c.state == stateWaiting {
				setState(c, stateWorking, ev)
			}
		}
	case engine.NeedsYou:
		if c != nil && c.waiting && c.state == stateWaiting {
			// Waited past waiting_limit: the time counts from the start.
			c.state, c.detail, c.reason = stateNeedsYou, ev.Detail, ev.Reason
			break
		}
		setState(c, stateNeedsYou, ev)
	case engine.NeedsYouClear, engine.VerifyFailed:
		setState(c, stateWorking, ev)
	case engine.VerifyStarted:
		setState(c, stateVerifying, ev)
	case engine.SessionLost:
		setState(c, stateLost, ev)
		if c != nil {
			c.session, c.overdue, c.rankMismatch = nil, "", "" // the attempt ended
		}
	case engine.Asked:
		e := ev
		if ev.Question == engine.QuestionConfirmReset {
			m.resetAsk = &e // the task's state is its own
			m.dialog = questionDialog(ev)
			break
		}
		m.asks[ev.Task] = &e
		setState(c, stateQuestion, ev)
		// It opens at once, but never over another task's question: that
		// one is answered first, and this one comes next (SPEC §15.1).
		if d := m.dialog; d == nil || (d.question == "" && d.answers == "") || d.task == ev.Task {
			m.dialog = questionDialog(ev)
		}
	case engine.PlanClosed:
		if q := m.asks[ev.Task]; q != nil && q.Question == engine.QuestionPlanApproval {
			delete(m.asks, ev.Task)
		}
		if d := m.dialog; d != nil && d.task == ev.Task && (d.question == engine.QuestionPlanApproval || d.answers == engine.QuestionPlanApproval) {
			m.dialog = nil
		}
		if c != nil && c.state == stateQuestion {
			setState(c, stateNeedsYou, ev) // until the agent works again
		}
	case engine.ResetDropped:
		m.dropResetAsk(ev.Task)
	case engine.Retrying:
		if c != nil {
			c.session, c.overdue, c.rankMismatch = nil, "", ""
		}
		setState(c, stateWorking, ev)
		m.settle(ev.Task)
	case engine.Committed, engine.NotCommitted:
		m.settle(ev.Task)
	case engine.TaskReset:
		m.dropResetAsk(ev.Task)
		if c != nil {
			m.sum.fold(c.tokens)
			m.dropCard(ev.Task) // the run let go of it and pauses
			m.settle(ev.Task)
			if m.dialog != nil && m.dialog.task == ev.Task {
				m.dialog = nil
			}
		}
	case engine.TaskDone, engine.TaskSkipped:
		if c != nil {
			m.sum.fold(c.tokens)
		}
		m.dropCard(ev.Task)
		m.settle(ev.Task)
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
	case engine.WorktreeReady:
		m.worktree = true
	case engine.RunStopped:
		m.ended = true
		m.endText = "the run stopped: " + ev.Detail
		clear(m.asks)
		if !m.side.owns(m.dialog) {
			m.dialog = nil // nothing left to ask or confirm; a side session isn't the run's
		}
		for _, c := range m.cards {
			m.sum.fold(c.tokens) // the attempts end with the run
			c.tokens = nil
		}
		m.endFocus()
	}
}

// endFocus puts the focus on the summary's bar with Home focused, so the
// outcome can be read and one key leaves (SPEC §15.3).
func (m *model) endFocus() {
	m.focus, m.barFocus = focusBar, actQuit
}

// needs reports whether c's task waits on the owner: its state, or a
// question for it.
func (m *model) needs(c *current) bool { return c.needsOwner() || m.cardAsk(c) != nil }

// logEvent adds ev's log lines. A run's first line is Arise. (SPEC
// §15.1): the phases, the task count and the mode, in bold accent, the
// one line that isn't an event.
func (m *model) logEvent(ev engine.Event) {
	if ev.Kind == engine.RunStarted {
		m.sum.phases = scopePhases(ev.Detail) // named before the line is written
		m.ariseCounted = false
		m.log = append(m.log, logEntry{at: ev.At, tone: lookAccentBold, arise: true})
		m.refreshArise()
		if len(m.log) > maxLog {
			m.log = m.log[len(m.log)-maxLog:]
		}
		return
	}
	for _, line := range logLines(ev) {
		m.addLog(ev.At, line, logLook(ev.Kind))
	}
}

// refreshArise writes the Arise. line's text: `Arise. phase M1 · 8 tasks
// · mode plan`, the count left out until the plan is read. It counts as
// serve and --no-tui do (SPEC §14): the slice's tasks for a sliced run
// (§5.5), else the unfinished tasks of the run's phases, with those this
// view saw the run finish before the plan was read.
func (m *model) refreshArise() {
	for i := range m.log {
		if !m.log[i].arise {
			continue
		}
		text := "Arise."
		if len(m.sum.phases) > 0 {
			text += " phase " + strings.Join(m.sum.phases, ", ")
		}
		switch {
		case m.slice != nil:
			text += " · " + look.Count(len(m.slice), "task", "tasks")
		case m.plan != nil:
			if !m.ariseCounted {
				// Counted once: the plan read later has the run's own
				// tasks done.
				m.ariseTasks = unfinishedTasks(m.plan, m.sum.phases) + m.sum.done + m.sum.skipped
				m.ariseCounted = true
			}
			text += " · " + look.Count(m.ariseTasks, "task", "tasks")
		}
		if m.mode != "" {
			text += " · mode " + m.mode
		}
		m.log[i].text = text
	}
}

func setState(c *current, s taskState, ev engine.Event) {
	if c == nil {
		return
	}
	c.state, c.since, c.detail, c.reason = s, ev.At, ev.Detail, ev.Reason
}

// settle drops task id's pending question: a later event answered it. A
// reset request is not the task's question and stays.
func (m *model) settle(id string) {
	delete(m.asks, id)
	if d := m.dialog; d != nil && d.question != "" && d.question != engine.QuestionConfirmReset && d.task == id {
		m.dialog = nil
	}
}

// question is the question Answer… opens: a reset request first, then the
// first of the queue in plan order (SPEC §15.1).
func (m *model) question() *engine.Event {
	if m.resetAsk != nil {
		return m.resetAsk
	}
	return m.firstAsk()
}

// firstAsk is the first pending question in plan order: the cards' first,
// then any for a task no card shows, oldest first.
func (m *model) firstAsk() *engine.Event {
	for _, c := range m.cards {
		if q := m.asks[c.id]; q != nil {
			return q
		}
	}
	var first *engine.Event
	for _, q := range m.asks {
		if first == nil || q.At.Before(first.At) || (q.At.Equal(first.At) && q.Task < first.Task) {
			first = q
		}
	}
	return first
}

// cardAsk is what c's task waits for the owner to answer: its reset
// request, else its question; nil when nothing.
func (m *model) cardAsk(c *current) *engine.Event {
	if m.resetAsk != nil && m.resetAsk.Task == c.id {
		return m.resetAsk
	}
	return m.asks[c.id]
}

// dropResetAsk forgets the reset request for task id once it is applied,
// declined or withdrawn.
func (m *model) dropResetAsk(id string) {
	if m.resetAsk == nil || m.resetAsk.Task != id {
		return
	}
	m.resetAsk = nil
	if m.dialog != nil && m.dialog.question == engine.QuestionConfirmReset {
		m.dialog = nil
		if q := m.firstAsk(); q != nil {
			m.dialog = questionDialog(*q)
		}
	}
}

func (m *model) addLog(at time.Time, text string, l tone) {
	m.log = append(m.log, logEntry{at: at, text: text, tone: l})
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
