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

// The Doctor and History pages (SPEC §15.6). Both are read-only views over
// what home already has or can read; they hand the actions only home can do
// back to it with doMsg.

// ---- doctor ----

// doctorScreen lists doctor's results, one row each: a glyph and a word
// (`✓ ok`, `! warn`, `⨯ fail`) then the message. The selected row expands
// to its next step, and the page offers the row's own safe action.
type doctorScreen struct {
	home *homeScreen
	w, h int
	p    *page

	sel    int
	follow bool // keep the selection in view in the next frame
	// owner maps each body line to its result's index, -1 for none.
	owner  []int
	notice string
	zones  zones
}

func newDoctorScreen(home *homeScreen) *doctorScreen {
	s := &doctorScreen{home: home, w: 80, h: 24, follow: true}
	s.p = &page{body: s.body}
	// Start on the first problem: it is what the owner came to read.
	for i, r := range home.doctor {
		if r.Level == checks.Fail {
			s.sel = i
			break
		}
		if r.Level == checks.Warn && home.doctor[s.sel].Level == checks.OK {
			s.sel = i
		}
	}
	return s
}

func (s *doctorScreen) Init() tea.Cmd { return nil }

func (s *doctorScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"↑ ↓ j k", "Select", "move along the checks; the selected one shows its next step"},
		{"y", "Copy fix command", "copy the selected check's command to the clipboard"},
		{"i", "Run again", "run doctor's checks again"},
		{"e", "Edit config", "open igris.toml in your editor (when the config is invalid)"},
		{"I", "Init", "create igris.toml and .igris/ (when the selected check asks for it)"},
		{"c", "Check", "the plan's problems and warnings"},
		{"A", "Adapt", "let Claude propose a canonical plan (when the plan is invalid)"},
		{"pgup pgdn", "Scroll", "when the list doesn't fit"},
		{"esc", "Close", "back to home"},
	}
}

// results are doctor's results, as home last heard them.
func (s *doctorScreen) results() []checks.Result { return s.home.doctor }

// selected is the selected result, if there is one.
func (s *doctorScreen) selected() (checks.Result, bool) {
	rs := s.results()
	if s.sel < 0 || s.sel >= len(rs) {
		return checks.Result{}, false
	}
	return rs[s.sel], true
}

// rowActions are the safe actions home has for result r, where it has one
// (SPEC §5.5): everything else is text, and `y` copies the command.
func (s *doctorScreen) rowActions(r checks.Result) []action {
	h := s.home
	var out []action
	add := func(a action) {
		if a == actEditConfig || h.offers(a) {
			out = append(out, a)
		}
	}
	switch r.ID {
	case checks.IDConfigValid:
		if r.Level == checks.Fail {
			add(actEditConfig)
		} else if r.Next == "igris init" {
			add(actInit)
		}
	case checks.IDProject:
		if r.Next == "igris init" {
			add(actInit)
		}
	case checks.IDAllowRules:
		if r.Level != checks.OK {
			add(actInit)
		}
	case checks.IDPlanValid:
		if r.Level != checks.OK {
			add(actCheck)
			add(actAdapt)
		}
	}
	return out
}

var doctorActionButtons = map[action]pageButton{
	actEditConfig: {"Edit config", "Edit", actEditConfig},
	actInit:       {"Init", "", actInit},
	actCheck:      {"Check", "", actCheck},
	actAdapt:      {"Adapt", "", actAdapt},
}

func (s *doctorScreen) buttons() []pageButton {
	var out []pageButton
	if r, ok := s.selected(); ok {
		for _, a := range s.rowActions(r) {
			out = append(out, doctorActionButtons[a])
		}
		if r.Next != "" {
			out = append(out, pageButton{"Copy fix command", "Copy", actCopy})
		}
	}
	return append(out, pageButton{"Run again", "Again", actDoctor})
}

func (s *doctorScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "up", "k":
			s.move(-1)
		case "down", "j":
			s.move(1)
		case "y":
			return s, s.act(actCopy)
		case "i":
			return s, s.act(actDoctor)
		case "e":
			return s, s.act(actEditConfig)
		case "I":
			return s, s.act(actInit)
		case "c":
			return s, s.act(actCheck)
		case "A":
			return s, s.act(actAdapt)
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
				s.sel, s.follow = s.owner[i], true
			}
		}
	}
	return s, nil
}

func (s *doctorScreen) move(by int) {
	if n := len(s.results()); n > 0 {
		s.sel, s.follow = min(max(s.sel+by, 0), n-1), true
	}
}

// act runs one of the page's actions.
func (s *doctorScreen) act(a action) tea.Cmd {
	h := s.home
	switch a {
	case actCopy:
		if r, ok := s.selected(); ok && r.Next != "" {
			s.notice = copyNotice(h.out, r.Next)
		}
	case actDoctor:
		s.notice = "checked again"
		return h.runDoctor()
	default:
		// A row's own action, and only the selected row's.
		if r, ok := s.selected(); ok {
			for _, ra := range s.rowActions(r) {
				if ra == a {
					return pop(doMsg{a})
				}
			}
		}
	}
	return nil
}

func (s *doctorScreen) View() string {
	title := "Doctor"
	if s.notice != "" {
		title += " · " + s.notice
	}
	s.p.title = title + " · i runs again"
	s.p.buttons = s.buttons()
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

// levelWord is a result's level as a glyph and a word.
func levelWord(l checks.Level) (string, look) {
	switch l {
	case checks.Fail:
		return glyphs[plan.Blocked] + " fail", lookAlert
	case checks.Warn:
		return "! warn", lookAlert
	}
	return glyphs[plan.Done] + " ok", lookAccent
}

func (s *doctorScreen) body(w int) []string {
	th, h := s.home.th, s.home
	s.owner = nil
	if !h.doctorKnown {
		return []string{th.paint(lookDim, "running doctor's checks…")}
	}
	rs := s.results()
	if len(rs) == 0 {
		return []string{th.paint(lookDim, "doctor has nothing to report")}
	}
	s.sel = min(max(s.sel, 0), len(rs)-1)
	var out []string
	var fails, warns int
	for _, r := range rs {
		switch r.Level {
		case checks.Fail:
			fails++
		case checks.Warn:
			warns++
		}
	}
	summary := "all " + plural(len(rs), "check") + " ok"
	if fails+warns > 0 {
		summary = plural(len(rs), "check") + " · " + strconv.Itoa(fails) + " fail · " + strconv.Itoa(warns) + " warn"
	}
	out = append(out, th.paint(lookDim, fit(summary, w)), "")
	head := len(out)
	s.owner = []int{-1, -1}
	selAt := head
	for i, r := range rs {
		if i == s.sel {
			selAt = len(out)
		}
		lines := s.rowLines(r, i == s.sel, w)
		for range lines {
			s.owner = append(s.owner, i)
		}
		out = append(out, lines...)
	}
	if s.follow && s.p.rows > 0 {
		if selAt < s.p.top {
			s.p.top = selAt
		} else if end := selAt + 1; end > s.p.top+s.p.rows {
			s.p.top = end - s.p.rows
		}
		s.follow = false
	}
	return out
}

// wordW is the width of the widest level word ("⨯ fail").
const wordW = 6

// rowLines lays one result out: its level and message, and when it is
// selected the location and the next step under it.
func (s *doctorScreen) rowLines(r checks.Result, selected bool, w int) []string {
	th := s.home.th
	word, l := levelWord(r.Level)
	mark := "  "
	if selected {
		mark = th.paint(lookTitle, "› ")
	}
	text := r.Message
	if text == "" {
		text = r.ID
	}
	if r.File != "" {
		text = warningText(r) // where it is, then what
	}
	prefix := mark + th.paint(l, pad(word, wordW)) + "  "
	pw := textWidth(prefix)
	lines := wrap(text, w-pw)
	indent := strings.Repeat(" ", pw)
	out := make([]string, 0, len(lines)+2)
	for i, line := range lines {
		if i == 0 {
			out = append(out, prefix+line)
		} else {
			out = append(out, indent+line)
		}
	}
	if !selected {
		return out
	}
	next := "nothing to do"
	if r.Next != "" {
		next = r.Next
	}
	out = append(out, painted(th, lookDim, indent+"next: ", next, w)...)
	if r.Confirm {
		out = append(out, painted(th, lookDim, indent, "asks you before a run starts", w)...)
	}
	return out
}

// ---- history ----

// historyPageRuns is how many runs the History page reads.
const historyPageRuns = 30

// historyScreen is the History page: the last runs, then one run's tasks,
// then one task's attempts. It reads the run log and changes nothing.
type historyScreen struct {
	home *homeScreen
	w, h int
	p    *page

	loaded bool
	err    error
	runs   []report.HistoryRun

	// level is how deep the owner is: 0 the runs, 1 a run's tasks, 2 a
	// task's attempts. runSel and taskSel are the selections at 0 and 1.
	level           int
	runSel, taskSel int
	follow          bool
	owner           []int
	zones           zones
}

// historyMsg is the run log's answer.
type historyMsg struct {
	h   report.History
	err error
}

func newHistoryScreen(home *homeScreen) *historyScreen {
	s := &historyScreen{home: home, w: 80, h: 24, follow: true}
	s.p = &page{body: s.body}
	return s
}

func (s *historyScreen) Init() tea.Cmd {
	ctx, svc := s.home.ctx, s.home.svc
	if svc == nil {
		return nil
	}
	return async(s, "history", func() tea.Msg {
		h, err := svc.History(ctx, historyPageRuns)
		return historyMsg{h, err}
	})
}

func (s *historyScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"↑ ↓ j k", "Select", "move along the runs, or a run's tasks"},
		{"enter", "Open", "a run shows its tasks; a task shows its attempts"},
		{"esc backspace", "Back", "up one level; on the runs, back to home"},
		{"pgup pgdn", "Scroll", "when the list doesn't fit"},
	}
}

func (s *historyScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case historyMsg:
		s.loaded, s.err = true, msg.err
		s.runs = msg.h.Runs
		s.runSel = min(s.runSel, max(len(s.runs)-1, 0))
		if s.level > 0 && !s.valid() {
			s.level = 0
		}
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "up", "k":
			s.move(-1)
		case "down", "j":
			s.move(1)
		case "enter", " ", "right":
			s.descend()
		case "esc", "backspace", "left":
			return s, s.ascend()
		case "q":
			return s, pop(nil)
		case "?":
			return s, showHelp
		case "pgup", "pgdown", "home", "end":
			s.p.key(k)
			s.follow = false
		}
	case tea.MouseMsg:
		if a, ok := pageMouse(s.p, s.zones, msg); ok && a == actClose {
			return s, pop(nil)
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if i := msg.Y - 2 + s.p.top; i >= 0 && i < len(s.owner) && s.owner[i] >= 0 {
				if s.sel() == s.owner[i] {
					s.descend()
				} else {
					s.setSel(s.owner[i])
					s.follow = true
				}
			}
		}
	}
	return s, nil
}

// run is the selected run, nil when there is none.
func (s *historyScreen) run() *report.HistoryRun {
	if s.runSel < 0 || s.runSel >= len(s.runs) {
		return nil
	}
	return &s.runs[s.runSel]
}

// task is the selected task of the selected run, nil when there is none.
func (s *historyScreen) task() *report.TaskRun {
	r := s.run()
	if r == nil || s.taskSel < 0 || s.taskSel >= len(r.Tasks) {
		return nil
	}
	return &r.Tasks[s.taskSel]
}

// valid says the selection still points at something the level shows.
func (s *historyScreen) valid() bool {
	switch s.level {
	case 1:
		return s.run() != nil
	case 2:
		return s.task() != nil
	}
	return true
}

// sel and setSel are the selection of the level.
func (s *historyScreen) sel() int {
	if s.level == 1 {
		return s.taskSel
	}
	return s.runSel
}

func (s *historyScreen) setSel(i int) {
	if s.level == 1 {
		s.taskSel = i
	} else {
		s.runSel = i
	}
}

func (s *historyScreen) move(by int) {
	n := len(s.runs)
	switch s.level {
	case 1:
		if r := s.run(); r != nil {
			n = len(r.Tasks)
		}
	case 2:
		return
	}
	if n > 0 {
		s.setSel(min(max(s.sel()+by, 0), n-1))
		s.follow = true
	}
}

func (s *historyScreen) descend() {
	switch {
	case s.level == 0 && s.run() != nil:
		s.level, s.taskSel, s.follow = 1, 0, true
		s.p.top = 0
	case s.level == 1 && s.task() != nil:
		s.level = 2
		s.p.top = 0
	}
}

// ascend goes up a level; on the runs it closes the page.
func (s *historyScreen) ascend() tea.Cmd {
	if s.level == 0 {
		return pop(nil)
	}
	s.level--
	s.follow = true
	return nil
}

func (s *historyScreen) View() string {
	title := "History"
	switch s.level {
	case 1:
		if r := s.run(); r != nil {
			title += " · run " + s.when(r.StartedAt)
		}
	case 2:
		if t := s.task(); t != nil {
			title += " · " + t.ID
		}
	}
	if s.level > 0 {
		title += " · esc goes back"
	} else {
		title += " · enter opens · esc closes"
	}
	s.p.title = title
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

// when is an RFC 3339 time as "Jan 02 15:04" in home's zone.
func (s *historyScreen) when(rfc string) string {
	if t, err := time.Parse(time.RFC3339, rfc); err == nil {
		return t.In(s.home.loc).Format("Jan 02 15:04")
	}
	return "—"
}

func (s *historyScreen) body(w int) []string {
	th := s.home.th
	s.owner = nil
	switch {
	case !s.loaded:
		return []string{th.paint(lookDim, "reading the run log…")}
	case s.err != nil:
		return painted(th, lookAlert, "", "the run log can't be read: "+errText(s.err), w)
	case len(s.runs) == 0:
		return []string{th.paint(lookDim, "none: igris hasn't run yet")}
	}
	var out []string
	var owned []int
	selAt := 0
	switch s.level {
	case 0:
		out, owned, selAt = s.runLines(w)
	case 1:
		out, owned, selAt = s.taskLines(w)
	default:
		out = s.attemptLines(w)
		for range out {
			owned = append(owned, -1)
		}
	}
	s.owner = owned
	if s.follow && s.p.rows > 0 {
		if selAt < s.p.top {
			s.p.top = selAt
		} else if selAt >= s.p.top+s.p.rows {
			s.p.top = selAt - s.p.rows + 1
		}
		s.follow = false
	}
	return out
}

// marker is the selection mark of a row.
func (s *historyScreen) marker(selected bool) string {
	if selected {
		return s.home.th.paint(lookTitle, "› ")
	}
	return "  "
}

// runLines are the runs, newest first: when, phases, counts, how it ended.
func (s *historyScreen) runLines(w int) (out []string, owner []int, selAt int) {
	th := s.home.th
	for i, r := range s.runs {
		if i == s.runSel {
			selAt = len(out)
		}
		text := strings.Join(r.Phases, ", ") + "  " + strconv.Itoa(r.Done) + " done"
		if r.Skipped > 0 {
			text += " · " + strconv.Itoa(r.Skipped) + " skipped"
		}
		text += " · " + r.End
		if r.DurationS > 0 {
			text += " · " + shortDur(r.DurationS)
		}
		lines := painted(th, endLook(r.End), s.marker(i == s.runSel)+s.when(r.StartedAt)+"  ", text, w)
		for range lines {
			owner = append(owner, i)
		}
		out = append(out, lines...)
	}
	return out, owner, selAt
}

// endLook dims what ended well; a run that stopped short stands out.
func endLook(end string) look {
	switch end {
	case "completed":
		return lookPlain
	case report.EndRunning:
		return lookAccent
	}
	return lookAlert
}

// taskLines are one run's facts, then its tasks.
func (s *historyScreen) taskLines(w int) (out []string, owner []int, selAt int) {
	th := s.home.th
	r := s.run()
	add := func(i int, lines ...string) {
		for range lines {
			owner = append(owner, i)
		}
		out = append(out, lines...)
	}
	end := r.End
	if r.DurationS > 0 {
		end += " · " + shortDur(r.DurationS)
	}
	add(-1, painted(th, endLook(r.End), "", strings.Join(r.Phases, ", ")+" · "+end, w)...)
	if n := len(r.Commits); n > 0 {
		add(-1, painted(th, lookDim, "", plural(n, "commit")+": "+strings.Join(r.Commits, " "), w)...)
	}
	for _, e := range r.Errors {
		add(-1, painted(th, lookAlert, "! ", e, w)...)
	}
	add(-1, "")
	if len(r.Tasks) == 0 {
		add(-1, th.paint(lookDim, "no tasks ran"))
	}
	for i, t := range r.Tasks {
		if i == s.taskSel {
			selAt = len(out)
		}
		parts := []string{t.Result}
		if t.Rank != "" || t.Model != "" {
			parts = append(parts, strings.TrimSpace(t.Rank+" → "+t.Model))
		}
		parts = append(parts, plural(t.Attempts, "attempt"))
		if t.DurationS > 0 {
			parts = append(parts, shortDur(t.DurationS))
		}
		look := lookPlain
		if t.Result != report.ResultDone {
			look = lookDim
		}
		add(i, painted(th, look, s.marker(i == s.taskSel)+pad(t.ID, taskIDWidth(r))+"  ", strings.Join(parts, " · "), w)...)
	}
	return out, owner, selAt
}

// taskIDWidth is the width of the widest task ID of r.
func taskIDWidth(r *report.HistoryRun) int {
	n := 0
	for _, t := range r.Tasks {
		n = max(n, textWidth(t.ID))
	}
	return n
}

// attemptLines tell the selected task's part in its run: how it ended,
// who ran it, how many tries it took and how verify went.
func (s *historyScreen) attemptLines(w int) []string {
	th := s.home.th
	r, t := s.run(), s.task()
	out := []string{th.paint(lookDim, fit("run "+s.when(r.StartedAt)+" · "+strings.Join(r.Phases, ", "), w)), ""}
	row := func(name, value string) {
		out = append(out, hang(th.paint(lookTitle, pad(name, 9))+"  ", value, w)...)
	}
	row("result", t.Result)
	if t.Rank != "" || t.Model != "" {
		row("run by", strings.TrimSpace(t.Rank+" → "+t.Model))
	}
	row("attempts", strconv.Itoa(t.Attempts))
	if t.DurationS > 0 {
		row("took", shortDur(t.DurationS))
	}
	row("verify", fmt.Sprintf("%d failed, %d passed", t.VerifyFailed, t.VerifyPassed))
	return out
}
