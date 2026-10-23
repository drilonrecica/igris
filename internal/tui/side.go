package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/side"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Side sessions in the TUI (SPEC §15.7): `S` on home and in the run view
// opens the Side sessions page while some are open, else the dialog that
// opens one. Both screens hold a sideUI; it draws its steps in the
// screen's dialog slot, so it works the same in a standalone run view,
// which has no app stack to push pages on.

// SideSessions is what the TUI asks of side sessions (SPEC §6.10):
// internal/project implements it on the project's backend, tests use a
// fake. Text in the results is already cleaned (SPEC §16).
type SideSessions interface {
	// Setup is what the dialog offers: the ranks, the [side] defaults and
	// the run going in this project. An error says why none can open.
	Setup(ctx context.Context) (SideSetup, error)
	// List reattaches the recorded side sessions and lists the open ones;
	// the lines say which ended and what was left alone.
	List(ctx context.Context) (SideList, error)
	// Prefill is a preset's first prompt (SPEC §6.10), cleaned.
	Prefill(ctx context.Context, req SidePrefill) (string, error)
	// Open opens a side session and returns its ID. An error with an ID
	// says it is open but its first prompt didn't reach it.
	Open(ctx context.Context, req SideRequest) (string, error)
	// Focus brings side session id's pane to the front.
	Focus(ctx context.Context, id string) error
	// Close closes side session id and says what became of it (its
	// worktree removed or kept), for the feed.
	Close(ctx context.Context, id string) (string, error)
}

// SideSetup is what the side-session dialog offers.
type SideSetup struct {
	Ranks []SideRank // the [models] ranks, [side] model first
	Mode  string     // [side] mode
	// Run is the run going in this project, nil for none: another igris's
	// for a read-only home.
	Run *SideRun
}

// SideRank is a [models] rank and the model it runs.
type SideRank struct{ Rank, Model string }

// SideRun is a run going while a side session opens.
type SideRun struct {
	Task string // the task a session works on now; "" between tasks
	// Worktree: the run's sessions work in a worktree of their own
	// (SPEC §6.8), not in the project root.
	Worktree bool
	// Parallel: the run's tasks work in worktrees of their own (SPEC
	// §6.11) and merge into the project root.
	Parallel bool
}

// SideRow is an open side session, as the Side sessions page lists it.
type SideRow struct {
	ID       string
	State    string // from its hooks: working, idle, blocked, waiting or unknown
	At       time.Time
	Subject  string // the task for about this task, "igris" for igris problem, "" for blank
	Rank     string
	Mode     string
	Worktree bool // it has a worktree of its own
}

// SideList is what List found.
type SideList struct {
	Rows []SideRow
	// Lines are side sessions found gone and records left alone, one
	// cleaned line each, for the feed.
	Lines []string
}

// SidePrefill asks for a preset's first prompt.
type SidePrefill struct {
	Preset  string // state.SidePresetTask or SidePresetIgris
	Task    string // the task, for SidePresetTask
	Running bool   // the task runs now
	// Run is the run state for SidePresetIgris; nil when none is going.
	Run *side.RunInfo
}

// SideRequest is a side session the dialog chose.
type SideRequest struct {
	Preset   string // state.SidePresetBlank, SidePresetTask or SidePresetIgris
	Task     string // the task, for SidePresetTask
	Rank     string
	Mode     string
	Worktree bool   // its own worktree
	Prompt   string // the first prompt; "" types nothing
	// RunTask is the task a run's session works on in the same tree now,
	// "" for none: the session is told to ask before editing files.
	RunTask string
}

// sideStep is the side flow's open dialog.
type sideStep int

const (
	sidePage       sideStep = iota // the Side sessions page
	sideCloseWhich                 // which session Close… is about
	sideCloseAsk                   // Keep it · Close session
	sideClosing                    // Close runs
	sidePreset                     // Blank · About this task · igris problem
	sidePreparing                  // Setup and the prefill run
	sidePrompt                     // the first prompt
	sideModel                      // the rank
	sideMode                       // the mode
	sideWhere                      // during a run: shared tree or own worktree
	sideConfirm                    // Open · Cancel
	sideOpening                    // Open runs
	sideFailed                     // it can't open; OK
)

// sideAt is what the screen knows when the owner presses S.
type sideAt struct {
	task    string // the task About this task is about; "" offers no such preset
	running bool   // that task runs now
	// run is the run going in this tree as the screen knows it (the run
	// view's own); nil leaves it to Setup.
	run *SideRun
	// state is the run state for the igris problem preset; nil for none.
	state *side.RunInfo
	place string // where a session opens: "herdr tab", "tmux window"
}

// sideFlow is one go of the page or the dialog; its answers go with it.
type sideFlow struct {
	step    sideStep
	d       *dialog // the dialog it shows; the screen's own while it holds the slot
	at      sideAt
	setup   SideSetup
	run     *SideRun // at.run, else setup.Run
	req     SideRequest
	presets []string
	modes   []string
	closing string // the session Close… asks about
}

// sideUI is a screen's side sessions: the rows of the last List and the
// flow while one is open.
type sideUI struct {
	ctx  context.Context
	svc  SideSessions
	now  func() time.Time
	wrap func(tea.Msg) tea.Msg // routes a result to the screen holding the UI
	rows []SideRow
	flow *sideFlow
	// shown are the feed lines already given, so a record left alone is
	// reported once, not on every List.
	shown map[string]bool
}

func newSideUI(ctx context.Context, svc SideSessions, now func() time.Time, wrap func(tea.Msg) tea.Msg) *sideUI {
	if wrap == nil {
		wrap = func(m tea.Msg) tea.Msg { return m }
	}
	return &sideUI{ctx: ctx, svc: svc, now: now, wrap: wrap, shown: map[string]bool{}}
}

// sideKind says which call a sideMsg answers.
type sideKind int

const (
	sideListed sideKind = iota
	sidePrepared
	sideOpened
	sideFocused
	sideClosed
)

// sideMsg is a side call's result; flow is the flow that asked, nil for a
// List of its own.
type sideMsg struct {
	ui    *sideUI
	flow  *sideFlow
	kind  sideKind
	list  SideList
	setup SideSetup
	text  string // the prefill, or Close's line
	id    string
	err   error
}

// sideTimeout bounds a call that doesn't open anything.
const sideTimeout = 30 * time.Second

// call runs fn off the loop and hands its result to the UI's screen.
func (s *sideUI) call(f *sideFlow, kind sideKind, timeout time.Duration, fn func(context.Context, *sideMsg)) tea.Cmd {
	ctx := s.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		msg := sideMsg{ui: s, flow: f, kind: kind}
		fn(ctx, &msg)
		return s.wrap(msg)
	}
}

// list asks for the open side sessions.
func (s *sideUI) list() tea.Cmd {
	if s == nil {
		return nil
	}
	svc := s.svc
	return s.call(nil, sideListed, sideTimeout, func(ctx context.Context, m *sideMsg) { m.list, m.err = svc.List(ctx) })
}

// count is how many side sessions are open, as last listed.
func (s *sideUI) count() int {
	if s == nil {
		return 0
	}
	return len(s.rows)
}

// owns says d is the dialog of the open flow.
func (s *sideUI) owns(d *dialog) bool {
	return s != nil && s.flow != nil && d != nil && s.flow.d == d
}

// open is S: the page while some are open, else the dialog; the list is
// read again either way.
func (s *sideUI) open(at sideAt) (*dialog, tea.Cmd) {
	s.flow = &sideFlow{at: at}
	if len(s.rows) > 0 {
		return s.page(0), s.list()
	}
	return s.presetStep(), s.list()
}

// show makes d the flow's dialog at step.
func (s *sideUI) show(step sideStep, d *dialog) *dialog {
	s.flow.step, s.flow.d = step, d
	return d
}

// done ends the flow; the screen's dialog closes.
func (s *sideUI) done() *dialog {
	s.flow = nil
	return nil
}

// page is the Side sessions page with option sel selected.
func (s *sideUI) page(sel int) *dialog {
	d := &dialog{
		title:  fmt.Sprintf("Side sessions · %d open", len(s.rows)),
		detail: "enter or o brings a session's pane to the front, x closes it. Quitting igris leaves them open.",
		cancel: actClose,
	}
	for _, r := range s.rows {
		d.options = append(d.options, option{s.rowLabel(r), actSidePick})
	}
	d.options = append(d.options, option{"New…", actSideNew}, option{"Close a session…", actSideCloseAsk}, option{"Back", actClose})
	d.selected = min(max(sel, 0), len(d.options)-1)
	return s.show(sidePage, d)
}

// rowLabel is a row as the page lists it: "a3f2c1 · working · 12m · M2-04
// · sonnet · default · worktree".
func (s *sideUI) rowLabel(r SideRow) string {
	parts := []string{"side " + r.ID, r.State, shortSince(s.now(), r.At)}
	if r.Subject != "" {
		parts = append(parts, r.Subject)
	}
	parts = append(parts, r.Rank, r.Mode)
	if r.Worktree {
		parts = append(parts, "worktree")
	}
	return strings.Join(parts, " · ")
}

// selectedRow is the row the page's selection is on, if it is one.
func (s *sideUI) selectedRow() (SideRow, bool) {
	f := s.flow
	if f == nil || f.step != sidePage || f.d.selected >= len(s.rows) {
		return SideRow{}, false
	}
	return s.rows[f.d.selected], true
}

// key handles the page's own keys, o and x, before the dialog's; ok says
// it used the key.
func (s *sideUI) key(msg tea.KeyMsg) (d *dialog, cmd tea.Cmd, ok bool) {
	f := s.flow
	if f == nil {
		return nil, nil, false
	}
	if f.step != sidePage {
		return f.d, nil, false
	}
	switch msg.String() {
	case "o":
		if r, ok := s.selectedRow(); ok {
			d, cmd := s.focus(r.ID)
			return d, cmd, true
		}
		return f.d, nil, true
	case "x":
		if r, ok := s.selectedRow(); ok {
			return s.closeAsk(r.ID), nil, true
		}
		return f.d, nil, true
	}
	return f.d, nil, false
}

// pick runs what the owner picked in the flow's dialog and returns the
// dialog to show next; nil closes it.
func (s *sideUI) pick(a action) (*dialog, tea.Cmd) {
	f := s.flow
	d := f.d
	switch a {
	case actNone:
		return d, nil
	case actClose:
		return s.done(), nil
	}
	switch f.step {
	case sidePage:
		switch a {
		case actSidePick:
			if d.selected < len(s.rows) {
				return s.focus(s.rows[d.selected].ID)
			}
		case actSideNew:
			return s.presetStep(), nil
		case actSideCloseAsk:
			return s.closeWhich(), nil
		}
	case sideCloseWhich:
		switch {
		case a == actSideBack:
			return s.page(0), nil
		case a == actSidePick && d.selected < len(s.rows):
			return s.closeAsk(s.rows[d.selected].ID), nil
		}
	case sideCloseAsk:
		switch a {
		case actSideBack:
			return s.page(slices.IndexFunc(s.rows, func(r SideRow) bool { return r.ID == f.closing })), nil
		case actSideCloseConfirm:
			return s.closeSession(f.closing)
		}
	case sidePreset:
		if a == actSidePick {
			return s.prepare(f.presets[d.selected])
		}
	case sidePrompt:
		if a == actSideNext {
			return s.promptDone()
		}
	case sideModel:
		if a == actSidePick {
			f.req.Rank = f.setup.Ranks[d.selected].Rank
			return s.modeStep(), nil
		}
	case sideMode:
		if a == actSidePick {
			f.req.Mode = f.modes[d.selected]
			if f.run != nil {
				return s.whereStep(), nil
			}
			return s.confirmStep(), nil
		}
	case sideWhere:
		if a == actSidePick {
			f.req.Worktree = d.selected == 1
			return s.confirmStep(), nil
		}
	case sideConfirm:
		if a == actSideOpen {
			return s.openSession()
		}
	}
	return d, nil
}

// focus brings side session id's pane to the front; the page closes.
func (s *sideUI) focus(id string) (*dialog, tea.Cmd) {
	svc := s.svc
	cmd := s.call(nil, sideFocused, sideTimeout, func(ctx context.Context, m *sideMsg) {
		m.id, m.err = id, svc.Focus(ctx, id)
	})
	return s.done(), cmd
}

// closeWhich asks which session to close; with one there is nothing to
// ask.
func (s *sideUI) closeWhich() *dialog {
	if len(s.rows) == 1 {
		return s.closeAsk(s.rows[0].ID)
	}
	d := &dialog{title: "Close which side session?", cancel: actSideBack}
	for _, r := range s.rows {
		d.options = append(d.options, option{s.rowLabel(r), actSidePick})
	}
	d.options = append(d.options, option{"Back", actSideBack})
	return s.show(sideCloseWhich, d)
}

// closeAsk asks before closing side session id; keeping it is the
// default.
func (s *sideUI) closeAsk(id string) *dialog {
	s.flow.closing = id
	return s.show(sideCloseAsk, &dialog{
		title:   "Close side " + id + "?",
		detail:  "Its pane closes. A worktree of its own is removed when it has no changes (its branch stays), and kept otherwise.",
		options: []option{{"Keep it", actSideBack}, {"Close session", actSideCloseConfirm}},
		cancel:  actSideBack,
	})
}

func (s *sideUI) closeSession(id string) (*dialog, tea.Cmd) {
	svc := s.svc
	d := s.show(sideClosing, &dialog{
		title:   "Closing side " + id + "…",
		options: []option{{"Hide", actClose}},
		cancel:  actClose,
	})
	return d, s.call(s.flow, sideClosed, sideTimeout+gitTimeout, func(ctx context.Context, m *sideMsg) {
		m.id = id
		m.text, m.err = svc.Close(ctx, id)
	})
}

// gitTimeout leaves Open and Close the time side.Manager gives git for a
// worktree.
const gitTimeout = 2 * time.Minute

// presetStep asks what the session is about; About this task only with a
// task to be about.
func (s *sideUI) presetStep() *dialog {
	f := s.flow
	f.presets = []string{state.SidePresetBlank}
	d := &dialog{
		title:   "Side session",
		detail:  "A Claude Code session of your own, in a " + f.at.place + ": igris doesn't watch it, and it finishes no task.",
		options: []option{{"Blank — no first prompt", actSidePick}},
		cancel:  actClose,
	}
	if f.at.task != "" {
		f.presets = append(f.presets, state.SidePresetTask)
		d.options = append(d.options, option{"About this task — " + f.at.task, actSidePick})
	}
	f.presets = append(f.presets, state.SidePresetIgris)
	d.options = append(d.options, option{"igris problem — the run state, the last events, doctor", actSidePick})
	return s.show(sidePreset, d)
}

// prepare reads the setup and the preset's first prompt off the loop.
func (s *sideUI) prepare(preset string) (*dialog, tea.Cmd) {
	f := s.flow
	f.req = SideRequest{Preset: preset}
	if preset == state.SidePresetTask {
		f.req.Task = f.at.task
	}
	d := s.show(sidePreparing, &dialog{title: "Preparing…", options: []option{{"Cancel", actClose}}, cancel: actClose})
	svc, req := s.svc, SidePrefill{Preset: preset, Task: f.req.Task, Running: f.at.running, Run: f.at.state}
	return d, s.call(f, sidePrepared, sideTimeout, func(ctx context.Context, m *sideMsg) {
		if m.setup, m.err = svc.Setup(ctx); m.err != nil || preset == state.SidePresetBlank {
			return
		}
		m.text, m.err = svc.Prefill(ctx, req)
	})
}

// promptStep shows the first prompt in a field, its whole text above it.
func (s *sideUI) promptStep(text string) *dialog {
	return s.show(sidePrompt, &dialog{
		title:   "First prompt",
		echo:    "Empty: the session opens at its input box and igris types nothing.",
		pre:     true,
		wide:    true,
		input:   &field{label: "Prompt: ", value: text},
		inField: true,
		submit:  actSideNext,
		options: []option{{"Next", actSideNext}, {"Cancel", actClose}},
		cancel:  actClose,
	})
}

// promptDone takes the first prompt, cleaned, once it fits.
func (s *sideUI) promptDone() (*dialog, tea.Cmd) {
	f := s.flow
	text := textsafe.Clean(f.d.input.value)
	if len(text) > side.MaxPrompt {
		f.d.inField = true
		f.d.input.hint = fmt.Sprintf("%.1f KiB: a first prompt is at most 8 KiB; shorten it", float64(len(text))/1024)
		return f.d, nil
	}
	f.req.Prompt = text
	return s.modelStep(), nil
}

// modelStep asks for the rank, [side] model first.
func (s *sideUI) modelStep() *dialog {
	d := &dialog{title: "Model", detail: "The rank the session runs, from [models].", cancel: actClose}
	for _, r := range s.flow.setup.Ranks {
		label := r.Rank
		if r.Model != r.Rank {
			label += " → " + r.Model
		}
		d.options = append(d.options, option{label, actSidePick})
	}
	return s.show(sideModel, d)
}

// sharedRun says a run's sessions work in the project root now, the tree a
// session opens in unless it gets its own worktree.
func (f *sideFlow) sharedRun() bool { return f.run != nil && !f.run.Worktree }

// modeStep asks for the mode: [side] mode first, manual first during a
// run in the shared tree; never yolo (SPEC §6.10).
func (s *sideUI) modeStep() *dialog {
	f := s.flow
	first := f.setup.Mode
	switch {
	case f.sharedRun():
		first = engine.ModeManual // every prompt to the owner, whatever Claude Code's default
	case !slices.Contains(state.SideModes, first):
		first = engine.ModeDefault
	}
	f.modes = append([]string{first}, slices.DeleteFunc(slices.Clone(state.SideModes), func(m string) bool { return m == first })...)
	d := &dialog{title: "Mode", detail: "The session's permission mode. Side sessions never skip permissions.", cancel: actClose}
	if f.sharedRun() {
		d.detail += " A run works in this tree, so manual comes first."
	}
	for _, md := range f.modes {
		label := md
		for _, ma := range modeActs {
			if ma.mode == md {
				label = ma.label
			}
		}
		if md == engine.ModePlan {
			label += " (approved in the pane)"
		}
		d.options = append(d.options, option{label, actSidePick})
	}
	return s.show(sideMode, d)
}

// treeWarning is what the dialog warns about while a run goes.
func (f *sideFlow) treeWarning() string {
	switch {
	case f.run == nil:
		return ""
	case f.run.Worktree:
		return "The run works in its own worktree; a plan edit holds the run."
	case f.run.Parallel:
		return "The run merges into this tree; a plan edit holds the run."
	case f.run.Task != "":
		return f.run.Task + " is working in this tree; a plan edit holds the run."
	}
	return "A run is going in this tree; a plan edit holds the run."
}

// whereStep asks, during a run, whether the session shares the tree.
func (s *sideUI) whereStep() *dialog {
	f := s.flow
	return s.show(sideWhere, &dialog{
		title:  "Where",
		detail: f.treeWarning(),
		options: []option{
			{"Shared tree — the project root", actSidePick},
			{"Own worktree — a git worktree and branch igris/side-<id> of its own", actSidePick},
		},
		cancel: actClose,
	})
}

// confirmStep sums the session up: Open · Cancel.
func (s *sideUI) confirmStep() *dialog {
	f := s.flow
	r := f.req
	about := "blank"
	switch r.Preset {
	case state.SidePresetTask:
		about = "about " + r.Task
	case state.SidePresetIgris:
		about = "igris problem"
	}
	lines := []string{"Side session · " + about + " · " + r.Rank + " · " + r.Mode}
	switch {
	case f.run != nil && r.Worktree:
		lines = append(lines, "In a worktree of its own.")
	case f.run != nil:
		lines = append(lines, "In the shared tree: "+f.treeWarning())
	}
	if r.Prompt == "" {
		lines = append(lines, "No first prompt.")
	} else {
		lines = append(lines, fmt.Sprintf("First prompt: %s, typed once the session is ready.", plural(len(strings.Split(r.Prompt, "\n")), "line")))
	}
	return s.show(sideConfirm, &dialog{
		title:   "Open a side session?",
		detail:  strings.Join(lines, "\n"),
		options: []option{{"Open", actSideOpen}, {"Cancel", actClose}},
		cancel:  actClose,
	})
}

// openSession opens the session off the loop.
func (s *sideUI) openSession() (*dialog, tea.Cmd) {
	f := s.flow
	if f.sharedRun() && !f.req.Worktree {
		f.req.RunTask = f.run.Task
	}
	d := s.show(sideOpening, &dialog{
		title:   "Opening…",
		detail:  "Starting Claude Code in a " + f.at.place + ".",
		options: []option{{"Hide", actClose}},
		cancel:  actClose,
	})
	svc, req := s.svc, f.req
	return d, s.call(f, sideOpened, sideTimeout+gitTimeout+2*time.Minute, func(ctx context.Context, m *sideMsg) {
		m.id, m.err = svc.Open(ctx, req)
	})
}

// failed says the session can't open.
func (s *sideUI) failed(err error) *dialog {
	return s.show(sideFailed, &dialog{
		title:   "The side session did not open",
		detail:  textsafe.Line(err.Error()),
		options: []option{{"OK", actClose}},
		cancel:  actClose,
	})
}

// result applies msg, a call's answer. cur is the screen's dialog: when
// it is the flow's, d (nil closes it) replaces it and ok is set. lines go
// to the feed.
func (s *sideUI) result(msg sideMsg, cur *dialog) (d *dialog, ok bool, cmd tea.Cmd, lines []string) {
	f := s.flow
	mine := msg.flow != nil && msg.flow == f && s.owns(cur)
	switch msg.kind {
	case sideListed:
		if msg.err != nil {
			return nil, false, nil, s.fresh("side sessions: " + textsafe.Line(msg.err.Error()))
		}
		s.rows = msg.list.Rows
		lines = s.fresh(msg.list.Lines...)
		if s.owns(cur) {
			switch {
			case f.step == sidePage && len(s.rows) == 0:
				return s.presetStep(), true, nil, lines
			case f.step == sidePage:
				return s.page(cur.selected), true, nil, lines
			}
		}
		return nil, false, nil, lines
	case sideFocused:
		if msg.err != nil {
			lines = []string{"could not focus side " + msg.id + ": " + textsafe.Line(msg.err.Error())}
		}
		return nil, false, nil, lines
	case sideClosed:
		if msg.err != nil {
			lines = []string{"could not close side " + msg.id + ": " + textsafe.Line(msg.err.Error())}
		} else {
			lines = []string{msg.text}
			s.rows = slices.DeleteFunc(s.rows, func(r SideRow) bool { return r.ID == msg.id })
		}
		if mine {
			if len(s.rows) == 0 {
				return s.done(), true, s.list(), lines
			}
			return s.page(0), true, s.list(), lines
		}
		return nil, false, s.list(), lines
	case sideOpened:
		switch {
		case msg.id != "" && msg.err != nil:
			lines = []string{textsafe.Line(msg.err.Error())}
		case msg.err != nil:
			if mine {
				return s.failed(msg.err), true, nil, nil
			}
			return nil, false, nil, []string{"the side session did not open: " + textsafe.Line(msg.err.Error())}
		default:
			lines = []string{"side " + msg.id + " opened"}
		}
		if mine {
			return s.done(), true, s.list(), lines
		}
		return nil, false, s.list(), lines
	case sidePrepared:
		if !mine {
			return nil, false, nil, nil
		}
		if msg.err != nil {
			return s.failed(msg.err), true, nil, nil
		}
		if len(msg.setup.Ranks) == 0 {
			return s.failed(fmt.Errorf("[models] has no ranks; add one to igris.toml")), true, nil, nil
		}
		f.setup, f.run = msg.setup, f.at.run
		if f.run == nil {
			f.run = msg.setup.Run
		}
		return s.promptStep(msg.text), true, nil, nil
	}
	return nil, false, nil, nil
}

// fresh are the lines not shown before.
func (s *sideUI) fresh(lines ...string) []string {
	var out []string
	for _, l := range lines {
		if !s.shown[l] {
			s.shown[l] = true
			out = append(out, l)
		}
	}
	return out
}
