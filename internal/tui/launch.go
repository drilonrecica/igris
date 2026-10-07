package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The start-run wizard (SPEC §15.6) is a sequence of dialogs over home:
// what to run, phase, through, mode, a summary, the start-up questions
// and the launch. Each question has `igris arise`'s default and meaning.
// Everything the owner answers lives in one launch value, thrown away when
// the launch is over: nothing carries over to the next run (§7.3).
//
// Once the run passed its start-up checks (its first event), the run view
// is pushed over home. Leaving it, or a Stop, stops the engine; home waits
// for the end, closes the view and says how the run ended.

// wizStep is the wizard's open dialog.
type wizStep int

const (
	wizWhat       wizStep = iota // resume, or start a phase
	wizPhase                     // which phase
	wizThrough                   // through which phase
	wizMode                      // the run mode
	wizYolo                      // the typed phrase for a yolo pick
	wizChecking                  // arise's start-up checks are running
	wizSummary                   // what will run, and the warnings
	wizConfirm                   // a start-up warning that needs a yes
	wizStarting                  // the engine runs its start-up checks
	wizDrift                     // readiness drift: let igris fix it?
	wizYoloLaunch                // a task needs yolo: the typed phrase
	wizUnlock                    // a stale or remote lock: clear it?
	wizFailed                    // the run can't start; no retry
)

// launch is one go of the wizard: the request, the owner's answers and
// the engine being started.
type launch struct {
	step   wizStep
	req    report.RunRequest
	conf   report.Confirmations
	resume bool // run the last run's phases again (Phase "")
	// phases and throughs are the phase and through dialogs' options, in
	// order; "" in throughs is "only the phase".
	phases, throughs []string
	pre              *report.Prelaunch // the start-up checks, once back
	confirm          int               // the warning to ask about next
	run              *runHandle        // the engine starting, if any
	runner           chan Runner       // hands the built engine to the wait
	cancelled        bool              // Cancel was picked while starting
}

// liveRun is the run home started, while its run view is on the stack.
type liveRun struct {
	h       *runHandle
	sender  Runner
	view    tea.Model // nil when it ended before it was shown
	leaving bool      // the owner left the view: close it at the end
	ended   bool
}

// Messages of the launch and the run. They reach home wherever it is on
// the stack (ownedMsg).
type (
	prelaunchMsg struct {
		l   *launch
		pre report.Prelaunch
	}
	// launchedMsg: the run passed its start-up checks.
	launchedMsg struct {
		l *launch
		r Runner
	}
	// launchFailedMsg: the run ended before its first event.
	launchFailedMsg struct {
		l   *launch
		res engine.Result
		err error
	}
	leaveRunMsg struct{}             // the owner left the run view
	runEndedMsg struct{ r *liveRun } // the run home started ended
)

// notStarted is what arise says when a start-up question was declined.
const notStarted = "not confirmed; nothing was started"

// openWizard starts a launch, prefilled with arise's flags.
func (m *homeScreen) openWizard(pre Wizard) tea.Cmd {
	if m.launch != nil || m.run != nil {
		return nil
	}
	m.launch = &launch{
		req:  report.RunRequest{Through: pre.Through, Mode: pre.Mode},
		conf: report.Confirmations{ForceUnlock: pre.ForceUnlock},
	}
	if m.resumable() {
		m.launch.step = wizWhat
		m.dialog = &dialog{
			title:  "What to run",
			detail: "Resume continues the last run's phases (" + m.lastRange() + "); a session left open is reattached.",
			options: []option{
				{"Resume last run (" + m.resumeFirst() + ")", actWizResume},
				{"Start a phase…", actWizPhase},
			},
			cancel: actClose,
		}
		return nil
	}
	return m.phaseStep()
}

// ariseFrom opens the wizard with the choices of a previewed request: its
// phase is selected (resume stays the first choice), its through and mode
// are the ones to start on.
func (m *homeScreen) ariseFrom(req report.RunRequest) tea.Cmd {
	if !m.offers(actArise) {
		m.setStatus("Arise isn't available here; the card says why")
		return nil
	}
	if req.Phase == "" {
		return m.openWizard(Wizard{Mode: req.Mode, Through: req.Through})
	}
	if m.launch != nil || m.run != nil {
		return nil
	}
	m.launch = &launch{req: report.RunRequest{Phase: req.Phase, Through: req.Through, Mode: req.Mode}}
	return m.phaseStep()
}

// openPending opens the wizard `igris arise` asked for once home knows
// whether a run can start here.
func (m *homeScreen) openPending() tea.Cmd {
	if m.pending == nil || m.snap == nil || !m.backendKnown {
		return nil
	}
	pre := *m.pending
	m.pending = nil
	if !m.offers(actArise) {
		m.setStatus("Arise isn't available here; the card says why")
		return nil
	}
	return m.openWizard(pre)
}

// offers says the bar has a in it right now.
func (m *homeScreen) offers(a action) bool {
	for _, b := range m.buttons() {
		if b.act == a {
			return true
		}
	}
	return false
}

// lastRange is the last run's range, "M2 through M3".
func (m *homeScreen) lastRange() string {
	r := m.snap.Run
	return rangeText(r.Phases, r.Through)
}

// resumeFirst says what Resume does first: the interrupted task, else the
// range.
func (m *homeScreen) resumeFirst() string {
	if t := m.snap.Run.Task; t != "" {
		return t + " first"
	}
	return m.lastRange()
}

// phaseStep asks which phase: phases with work first, complete ones last.
// The selected phase is the default when it has work.
func (m *homeScreen) phaseStep() tea.Cmd {
	l := m.launch
	want := cmp.Or(l.req.Phase, m.phaseID) // a previewed phase starts selected
	l.step, l.resume, l.req.Phase = wizPhase, false, ""
	d := &dialog{title: "Phase", detail: "Which phase to run.", cancel: actClose}
	var work, complete []option
	var workIDs, completeIDs []string
	for _, r := range m.phaseRowsData() {
		if r.word == "done" {
			complete = append(complete, option{r.id + " " + r.title + " · " + r.count + " complete", actWizPhase})
			completeIDs = append(completeIDs, r.id)
			continue
		}
		work = append(work, option{r.id + " " + r.title + " · " + r.count + " " + r.word, actWizPhase})
		workIDs = append(workIDs, r.id)
	}
	d.options = append(work, complete...)
	l.phases = append(workIDs, completeIDs...)
	if len(d.options) == 0 {
		m.closeWizard()
		m.setStatus("the plan has no phases to run")
		return nil
	}
	// The phases with work come first, so the first is the default when
	// the selected phase has none.
	d.selected = max(slices.Index(workIDs, want), 0)
	m.dialog = d
	return nil
}

// throughStep asks how far to run: only the phase, or through a later one.
// With no later phase there is nothing to ask.
func (m *homeScreen) throughStep() tea.Cmd {
	l := m.launch
	ids := m.phaseIDs()
	at := slices.Index(ids, l.req.Phase)
	if at < 0 || at == len(ids)-1 {
		l.req.Through = ""
		return m.modeStep()
	}
	l.step = wizThrough
	d := &dialog{title: "Through", detail: "Run the following phases too, up to and including the one picked; a stuck phase ends the run early.", cancel: actClose}
	l.throughs = []string{""}
	d.options = []option{{"Only " + l.req.Phase, actWizThrough}}
	for _, id := range ids[at+1:] {
		if id == l.req.Through {
			d.selected = len(d.options)
		}
		l.throughs = append(l.throughs, id)
		d.options = append(d.options, option{l.req.Phase + " through " + id, actWizThrough})
	}
	m.dialog = d
	return nil
}

// phaseIDs are the plan's phases in file order.
func (m *homeScreen) phaseIDs() []string {
	var out []string
	for _, ph := range m.phases() {
		out = append(out, ph.ID)
	}
	return out
}

// modeStep asks for the run mode; the one picked before (--mode) starts
// selected, else As planned.
func (m *homeScreen) modeStep() tea.Cmd {
	l := m.launch
	l.step = wizMode
	def := "default"
	if m.snap.Config != nil && m.snap.Config.DefaultMode != "" {
		def = m.snap.Config.DefaultMode
	}
	d := &dialog{
		title:   "Mode",
		detail:  "The permission mode of this run's sessions.",
		options: []option{{"As planned (Mode column, then default_mode: " + def + ")", actWizPlanned}},
		cancel:  actClose,
	}
	for _, md := range modeActs {
		if md.mode == l.req.Mode {
			d.selected = len(d.options)
		}
		d.options = append(d.options, option{md.label, md.act})
	}
	m.dialog = d
	return nil
}

// checkStep runs arise's start-up checks; the summary follows.
func (m *homeScreen) checkStep() tea.Cmd {
	l := m.launch
	l.step = wizChecking
	m.dialog = &dialog{
		title:   "Checking…",
		detail:  "Running arise's start-up checks: the backend, Claude Code, the config, the plan, git.",
		options: []option{{"Cancel", actClose}},
		cancel:  actClose,
	}
	ctx, svc, req := m.ctx, m.svc, l.req
	return async(m, "prelaunch", func() tea.Msg { return prelaunchMsg{l, svc.Prelaunch(ctx, req)} })
}

// summaryStep shows what will run and what arise warns about.
func (m *homeScreen) summaryStep() {
	l := m.launch
	l.step = wizSummary
	var lines []string
	for _, w := range l.pre.Warnings {
		lines = append(lines, "warning: "+w.String())
	}
	if t := l.pre.Interrupted; t != "" {
		lines = append(lines, "the interrupted task "+t+" is picked up first")
	}
	m.dialog = &dialog{
		title:   m.summary(),
		detail:  strings.Join(lines, "\n"),
		options: []option{{"Arise", actWizArise}, {"Preview", actPreview}, {"Cancel", actClose}},
		cancel:  actClose,
	}
}

// summary is the run in one line: "Arise M2 through M3 · mode plan · 9
// tasks · first M2-04 sonnet".
func (m *homeScreen) summary() string {
	l := m.launch
	verb, from, through := "Arise", l.req.Phase, l.req.Through
	if l.resume {
		verb, from, through = "Resume", m.snap.Run.Phases[0], m.snap.Run.Through
	}
	scope := from
	if through != "" && through != from {
		scope += " through " + through
	}
	mode := l.req.Mode
	if mode == "" {
		mode = "as planned"
	}
	out := verb + " " + scope + " · mode " + mode + badge(l.req.Mode)
	p := m.snap.Plan
	phases, err := p.PhasesThrough(from, through)
	if err != nil {
		return out
	}
	left := 0
	for _, ph := range phases {
		for _, t := range ph.Tasks {
			if !t.Status.Satisfied() {
				left++
			}
		}
	}
	out += " · " + plural(left, "task")
	first := l.pre.Interrupted
	for _, ph := range m.phases() {
		if first != "" {
			break
		}
		if slices.ContainsFunc(phases, func(x *plan.Phase) bool { return x.ID == ph.ID }) {
			first = ph.Next
		}
	}
	if t := p.Task(first); t != nil {
		out += " · first " + t.ID + " " + rank(t)
	}
	return out
}

// nextConfirm asks about the next start-up warning that needs a yes, Cancel
// first as in arise; once none is left, the run starts.
func (m *homeScreen) nextConfirm() tea.Cmd {
	l := m.launch
	for ; l.confirm < len(l.pre.Warnings); l.confirm++ {
		w := l.pre.Warnings[l.confirm]
		if !w.Confirm {
			continue
		}
		l.step = wizConfirm
		m.dialog = &dialog{
			title:   "Start the run anyway?",
			detail:  "warning: " + w.String(),
			options: []option{{"Cancel", actClose}, {"Start anyway", actWizConfirm}},
			cancel:  actClose,
		}
		return nil
	}
	return m.start()
}

// start builds and runs the engine with the answers so far. Once it passed
// its start-up checks (the first event) the run view is pushed; an error
// before that ends up in launchFailed, after the engine is gone.
func (m *homeScreen) start() tea.Cmd {
	l := m.launch
	l.step, l.cancelled = wizStarting, false
	m.dialog = &dialog{
		title:   "Starting…",
		detail:  "checking the backend, taking the lock",
		options: []option{{"Cancel", actClose}},
		cancel:  actClose,
	}
	feed := NewFeed()
	runCtx, stop := context.WithCancel(m.ctx)
	l.run = &runHandle{feed: feed, stop: stop}
	l.runner = make(chan Runner, 1)
	if m.setRun != nil {
		m.setRun(l.run)
	}
	svc, req, conf, runners := m.svc, l.req, l.conf, l.runner
	go func() {
		r, err := svc.Start(runCtx, req, conf, feed.Push)
		if err != nil {
			feed.End(engine.Result{}, err)
			return
		}
		runners <- r
		feed.End(r.Run(runCtx))
	}()
	return func() tea.Msg {
		select {
		case <-feed.Started():
		case <-feed.Ended():
			select {
			case <-feed.Started(): // it started, then ended at once
			default:
				res, err := feed.Result()
				return ownedMsg{m, launchFailedMsg{l, res, err}}
			}
		}
		return ownedMsg{m, launchedMsg{l, <-runners}}
	}
}

// launched pushes the run view over home.
func (m *homeScreen) launched(msg launchedMsg) tea.Cmd {
	l := msg.l
	if l != m.launch {
		return nil
	}
	r := &liveRun{h: l.run, sender: msg.r}
	m.run = r
	m.closeWizard() // the launch is over; its answers go with it
	wait := func() tea.Msg {
		<-r.h.feed.Ended()
		return ownedMsg{m, runEndedMsg{r}}
	}
	if l.cancelled {
		r.leaving = true
		r.h.stop()
		return wait
	}
	r.view = m.runView(l.req, r)
	return tea.Batch(push(r.view), wait)
}

// runView is the run view of r, as `igris arise` shows it, with Home in
// place of Quit.
func (m *homeScreen) runView(req report.RunRequest, r *liveRun) tea.Model {
	s := m.snap
	o := Options{
		Project:  s.Project,
		Mode:     req.Mode,
		PlanPath: s.PlanPath,
		Feed:     r.h.feed,
		Sender:   r.sender,
		Focus:    m.svc.Focus,
		Leave:    func() tea.Msg { return ownedMsg{m, leaveRunMsg{}} },
	}
	if s.Config != nil {
		o.Backend = s.Config.Backend
		o.PlanOptions = plan.Options{Columns: s.Config.Columns}
	}
	v := newModel(m.ctx, o)
	v.th = m.th
	return v
}

// launchFailed answers an error from before the run started as arise
// does: a question to retry with, or the error.
func (m *homeScreen) launchFailed(msg launchFailedMsg) tea.Cmd {
	l := msg.l
	if l != m.launch {
		return nil
	}
	if m.setRun != nil {
		m.setRun(nil) // the engine is gone
	}
	l.run = nil
	err := msg.err
	if l.cancelled {
		m.closeWizard()
		m.setStatus("nothing was started")
		return nil
	}
	var drift *engine.DriftError
	var stale *state.StaleLockError
	var locked *state.LockedError
	switch {
	case errors.As(err, &drift) && !l.conf.Drift:
		// SPEC §5.2: list the drift, ask before the first write fixes it.
		lines := []string{"The plan's ready/blocked cells don't match its dependencies; igris's first write would change:"}
		for _, c := range drift.Changes {
			lines = append(lines, "· "+textsafe.Line(c.String()))
		}
		l.step = wizDrift
		m.dialog = &dialog{
			title:   "Let igris fix them?",
			detail:  strings.Join(lines, "\n"),
			options: []option{{"Cancel", actClose}, {"Let igris fix them", actWizConfirm}},
			cancel:  actClose,
		}
	case errors.Is(err, engine.ErrYoloUnconfirmed) && !l.conf.Yolo:
		// SPEC §7.3: typed confirmation, every run.
		l.step = wizYoloLaunch
		d := yoloDialog("")
		d.detail = textsafe.Line(err.Error()) + ". Sessions in this mode run with --dangerously-skip-permissions: Claude Code acts without asking. Type \"" + engine.YoloPhrase + "\" to confirm."
		m.dialog = d
	case errors.As(err, &stale) && !l.conf.ForceUnlock:
		l.step = wizUnlock
		m.dialog = unlockDialog("The last run did not clean up: lock " + textsafe.Line(stale.Reason) + ".")
	case errors.As(err, &locked) && locked.Remote && !l.conf.ForceUnlock:
		l.step = wizUnlock
		m.dialog = unlockDialog("igris is locked by a run on another host (" + textsafe.Line(locked.Info.String()) + "); igris can't tell if it is alive. Clear the lock only if that run is gone.")
	case err != nil:
		l.step = wizFailed
		m.dialog = &dialog{
			title:   "The run did not start",
			detail:  textsafe.Line(err.Error()) + "\n\nDoctor shows what is wrong with the setup and what fixes it.",
			options: []option{{"OK", actClose}, {"Doctor", actDoctor}},
			cancel:  actClose,
		}
	default:
		// It ended without an event or an error: nothing ran.
		m.closeWizard()
		m.setStatus(ExitText(msg.res, nil))
	}
	return nil
}

// unlockDialog asks before clearing a lock (--force-unlock).
func unlockDialog(why string) *dialog {
	return &dialog{
		title:   "Clear the lock and start?",
		detail:  why + " Clearing it is what `igris arise --force-unlock` does.",
		options: []option{{"Cancel", actClose}, {"Clear the lock and start", actWizConfirm}},
		cancel:  actClose,
	}
}

// wizardPick runs what the owner picked in the wizard's dialog.
func (m *homeScreen) wizardPick(a action) tea.Cmd {
	l, d := m.launch, m.dialog
	switch a {
	case actNone:
		return nil
	case actClose:
		return m.cancelWizard()
	}
	switch l.step {
	case wizWhat:
		if a == actWizResume {
			l.resume, l.req.Phase, l.req.Through = true, "", ""
			return m.modeStep()
		}
		return m.phaseStep()
	case wizPhase:
		l.req.Phase = l.phases[d.selected]
		return m.throughStep()
	case wizThrough:
		l.req.Through = l.throughs[d.selected]
		return m.modeStep()
	case wizMode:
		if a == actWizPlanned {
			l.req.Mode = ""
			return m.checkStep()
		}
		mode, ok := modeOf(a)
		if !ok {
			return nil
		}
		if mode == engine.ModeYolo {
			// Never a single click or key (SPEC §7.3).
			l.step = wizYolo
			m.dialog = yoloDialog("")
			m.dialog.detail = "Claude Code runs this run's sessions with --dangerously-skip-permissions: no permission prompts at all. Type \"" + engine.YoloPhrase + "\" to confirm."
			return nil
		}
		l.req.Mode = mode
		return m.checkStep()
	case wizYolo, wizYoloLaunch:
		if a != actYoloConfirm {
			return nil
		}
		if strings.TrimSpace(d.input.value) != engine.YoloPhrase {
			d.inField, d.input.hint = true, "Type exactly: "+engine.YoloPhrase
			return nil
		}
		l.conf.Yolo = true
		if l.step == wizYolo {
			l.req.Mode = engine.ModeYolo
			return m.checkStep()
		}
		return m.start()
	case wizSummary:
		switch a {
		case actWizArise:
			l.confirm = 0
			return m.nextConfirm()
		case actPreview:
			req := l.req // resuming has no phase: the last run's
			m.closeWizard()
			return m.openPreview(req)
		}
	case wizConfirm:
		if a == actWizConfirm {
			l.confirm++
			return m.nextConfirm()
		}
	case wizDrift:
		if a == actWizConfirm {
			l.conf.Drift = true
			return m.start()
		}
	case wizUnlock:
		if a == actWizConfirm {
			l.conf.ForceUnlock = true
			return m.start()
		}
	case wizFailed:
		if a == actDoctor {
			m.closeWizard()
			return m.activate(actDoctor)
		}
	}
	return nil
}

// cancelWizard is Cancel or esc: the wizard closes and nothing starts.
// While the engine starts, it is stopped first and the wizard closes once
// it is gone.
func (m *homeScreen) cancelWizard() tea.Cmd {
	l := m.launch
	switch l.step {
	case wizStarting:
		if !l.cancelled {
			l.cancelled = true
			l.run.stop()
			m.dialog.detail = "stopping…"
		}
		return nil
	case wizYolo:
		return m.modeStep() // back to the mode, which stays unchanged
	case wizConfirm, wizDrift, wizYoloLaunch, wizUnlock:
		m.closeWizard()
		m.setStatus(notStarted)
		return nil
	}
	m.closeWizard()
	return nil
}

// closeWizard throws the launch away.
func (m *homeScreen) closeWizard() {
	m.launch, m.dialog = nil, nil
}

// leaveRun is the run view's Home: the engine is stopped (the session
// keeps running) and the view closes once it is gone.
func (m *homeScreen) leaveRun() tea.Cmd {
	r := m.run
	if r == nil {
		return nil
	}
	if r.ended {
		return m.runOver(r)
	}
	r.leaving = true
	r.h.stop()
	return nil
}

// runEnded handles the end of the run home started. A run the owner
// stopped or left goes back to home; one that ended on its own keeps its
// view open, with its end banner, until the owner leaves it.
func (m *homeScreen) runEnded(r *liveRun) tea.Cmd {
	if r != m.run {
		return nil
	}
	r.ended = true
	res, err := r.h.feed.Result()
	if r.leaving || r.view == nil || (err == nil && res.Outcome == engine.Stopped) {
		return m.runOver(r)
	}
	return nil
}

// runOver closes the run's view and says on the status line how the run
// ended, in arise's words.
func (m *homeScreen) runOver(r *liveRun) tea.Cmd {
	m.run = nil
	if m.setRun != nil {
		m.setRun(nil)
	}
	res, err := r.h.feed.Result()
	m.setStatus(ExitText(res, err))
	if r.view == nil {
		return m.refresh()
	}
	return closeScreen(r.view) // home refreshes when it is on top again
}

// ExitText is how a run shown in the TUI ended, in the words `igris
// arise` prints once the terminal is back.
func ExitText(res engine.Result, err error) string {
	switch {
	case err != nil:
		return "the run failed: " + textsafe.Line(err.Error())
	case res.Outcome == engine.Stuck:
		return fmt.Sprintf("phase %s is stuck: unfinished tasks, none can start (see `igris status %s`)", res.Phase, res.Phase)
	case res.Outcome == engine.Stopped:
		return "igris stopped; a running session keeps running — `igris arise` resumes"
	}
	return fmt.Sprintf("run %s", res.Outcome)
}
