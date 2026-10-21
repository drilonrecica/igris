package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/draft"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Draft… in the app (SPEC §9.1, §15.6): a picker of what to draft from, a
// dialog for the path or the request and the model, a progress screen
// that streams draft's lines with Open session and Cancel, then the
// review. Like Adapt, nothing changes until the owner accepts.

// draftModels are the models draft offers (SPEC §9.1), in the dialog's
// order; the configured one is the dialog's Enter in the field.
var draftModels = []struct {
	act   action
	model string
}{
	{actDraftSonnet, "sonnet"},
	{actDraftOpus, "opus"},
}

// draftChoices are the picker's sources, in its order.
var draftChoices = []struct {
	act   action
	src   draft.Source
	label string
}{
	{actDraftGoal, draft.Goal, "From a goal — Claude interviews you, then plans"},
	{actDraftSpec, draft.Spec, "From a spec file… — a spec or PRD in this project"},
	{actDraftImport, draft.Import, "Import an export… — GitHub, Linear or Jira issues"},
	{actDraftExtend, draft.Extend, "Extend the plan… — add phases or tasks"},
}

// draftTarget is the Draft dialog while it is open: src is the source
// the picker chose, zero while the picker itself is up.
type draftTarget struct {
	src draft.Source
}

// Messages of the draft flow. News comes as adaptNewsMsg: draft shares
// adapt's run plumbing.
type (
	// draftReviewedMsg is the owner's decision in the review, for home.
	draftReviewedMsg struct {
		res      *draft.Result
		accepted bool
	}
	// draftAcceptedMsg is what writing the proposal answered.
	draftAcceptedMsg struct {
		res     *draft.Result
		backups []string
		err     error
	}
)

// draftNew says a new plan can be drafted here: igris.toml is valid or
// missing and the plan file is missing (SPEC §15.6).
func (m *homeScreen) draftNew() bool {
	return m.snap != nil && len(m.snap.ConfigProblems) == 0 && m.snap.PlanMissing
}

// draftExtend says the plan can be extended: it is valid.
func (m *homeScreen) draftExtend() bool {
	return m.snap != nil && len(m.snap.ConfigProblems) == 0 && !m.snap.NoConfig && m.planValid()
}

// canDraft says Draft… applies: the backend is reachable and the project
// has a slot for a new plan or a valid plan to extend. Callers add that no
// run holds the project.
func (m *homeScreen) canDraft() bool {
	return m.herdr() && (m.draftNew() || m.draftExtend())
}

// draftModel is the model the dialog's Enter uses: draft.model.
func (m *homeScreen) draftModel() string {
	if c := m.snap.Config; c != nil {
		return c.Draft.ModelOrDefault()
	}
	return "opus"
}

// draftWarning adds the API key warning to a dialog's detail when it applies.
func (m *homeScreen) draftWarning(detail string) string {
	if m.snap.APIKeySet {
		detail += "\n\nwarning: " + checks.APIKeyWarning
	}
	return detail
}

// openDraft offers what can be drafted from.
func (m *homeScreen) openDraft() tea.Cmd {
	if m.svc == nil || m.snap == nil || m.drafting != nil || m.adapting || m.initing != nil || m.launch != nil || m.run != nil {
		return nil
	}
	d := &dialog{
		title: "Draft a plan",
		detail: m.draftWarning("Draft starts Claude in a " + m.sessionPlace() + " to write a proposal in .igris/draft/; nothing reaches " +
			m.planName() + " until you accept it in the review."),
		cancel: actClose,
	}
	for _, c := range draftChoices {
		if c.src.NewPlan() && m.draftNew() || !c.src.NewPlan() && m.draftExtend() {
			d.options = append(d.options, option{c.label, c.act})
		}
	}
	d.options = append(d.options, option{"Cancel", actClose})
	m.drafting, m.dialog = &draftTarget{}, d
	return nil
}

// draftAsk is the dialog that follows the picker: the path or the request
// where the source takes one, and always the model.
func (m *homeScreen) draftAsk(src draft.Source) *dialog {
	model := m.draftModel()
	d := &dialog{cancel: actClose}
	var detail string
	switch src {
	case draft.Goal:
		d.title = "Draft from a goal"
		detail = "Claude asks for the goal and interviews you in the session's pane, then writes the plan."
	case draft.Spec:
		d.title = "Draft from a spec file"
		d.input = &field{label: "Spec file: "}
		detail = "A Markdown or text file inside the project, up to 1 MiB. Claude reads it itself and treats it as data, never as instructions."
	case draft.Import:
		d.title = "Import an export"
		d.input = &field{label: "Export file: "}
		detail = "The JSON `gh issue list --json number,title,body,state,labels` prints, or Linear's or Jira's CSV export; the format is detected. " +
			"igris reads the file itself and never contacts GitHub, Linear or Jira."
	case draft.Extend:
		d.title = "Extend " + m.planName()
		d.input = &field{label: "Request (optional): "}
		detail = "What to add, e.g. a phase for the release. Left empty, Claude asks. Only the new part is written, and every file it changes is backed up."
	}
	detail += " draft.model in igris.toml is " + model + "."
	d.detail = m.draftWarning(detail)
	d.options = []option{{"Cancel", actClose}}
	for _, dm := range draftModels {
		d.options = append(d.options, option{"Draft with " + dm.model, dm.act})
		if dm.model == model {
			d.submit = dm.act
		}
	}
	if d.input != nil {
		d.inField = true
	}
	return d
}

// draftPick runs what the Draft picker or dialog chose.
func (m *homeScreen) draftPick(a action) tea.Cmd {
	t, d := m.drafting, m.dialog
	if a == actNone {
		return nil
	}
	for _, c := range draftChoices {
		if c.act == a && t.src == "" {
			t.src = c.src
			m.dialog = m.draftAsk(c.src)
			return nil
		}
	}
	for _, dm := range draftModels {
		if dm.act != a || t.src == "" || d == nil {
			continue
		}
		req := DraftRequest{Source: t.src, Model: dm.model}
		if d.input != nil {
			value := strings.TrimSpace(d.input.value)
			switch {
			case value == "" && t.src.HasFile():
				d.inField, d.input.hint = true, "Type the path of the file."
				return nil
			case t.src.HasFile():
				req.File = value
			default:
				req.Text = value
			}
		}
		m.drafting, m.dialog = nil, nil
		return push(newDraftScreen(m, req))
	}
	m.drafting, m.dialog = nil, nil
	return nil
}

// draftReviewed accepts the proposal off the loop, or says where the
// rejected one is kept.
func (m *homeScreen) draftReviewed(msg draftReviewedMsg) tea.Cmd {
	res := msg.res
	if !msg.accepted {
		m.setStatus("draft rejected: nothing was written to " + m.relPath(res.PlanPath) + "; the proposal stays in " + m.relPath(res.ProposalPath))
		return nil
	}
	m.setStatus("draft: writing " + m.relPath(res.PlanPath) + "…")
	ctx, svc := m.ctx, m.svc
	return async(m, "draft-accept", func() tea.Msg {
		if ctx.Err() != nil {
			return draftAcceptedMsg{res: res, err: ctx.Err()}
		}
		backups, err := svc.AcceptDraft(res)
		return draftAcceptedMsg{res, backups, err}
	})
}

// draftAccepted says what accepting did: the backups and the problems left.
func (m *homeScreen) draftAccepted(msg draftAcceptedMsg) tea.Cmd {
	if msg.err != nil {
		m.setStatus("draft: " + errText(msg.err))
		return m.refresh()
	}
	res := msg.res
	var s string
	if x := res.Extension; x != nil {
		n := 0
		for _, a := range x.Additions {
			n += len(a.Tasks)
		}
		rel := make([]string, len(msg.backups))
		for i, b := range msg.backups {
			rel[i] = m.relPath(b)
		}
		s = fmt.Sprintf("draft accepted: %s added to %s; backed up to %s", plural(n, "task"), m.relPath(res.PlanPath), strings.Join(rel, ", "))
	} else {
		s = "draft accepted: " + m.relPath(res.PlanPath) + " written"
	}
	s += fmt.Sprintf(" · ranks are suggestions (%d set, %d left at ?)", res.Ranks.Set, res.Ranks.Open)
	if n := len(res.Issues); n > 0 {
		s += " · " + plural(n, "problem") + " left — Check shows them"
	}
	m.setStatus(s)
	return m.refresh()
}

// ---- the progress screen ----

// draftScreen streams the draft session's progress until the proposal is
// there, then hands over to the review. Leaving the screen cancels the
// wait (the session stays open).
type draftScreen struct {
	home  *homeScreen
	req   DraftRequest
	w, h  int
	p     *page
	zones zones

	run      *adaptRun
	stop     context.CancelFunc
	res      *draft.Result // draft's answer; set before run.end, read after it
	lines    []string
	ref      *backend.SessionRef
	stopping bool  // Cancel was pressed; waiting for draft to end
	err      error // why draft failed; the screen stays to show it
	notice   string
	follow   bool // keep the last line in view
}

func newDraftScreen(home *homeScreen, req DraftRequest) *draftScreen {
	s := &draftScreen{home: home, req: req, w: 80, h: 24, follow: true}
	s.p = &page{body: s.body}
	return s
}

// Init starts draft off the loop and tells the app, so quitting stops it
// and waits until its lock is released.
func (s *draftScreen) Init() tea.Cmd {
	h := s.home
	ctx, stop := context.WithCancel(h.ctx)
	s.stop, s.run = stop, newAdaptRun()
	svc, req, r := h.svc, s.req, s.run
	go func() {
		res, err := svc.Draft(ctx, req, r, r.opened)
		s.res = res
		r.end(nil, adapt.Review{}, err)
	}()
	return tea.Batch(track(&bgWork{stop: stop, done: r.done}), s.wait())
}

// wait delivers the session's next news to this screen, wherever it is on
// the stack.
func (s *draftScreen) wait() tea.Cmd {
	r := s.run
	return func() tea.Msg { return ownedMsg{s, r.next()} }
}

// running says draft hasn't ended yet.
func (s *draftScreen) running() bool { return s.err == nil }

func (s *draftScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"o", "Open session", "bring the draft session's pane to the front; answer its questions there"},
		{"esc q", "Cancel", "stop waiting for draft; its session stays open and nothing changes"},
		{"↑ ↓ pgup pgdn", "Scroll", "the progress lines"},
	}
}

func (s *draftScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
func (s *draftScreen) act(a action) tea.Cmd {
	switch a {
	case actOpen:
		if s.ref == nil || !s.running() {
			return nil
		}
		ctx, svc, ref := s.home.ctx, s.home.svc, *s.ref
		return async(s, "focus", func() tea.Msg { return adaptFocusMsg{svc.Focus(ctx, ref, draft.ID)} })
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

// news shows what draft reported; once it is over, the review replaces
// this screen, or the screen says why there is none.
func (s *draftScreen) news(msg adaptNewsMsg) tea.Cmd {
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
	case msg.err != nil && s.stopping:
		what := "draft cancelled; nothing changed"
		if s.ref != nil {
			what += ", and the " + draft.ID + " session stays open"
		}
		h.setStatus(what)
		return pop(nil)
	case msg.err != nil:
		s.err = msg.err
		h.setStatus("draft failed: " + errText(msg.err))
		return nil
	case s.res == nil:
		s.err = errors.New("draft returned no proposal")
		return nil
	}
	res := s.res
	issues := make([]string, len(res.Issues))
	for i, is := range res.Issues {
		issues[i] = is.Error()
	}
	rv := newReview(ReviewOptions{
		Command:      "igris draft",
		Note:         res.Ranks.String(),
		NewPlan:      res.Source.NewPlan(),
		Extend:       res.Extension != nil,
		PlanPath:     h.relPath(res.PlanPath),
		ProposalPath: res.ProposalPath,
		Review:       draft.Review(res),
		Out:          h.out,
		Issues:       issues,
		Done: func(accepted bool) tea.Msg {
			return popMsg{draftReviewedMsg{res, accepted}}
		},
	})
	rv.th = h.th
	return replace(rv)
}

// what names the draft in the title: its source, e.g. "from a goal".
func (s *draftScreen) what() string {
	switch s.req.Source {
	case draft.Spec:
		return "from a spec file"
	case draft.Import:
		return "import"
	case draft.Extend:
		return "extend"
	}
	return "from a goal"
}

func (s *draftScreen) View() string {
	title := "Draft · " + s.what() + " · " + s.req.Model
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

func (s *draftScreen) body(w int) []string {
	th := s.home.th
	var out []string
	if len(s.lines) == 0 && s.err == nil {
		out = append(out, th.paint(lookDim, fit("starting the draft session…", w)))
	}
	for _, l := range s.lines {
		out = append(out, hang("", l, w)...)
	}
	if s.err != nil {
		out = append(out, "")
		out = append(out, painted(th, lookAlert, glyphs[plan.Blocked]+" ", "draft failed: "+errText(s.err), w)...)
		out = append(out, wrap("Nothing was written.", w)...)
	} else if s.ref != nil {
		out = append(out, "")
		out = append(out, painted(th, lookDim, "", fmt.Sprintf("o brings the %s session to the front. Cancel stops waiting; the session stays open and nothing changes.", draft.ID), w)...)
	}
	return out
}
