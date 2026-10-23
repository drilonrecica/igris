package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/serve"
)

// The run view attached to a run served by igris serve (SPEC §13, §14,
// §15.3): it starts from serve's snapshot and starts over from a fresh one
// after a reconnect; the events in between are the engine's, as for a run
// in this process.

// cardStates are the snapshot's card states.
var cardStates = map[string]taskState{
	serve.StateWorking:   stateWorking,
	serve.StateYourTurn:  stateYourTurn,
	serve.StateWaiting:   stateWaiting,
	serve.StateNeedsYou:  stateNeedsYou,
	serve.StateVerifying: stateVerifying,
	serve.StateLost:      stateLost,
	serve.StateQuestion:  stateQuestion,
}

// restore puts the view in the state of s: the run's facts, the current
// task, a pending question and the log of its last events. What the
// owner had open stays only when it is a side session's (not the run's).
func (m *model) restore(s serve.Snapshot) {
	m.lost = false
	m.servedBy = ""
	if m.opts.Version != "" && s.Server != "" && s.Server != "igris "+m.opts.Version {
		m.servedBy = s.Server
	}
	m.phase, m.mode, m.backend = s.Phase, s.Mode, s.Backend
	m.slice = nil
	if s.Slice != nil {
		m.slice = map[string]bool{}
		for _, id := range s.Slice {
			m.slice[id] = true
		}
	}
	m.overrides = map[string]string{}
	for k, v := range s.Overrides {
		m.overrides[k] = v
	}
	m.paused, m.holding, m.worktree, m.quota = s.Paused, s.Holding, s.Worktree, s.Quota
	m.ended, m.endText = s.Ended, ""
	if s.Ended {
		m.endText = "the run stopped: " + s.End
	}
	// Every active task since 1.5; a serve of 1.4 sends the current one.
	sel := ""
	if m.cur != nil {
		sel = m.cur.id
	}
	m.cards, m.cur = nil, nil
	active := s.Active
	if len(active) == 0 && s.Current != nil {
		active = []serve.Current{*s.Current}
	}
	for i := range active {
		m.addCard(currentOf(&active[i]))
	}
	if c := m.card(sel); c != nil {
		m.cur = c // the owner's selection survives a reconnect
	}
	m.asks = map[string]*engine.Event{}
	questions := s.Questions
	if len(questions) == 0 && s.Question != nil {
		questions = []serve.Event{*s.Question}
	}
	for i := range questions {
		if q := eventOf(&questions[i]); q != nil {
			m.asks[q.Task] = q
		}
	}
	m.resetAsk = eventOf(s.Reset)
	m.tails = map[string][]string{}

	m.log, m.scroll = nil, 0
	prev := m.sum
	m.sum = summary{run: s.Run, start: s.Started}
	if prev.run != "" && prev.run == s.Run {
		// A reconnect: the feed can't rebuild the needs-you time so far.
		m.sum.needsYou, m.sum.needSince = prev.needsYou, prev.needSince
	}
	for _, w := range s.Feed {
		if ev, ok := w.Engine(); ok {
			m.logEvent(ev)
			m.sum.note(ev)
		}
	}
	// What waits on the owner now has waited since it was asked, also for
	// a view that wasn't there to see it.
	m.sum.need(m.needsOwner() && !s.Ended, m.askedSince())
	if s.Ended {
		m.sum.outcome = outcomeOf(s.End)
		m.endFocus()
	}
	if m.servedBy != "" {
		m.addLog(m.opts.Now(), "this run is served by "+m.servedBy+" and keeps that igris until it ends", lookTitle)
	}

	if !m.side.owns(m.dialog) {
		m.dialog = nil
		if q := m.question(); q != nil {
			m.dialog = questionDialog(*q)
		}
	}
	m.taskTop = -1
	m.resetTail()
}

// askedSince is when what waits on the owner began: its oldest pending
// question's time, else now.
func (m *model) askedSince() time.Time {
	at := m.opts.Now()
	for _, q := range m.asks {
		if !q.At.IsZero() && q.At.Before(at) {
			at = q.At
		}
	}
	if q := m.resetAsk; q != nil && !q.At.IsZero() && q.At.Before(at) {
		at = q.At
	}
	return at
}

// currentOf is the snapshot's current task as the card's.
func currentOf(c *serve.Current) *current {
	if c == nil {
		return nil
	}
	cur := &current{
		id: c.Task, title: c.Title, rank: c.Rank, model: c.Model, mode: c.Mode, user: c.User, slot: c.Slot,
		started: c.Started, state: cardStates[c.State], since: c.Since, detail: c.Detail, reason: c.Reason,
		overdue: c.Overdue, rankMismatch: c.RankMismatch, waiting: c.Waiting,
		session: c.Session, claudeSession: c.ClaudeSession,
	}
	if t := c.Tokens; t != nil {
		cur.tokens = &engine.Tokens{In: t.In, Out: t.Out, Models: t.Models, Unavailable: t.Unavailable}
	}
	return cur
}

// eventOf is a snapshot's pending question as the engine's event; nil
// when there is none or its type is unknown.
func eventOf(w *serve.Event) *engine.Event {
	if w == nil {
		return nil
	}
	ev, ok := w.Engine()
	if !ok {
		return nil
	}
	return &ev
}

// connLost says the connection to serve is gone: until the fresh snapshot
// the bar offers only Quit, and nothing the view showed can be answered.
func (m *model) connLost() {
	m.lost = true
	if !m.side.owns(m.dialog) {
		m.dialog = nil // the snapshot brings back what is still pending
	}
	m.page = nil
	m.resetTail()
}

// whileLost are the actions that work while the connection is lost:
// none of them goes to the run.
func whileLost(a action) bool {
	switch a {
	case actQuit, actDetach, actHelp, actClose, actCopy, actNone:
		return true
	}
	return false
}

// leave closes the view: back home when home pushed it, else the program
// ends. For an attached view this detaches; the run goes on in serve.
func (m *model) leave() tea.Cmd {
	if m.opts.Leave != nil {
		return m.opts.Leave
	}
	return tea.Quit
}

// attachedHelp is the help page's actions for an attached view: q
// detaches.
func attachedHelp() []helpEntry {
	out := make([]helpEntry, len(helpActions))
	copy(out, helpActions)
	for i, e := range out {
		if e.key == "q" {
			out[i].what = "leave the run view: Detach (the run goes on in igris serve; igris attach shows it again) or Stop run; ctrl+c detaches at once"
		}
	}
	return out
}

// detachDialog is what q asks in an attached view while its run goes on
// (SPEC §15.3): Detach, focused, leaves the run going; Stop run stops it
// as x does and closes the view once it has stopped. home says home
// pushed the view, so detaching goes home.
func detachDialog(home bool) *dialog {
	detach := "Detach"
	if home {
		detach = "Home"
	}
	return &dialog{
		title: "Leave the run view?",
		detail: strings.Join([]string{
			detach + " leaves the run going in igris serve; igris attach shows it again.",
			"Stop run stops it; the session stays open and igris arise picks the run up again.",
		}, " "),
		options: []option{{detach, actDetach}, {"Stop run", actStopLeave}, {"Cancel", actClose}},
		cancel:  actClose,
	}
}
