package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
)

// The plan pages (SPEC §15.6): phase detail, task detail and Check. They
// read the project through home (the screen below them), so a refresh
// that happens while they are open shows at once.

// doMsg asks home to run action a, as if its button was pressed: pages
// close themselves and hand over the actions only home can do.
type doMsg struct{ a action }

// statusOrder is the order the counts are told in.
var statusOrder = []plan.Status{plan.Done, plan.Skipped, plan.InProgress, plan.Ready, plan.Blocked}

// rowsFor is the shared task builder on home's plan, for phase.
func (m *homeScreen) rowsFor(phase string) rows {
	r := rows{phase: phase, th: m.th}
	if m.snap != nil {
		r.plan = m.snap.Plan
		if m.snap.Config != nil {
			r.mode = m.snap.Config.DefaultMode
		}
	}
	return r
}

// pageMouse scrolls p with the wheel and returns the action of the
// button under a left click; ok says the event was a click on one.
func pageMouse(p *page, z zones, msg tea.MouseMsg) (a action, ok bool) {
	if msg.Action != tea.MouseActionPress {
		return actNone, false
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		p.scroll(-3)
	case tea.MouseButtonWheelDown:
		p.scroll(3)
	case tea.MouseButtonLeft:
		if t, hit := z.at(msg.X, msg.Y); hit && t.act != actNone {
			return t.act, true
		}
	}
	return actNone, false
}

// ---- phase detail ----

// phaseScreen is a phase's detail page: the outcome, the counts and the
// task rows the run view shows. enter opens a task.
type phaseScreen struct {
	home *homeScreen
	id   string
	w, h int
	p    *page

	sel    int  // the selected task's index in the phase
	follow bool // keep the selection in view in the next frame
	// owner maps each body line to its task's index, -1 for none.
	owner []int
	zones zones
}

func newPhaseScreen(home *homeScreen, id string) *phaseScreen {
	s := &phaseScreen{home: home, id: id, w: 80, h: 24, follow: true}
	s.p = &page{body: s.body}
	for i, t := range s.rows().tasks() {
		if t.Status == plan.InProgress {
			s.sel = i
			break
		}
		if t.Status == plan.Ready && s.rows().tasks()[s.sel].Status != plan.Ready {
			s.sel = i
		}
	}
	return s
}

func (s *phaseScreen) rows() rows { return s.home.rowsFor(s.id) }

// status is the phase as the snapshot reports it; nil when it is gone.
func (s *phaseScreen) status() *report.PhaseStatus {
	for _, ph := range s.home.phases() {
		if ph.ID == s.id {
			return &ph
		}
	}
	return nil
}

func (s *phaseScreen) Init() tea.Cmd { return nil }

func (s *phaseScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"↑ ↓ j k", "Select", "move along the tasks"},
		{"enter", "Task details", "the selected task in full, with its last attempts"},
		{"a", "Arise this phase…", "open the start-run wizard on this phase"},
		{"v", "Preview this phase", "the dry run of this phase"},
		{"e", "Edit plan", "open the plan in your editor"},
		{"pgup pgdn", "Scroll", "when the tasks don't fit"},
		{"esc", "Close", "back to home"},
	}
}

func (s *phaseScreen) buttons() []pageButton {
	var out []pageButton
	if s.home.offers(actArise) {
		out = append(out, pageButton{"Arise this phase…", "Arise", actArise})
	}
	out = append(out, pageButton{"Preview this phase", "Preview", actPreview})
	if s.home.offers(actEdit) {
		out = append(out, pageButton{"Edit plan", "Edit", actEdit})
	}
	return out
}

func (s *phaseScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case ariseMsg:
		return s, pop(msg) // the preview's, on its way to home
	case tea.KeyMsg:
		k := msg.String()
		switch k {
		case "up", "k":
			s.move(-1)
		case "down", "j":
			s.move(1)
		case "enter", " ":
			return s, s.openTask()
		case "a":
			return s, s.act(actArise)
		case "v":
			return s, s.act(actPreview)
		case "e":
			return s, s.act(actEdit)
		case "?":
			return s, showHelp
		case "q", "esc":
			return s, pop(nil)
		case "pgup", "pgdown", "home", "end":
			s.p.key(k)
			s.follow = false
		}
	case tea.MouseMsg:
		if a, ok := pageMouse(s.p, s.zones, msg); ok {
			if a == actClose {
				return s, pop(nil)
			}
			return s, s.act(a)
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if i := msg.Y - 2 + s.p.top; i >= 0 && i < len(s.owner) && s.owner[i] >= 0 {
				if s.owner[i] == s.sel {
					return s, s.openTask()
				}
				s.sel, s.follow = s.owner[i], true
			}
		}
	}
	return s, nil
}

func (s *phaseScreen) move(by int) {
	n := len(s.rows().tasks())
	if n > 0 {
		s.sel, s.follow = min(max(s.sel+by, 0), n-1), true
	}
}

// act runs one of the page's actions.
func (s *phaseScreen) act(a action) tea.Cmd {
	h := s.home
	switch a {
	case actArise:
		if h.offers(actArise) {
			return pop(ariseMsg{report.RunRequest{Phase: s.id}})
		}
	case actPreview:
		return push(newPreview(h.ctx, h.svc, h.th, report.RunRequest{Phase: s.id}))
	case actEdit:
		if h.offers(actEdit) {
			return pop(doMsg{actEdit})
		}
	}
	return nil
}

func (s *phaseScreen) openTask() tea.Cmd {
	tasks := s.rows().tasks()
	if s.sel < 0 || s.sel >= len(tasks) {
		return nil
	}
	return push(newTaskScreen(s.home, tasks[s.sel].ID))
}

func (s *phaseScreen) title() string {
	t := "Phase " + s.id
	if ph := s.status(); ph != nil && ph.Title != "" {
		t += " · " + ph.Title
	}
	return t + " · esc closes"
}

func (s *phaseScreen) View() string {
	s.p.title = s.title()
	s.p.buttons = s.buttons()
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

// body lays the outcome, the counts and the task rows out for w cells.
func (s *phaseScreen) body(w int) []string {
	th := s.home.th
	ph := s.status()
	if ph == nil {
		s.owner = nil
		return wrap("This phase is no longer in the plan.", w)
	}
	var out []string
	if row, ok := s.home.phaseRow(s.id); ok {
		text := row.word
		switch {
		case ph.Next != "":
			text += " — " + ph.Next + " is next"
		case row.word == "wait":
			text += " — it completes once tasks of other phases are done"
		case row.word == "stuck":
			text += " — some tasks can never start as planned"
		}
		out = append(out, th.paint(row.glyphLook, row.glyph)+" "+th.paint(lookTitle, "Outcome")+"  "+fit(text, w-textWidth("Outcome")-4))
	}
	out = append(out, th.paint(lookDim, fit(s.counts(ph), w)), "")
	head := len(out)
	r := s.rows()
	lines, follow, owner := r.lines(w, s.sel)
	s.owner = make([]int, head, head+len(owner))
	for i := range s.owner {
		s.owner[i] = -1
	}
	s.owner = append(s.owner, owner...)
	if s.follow && follow >= 0 && s.p.rows > 0 {
		at := head + follow
		if at < s.p.top {
			s.p.top = at
		} else if at >= s.p.top+s.p.rows {
			s.p.top = at - s.p.rows + 1
		}
		s.follow = false
	}
	return append(out, lines...)
}

// counts tells how many tasks there are and how many are in each status.
func (s *phaseScreen) counts(ph *report.PhaseStatus) string {
	parts := []string{plural(ph.Total, "task")}
	for _, st := range statusOrder {
		if n := ph.Counts[st.String()]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+st.String())
		}
	}
	return strings.Join(parts, " · ")
}

// phaseRow is the PHASES row of phase id as home draws it.
func (m *homeScreen) phaseRow(id string) (phaseRow, bool) {
	for _, r := range m.phaseRowsData() {
		if r.id == id {
			return r, true
		}
	}
	return phaseRow{}, false
}

// ---- task detail ----

// attemptsLast is how many runs' attempts the task page lists.
const attemptsLast = 5

// historyRuns is how many runs of history the task page reads.
const historyRuns = 30

// taskScreen is a task's detail page: the run view's details, then the
// last attempts at it from the run log.
type taskScreen struct {
	home *homeScreen
	id   string
	w, h int
	p    *page

	loaded   bool
	err      error
	attempts []taskAttempt
	notice   string
	zones    zones
}

// taskAttempt is the task's part in one run.
type taskAttempt struct {
	started string // the run's start, RFC 3339
	report.TaskRun
}

// taskHistoryMsg is the run log's answer.
type taskHistoryMsg struct {
	h   report.History
	err error
}

func newTaskScreen(home *homeScreen, id string) *taskScreen {
	s := &taskScreen{home: home, id: id, w: 80, h: 24}
	s.p = &page{
		body:    s.body,
		buttons: []pageButton{{"Copy ID", "Copy", actCopy}},
	}
	return s
}

func (s *taskScreen) Init() tea.Cmd {
	ctx, svc := s.home.ctx, s.home.svc
	if svc == nil {
		return nil
	}
	return async(s, "history", func() tea.Msg {
		h, err := svc.History(ctx, historyRuns)
		return taskHistoryMsg{h, err}
	})
}

func (s *taskScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"y", "Copy ID", "copy the task's ID to the clipboard"},
		{"↑ ↓ pgup pgdn", "Scroll", "when the details don't fit"},
		{"esc", "Close", "back to the phase"},
	}
}

func (s *taskScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case taskHistoryMsg:
		s.loaded, s.err = true, msg.err
		s.attempts = nil
		for _, run := range msg.h.Runs { // newest first
			for _, t := range run.Tasks {
				if t.ID == s.id && len(s.attempts) < attemptsLast {
					s.attempts = append(s.attempts, taskAttempt{run.StartedAt, t})
				}
			}
		}
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "y":
			s.copy()
		case "q":
			return s, pop(nil)
		case "?":
			return s, showHelp
		default:
			if s.p.key(k) {
				return s, pop(nil)
			}
		}
	case tea.MouseMsg:
		if a, ok := pageMouse(s.p, s.zones, msg); ok {
			switch a {
			case actClose:
				return s, pop(nil)
			case actCopy:
				s.copy()
			}
		}
	}
	return s, nil
}

func (s *taskScreen) copy() {
	s.notice = copyNotice(s.home.out, s.id)
}

func (s *taskScreen) View() string {
	title := s.id
	if s.notice != "" {
		title += " · " + s.notice
	}
	s.p.title = title + " · esc closes"
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

// phaseOf is the phase the task is in, "" when it is gone from the plan.
func (s *taskScreen) phaseOf() string {
	if p := s.home.snapPlan(); p != nil {
		if t := p.Task(s.id); t != nil && t.Phase != nil {
			return t.Phase.ID
		}
	}
	return ""
}

func (m *homeScreen) snapPlan() *plan.Plan {
	if m.snap == nil {
		return nil
	}
	return m.snap.Plan
}

func (s *taskScreen) body(w int) []string {
	th := s.home.th
	out := s.home.rowsFor(s.phaseOf()).detail(s.id, w)
	out = append(out, "", th.paint(lookTitle, "Last attempts"))
	switch {
	case !s.loaded:
		return append(out, th.paint(lookDim, "reading the run log…"))
	case s.err != nil:
		return append(out, painted(th, lookAlert, "", "the run log can't be read: "+errText(s.err), w)...)
	case len(s.attempts) == 0:
		return append(out, th.paint(lookDim, "none: igris hasn't run this task"))
	}
	for _, a := range s.attempts {
		out = append(out, s.attemptLines(a, w)...)
	}
	return out
}

// attemptLines tells one run's attempts at the task: when, how it ended,
// who ran it and how it went.
func (s *taskScreen) attemptLines(a taskAttempt, w int) []string {
	when := "—"
	if t, err := time.Parse(time.RFC3339, a.started); err == nil {
		when = t.In(s.home.loc).Format("Jan 02 15:04")
	}
	parts := []string{a.Result}
	if a.Rank != "" || a.Model != "" {
		parts = append(parts, strings.TrimSpace(a.Rank+" → "+a.Model))
	}
	parts = append(parts, plural(a.Attempts, "attempt"))
	if a.DurationS > 0 {
		parts = append(parts, shortDur(a.DurationS))
	}
	if a.VerifyFailed+a.VerifyPassed > 0 {
		parts = append(parts, fmt.Sprintf("verify %d failed, %d passed", a.VerifyFailed, a.VerifyPassed))
	}
	look := lookPlain
	if a.Result != report.ResultDone {
		look = lookDim
	}
	return painted(s.home.th, look, when+"  ", strings.Join(parts, " · "), w)
}

// shortDur is a duration in seconds as "45s", "14m" or "2h05m".
func shortDur(sec int) string {
	switch {
	case sec < 60:
		return strconv.Itoa(sec) + "s"
	case sec < 3600:
		return strconv.Itoa(sec/60) + "m"
	}
	return fmt.Sprintf("%dh%02dm", sec/3600, sec%3600/60)
}

// ---- check ----

// checkIDs are the checks `igris check` lists as warnings.
var checkIDs = []string{
	checks.IDClaude, checks.IDHerdr, checks.IDTmux, checks.IDConfig, checks.IDAPIKey, checks.IDProject, checks.IDPlanHints, checks.IDDrift,
}

// checkScreen is the Check page: the plan's problems as file:line:
// message, then the warnings of check (drift, hints, setup).
type checkScreen struct {
	home *homeScreen
	w, h int
	p    *page

	notice string
	zones  zones
}

func newCheckScreen(home *homeScreen) *checkScreen {
	s := &checkScreen{home: home, w: 80, h: 24}
	s.p = &page{body: s.body}
	return s
}

func (s *checkScreen) Init() tea.Cmd { return nil }

func (s *checkScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"c", "Check again", "read the plan again and run the checks again"},
		{"e", "Edit plan", "open the plan in your editor"},
		{"A", "Adapt", "let Claude propose a canonical plan (when the plan is invalid)"},
		{"↑ ↓ pgup pgdn", "Scroll", "when the list doesn't fit"},
		{"esc", "Close", "back to home"},
	}
}

func (s *checkScreen) buttons() []pageButton {
	var out []pageButton
	if s.home.offers(actEdit) {
		out = append(out, pageButton{"Edit plan", "Edit", actEdit})
	}
	if s.home.offers(actAdapt) {
		out = append(out, pageButton{"Adapt", "", actAdapt})
	}
	return append(out, pageButton{"Check again", "Again", actCheck})
}

func (s *checkScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "c":
			return s, s.act(actCheck)
		case "e":
			return s, s.act(actEdit)
		case "A":
			return s, s.act(actAdapt)
		case "?":
			return s, showHelp
		case "q", "esc":
			return s, pop(nil)
		default:
			if k != "enter" && k != " " && s.p.key(k) {
				return s, pop(nil)
			}
		}
	case tea.MouseMsg:
		if a, ok := pageMouse(s.p, s.zones, msg); ok {
			if a == actClose {
				return s, pop(nil)
			}
			return s, s.act(a)
		}
	}
	return s, nil
}

func (s *checkScreen) act(a action) tea.Cmd {
	h := s.home
	switch a {
	case actCheck:
		s.notice = "checked again"
		var cmds []tea.Cmd
		if !h.reading {
			cmds = append(cmds, h.refresh())
		}
		return tea.Batch(append(cmds, h.runDoctor())...)
	case actEdit, actAdapt:
		if h.offers(a) {
			return pop(doMsg{a})
		}
	}
	return nil
}

func (s *checkScreen) View() string {
	title := "Check"
	if s.notice != "" {
		title += " · " + s.notice
	}
	s.p.title = title + " · c checks again"
	s.p.buttons = s.buttons()
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

// warnings are the check results that aren't OK, in check's order.
func (s *checkScreen) warnings() []checks.Result {
	return checks.Problems(checks.Pick(s.home.doctor, checkIDs...))
}

func (s *checkScreen) body(w int) []string {
	th, snap := s.home.th, s.home.snap
	switch {
	case snap == nil:
		return []string{th.paint(lookDim, "reading the plan…")}
	case snap.PlanMissing:
		return painted(th, lookAlert, "", glyphs[plan.Blocked]+" plan missing: "+s.home.planName()+" not found", w)
	case snap.Plan == nil:
		return painted(th, lookAlert, "", glyphs[plan.Blocked]+" plan unreadable: "+snap.PlanErr, w)
	}
	var out []string
	if len(snap.Issues) == 0 {
		out = append(out, th.paint(lookAccent, fmt.Sprintf("%s plan valid · %s · %s", glyphs[plan.Done], plural(len(snap.Plan.Phases), "phase"), plural(len(snap.Plan.Tasks), "task"))))
	} else {
		out = append(out, th.paint(lookAlert, glyphs[plan.Blocked]+" plan invalid · "+plural(len(snap.Issues), "problem")), "")
		for _, is := range snap.Issues {
			out = append(out, painted(th, lookPlain, "", is.String(), w)...)
		}
	}
	out = append(out, "")
	if !s.home.doctorKnown {
		return append(out, th.paint(lookDim, "warnings: checking…"))
	}
	ws := s.warnings()
	if len(ws) == 0 {
		return append(out, th.paint(lookDim, "no warnings"))
	}
	out = append(out, th.paint(lookTitle, plural(len(ws), "warning")))
	for _, c := range ws {
		out = append(out, painted(th, lookAlert, "! ", warningText(c), w)...)
	}
	return out
}

// warningText is a warning as check prints it: where it is, then what.
func warningText(c checks.Result) string {
	switch {
	case c.File == "":
		return c.Message
	case c.Line == 0:
		return c.File + ": " + c.Message
	}
	return fmt.Sprintf("%s:%d: %s", c.File, c.Line, c.Message)
}
