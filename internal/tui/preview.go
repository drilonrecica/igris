package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The preview page (SPEC §15.6) shows `igris arise --dry-run` as data:
// the numbered sessions and user tasks with their rank, model and mode,
// the warnings and the totals. `v` runs it again; "Arise with these
// settings" goes back to home and opens the wizard with the same choices.

// previewScreen is the dry run of one request, read off the loop.
type previewScreen struct {
	ctx  context.Context
	svc  Services
	th   *theme
	w, h int
	req  report.RunRequest
	p    *page

	res     *report.DryRun // nil until the first answer is back
	err     error
	loading bool
	zones   zones
}

// ariseMsg asks home to open the wizard prefilled with req (an empty
// Phase is the last run's phases).
type ariseMsg struct{ req report.RunRequest }

// previewMsg is the dry run's answer.
type previewMsg struct {
	res *report.DryRun
	err error
}

func newPreview(ctx context.Context, svc Services, th *theme, req report.RunRequest) *previewScreen {
	s := &previewScreen{ctx: ctx, svc: svc, th: th, w: 80, h: 24, req: req}
	s.p = &page{
		title: "Preview · " + s.scope() + " · v runs again",
		body:  s.body,
		buttons: []pageButton{
			{"Arise with these settings", "Arise", actAriseWith},
			{"Preview again", "Again", actPreview},
		},
	}
	return s
}

// scope names what is previewed: the walk's own scope once known, else
// the request's.
func (s *previewScreen) scope() string {
	switch {
	case s.res != nil && s.res.Scope != "":
		return s.res.Scope
	case s.req.Phase == "":
		return "the last run's phases"
	case s.req.Through != "" && s.req.Through != s.req.Phase:
		return s.req.Phase + " through " + s.req.Through
	}
	return s.req.Phase
}

func (s *previewScreen) Init() tea.Cmd { return s.run() }

// run asks for the dry run; a newer answer replaces an older one.
func (s *previewScreen) run() tea.Cmd {
	s.loading = true
	ctx, svc, req := s.ctx, s.svc, s.req
	return async(s, "preview", func() tea.Msg {
		res, err := svc.Preview(ctx, req)
		return previewMsg{res, err}
	})
}

// ready says there is a walk to arise with.
func (s *previewScreen) ready() bool {
	return !s.loading && s.err == nil && s.res != nil && s.res.Scope != ""
}

func (s *previewScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"v", "Preview again", "run the dry run again, after the plan changed"},
		{"a", "Arise with these settings", "open the start-run wizard with these choices"},
		{"↑ ↓ pgup pgdn", "Scroll", "the steps, when they don't fit"},
		{"esc", "Close", "back to home"},
	}
}

func (s *previewScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case previewMsg:
		s.loading, s.res, s.err = false, msg.res, msg.err
		s.p.title = "Preview · " + s.scope() + " · v runs again"
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "v":
			return s, s.run()
		case "a":
			return s, s.arise()
		case "?":
			return s, showHelp
		case "q", "esc":
			return s, pop(nil)
		default:
			if s.p.key(k) {
				return s, pop(nil)
			}
		}
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionPress {
			break
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			s.p.scroll(-3)
		case tea.MouseButtonWheelDown:
			s.p.scroll(3)
		case tea.MouseButtonLeft:
			if t, ok := s.zones.at(msg.X, msg.Y); ok {
				switch t.act {
				case actClose:
					return s, pop(nil)
				case actPreview:
					return s, s.run()
				case actAriseWith:
					return s, s.arise()
				}
			}
		}
	}
	return s, nil
}

// arise goes back to home, which opens the wizard.
func (s *previewScreen) arise() tea.Cmd {
	if !s.ready() {
		return nil
	}
	return pop(ariseMsg{s.req})
}

func (s *previewScreen) View() string {
	lines, z := s.p.render(s.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

// body lays the dry run out for w cells: the interrupted task and the
// warnings, the numbered steps, the totals.
func (s *previewScreen) body(w int) []string {
	th := s.th
	switch {
	case s.loading:
		return []string{th.paint(lookDim, "running the dry run…")}
	case s.res == nil:
		return wrap("The dry run failed: "+errText(s.err), w)
	}
	r := s.res
	var out []string
	if r.Interrupted != "" {
		out = append(out, hang("", "note: the last run stopped during "+r.Interrupted+"; a real run picks it up first", w)...)
	}
	for _, x := range r.Warnings {
		out = append(out, painted(th, lookAlert, "", "warning: "+x, w)...)
	}
	if r.Scope != "" {
		out = append(out, th.paint(lookDim, fit("nothing is written and no session starts", w)))
		if len(r.Hooks) > 0 {
			out = append(out, painted(th, lookDim, "", "task hooks "+strings.Join(r.Hooks, " and ")+" would run around each agent session; the dry run runs none", w)...)
		}
		out = append(out, "")
	}
	for _, st := range r.Steps {
		out = append(out, s.step(st, w)...)
	}
	if s.err != nil {
		out = append(out, painted(th, lookAlert, "", "the dry run failed: "+errText(s.err), w)...)
	}
	if r.Scope != "" && s.err == nil {
		out = append(out, "", th.paint(lookTitle, "Total: ")+plural(r.Sessions, "session")+" · "+plural(r.Users, "user task"))
	}
	return out
}

// painted wraps plain text under prefix and paints each line, so the
// wrapping never counts an escape sequence.
func painted(th *theme, l look, prefix, text string, w int) []string {
	var out []string
	for i, line := range wrap(text, w-textWidth(prefix)) {
		lead := prefix
		if i > 0 {
			lead = strings.Repeat(" ", textWidth(prefix))
		}
		out = append(out, lead+th.paint(l, line))
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return "no answer"
	}
	return textsafe.Line(err.Error())
}

// step lays one line of the walk out: a numbered session or user task
// with its title under it, or a note.
func (s *previewScreen) step(st report.DryStep, w int) []string {
	th := s.th
	const indent = 6
	switch st.Kind {
	case report.StepSession:
		text := fmt.Sprintf("%s  %s → %s · mode %s", st.Task, st.Rank, st.Model, st.Mode)
		if st.Verify != "" {
			text += " · verify " + st.Verify
		}
		if st.Resumed {
			text += " · resumed: fresh session"
		}
		return s.numbered(st, text, st.Mode, w)
	case report.StepUser:
		return s.numbered(st, st.Task+"  user task: waits for you", "", w)
	case report.StepPhaseDone:
		return []string{strings.Repeat(" ", indent) + th.paint(lookDim, fit("phase "+st.Phase+" complete", w-indent))}
	case report.StepWarning:
		if len(st.Lines) > 0 {
			return painted(th, lookAlert, "", "warning: "+st.Lines[0], w)
		}
	case report.StepNote:
		var out []string
		for _, l := range st.Lines {
			out = append(out, hang(strings.Repeat(" ", indent), l, w)...)
		}
		return out
	}
	return nil
}

// numbered is a session or user task: "  1. text", the title under it.
func (s *previewScreen) numbered(st report.DryStep, text, mode string, w int) []string {
	prefix := fmt.Sprintf("%3d. ", st.N)
	out := hang(prefix, text, w)
	if b := badge(mode); b != "" {
		// The badge is one word: it goes to the next line rather than break.
		if last := len(out) - 1; textWidth(out[last])+textWidth(b) <= w {
			out[last] += b
		} else {
			out = append(out, strings.Repeat(" ", textWidth(prefix))+strings.TrimPrefix(b, " "))
		}
	}
	for i := range out {
		out[i] = s.th.marks(out[i])
	}
	if st.Title != "" {
		out = append(out, painted(s.th, lookDim, strings.Repeat(" ", textWidth(prefix)), st.Title, w)...)
	}
	return out
}
