package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Onboarding (SPEC §15.6, first run): the GET STARTED card leads to Init,
// which asks first and then shows what each step did; the example plan is
// offered once igris.toml exists and the plan doesn't. Init only adds what
// is missing and never overwrites, so running it again is safe.

// initTarget is the Init dialog while it is open or its run is going.
type initTarget struct {
	example bool // the example plan too
}

// initDoneMsg is what Init answered.
type initDoneMsg struct {
	steps   []report.Step
	err     error
	example bool
}

// openInit asks before init runs, naming every file it touches.
func (m *homeScreen) openInit(example bool) tea.Cmd {
	if m.svc == nil || m.initing != nil || m.launch != nil || m.run != nil {
		return nil
	}
	m.initing = &initTarget{example: example}
	d := &dialog{cancel: actClose}
	if example {
		d.title = "Example plan"
		d.detail = "Writes the example plan to " + m.planName() + " (" + m.planPath() + "). A file that is already there is never overwritten."
		d.options = []option{{"Create example plan", actInitCreate}, {"Cancel", actClose}}
	} else {
		d.title = "Init"
		d.detail = "Init adds what is missing and never overwrites. It touches: " + strings.Join(m.svc.InitFiles(false), ", ") + "."
		d.options = []option{{"Create files", actInitCreate}, {"Cancel", actClose}}
	}
	m.dialog = d
	return nil
}

// initPick runs what the Init dialog chose.
func (m *homeScreen) initPick(a action) tea.Cmd {
	switch a {
	case actNone:
		return nil
	case actInitCreate:
		t := *m.initing
		m.dialog = nil
		m.setStatus("init: working…")
		ctx, svc := m.ctx, m.svc
		return async(m, "init", func() tea.Msg {
			steps, err := svc.Init(ctx, t.example)
			return initDoneMsg{steps, err, t.example}
		})
	}
	m.initing, m.dialog = nil, nil
	return nil
}

// initDone shows what init did, on the status line and on a page of its
// own, and reads the project again.
func (m *homeScreen) initDone(msg initDoneMsg) tea.Cmd {
	m.initing = nil
	var did []string
	for _, st := range msg.steps {
		if st.ID != report.StepHerdrHint {
			did = append(did, st.Message)
		}
	}
	switch {
	case msg.err != nil:
		m.setStatus("init failed: " + textsafe.Line(msg.err.Error()))
	case len(did) == 0:
		m.setStatus("init: nothing to do")
	default:
		m.setStatus("init: " + strings.Join(did, ", "))
	}
	return tea.Batch(m.refresh(), push(newInitScreen(m, msg)))
}

// ---- the result page ----

// initScreen lists each init step's result, then what to do next.
type initScreen struct {
	home  *homeScreen
	res   initDoneMsg
	w, h  int
	p     *page
	zones zones
}

func newInitScreen(home *homeScreen, res initDoneMsg) *initScreen {
	s := &initScreen{home: home, res: res, w: 80, h: 24}
	s.p = &page{body: s.body}
	return s
}

func (s *initScreen) Init() tea.Cmd { return nil }

func (s *initScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"E", "Example plan", "write the example plan, when igris.toml exists and the plan doesn't"},
		{"c", "Check", "check the plan"},
		{"i", "Doctor", "what is wrong with the setup, and what fixes it"},
		{"↑ ↓ j k", "Scroll", "move through the steps"},
		{"esc", "Close", "back to home"},
	}
}

func (s *initScreen) buttons() []pageButton {
	var out []pageButton
	if s.home.offers(actExample) {
		out = append(out, pageButton{"Example plan", "Example", actExample})
	}
	if s.home.offers(actCheck) {
		out = append(out, pageButton{"Check", "", actCheck})
	}
	return append(out, pageButton{"Doctor", "", actDoctor})
}

// act hands a to home, which does the ones that leave this page.
func (s *initScreen) act(a action) tea.Cmd {
	if a == actDoctor || s.home.offers(a) {
		return pop(doMsg{a})
	}
	return nil
}

func (s *initScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "E":
			return s, s.act(actExample)
		case "c":
			return s, s.act(actCheck)
		case "i":
			return s, s.act(actDoctor)
		case "?":
			return s, showHelp
		case "q":
			return s, pop(nil)
		default:
			if s.p.key(k) {
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

func (s *initScreen) View() string {
	title := "Init · done"
	if s.res.err != nil {
		title = "Init · stopped"
	}
	s.p.title = title
	s.p.buttons = s.buttons()
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

func (s *initScreen) body(w int) []string {
	th := s.home.th
	var out []string
	for _, st := range s.res.steps {
		glyph := glyphs[plan.Done]
		label := st.Path
		if st.ID == report.StepHerdrHint {
			glyph, label = "·", "backend"
		}
		out = append(out, hang(th.paint(lookAccent, glyph)+" "+th.paint(lookTitle, label)+"  ", st.Message, w)...)
	}
	if err := s.res.err; err != nil {
		out = append(out, hang(th.paint(lookAlert, glyphs[plan.Blocked])+" ", "init stopped: "+textsafe.Line(err.Error()), w)...)
		out = append(out, wrap("Fix that and run Init again; what was written stays, and nothing is overwritten.", w)...)
		return out
	}
	out = append(out, "")
	return append(out, wrap(s.next(), w)...)
}

// next says what to do now, by what exists.
func (s *initScreen) next() string {
	h := s.home
	switch {
	case h.offers(actExample):
		return "Next: Example plan writes a small canonical plan to try igris on, or write your own at " + h.planName() + "."
	case h.planValid():
		return "Next: c checks the plan, v previews a run, a starts one."
	case h.planFile():
		return "Next: c shows what the plan needs; Adapt (A) can propose a canonical one."
	}
	return "Next: write the plan at " + h.planName() + " or point plan = in igris.toml at it."
}
