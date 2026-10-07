package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Adapt in the app (SPEC §9, §15.6): a dialog asks first and picks the
// model, a progress screen streams adapt's lines with Open session and
// Cancel, and the review replaces it once the proposal is there. Home
// accepts or rejects and says what happened on its status line. Nothing
// changes until the owner accepts.

// adaptModels are the models adapt offers (SPEC §9), in the dialog's
// order.
var adaptModels = []struct {
	act   action
	model string
}{
	{actAdaptSonnet, "sonnet"},
	{actAdaptOpus, "opus"},
}

// Messages of the adapt flow.
type (
	// adaptNewsMsg is what the adapt session reported since the last one;
	// ended says it is over and res, review and err are final.
	adaptNewsMsg struct {
		lines  []string
		ref    *backend.SessionRef
		ended  bool
		res    *adapt.Result
		review adapt.Review
		err    error
	}
	// adaptReviewedMsg is the owner's decision in the review, for home.
	adaptReviewedMsg struct {
		res      *adapt.Result
		accepted bool
	}
	// adaptAcceptedMsg is what replacing the plan answered.
	adaptAcceptedMsg struct {
		res    *adapt.Result
		backup string
		err    error
	}
	// adaptFocusMsg is what bringing the session's pane forward answered.
	adaptFocusMsg struct{ err error }
)

// openAdapt asks before adapt starts a session, and with which model.
func (m *homeScreen) openAdapt() tea.Cmd {
	if m.svc == nil || m.snap == nil || m.adapting || m.initing != nil || m.launch != nil || m.run != nil {
		return nil
	}
	model := "sonnet"
	if c := m.snap.Config; c != nil && c.Adapt.Model != "" {
		model = c.Adapt.Model
	}
	detail := "Adapt starts Claude in a " + m.sessionPlace() + " to propose a canonical plan for " + m.planName() +
		" in .igris/adapt/; nothing changes until you accept it in the review. adapt.model in igris.toml is " + model + "."
	if m.snap.APIKeySet {
		detail += "\n\nwarning: " + checks.APIKeyWarning
	}
	d := &dialog{title: "Adapt " + m.planName() + "?", detail: detail, cancel: actClose}
	d.options = []option{{"Cancel", actClose}}
	for _, am := range adaptModels {
		d.options = append(d.options, option{"Adapt with " + am.model, am.act})
	}
	m.adapting, m.dialog = true, d
	return nil
}

// adaptPick runs what the Adapt dialog chose.
func (m *homeScreen) adaptPick(a action) tea.Cmd {
	if a == actNone {
		return nil
	}
	m.adapting, m.dialog = false, nil
	for _, am := range adaptModels {
		if am.act == a {
			return push(newAdaptScreen(m, am.model))
		}
	}
	return nil
}

// adaptReviewed accepts the proposal off the loop, or says where the
// rejected one is kept.
func (m *homeScreen) adaptReviewed(msg adaptReviewedMsg) tea.Cmd {
	res := msg.res
	if !msg.accepted {
		m.setStatus("adapt rejected: " + m.relPath(res.PlanPath) + " is unchanged; the proposal stays in " + m.relPath(res.ProposalPath))
		return nil
	}
	m.setStatus("adapt: replacing " + m.relPath(res.PlanPath) + "…")
	ctx, svc := m.ctx, m.svc
	return async(m, "adapt-accept", func() tea.Msg {
		if ctx.Err() != nil {
			return adaptAcceptedMsg{res: res, err: ctx.Err()}
		}
		backup, err := svc.AcceptAdapt(res)
		return adaptAcceptedMsg{res, backup, err}
	})
}

// adaptAccepted says what accepting did: the backup and the problems left.
func (m *homeScreen) adaptAccepted(msg adaptAcceptedMsg) tea.Cmd {
	if msg.err != nil {
		m.setStatus("adapt: " + errText(msg.err))
		return m.refresh()
	}
	s := "adapt accepted: " + m.relPath(msg.res.PlanPath) + " replaced; the original is backed up to " + m.relPath(msg.backup)
	if n := len(msg.res.Issues); n > 0 {
		s += " · " + plural(n, "problem") + " left (e.g. models at ?) — Check shows them"
	}
	m.setStatus(s)
	return m.refresh()
}

// relPath shows path relative to the project root when it is inside it.
func (m *homeScreen) relPath(path string) string {
	if m.snap != nil && m.snap.Root != "" {
		if r, err := filepath.Rel(m.snap.Root, path); err == nil && !strings.HasPrefix(r, "..") {
			return textsafe.Line(r)
		}
	}
	return textsafe.Line(path)
}

// ---- the progress screen ----

// adaptScreen streams the adapt session's progress until the proposal is
// there, then hands over to the review. There is no adapt in the
// background: leaving the screen cancels it (the session stays open).
type adaptScreen struct {
	home  *homeScreen
	model string
	w, h  int
	p     *page
	zones zones

	run      *adaptRun
	stop     context.CancelFunc
	lines    []string
	ref      *backend.SessionRef
	stopping bool  // Cancel was pressed; waiting for adapt to end
	err      error // why adapt failed; the screen stays to show it
	notice   string
	follow   bool // keep the last line in view
}

func newAdaptScreen(home *homeScreen, model string) *adaptScreen {
	s := &adaptScreen{home: home, model: model, w: 80, h: 24, follow: true}
	s.p = &page{body: s.body}
	return s
}

// Init starts adapt off the loop and tells the app, so quitting stops it
// and waits until its lock is released.
func (s *adaptScreen) Init() tea.Cmd {
	h := s.home
	ctx, stop := context.WithCancel(h.ctx)
	s.stop, s.run = stop, newAdaptRun()
	var columns map[string]string
	if h.snap != nil && h.snap.Config != nil {
		columns = h.snap.Config.Columns
	}
	svc, model, r := h.svc, s.model, s.run
	go func() {
		res, err := svc.Adapt(ctx, model, r, r.opened)
		var rv adapt.Review
		if err == nil && res != nil {
			rv = adapt.Compare(res.Original, res.Proposed, plan.Options{Columns: columns})
		}
		r.end(res, rv, err)
	}()
	return tea.Batch(track(&bgWork{stop: stop, done: r.done}), s.wait())
}

// wait delivers the session's next news to this screen, wherever it is on
// the stack.
func (s *adaptScreen) wait() tea.Cmd {
	r := s.run
	return func() tea.Msg { return ownedMsg{s, r.next()} }
}

// running says adapt hasn't ended yet.
func (s *adaptScreen) running() bool { return s.err == nil }

func (s *adaptScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"o", "Open session", "bring the adapt session's pane to the front; answer its questions there"},
		{"esc q", "Cancel", "stop waiting for adapt; its session stays open and nothing changes"},
		{"↑ ↓ pgup pgdn", "Scroll", "the progress lines"},
	}
}

func (s *adaptScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case adaptNewsMsg:
		return s, s.news(msg)
	case adaptFocusMsg:
		s.notice = ""
		if msg.err != nil {
			s.notice = "open session: " + errText(msg.err)
		}
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "o":
			return s, s.act(actOpen)
		case "esc", "q":
			return s, s.act(actClose)
		case "?":
			return s, showHelp
		case "end":
			s.follow = true
		case "up", "k", "down", "j", "pgup", "pgdown", "home":
			s.p.key(k)
			s.follow = false
		}
	case tea.MouseMsg:
		if a, ok := pageMouse(s.p, s.zones, msg); ok {
			return s, s.act(a)
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp {
			s.follow = false
		}
	}
	return s, nil
}

// act runs a footer action: Open session, or Cancel / Close.
func (s *adaptScreen) act(a action) tea.Cmd {
	switch a {
	case actOpen:
		if s.ref == nil || !s.running() {
			return nil
		}
		ctx, svc, ref := s.home.ctx, s.home.svc, *s.ref
		return async(s, "focus", func() tea.Msg { return adaptFocusMsg{svc.Focus(ctx, ref)} })
	case actClose:
		if !s.running() {
			return pop(nil)
		}
		if !s.stopping {
			s.stopping = true
			s.stop()
		}
	}
	return nil
}

// news shows what adapt reported; once it is over, the review replaces
// this screen, or home says why there is none.
func (s *adaptScreen) news(msg adaptNewsMsg) tea.Cmd {
	for _, l := range msg.lines {
		s.lines = append(s.lines, textsafe.Line(l))
	}
	if msg.ref != nil {
		s.ref = msg.ref
	}
	if !msg.ended {
		return s.wait()
	}
	h := s.home
	switch {
	case errors.Is(msg.err, adapt.ErrAlreadyValid):
		h.setStatus(h.planName() + " passes check; nothing to adapt")
		return pop(nil)
	case msg.err != nil && s.stopping:
		what := "adapt cancelled; nothing changed"
		if s.ref != nil {
			what += ", and the " + adapt.ID + " session stays open"
		}
		h.setStatus(what)
		return pop(nil)
	case msg.err != nil:
		s.err = msg.err
		h.setStatus("adapt failed: " + errText(msg.err))
		return nil
	case msg.res == nil:
		s.err = errors.New("adapt returned no proposal")
		return nil
	}
	res := msg.res
	issues := make([]string, len(res.Issues))
	for i, is := range res.Issues {
		issues[i] = is.Error()
	}
	rv := newReview(ReviewOptions{
		PlanPath:     h.relPath(res.PlanPath),
		ProposalPath: res.ProposalPath,
		Review:       msg.review,
		Out:          h.out,
		Issues:       issues,
		Done: func(accepted bool) tea.Msg {
			return popMsg{adaptReviewedMsg{res, accepted}}
		},
	})
	rv.th = h.th
	return replace(rv)
}

func (s *adaptScreen) View() string {
	title := "Adapt · " + s.model
	switch {
	case s.err != nil:
		title += " · failed"
	case s.stopping:
		title += " · cancelling…"
	default:
		title += " · working…"
	}
	if s.notice != "" {
		title += " · " + s.notice
	}
	s.p.title = title
	s.p.buttons = nil
	s.p.closeLabel = ""
	if s.running() {
		if s.ref != nil {
			s.p.buttons = []pageButton{{"Open session", "Open", actOpen}}
		}
		s.p.closeLabel = "Cancel"
	}
	if s.follow {
		s.p.top = maxTop
	}
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

func (s *adaptScreen) body(w int) []string {
	th := s.home.th
	var out []string
	if len(s.lines) == 0 && s.err == nil {
		out = append(out, th.paint(lookDim, fit("starting the adapt session…", w)))
	}
	for _, l := range s.lines {
		out = append(out, hang("", l, w)...)
	}
	if s.err != nil {
		out = append(out, "")
		out = append(out, painted(th, lookAlert, glyphs[plan.Blocked]+" ", "adapt failed: "+errText(s.err), w)...)
		out = append(out, wrap("The plan is unchanged.", w)...)
	} else if s.ref != nil {
		out = append(out, "")
		out = append(out, painted(th, lookDim, "", fmt.Sprintf("o brings the %s session to the front. Cancel stops waiting; the session stays open and nothing changes.", adapt.ID), w)...)
	}
	return out
}

// adaptRun carries one adapt session's output to the screen: Write and
// opened never block adapt, and next waits for news.
type adaptRun struct {
	mu      sync.Mutex
	partial []byte
	lines   []string
	ref     *backend.SessionRef
	final   adaptNewsMsg
	wake    chan struct{}
	done    chan struct{}
}

func newAdaptRun() *adaptRun {
	return &adaptRun{wake: make(chan struct{}, 1), done: make(chan struct{})}
}

// Write takes adapt's progress; each complete line is news.
func (r *adaptRun) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.partial = append(r.partial, p...)
	for {
		i := strings.IndexByte(string(r.partial), '\n')
		if i < 0 {
			break
		}
		r.lines = append(r.lines, string(r.partial[:i]))
		r.partial = r.partial[i+1:]
	}
	r.mu.Unlock()
	r.poke()
	return len(p), nil
}

// opened records the session's ref, for Open session.
func (r *adaptRun) opened(ref backend.SessionRef) {
	r.mu.Lock()
	r.ref = &ref
	r.mu.Unlock()
	r.poke()
}

// end records how adapt ended; called once.
func (r *adaptRun) end(res *adapt.Result, rv adapt.Review, err error) {
	r.mu.Lock()
	if len(r.partial) > 0 {
		r.lines = append(r.lines, string(r.partial))
		r.partial = nil
	}
	r.final = adaptNewsMsg{ended: true, res: res, review: rv, err: err}
	r.mu.Unlock()
	close(r.done)
}

func (r *adaptRun) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// next waits for news and takes it; the end comes with the last lines.
func (r *adaptRun) next() adaptNewsMsg {
	select {
	case <-r.wake:
	case <-r.done:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	msg := adaptNewsMsg{lines: r.lines, ref: r.ref}
	r.lines, r.ref = nil, nil
	select {
	case <-r.done:
		f := r.final
		f.lines, f.ref = msg.lines, msg.ref
		return f
	default:
		return msg
	}
}
