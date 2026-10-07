package tui

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// The Settings page and the editor (SPEC §15.6, §16). Settings is a
// read-only view of the effective igris.toml; `e` opens a file in the
// owner's editor with tea.Exec, and home reads the project again when the
// editor returns. igris never writes igris.toml itself.

// ---- settings ----

// settingsScreen shows the config's problems first, then the effective
// config in TOML-shaped sections. Values from the defaults say so in words
// (`· default`); secrets say only whether they are set and where from.
type settingsScreen struct {
	home   *homeScreen
	w, h   int
	p      *page
	notice string
	zones  zones
}

func newSettingsScreen(home *homeScreen) *settingsScreen {
	s := &settingsScreen{home: home, w: 80, h: 24}
	s.p = &page{body: s.body}
	return s
}

func (s *settingsScreen) Init() tea.Cmd { return nil }

func (s *settingsScreen) helpKeys() []helpEntry {
	return []helpEntry{
		{"e", "Edit igris.toml", "open it in $VISUAL or $EDITOR; igris checks it again when the editor closes"},
		{"i", "Doctor", "what is wrong with the setup, and what fixes it"},
		{"I", "Init", "create igris.toml and .igris/ (when there is no igris.toml)"},
		{",", "Read again", "read igris.toml again"},
		{"↑ ↓ j k", "Scroll", "move through the settings"},
		{"esc", "Close", "back to home"},
	}
}

func (s *settingsScreen) buttons() []pageButton {
	out := []pageButton{}
	if s.home.snap != nil {
		out = append(out, pageButton{"Edit igris.toml", "Edit", actEditConfig})
	}
	out = append(out, pageButton{"Doctor", "", actDoctor})
	if s.home.offers(actInit) {
		out = append(out, pageButton{"Init", "", actInit})
	}
	return out
}

func (s *settingsScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.w, s.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch k := msg.String(); k {
		case "e":
			return s, s.act(actEditConfig)
		case "i":
			return s, s.act(actDoctor)
		case "I":
			return s, s.act(actInit)
		case ",":
			return s, s.act(actSettings)
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

// act runs one of the page's actions; home does the ones that leave it.
func (s *settingsScreen) act(a action) tea.Cmd {
	h := s.home
	switch a {
	case actSettings:
		s.notice = "read again"
		return h.refresh()
	case actEditConfig:
		if h.snap != nil {
			return pop(doMsg{a})
		}
	case actDoctor:
		return pop(doMsg{a})
	case actInit:
		if h.offers(a) {
			return pop(doMsg{a})
		}
	}
	return nil
}

func (s *settingsScreen) View() string {
	title := "Settings · igris.toml"
	if s.notice != "" {
		title += " · " + s.notice
	}
	s.p.title = title + " · e edits"
	s.p.buttons = s.buttons()
	lines, z := s.p.render(s.home.th, s.w, s.h)
	s.zones = z
	return strings.Join(lines, "\n")
}

func (s *settingsScreen) body(w int) []string {
	th, snap := s.home.th, s.home.snap
	if snap == nil {
		if err := s.home.snapErr; err != nil {
			return painted(th, lookAlert, "", "can't read the project: "+textsafe.Line(err.Error()), w)
		}
		return []string{th.paint(lookDim, "reading igris.toml…")}
	}
	out := painted(th, lookDim, "", textsafe.Line(snap.ConfigPath), w)
	out = append(out, "")
	switch {
	case snap.NoConfig:
		out = append(out, wrap("There is no igris.toml here, so igris uses the defaults below. Init creates one; e opens a new one in your editor.", w)...)
		out = append(out, "")
	case len(snap.ConfigProblems) > 0 || len(snap.ConfigWarnings) > 0:
		out = append(out, th.paint(lookTitle, "PROBLEMS"))
		for _, p := range snap.ConfigProblems {
			out = append(out, s.problem(checks.Fail, p, w)...)
		}
		for _, p := range snap.ConfigWarnings {
			out = append(out, s.problem(checks.Warn, p, w)...)
		}
		if len(snap.ConfigProblems) > 0 {
			out = append(out, painted(th, lookDim, "", "Until igris.toml is fixed, igris uses the defaults below.", w)...)
		}
		out = append(out, "")
	}
	for i, sec := range snap.Settings {
		if i > 0 {
			out = append(out, "")
		}
		if sec.Name != "" {
			out = append(out, th.paint(lookTitle, "["+sec.Name+"]"))
		}
		if len(sec.Rows) == 0 {
			out = append(out, th.paint(lookDim, "# none set"))
		}
		out = append(out, settingLines(th, sec.Rows, w)...)
	}
	return out
}

// problem is one config problem: its level as a glyph and a word, then the
// text wrapped under itself.
func (s *settingsScreen) problem(l checks.Level, text string, w int) []string {
	word, lk := levelWord(l)
	return hang("  "+s.home.th.paint(lk, pad(word, wordW))+"  ", text, w)
}

// settingLines lays rows out as `key = value`, keys padded to the widest
// in the section; a default value ends in `· default`.
func settingLines(th *theme, rows []report.Setting, w int) []string {
	keyW := 0
	for _, r := range rows {
		keyW = max(keyW, textWidth(r.Key))
	}
	var out []string
	for _, r := range rows {
		text := r.Value
		if r.Default {
			text += " " + th.paint(lookDim, "· default")
		}
		out = append(out, hang(pad(r.Key, keyW)+" = ", text, w)...)
	}
	return out
}

// ---- editor ----

// editTarget is a file `e` opens: the plan, or igris.toml.
type editTarget struct {
	path string // absolute
	plan bool
	// confirmed says the owner chose Edit anyway while a run holds the
	// plan.
	confirmed bool
	vi        tea.ExecCommand // vi on path, offered when no editor is set
}

// name is the file's base name, for the status line.
func (t editTarget) name() string { return filepath.Base(t.path) }

// editedMsg says the editor on t has closed; err is how it ended.
type editedMsg struct {
	t   editTarget
	err error
}

// configTarget is igris.toml; ok is false before the first read.
func (m *homeScreen) configTarget() (editTarget, bool) {
	if m.snap == nil || m.snap.ConfigPath == "" {
		return editTarget{}, false
	}
	return editTarget{path: m.snap.ConfigPath}, true
}

// planTarget is the plan file; ok is false before the first read.
func (m *homeScreen) planTarget() (editTarget, bool) {
	if m.snap == nil || m.snap.PlanPath == "" {
		return editTarget{}, false
	}
	return editTarget{path: m.snap.PlanPath, plan: true}, true
}

// planBusy says a run may write the plan's Status cells right now: another
// igris on this host holds the lock, or home's own run is going.
func (m *homeScreen) planBusy() bool {
	return m.run != nil || (m.snap != nil && m.snap.Lock.Alive)
}

// edit opens t in the owner's editor: asking first when a run holds the
// plan, and asking how when no editor is set (SPEC §15.6).
func (m *homeScreen) edit(t editTarget) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	if t.plan && !t.confirmed && m.planBusy() {
		m.editing = &t
		m.dialog = &dialog{
			title:   "A run is going",
			detail:  "igris writes " + t.name() + "'s Status cells while it runs. If your editor saves over them, the run holds until you look (the plan changed under it).",
			options: []option{{"Cancel", actClose}, {"Edit anyway", actEditAnyway}},
			cancel:  actClose,
		}
		return nil
	}
	cmd, err := m.svc.EditCommand(t.path)
	switch {
	case errors.Is(err, ErrNoEditor):
		t.vi = m.svc.ViCommand(t.path)
		m.editing = &t
		m.dialog = noEditorDialog(t)
		return nil
	case err != nil:
		m.setStatus("can't open the editor: " + textsafe.Line(err.Error()))
		return nil
	}
	return m.exec(t, cmd)
}

// noEditorDialog shows the path and asks how to open it; vi is offered
// only when it is on PATH, never picked on its own.
func noEditorDialog(t editTarget) *dialog {
	d := &dialog{
		title:   "No editor set",
		detail:  "Set $VISUAL or $EDITOR (e.g. export EDITOR=nano) to edit from igris. The file: " + textsafe.Line(t.path),
		options: []option{{"Close", actClose}},
		cancel:  actClose,
	}
	if t.vi != nil {
		d.options = append(d.options, option{"Open with vi", actEditVi})
	}
	d.options = append(d.options, option{"Copy path (y)", actCopy})
	return d
}

// exec suspends the TUI and runs cmd; home hears when it is over,
// wherever it is on the stack.
func (m *homeScreen) exec(t editTarget, cmd tea.ExecCommand) tea.Cmd {
	t.vi = nil
	return tea.Exec(cmd, func(err error) tea.Msg { return ownedMsg{m, editedMsg{t, err}} })
}

// editPick runs what an editor dialog chose.
func (m *homeScreen) editPick(a action) tea.Cmd {
	t := *m.editing
	switch a {
	case actNone:
		return nil
	case actCopy:
		if !hasOption(m.dialog, actCopy) {
			return nil
		}
		m.setStatus(copyNotice(m.out, t.path) + " " + textsafe.Line(t.path))
	case actEditAnyway:
		m.editing, m.dialog = nil, nil
		t.confirmed = true
		return m.edit(t)
	case actEditVi:
		if t.vi != nil {
			m.editing, m.dialog = nil, nil
			return m.exec(t, t.vi)
		}
		return nil
	}
	m.editing, m.dialog = nil, nil
	return nil
}

// hasOption says d offers a.
func hasOption(d *dialog, a action) bool {
	if d == nil {
		return false
	}
	for _, o := range d.options {
		if o.act == a {
			return true
		}
	}
	return false
}

// edited reads the project again after the editor closed; the status line
// says how it went once the read is back.
func (m *homeScreen) edited(msg editedMsg) tea.Cmd {
	m.afterEdit = &msg
	// A read already on its way may have started before the save: this
	// newer one replaces it.
	return tea.Batch(m.refresh(), m.runDoctor())
}

// editStatus is the status line after the editor on msg.t closed and the
// project was read again: the editor's exit, then the file's validity.
func (m *homeScreen) editStatus(msg editedMsg) string {
	var exit interface{ ExitCode() int }
	prefix := ""
	switch {
	case msg.err == nil:
	case errors.As(msg.err, &exit) && exit.ExitCode() > 0:
		prefix = "editor exited with status " + strconv.Itoa(exit.ExitCode()) + "; file reloaded · "
	default:
		return "the editor didn't run: " + textsafe.Line(msg.err.Error()) + "; check $VISUAL / $EDITOR"
	}
	return prefix + m.validity(msg.t)
}

// validity says whether t's file is valid as the last read found it.
func (m *homeScreen) validity(t editTarget) string {
	s, name := m.snap, t.name()
	fail := glyphs[plan.Blocked] + " " + name + ": "
	ok := glyphs[plan.Done] + " " + name + " valid"
	switch {
	case m.snapErr != nil:
		return fail + "can't read the project: " + textsafe.Line(m.snapErr.Error())
	case s == nil:
		return ""
	case !t.plan && s.NoConfig:
		return name + " wasn't saved; igris uses the defaults"
	case !t.plan && len(s.ConfigProblems) > 0:
		return fail + plural(len(s.ConfigProblems), "problem") + " — Settings"
	case !t.plan:
		return ok
	case s.PlanMissing:
		return name + " wasn't saved"
	case s.PlanErr != "":
		return fail + s.PlanErr
	case len(s.Issues) > 0:
		return fail + plural(len(s.Issues), "problem") + " — Check"
	}
	return ok
}
