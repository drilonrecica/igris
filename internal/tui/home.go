package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/report"
)

// homeScreen is the bottom of the app's stack (SPEC §15.6): the project at
// a glance. It reads the project when it opens, asks herdr and doctor off
// the loop, and shows PHASES, the NOW card, HEALTH, RECENT, a status line
// and the action bar. It never writes anything itself.
type homeScreen struct {
	ctx  context.Context
	svc  Services
	th   *theme
	w, h int
	now  func() time.Time
	loc  *time.Location

	snap    *report.Snapshot // nil until the first read is back
	snapErr error
	// poll is the interval of the stat poll; 0 means no poll. stamp is
	// the watched files as the last read found them; reading says a read
	// is on its way, so the poll doesn't start another.
	poll    time.Duration
	stamp   report.Stamp
	reading bool
	// backendErr is why herdr can't host sessions; backendKnown says the
	// check is back (it is slow, so the header shows "herdr …" until then).
	backendErr   error
	backendKnown bool
	doctor       []checks.Result
	doctorKnown  bool

	focus homeFocus
	// phaseID is the selected phase: the wizard's default. phaseTop is
	// the first phase row drawn, or -1 to follow the selection.
	phaseID  string
	phaseTop int
	// autoTop is the top row chosen to follow the selection in the last
	// frame; rowsShown how many phase rows that frame had.
	autoTop, rowsShown int
	lineSel            int    // the selected HEALTH/RECENT line
	barFocus           action // the bar button with the focus
	// barMoved says the owner moved along the bar; until then the focus
	// follows the state's own action (the first button).
	barMoved bool
	bar      []action
	folded   []option // the actions behind More… in the last frame
	lines    []homeLine
	dialog   *dialog // More…

	status   string // the last action's result
	statusAt time.Time
	zones    zones
}

// homeFocus is a focus region of home. Tab order: PHASES → the action bar
// → the HEALTH/RECENT lines.
type homeFocus int

const (
	homeBar homeFocus = iota // the default: the state's own action
	homeLines
	homePhases
)

var homeOrder = []homeFocus{homePhases, homeBar, homeLines}

// homeLine is a HEALTH or RECENT line; act is the page it opens. short is
// the line when several share a row (narrow), "" for the text itself.
type homeLine struct {
	text, short string
	act         action
}

// Messages for home's async reads.
type (
	snapshotMsg struct {
		s   *report.Snapshot
		err error
	}
	backendMsg struct{ err error }
	doctorMsg  struct{ results []checks.Result }
	// pollMsg is the poll's tick; stampMsg is what the stat found.
	pollMsg  struct{}
	stampMsg struct{ s report.Stamp }
	// refreshMsg asks home to read the project again now: the app sends it
	// when a screen it pushed ends or the terminal gets the focus back,
	// and pages send it when they changed a file (editor, init, adapt).
	refreshMsg struct{}
	// configMsg is the [tui] config of a good igris.toml, for the app to
	// apply live.
	configMsg struct{ tui config.TUI }
)

// pollEvery is the stat poll's default interval (SPEC §15.6).
const pollEvery = 2 * time.Second

func newHome(ctx context.Context, svc Services, th *theme) *homeScreen {
	return &homeScreen{ctx: ctx, svc: svc, th: th, w: 80, h: 24, now: time.Now, loc: time.Local, phaseTop: -1}
}

// Init reads the project, then asks for the slow checks.
func (m *homeScreen) Init() tea.Cmd {
	return tea.Batch(m.refresh(), m.checkBackend(), m.runDoctor(), m.nextPoll())
}

// nextPoll schedules the next stat poll. The tick goes through the app to
// home wherever it is on the stack, so the poll goes on under a page.
func (m *homeScreen) nextPoll() tea.Cmd {
	if m.svc == nil || m.poll <= 0 {
		return nil
	}
	return tea.Tick(m.poll, func(time.Time) tea.Msg { return ownedMsg{m, pollMsg{}} })
}

// statFiles stats the watched files off the loop and reports them to
// home, wherever it is on the stack.
func (m *homeScreen) statFiles() tea.Cmd {
	svc := m.svc
	return func() tea.Msg { return ownedMsg{m, stampMsg{svc.Stamp()}} }
}

// refresh reads the project again, off the program's loop.
func (m *homeScreen) refresh() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	ctx, svc := m.ctx, m.svc
	m.reading = true
	return async(m, "snapshot", func() tea.Msg {
		s, err := svc.Snapshot(ctx)
		return snapshotMsg{s, err}
	})
}

// checkBackend asks whether herdr can host sessions (slow: it runs herdr).
func (m *homeScreen) checkBackend() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	ctx, svc := m.ctx, m.svc
	return async(m, "backend", func() tea.Msg { return backendMsg{svc.BackendAvailable(ctx)} })
}

// runDoctor runs doctor's checks off the loop.
func (m *homeScreen) runDoctor() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	ctx, svc := m.ctx, m.svc
	return async(m, "doctor", func() tea.Msg { return doctorMsg{svc.Doctor(ctx)} })
}

func (m *homeScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"a", "Arise… / Resume…", "start a run, or resume the last one (when the plan and igris.toml are valid and herdr is reachable)"},
		{"v", "Preview", "the dry run: what a run would do"},
		{"c", "Check", "the plan's problems and warnings"},
		{"i", "Doctor", "what is wrong with the setup, and what fixes it"},
		{"h", "History", "the last runs, their tasks and attempts"},
		{"e", "Edit plan", "open the plan in your editor"},
		{",", "Settings", "the effective igris.toml; edit it from there"},
		{"n", "Notify test", "send a test message on every channel"},
		{"A", "Adapt", "let Claude propose a canonical plan (when the plan is invalid)"},
		{"I", "Init", "create igris.toml and .igris/ (when there is no igris.toml)"},
		{"o", "Open session", "bring the running session's pane to the front"},
		{"?", "Help", "this page"},
		{"q", "Quit", "quit igris"},
	}
}

// homeShortcuts maps home's keys to their actions (SPEC §15.6).
var homeShortcuts = map[string]action{
	"a": actArise, "v": actPreview, "c": actCheck, "i": actDoctor, "h": actHistory,
	"e": actEdit, ",": actSettings, "n": actNotify, "A": actAdapt, "I": actInit, "o": actOpen,
	"?": actHelp, "q": actQuit,
}

func (m *homeScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case pollMsg:
		return m, tea.Batch(m.statFiles(), m.nextPoll())
	case stampMsg:
		// Reload only when a watched file changed since the last read; a
		// read going on will see it, and the next poll checks again.
		if !m.reading && msg.s != m.stamp {
			return m, m.refresh()
		}
	case refreshMsg:
		if !m.reading {
			return m, m.refresh()
		}
	case snapshotMsg:
		m.reading = false
		m.snap, m.snapErr = msg.s, msg.err
		m.keepSelection()
		if msg.s != nil {
			m.stamp = msg.s.Stamp
			if msg.s.Config != nil && len(msg.s.ConfigProblems) == 0 {
				tui := msg.s.Config.TUI
				return m, func() tea.Msg { return configMsg{tui} }
			}
		}
	case backendMsg:
		m.backendErr, m.backendKnown = msg.err, true
	case doctorMsg:
		m.doctor, m.doctorKnown = msg.results, true
	case tea.KeyMsg:
		return m, m.key(msg)
	case tea.MouseMsg:
		return m, m.mouse(msg)
	}
	return m, nil
}

// keepSelection keeps the selected phase across reads; when it is gone (or
// nothing was selected yet) the phase with work is selected.
func (m *homeScreen) keepSelection() {
	phases := m.phases()
	if m.phaseID != "" {
		for _, ph := range phases {
			if ph.ID == m.phaseID {
				return
			}
		}
	}
	m.phaseID = ""
	for _, ph := range phases {
		if ph.Outcome == "next" {
			m.phaseID = ph.ID
			return
		}
	}
	if len(phases) > 0 {
		m.phaseID = phases[0].ID
	}
}

// phases are the plan's phases as status reports them; none for an
// invalid or missing plan.
func (m *homeScreen) phases() []report.PhaseStatus {
	if m.snap == nil || m.snap.Status == nil {
		return nil
	}
	return m.snap.Status.Phases
}

// selectedPhase is the selected phase's ID, "" when there are none.
func (m *homeScreen) selectedPhase() string { return m.phaseID }

// phaseIndex is the selected phase's index, -1 for none.
func (m *homeScreen) phaseIndex() int {
	for i, ph := range m.phases() {
		if ph.ID == m.phaseID {
			return i
		}
	}
	return -1
}

// key handles a key press: an open dialog gets it first, then the focused
// region; shortcut keys work from anywhere.
func (m *homeScreen) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if m.dialog != nil {
		a, _ := m.dialog.key(msg)
		return m.pick(a)
	}
	switch k {
	case "q":
		return quitApp
	case "esc":
		return nil // home is the bottom of the stack
	case "tab", "shift+tab":
		m.cycleFocus(k == "tab")
	case "enter", " ":
		switch m.focus {
		case homeBar:
			return m.activate(m.focusedButton())
		case homePhases:
			return m.openPhase()
		case homeLines:
			if m.lineSel >= 0 && m.lineSel < len(m.lines) {
				return m.activate(m.lines[m.lineSel].act)
			}
		}
	case "left", "right":
		if m.focus == homeBar {
			m.moveBar(k == "right")
		}
	case "up", "k":
		m.arrow(-1)
	case "down", "j":
		m.arrow(1)
	case "pgup":
		m.arrow(-max(m.phaseRows()-1, 1))
	case "pgdown":
		m.arrow(max(m.phaseRows()-1, 1))
	default:
		if a, ok := homeShortcuts[k]; ok {
			return m.activate(a)
		}
	}
	return nil
}

// pick runs what the More… dialog chose and closes it.
func (m *homeScreen) pick(a action) tea.Cmd {
	if a == actNone {
		return nil
	}
	m.dialog = nil
	if a == actClose {
		return nil
	}
	return m.activate(a)
}

// arrow moves within the focused region by n.
func (m *homeScreen) arrow(n int) {
	switch m.focus {
	case homePhases:
		phases := m.phases()
		if len(phases) == 0 {
			return
		}
		i := min(max(m.phaseIndex()+n, 0), len(phases)-1)
		m.phaseID = phases[i].ID
		m.phaseTop = -1 // bring it into view
	case homeLines:
		if len(m.lines) > 0 {
			m.lineSel = min(max(m.lineSel+n, 0), len(m.lines)-1)
		}
	}
}

// cycleFocus moves the focus to the next (or previous) region.
func (m *homeScreen) cycleFocus(next bool) {
	i := 0
	for j, f := range homeOrder {
		if f == m.focus {
			i = j
		}
	}
	if next {
		i++
	} else {
		i += len(homeOrder) - 1
	}
	m.focus = homeOrder[i%len(homeOrder)]
	if m.focus == homePhases {
		m.phaseTop = -1
	}
}

// focusedButton is the action of the focused bar button in the last
// frame; the first button when the focused one is gone.
func (m *homeScreen) focusedButton() action {
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
func (m *homeScreen) moveBar(right bool) {
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
	m.barFocus, m.barMoved = m.bar[i%len(m.bar)], true
}

// mouse handles a click or the wheel (SPEC §15.5).
func (m *homeScreen) mouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	t, ok := m.zones.at(msg.X, msg.Y)
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		by := -3
		if msg.Button == tea.MouseButtonWheelDown {
			by = 3
		}
		if m.dialog == nil && m.zones.regionAt(msg.X, msg.Y) == regionPhases {
			m.scrollPhases(by)
		}
	case tea.MouseButtonLeft:
		if !ok {
			return nil
		}
		if m.dialog != nil {
			if t.act == actOption {
				return m.pick(m.dialog.pick(t.option))
			}
			return nil // the dialog is modal
		}
		switch t.act {
		case actPhaseRow:
			return m.clickPhase(t.option)
		case actLine:
			if t.option >= 0 && t.option < len(m.lines) {
				m.focus, m.lineSel = homeLines, t.option
				return m.activate(m.lines[t.option].act)
			}
			return nil
		}
		return m.activate(t.act)
	}
	return nil
}

// clickPhase selects the i-th phase; clicking the selected phase again
// opens it.
func (m *homeScreen) clickPhase(i int) tea.Cmd {
	phases := m.phases()
	if i < 0 || i >= len(phases) {
		return nil
	}
	if m.focus == homePhases && phases[i].ID == m.phaseID {
		return m.openPhase()
	}
	m.focus, m.phaseID = homePhases, phases[i].ID
	return nil
}

// scrollPhases scrolls the phase list by n rows; the owner's scroll
// position then holds until the selection moves.
func (m *homeScreen) scrollPhases(n int) {
	top := m.phaseTop
	if top < 0 {
		top = m.autoTop
	}
	m.phaseTop = min(max(top+n, 0), max(len(m.phases())-m.phaseRows(), 0))
}

// openPhase opens the selected phase's detail page.
func (m *homeScreen) openPhase() tea.Cmd {
	if m.phaseID == "" {
		return nil
	}
	return m.notYet("Phase " + m.phaseID)
}

// activate runs a: a button, a shortcut key, a dialog option or a line all
// end up here. Actions the bar doesn't show right now do nothing.
func (m *homeScreen) activate(a action) tea.Cmd {
	switch a {
	case actHelp:
		return showHelp
	case actQuit:
		return quitApp
	case actMore:
		if len(m.folded) > 0 {
			m.dialog = moreDialog(m.folded)
		}
		return nil
	case actNone, actClose:
		return nil
	}
	label := ""
	for _, b := range m.buttons() {
		if b.act == a {
			label = b.label
		}
	}
	if label == "" {
		return nil // not offered right now
	}
	return m.notYet(label)
}

// notYet says on the status line that name's page isn't in this build:
// the pages are added one by one on top of the dashboard.
func (m *homeScreen) notYet(name string) tea.Cmd {
	m.setStatus(name + ": not available yet")
	return nil
}

// setStatus shows s on the status line with the time it happened.
func (m *homeScreen) setStatus(s string) {
	m.status, m.statusAt = s, m.now()
}
