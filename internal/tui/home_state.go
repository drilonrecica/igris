package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
)

// What home shows is derived from the snapshot, the herdr check and doctor
// on every frame; nothing here is remembered between frames.

// nowKind is the NOW card's state (SPEC §15.6), one at a time. The order is
// the precedence: the first that holds is shown.
type nowKind int

const (
	nowLoading          nowKind = iota // the first read isn't back
	nowUnreadable                      // the project can't be read
	nowGetStarted                      // no igris.toml
	nowRunningElsewhere                // a live igris on this host holds the lock
	nowConfigInvalid
	nowPlanMissing
	nowPlanInvalid // invalid or unreadable
	nowStaleLock   // a stale or unreadable lock file
	nowRemoteLock
	nowInterrupted // state.json has a current task
	nowStopped     // state.json has phases but no current task
	nowReady
)

func (m *homeScreen) kind() nowKind {
	s := m.snap
	switch {
	case m.snapErr != nil:
		return nowUnreadable
	case s == nil:
		return nowLoading
	case s.NoConfig:
		return nowGetStarted
	case s.Lock.Alive:
		return nowRunningElsewhere
	case len(s.ConfigProblems) > 0:
		return nowConfigInvalid
	case s.PlanMissing, s.Plan == nil && s.PlanErr == "":
		return nowPlanMissing
	case s.Plan == nil || len(s.Issues) > 0:
		return nowPlanInvalid
	case s.Lock.Stale || s.Lock.Unreadable:
		return nowStaleLock
	case s.Lock.Remote:
		return nowRemoteLock
	case s.Run != nil && s.Run.Task != "":
		return nowInterrupted
	case s.Run != nil && len(s.Run.Phases) > 0:
		return nowStopped
	}
	return nowReady
}

// stateWord is the run state the header shows on the right, and its look.
func (m *homeScreen) stateWord() (string, look) {
	switch m.kind() {
	case nowLoading:
		return "reading…", lookDim
	case nowUnreadable:
		return "UNREADABLE", lookAlert
	case nowGetStarted:
		return "GET STARTED", lookTitle
	case nowRunningElsewhere:
		return "RUNNING ELSEWHERE", lookAlert
	case nowConfigInvalid:
		return "CONFIG INVALID", lookAlert
	case nowPlanMissing:
		return "NO PLAN", lookTitle
	case nowPlanInvalid:
		return "PLAN INVALID", lookAlert
	case nowStaleLock:
		return "STALE LOCK", lookTitle
	case nowRemoteLock:
		return "LOCKED", lookTitle
	case nowInterrupted:
		return "INTERRUPTED", lookAlert
	case nowStopped:
		return "STOPPED", lookTitle
	}
	return "idle", lookDim
}

// Facts about the snapshot the rules below share.

// planFile says a plan file exists (it may be invalid or unreadable).
func (m *homeScreen) planFile() bool {
	return m.snap != nil && !m.snap.PlanMissing && (m.snap.Plan != nil || m.snap.PlanErr != "")
}

// planValid says the plan parsed and passes check.
func (m *homeScreen) planValid() bool {
	return m.snap != nil && m.snap.Plan != nil && len(m.snap.Issues) == 0
}

// herdr says herdr is known to be reachable.
func (m *homeScreen) herdr() bool { return m.backendKnown && m.backendErr == nil }

// resumable says a run can be resumed: state.json has a task or phases.
func (m *homeScreen) resumable() bool {
	return m.snap != nil && m.snap.Run != nil && (m.snap.Run.Task != "" || len(m.snap.Run.Phases) > 0)
}

// channels says at least one notification channel is set up.
func (m *homeScreen) channels() bool {
	if m.snap == nil || m.snap.NoConfig || m.snap.Config == nil {
		return false
	}
	n := m.snap.Config.Notify
	return n.Ntfy.Topic != "" || n.Discord.WebhookURL != "" || (n.Backend.Enabled && m.herdr())
}

// planName is the plan file's base name.
func (m *homeScreen) planName() string {
	if m.snap == nil || m.snap.PlanPath == "" {
		return "tasks.md"
	}
	return filepath.Base(m.snap.PlanPath)
}

// buttons are the actions the bar offers right now (SPEC §15.6, "shown
// when"), the state's own action first.
func (m *homeScreen) buttons() []option {
	s, k := m.snap, m.kind()
	var out []option
	add := func(when bool, label string, a action) {
		if when {
			out = append(out, option{label, a})
		}
	}
	found := s != nil
	live := found && s.Lock.Alive
	arise := "Arise…"
	if m.resumable() {
		arise = "Resume…"
	}
	add(found && len(s.ConfigProblems) == 0 && !s.NoConfig && m.planValid() && m.herdr() && !live, arise, actArise)
	add(live && s.Run != nil && s.Run.Session != "", "Open session", actOpen)
	add(m.planValid(), "Preview", actPreview)
	add(m.planFile(), "Check", actCheck)
	add(m.planFile() && !m.planValid() && m.herdr() && !live, "Adapt", actAdapt)
	add(true, "Doctor", actDoctor)
	add(found && (len(s.Recent) > 0 || s.Stamp.Log.Exists), "History", actHistory)
	add(m.planFile(), "Edit plan", actEdit)
	add(true, "Settings", actSettings)
	add(m.channels(), "Notify", actNotify)
	add(found && s.NoConfig && !live, "Init", actInit)
	add(found && !s.NoConfig && len(s.ConfigProblems) == 0 && s.PlanMissing && !live, "Example plan", actExample)
	out = append(out, option{"?", actHelp}, option{"Quit", actQuit})

	// The state's own action goes first: it is the one enter runs.
	first := actNone
	switch k {
	case nowGetStarted:
		first = actInit
	case nowPlanMissing:
		first = actExample
	case nowRunningElsewhere:
		first = actOpen
	case nowConfigInvalid:
		first = actSettings
	case nowPlanInvalid:
		first = actCheck
	case nowStaleLock, nowRemoteLock, nowInterrupted, nowStopped, nowReady:
		first = actArise
	}
	for i, b := range out {
		if b.act == first && i > 0 {
			out = append([]option{b}, append(out[:i:i], out[i+1:]...)...)
			break
		}
	}
	return out
}

// homeFold lists the actions the bar folds into More… first when it
// doesn't fit; the first button (the state's own action) is never folded.
var homeFold = []action{actNotify, actSettings, actEdit, actHistory, actDoctor, actAdapt, actInit, actExample, actCheck, actPreview, actOpen, actHelp, actArise}

// phaseRow is one PHASES row, ready to lay out.
type phaseRow struct {
	glyph, id, title, count, word string
	done, total                   int
	glyphLook, textLook           look
}

// phaseRows describe the plan's phases: the §5.1 outcome as a glyph and a
// word (done/next/wait/stuck), the satisfied count and the total.
func (m *homeScreen) phaseRowsData() []phaseRow {
	var out []phaseRow
	for _, ph := range m.phases() {
		r := phaseRow{id: ph.ID, title: ph.Title, total: ph.Total, done: ph.Counts[plan.Done.String()] + ph.Counts[plan.Skipped.String()]}
		r.count = fmt.Sprintf("%d/%d", r.done, r.total)
		switch ph.Outcome {
		case plan.Complete.String():
			r.glyph, r.word, r.glyphLook, r.textLook = glyphs[plan.Done], "done", lookAccent, lookDim
		case plan.Next.String():
			r.glyph, r.word, r.glyphLook, r.textLook = glyphs[plan.Ready], "next", lookPlain, lookPlain
			if ph.Counts[plan.InProgress.String()] > 0 {
				r.glyph, r.glyphLook, r.textLook = glyphs[plan.InProgress], lookAccentBold, lookTitle
			}
		default:
			r.glyph, r.word, r.glyphLook, r.textLook = glyphs[plan.Blocked], "stuck", lookDim, lookDim
			if m.waitsOnOtherPhases(ph.ID) {
				r.word = "wait"
			}
		}
		out = append(out, r)
	}
	return out
}

// waitsOnOtherPhases says the stuck phase id could still complete once
// the tasks of other phases are done: it waits on them rather than being
// stuck for good (a missing or skipped dependency, a cycle).
func (m *homeScreen) waitsOnOtherPhases(id string) bool {
	p := m.snap.Plan
	ph := p.Phase(id)
	if ph == nil {
		return false
	}
	// Suppose every task outside the phase were done; see whether the
	// phase's own tasks could then all start, in some order.
	resolved := func(dep string) bool {
		d := p.Task(dep)
		return d != nil && (d.Status.Satisfied() || d.Phase != ph)
	}
	done := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, t := range ph.Tasks {
			if done[t.ID] || t.Status.Satisfied() {
				continue
			}
			ok := true
			for _, dep := range t.Deps {
				if !done[dep] && !resolved(dep) {
					ok = false
					break
				}
			}
			if ok {
				done[t.ID] = true
				changed = true
			}
		}
	}
	for _, t := range ph.Tasks {
		if !t.Status.Satisfied() && !done[t.ID] {
			return false
		}
	}
	return true
}

// progressBar draws done of total as a bar of n cells: █ for the part
// that is done, ░ for the rest (SPEC §15.6).
func progressBar(done, total, n int) string {
	filled := 0
	if total > 0 {
		filled = min(done*n/total, n)
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", n-filled)
}

// cardLines is the NOW card w cells wide, painted.
func (m *homeScreen) cardLines(w int) []string {
	s := m.snap
	title := func(t string) string { return m.th.paint(lookTitle, fit(t, w)) }
	switch m.kind() {
	case nowLoading:
		return []string{m.th.paint(lookDim, "reading the project…")}
	case nowUnreadable:
		out := wrap("could not read the project: "+m.snapErr.Error(), w)
		return append(out, wrap("Doctor may say why.", w)...)
	case nowGetStarted, nowPlanMissing:
		return m.getStartedCard(w)
	case nowConfigInvalid:
		out := []string{title(fmt.Sprintf("CONFIG INVALID · igris.toml · %s", plural(len(s.ConfigProblems), "problem")))}
		out = append(out, m.firstLines(s.ConfigProblems, 3, "Settings shows all", w)...)
		return append(out, wrap("igris uses the defaults meanwhile. Edit igris.toml from Settings.", w)...)
	case nowPlanInvalid:
		return m.planInvalidCard(w)
	case nowRunningElsewhere:
		return m.runningCard(w)
	case nowStaleLock, nowRemoteLock:
		return m.lockCard(w)
	case nowInterrupted, nowStopped:
		return m.interruptedCard(w)
	}
	return m.readyCard(w)
}

// firstLines shows up to n of lines and says how many more there are.
func (m *homeScreen) firstLines(lines []string, n int, where string, w int) []string {
	var out []string
	for i, l := range lines {
		if i == n && len(lines) > n {
			out = append(out, m.th.paint(lookDim, fit(fmt.Sprintf("… %d more — %s", len(lines)-n, where), w)))
			break
		}
		out = append(out, fit(l, w))
	}
	return out
}

// getStartedCard is the onboarding stepper: each step turns ✓ as the data
// comes in (SPEC §15.6). It stands for a project with no igris.toml and,
// once Init made one, for a project with no plan yet.
func (m *homeScreen) getStartedCard(w int) []string {
	s := m.snap
	type step struct{ glyph, name, state string }
	cfgStep := step{glyphs[plan.Done], "igris.toml", "found"}
	if s.NoConfig {
		cfgStep = step{glyphs[plan.Blocked], "igris.toml", "not found"}
	}
	planStep := step{glyphs[plan.Ready], "plan", m.planName() + " not found"}
	checkStep := step{glyphs[plan.Ready], "check", "—"}
	switch {
	case m.planValid():
		planStep = step{glyphs[plan.Done], "plan", m.planName()}
		checkStep = step{glyphs[plan.Done], "check", "passes"}
	case s.PlanErr != "":
		planStep = step{glyphs[plan.Blocked], "plan", m.planName() + " unreadable"}
	case m.planFile():
		planStep = step{glyphs[plan.Blocked], "plan", fmt.Sprintf("%s: %s", m.planName(), plural(len(s.Issues), "problem"))}
	}
	steps := []step{
		cfgStep,
		planStep,
		checkStep,
		{glyphs[plan.Ready], "preview", "—"},
		{glyphs[plan.Ready], "arise", "—"},
	}
	out := []string{m.th.paint(lookTitle, "GET STARTED")}
	for i, st := range steps {
		out = append(out, fit(st.glyph+" "+strconv.Itoa(i+1)+" "+pad(st.name, 11)+" "+st.state, w))
	}
	hint := "Init creates igris.toml, .igris/, a .gitignore entry and the Claude allow rule for `igris done`."
	switch {
	case !s.NoConfig && !m.planFile():
		hint = "Example plan writes a small canonical plan to try igris on; or write your own at " + s.PlanPath + "."
	case m.planFile() && !m.planValid():
		hint += " The plan has problems: Check lists them, Adapt proposes a canonical one."
	}
	return append(out, wrap(hint, w)...)
}

func (m *homeScreen) planInvalidCard(w int) []string {
	s := m.snap
	var out []string
	if s.Plan == nil {
		out = append(out, m.th.paint(lookTitle, fit("PLAN UNREADABLE · "+m.planName(), w)))
		out = append(out, wrap(s.PlanErr, w)...)
	} else {
		out = append(out, m.th.paint(lookTitle, fit(fmt.Sprintf("PLAN INVALID · %s · %s", m.planName(), plural(len(s.Issues), "problem")), w)))
		lines := make([]string, len(s.Issues))
		for i, is := range s.Issues {
			lines[i] = is.String()
		}
		out = append(out, m.firstLines(lines, 3, "Check shows all", w)...)
	}
	if !m.herdr() {
		out = append(out, wrap("Adapt needs herdr ("+m.herdrWhy()+"): run igris inside a herdr pane.", w)...)
	}
	return out
}

// herdrWhy says why herdr isn't available, in a few words.
func (m *homeScreen) herdrWhy() string {
	if !m.backendKnown {
		return "still checking"
	}
	return "not reachable"
}

// taskTitleW is the title column of a task line.
const taskTitleW = 22

// taskLine is "● ID  title  rank" for the task id, w cells, with what
// follows the rank (mode, time) in tail.
func (m *homeScreen) taskLine(glyph, id, tail string, w int) string {
	title, rk := "", ""
	if p := m.snap.Plan; p != nil {
		if t := p.Task(id); t != nil {
			title, rk = t.Title, rank(t)
		}
	}
	rest := " " + m.th.rank(rk, false) + tail
	if rk == "" {
		rest = tail
	}
	head := glyph + " " + id
	titleW := min(w-textWidth(head)-2-textWidth(rest), taskTitleW)
	if title == "" || titleW < 4 {
		return fit(m.th.paint(lookTitle, head)+rest, w)
	}
	return fit(m.th.paint(lookTitle, head)+"  "+pad(fit(title, titleW), titleW)+rest, w)
}

func (m *homeScreen) runningCard(w int) []string {
	s := m.snap
	l := s.Lock
	out := wrap(fmt.Sprintf("RUNNING in another igris · pid %d on this host · since %s", l.Info.PID, l.Info.StartedAt.In(m.loc).Format("15:04")), w)
	for i := range out {
		out[i] = m.th.paint(lookAlert, out[i])
	}
	switch {
	case s.Run == nil || s.Run.Unreadable != "":
		out = append(out, m.th.paint(lookDim, fit("state.json: "+orText(s.Run, "not written yet"), w)))
	case s.Run.Task != "":
		tail := " · mode " + s.Run.Mode + badge(s.Run.Mode)
		if t, err := time.Parse(time.RFC3339, s.Run.Since); err == nil {
			tail += " · " + shortSince(m.now(), t)
		}
		out = append(out, m.th.marks(m.taskLine(glyphs[plan.InProgress], s.Run.Task, tail, w)))
	default:
		out = append(out, fit("between tasks · "+rangeText(s.Run.Phases, s.Run.Through), w))
	}
	if len(s.Recent) > 0 && s.Recent[0].End == report.EndRunning {
		var parts []string
		ts := s.Recent[0].Tasks
		for _, t := range ts[max(len(ts)-3, 0):] {
			parts = append(parts, t.ID+" "+t.Result)
		}
		if len(parts) > 0 {
			out = append(out, m.th.paint(lookDim, fit("recent: "+strings.Join(parts, " · "), w)))
		}
	}
	return append(out, wrap("This screen only watches. Use that terminal to control the run.", w)...)
}

func (m *homeScreen) lockCard(w int) []string {
	s := m.snap
	l := s.Lock
	var head string
	switch {
	case l.Unreadable:
		head = "LOCK UNREADABLE (" + l.Reason + ")"
	case l.Stale:
		head = fmt.Sprintf("LAST RUN DID NOT CLEAN UP (pid %d gone)", l.Info.PID)
	default:
		head = fmt.Sprintf("LOCKED by a run on host %s (since %s); igris can't tell if it is alive", l.Info.Host, l.Info.StartedAt.In(m.loc).Format("Jan 02 15:04"))
	}
	out := wrap(head, w)
	for i := range out {
		out[i] = m.th.paint(lookTitle, out[i])
	}
	if m.resumable() {
		out = append(out, m.runLines(w)...)
	}
	return append(out, wrap(m.ariseLabel()+" asks before clearing the lock.", w)...)
}

// ariseLabel is the bar's label for the run action, without the ellipsis.
func (m *homeScreen) ariseLabel() string {
	if m.resumable() {
		return "Resume"
	}
	return "Arise"
}

func (m *homeScreen) interruptedCard(w int) []string {
	r := m.snap.Run
	word, at := "STOPPED", r.StartedAt
	if r.Task != "" {
		word, at = "INTERRUPTED", r.Since
	}
	head := word + " · last run " + rangeText(r.Phases, r.Through)
	if t, err := time.Parse(time.RFC3339, at); err == nil {
		head += " · " + shortSince(m.now(), t) + " ago"
	}
	l := lookTitle
	if r.Task != "" {
		l = lookAlert
	}
	out := []string{m.th.paint(l, fit(head, w))}
	out = append(out, m.runLines(w)...)
	if r.Task != "" {
		return append(out, wrap("Resume picks "+r.Task+" up first, then the rest of "+rangeText(r.Phases, r.Through)+".", w)...)
	}
	return append(out, wrap("Resume continues "+rangeText(r.Phases, r.Through)+".", w)...)
}

// runLines describe the recorded run's task and session.
func (m *homeScreen) runLines(w int) []string {
	r := m.snap.Run
	if r == nil || r.Task == "" {
		return nil
	}
	out := []string{m.th.marks(m.taskLine(glyphs[plan.InProgress], r.Task, " · mode "+r.Mode+badge(r.Mode), w))}
	for _, l := range wrap("session: "+r.Session+" (Resume reattaches if it is still open)", w) {
		if r.Session != "" {
			out = append(out, m.th.paint(lookDim, l))
		}
	}
	return out
}

func (m *homeScreen) readyCard(w int) []string {
	p := m.snap.Plan
	var work *report.PhaseStatus
	var stuck *report.PhaseStatus
	for i := range m.phases() {
		ph := &m.phases()[i]
		switch ph.Outcome {
		case plan.Next.String():
			if work == nil {
				work = ph
			}
		case plan.Stuck.String():
			if stuck == nil {
				stuck = ph
			}
		}
	}
	var out []string
	switch {
	case work != nil:
		out = append(out, m.th.paint(lookTitle, fit("READY · phase "+work.ID+" "+work.Title, w)))
		out = append(out, m.nextLines(p, work, w)...)
	case stuck != nil:
		out = append(out, m.th.paint(lookAlert, fit("STUCK · phase "+stuck.ID+" "+stuck.Title+" can't start", w)))
		if sel, err := p.Select(stuck.ID); err == nil && len(sel.Waiting) > 0 {
			out = append(out, fit(sel.Waiting[0].String(), w))
		}
	default:
		out = append(out, m.th.paint(lookTitle, fit("COMPLETE · every phase is done", w)))
	}
	switch {
	case !m.backendKnown:
		out = append(out, m.th.paint(lookDim, "checking herdr…"))
	case m.backendErr != nil:
		out = append(out, wrap("Arise needs herdr (not reachable): run igris inside a herdr pane.", w)...)
	}
	return out
}

// nextLines are the next and then tasks of the phase with work, the tasks
// left and how many wait on the next one.
func (m *homeScreen) nextLines(p *plan.Plan, ph *report.PhaseStatus, w int) []string {
	next := p.Task(ph.Next)
	if next == nil {
		return nil
	}
	var then *plan.Task
	left, waitOn := 0, 0
	for _, t := range p.Phase(ph.ID).Tasks {
		if t.Status.Satisfied() {
			continue
		}
		left++
		if t == next {
			continue
		}
		wt := p.WaitingOn(t)
		if wt == nil {
			if then == nil && t.Status != plan.InProgress {
				then = t
			}
			continue
		}
		onNext := false
		other := false
		for _, dep := range wt.Unmet {
			if dep == next.ID {
				onNext = true
			} else {
				other = true
			}
		}
		if onNext {
			waitOn++
		}
		if then == nil && onNext && !other {
			then = t
		}
	}
	idW, rankW := textWidth(next.ID), textWidth(rank(next))
	if then != nil {
		idW, rankW = max(idW, textWidth(then.ID)), max(rankW, textWidth(rank(then)))
	}
	row := func(label string, t *plan.Task) string {
		titleW := w - 5 - 3 - idW - 2 - 1 - rankW // "next  · " + ID + "  " + title + " " + rank
		if titleW < 4 {
			return fit(label+" · "+t.ID+" "+m.th.rank(rank(t), false), w)
		}
		return fit(pad(label, 5)+" · "+pad(t.ID, idW)+"  "+pad(fit(t.Title, titleW), titleW)+" "+m.th.rank(rank(t), false), w)
	}
	out := []string{row("next", next)}
	if then != nil {
		out = append(out, row("then", then))
	}
	summary := strconv.Itoa(left) + " left"
	if waitOn > 0 {
		summary += " · " + strconv.Itoa(waitOn) + " wait on " + next.ID
	}
	return append(out, m.th.paint(lookDim, fit(summary, w)))
}

// healthLines are the HEALTH lines: the check result and the doctor
// summary, each a glyph and a word (SPEC §15.4).
func (m *homeScreen) healthLines() []homeLine {
	s := m.snap
	var planLine homeLine
	switch {
	case s == nil:
		planLine = homeLine{glyphs[plan.Ready] + " plan …", "", actNone}
	case s.PlanMissing:
		planLine = homeLine{glyphs[plan.Blocked] + " plan missing · " + m.planName() + " not found", glyphs[plan.Blocked] + " plan missing", actNone}
	case s.Plan == nil:
		planLine = homeLine{glyphs[plan.Blocked] + " plan unreadable · " + s.PlanErr, glyphs[plan.Blocked] + " plan unreadable", actCheck}
	case len(s.Issues) > 0:
		planLine = homeLine{glyphs[plan.Blocked] + " plan invalid · " + plural(len(s.Issues), "problem"), glyphs[plan.Blocked] + " plan invalid", actCheck}
	default:
		planLine = homeLine{fmt.Sprintf("%s plan valid · %s · %s", glyphs[plan.Done], plural(len(s.Plan.Phases), "phase"), plural(len(s.Plan.Tasks), "task")), glyphs[plan.Done] + " plan valid", actCheck}
	}
	doc := homeLine{"… doctor: checking", "", actDoctor}
	if m.doctorKnown {
		fails, warns := 0, 0
		first := ""
		for _, r := range m.doctor {
			switch r.Level {
			case checks.Fail:
				fails++
				if first == "" || warns > 0 && fails == 1 {
					first = r.Message
				}
			case checks.Warn:
				warns++
				if first == "" {
					first = r.Message
				}
			}
		}
		switch {
		case fails > 0:
			doc.short = glyphs[plan.Blocked] + " doctor: " + plural(fails, "failure")
		case warns > 0:
			doc.short = "! doctor: " + plural(warns, "warning")
		default:
			doc.short = glyphs[plan.Done] + " doctor: ok"
		}
		doc.text = doc.short
		if first != "" {
			doc.text += " — " + first
		}
	}
	return []homeLine{planLine, doc}
}

// recentLines are the RECENT lines: the last runs, newest first.
func (m *homeScreen) recentLines() []homeLine {
	if m.snap == nil || len(m.snap.Recent) == 0 {
		return []homeLine{{"no runs yet", "", actNone}}
	}
	var out []homeLine
	for _, r := range m.snap.Recent {
		when := "—"
		if t, err := time.Parse(time.RFC3339, r.StartedAt); err == nil {
			when = t.In(m.loc).Format("Jan 02 15:04")
		}
		text := when + "  " + strings.Join(r.Phases, ", ") + "  " + strconv.Itoa(r.Done) + " done"
		if r.Skipped > 0 {
			text += " · " + strconv.Itoa(r.Skipped) + " skipped"
		}
		out = append(out, homeLine{text + " · " + r.End, "", actHistory})
	}
	return out
}

// header is the header's left part: project, plan file, herdr and mode
// (wide), or project and herdr (narrow).
func (m *homeScreen) header(wide bool) string {
	parts := []string{m.th.paint(lookAccentBold, "igris")}
	s := m.snap
	if s != nil {
		parts = append(parts, s.Project)
	}
	if wide && s != nil {
		if s.NoConfig {
			parts = append(parts, "no igris.toml")
		} else if s.PlanMissing {
			parts = append(parts, "no plan")
		} else {
			parts = append(parts, m.planName())
		}
	}
	switch {
	case !m.backendKnown:
		parts = append(parts, "herdr …")
	case m.backendErr != nil:
		parts = append(parts, m.th.paint(lookAlert, "herdr ⨯"))
	default:
		parts = append(parts, m.th.paint(lookAccent, "herdr ✓"))
	}
	if wide && s != nil && !s.NoConfig && s.Config != nil {
		parts = append(parts, m.th.marks("mode "+s.Config.DefaultMode+badge(s.Config.DefaultMode)))
	}
	return strings.Join(parts, " · ")
}

// rangeText is a run's range: "M2", "M2 through M3" or "M2, M3".
func rangeText(phases []string, through string) string {
	switch {
	case len(phases) == 0:
		return "—"
	case through != "" && through != phases[len(phases)-1]:
		return phases[0] + " through " + through
	case len(phases) == 1:
		return phases[0]
	}
	return strings.Join(phases, ", ")
}

// shortSince is the time from t to now in the largest useful unit:
// "30s", "14m", "2h", "3d".
func shortSince(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(max(d, 0)/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// plural is "1 problem" / "2 problems".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// orText is r's Unreadable text, or def when there is no run.
func orText(r *report.RunInfo, def string) string {
	if r == nil {
		return def
	}
	return r.Unreadable
}
