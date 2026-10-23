package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// ReviewOptions configure the review of an `igris adapt` or `igris draft`
// proposal.
type ReviewOptions struct {
	Mouse bool
	Theme string // "auto", "dark" or "light", as [tui] theme
	// Command names the review in its title: "igris draft"; "" is
	// "igris adapt".
	Command string
	// Note is a line under the validation result, e.g. draft's
	// "ranks are suggestions: 12 set, 2 left at ?" (SPEC §9.1).
	Note string
	// NewPlan says Accept creates the plan rather than replacing it, for
	// the second confirmation's wording.
	NewPlan bool
	// Extend says Accept adds to the plan rather than replacing it
	// (`igris draft extend`), for the same wording.
	Extend bool
	// PlanPath is the plan the proposal would replace, as shown.
	PlanPath string
	// ProposalPath is where the proposal was written; `y` copies it.
	ProposalPath string
	// Review is what changed: table by table, or a line diff
	// (adapt.Compare).
	Review adapt.Review
	// Out is where the clipboard sequence goes; nil means os.Stdout.
	Out io.Writer
	// Issues are the proposal's `igris check` problems; none means it
	// passes.
	Issues []string
	// Done, when set, is called with the owner's decision as the review
	// ends, and its message is sent to the program holding the review
	// instead of quitting it. Nil means the review ends its own program.
	Done func(accepted bool) tea.Msg
}

// Review shows what the proposal changes in the plan with its validation
// result and returns whether the owner accepted it (SPEC §9). Quitting in
// any other way rejects it.
func Review(ctx context.Context, o ReviewOptions) (bool, error) {
	m := newReview(o)
	m.th = newTheme(lipgloss.NewRenderer(os.Stdout), darkTheme(o.Theme, lipgloss.HasDarkBackground), nil)
	final, err := tea.NewProgram(newHover(m), programOptions(ctx, o.Mouse)...).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return false, nil // ctx ended: nothing was accepted
	}
	if err != nil {
		return false, err
	}
	r, ok := unwrap(final).(*review)
	return ok && r.accepted, nil
}

// review is the TUI of the adapt review: the scrollable changes over a bar
// with Reject and Accept. Reject is the safe default and has the focus
// first.
type review struct {
	o             ReviewOptions
	th            *theme
	width, height int

	view     page // only its scroll state is used
	body     []string
	bodyW    int // width body was laid out for
	focus    action
	dialog   *dialog
	zones    zones
	accepted bool
	notice   string // "copied" after y
}

func newReview(o ReviewOptions) *review {
	return &review{o: o, th: &theme{}, width: 80, height: 24, focus: actReject}
}

func (r *review) Init() tea.Cmd { return nil }

func (r *review) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = msg.Width, msg.Height
	case tea.KeyMsg:
		return r, r.key(msg)
	case tea.MouseMsg:
		return r, r.mouse(msg)
	}
	return r, nil
}

func (r *review) key(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if k == "ctrl+c" {
		return r.activate(actReject)
	}
	if r.dialog != nil {
		if a, ok := r.dialog.key(msg); ok {
			return r.activate(a)
		}
		return nil
	}
	switch k {
	case "a":
		return r.activate(actAccept)
	case "y":
		r.notice = copyNotice(r.o.Out, r.o.ProposalPath)
		return nil
	case "r", "esc", "q":
		return r.activate(actReject)
	case "tab", "shift+tab", "left", "right", "h", "l":
		if r.focus == actReject {
			r.focus = actAccept
		} else {
			r.focus = actReject
		}
	case "enter", " ", "space":
		return r.activate(r.focus)
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		r.view.key(k)
	}
	return nil
}

func (r *review) mouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action != tea.MouseActionPress {
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		r.view.scroll(-3)
		return nil
	case tea.MouseButtonWheelDown:
		r.view.scroll(3)
		return nil
	case tea.MouseButtonLeft:
	default:
		return nil
	}
	t, ok := r.zones.at(msg.X, msg.Y)
	if !ok {
		return nil
	}
	if r.dialog != nil {
		if t.act == actOption {
			return r.activate(r.dialog.pick(t.option))
		}
		return nil // modal: the bar under it can't be used
	}
	return r.activate(t.act)
}

// activate runs action a and returns the command that ends the review
// once the owner has decided.
func (r *review) activate(a action) tea.Cmd {
	switch a {
	case actReject:
		return r.finish(false)
	case actAccept:
		if len(r.o.Issues) > 0 {
			verb := "Replace"
			switch {
			case r.o.NewPlan:
				verb = "Write"
			case r.o.Extend:
				verb = "Extend"
			}
			r.dialog = &dialog{
				title: verb + " the plan anyway?",
				detail: fmt.Sprintf("The proposal has %d problem(s) igris check rejects, e.g. models left at ?. "+
					"igris arise refuses the plan until you fix them.", len(r.o.Issues)),
				options: []option{{"Keep reviewing", actClose}, {verb + " anyway", actAcceptAnyway}},
				cancel:  actClose,
			}
			return nil
		}
		return r.finish(true)
	case actAcceptAnyway:
		return r.finish(true)
	case actClose:
		r.dialog = nil
	}
	return nil
}

// finish records the owner's decision and ends the review: it quits the
// program, or tells the one holding the review through Done.
func (r *review) finish(accepted bool) tea.Cmd {
	r.accepted = accepted
	if r.o.Done != nil {
		return func() tea.Msg { return r.o.Done(accepted) }
	}
	return tea.Quit
}

func (r *review) View() string {
	r.zones.reset()
	w, h := max(r.width, 20), max(r.height, 6)
	command := r.o.Command
	if command == "" {
		command = "igris adapt"
	}
	// An open screen (SPEC §15.1): the header names the review, then the
	// validation result and the note, a rule, the changes, a rule and the
	// bar.
	out := []string{
		r.th.header(w, strings.TrimPrefix(command, "igris ")+" · review "+r.o.PlanPath+r.th.paint(lookDim, " · "+r.o.Review.Summary()), ""),
		fit(r.status(), w),
	}
	if r.o.Note != "" {
		out = append(out, fit(r.th.paint(lookDim, clean(r.o.Note)), w))
	}
	out = append(out, r.th.rule(w, ""))
	if r.bodyW != w {
		r.body, r.bodyW = r.layout(w), w
	}
	rows := h - len(out) - 2
	r.view.rows, r.view.total = rows, len(r.body)
	r.view.clamp()
	for i := range rows {
		out = append(out, line(r.body, r.view.top+i))
	}
	out = append(out, r.th.rule(w, ""))

	modal := r.dialog != nil
	bar := layoutBar(r.th, []button{{label: "Reject", key: "r", act: actReject}, {label: "Accept", key: "a", act: actAccept}}, w, 1, r.focus, modal, nil)
	bar.barZones(&r.zones, h-1, modal)
	foot := bar.text[0]
	if len(r.body) > rows {
		foot += r.th.paint(lookDim, fmt.Sprintf("  %d–%d of %d · ↑↓ scroll", r.view.top+1, min(r.view.top+rows, len(r.body)), len(r.body)))
	}
	out = append(out, fit(foot, w))
	if modal {
		// The confirmation floats over the dimmed review when there is
		// room, else it takes the screen (SPEC §15.5).
		var z zones
		if w >= wideWidth {
			out, z = r.th.floatDialog(out, r.dialog, w, 2, h-chromeRows)
		} else {
			out, z = r.th.fullScreenDialog(r.dialog, w, h)
		}
		r.zones.merge(z, 0, 0)
	}
	return strings.Join(out, "\n")
}

// status is the proposal's validation result.
func (r *review) status() string {
	s := r.th.paint(lookAccent, "✓ the proposal passes igris check")
	if len(r.o.Issues) > 0 {
		s = r.th.paint(lookAlert, fmt.Sprintf("⨯ the proposal has %d problem(s) igris check rejects", len(r.o.Issues)))
	}
	if r.notice != "" {
		s += r.th.paint(lookDim, " · "+r.notice)
	}
	return s
}

// diffContext is how many unchanged lines are shown around a change.
const diffContext = 3

// layout lays the problems and the changes out for w cells: the task
// tables change by change, then a line diff of the rest, or only a line
// diff when the files weren't compared table by table.
func (r *review) layout(w int) []string {
	var out []string
	if len(r.o.Issues) > 0 {
		out = append(out, r.th.paint(lookAlert, fit("Problems (fix them after accepting, or reject):", w)))
		for _, is := range r.o.Issues {
			for _, l := range hang("  ⨯ ", clean(is), w) {
				out = append(out, r.th.paint(lookAlert, l))
			}
		}
		out = append(out, "")
	}
	rv := r.o.Review
	if !rv.Tables {
		if len(rv.Prose) == 0 {
			return append(out, r.th.paint(lookDim, "(both files are empty)"))
		}
		return append(out, r.lineDiff(rv.Prose, w)...)
	}
	if len(rv.Sections) == 0 {
		out = append(out, r.th.paint(lookDim, fit("The task tables are unchanged.", w)))
	}
	for _, s := range rv.Sections {
		out = append(out, r.marked("", s.Op, "phase "+s.Heading, w)...)
		out = append(out, r.changes(s.Changes, w)...)
	}
	out = append(out, "")
	if added, removed := adapt.Counts(rv.Prose); added+removed == 0 {
		out = append(out, r.th.paint(lookDim, fit("The text outside the task tables is unchanged.", w)))
	} else {
		out = append(out, r.th.paint(lookDim, fit("Outside the task tables:", w)))
		out = append(out, r.lineDiff(rv.Prose, w)...)
	}
	if len(rv.Notes) > 0 {
		out = append(out, "", r.th.paint(lookDim, fit("Draft notes (not written to the plan):", w)))
		for _, n := range rv.Notes {
			out = append(out, hang("  ", clean(n), w)...)
		}
	}
	return out
}

// changeIndent sets a phase's changes off from its heading.
const changeIndent = "  "

// changes draws the changes of one phase. A changed value takes one row:
// wide, the values lined up after the widest name; narrow, as
// `name: "old" → "new"`. When that doesn't fit, the old and the new value
// get rows of their own, marked - and +.
func (r *review) changes(cs []adapt.Change, w int) []string {
	nameW := 0
	for _, c := range cs {
		if c.Op == adapt.Mod {
			nameW = max(nameW, textWidth(clean(c.What)))
		}
	}
	var out []string
	for _, c := range cs {
		if c.Op != adapt.Mod {
			out = append(out, r.marked(changeIndent, c.Op, clean(c.String()), w)...)
			continue
		}
		what := clean(c.What)
		one := clean(c.String())
		if w >= wideWidth {
			one = what + strings.Repeat(" ", nameW-textWidth(what)) + "  " + clean(adapt.Quote(c.Old)+" → "+adapt.Quote(c.New))
		}
		if len(changeIndent)+2+textWidth(one) <= w {
			out = append(out, r.marked(changeIndent, adapt.Mod, one, w)...)
			continue
		}
		out = append(out, r.marked(changeIndent, adapt.Mod, what, w)...)
		out = append(out, r.marked(changeIndent, adapt.Del, "  "+clean(adapt.Quote(c.Old)), w)...)
		out = append(out, r.marked(changeIndent, adapt.Add, "  "+clean(adapt.Quote(c.New)), w)...)
	}
	return out
}

// marked draws text after indent and the marker of op in rows of w cells,
// indent and marker on each, so the change never depends on color.
func (r *review) marked(indent string, op adapt.Op, text string, w int) []string {
	mark, lk := "  ", lookPlain
	switch op {
	case adapt.Del:
		mark, lk = "- ", lookAlert
	case adapt.Add:
		mark, lk = "+ ", lookAccent
	case adapt.Mod:
		mark, lk = "~ ", lookTitle
	}
	var out []string
	for _, part := range chop(text, w-len(indent)-2) {
		out = append(out, r.th.paint(lk, indent+mark+part))
	}
	return out
}

// lineDiff draws a line diff for w cells. Every line keeps its -, + or
// space marker, also when it wraps; long unchanged stretches are folded.
func (r *review) lineDiff(d []adapt.Line, w int) []string {
	var out []string
	changed := make([]bool, len(d))
	for i, l := range d {
		changed[i] = l.Op != adapt.Equal
	}
	near := func(i int) bool {
		for j := max(i-diffContext, 0); j <= min(i+diffContext, len(d)-1); j++ {
			if changed[j] {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(d); {
		if !near(i) {
			j := i
			for j < len(d) && !near(j) {
				j++
			}
			out = append(out, r.th.paint(lookDim, fit(fmt.Sprintf("  ⋯ %d unchanged line(s)", j-i), w)))
			i = j
			continue
		}
		out = append(out, r.marked("", d[i].Op, clean(d[i].Text), w)...)
		i++
	}
	return out
}

// clean makes a session-written line safe to draw: escape sequences, bidi
// and zero-width characters go (textsafe.Line, SPEC §16), a tab becomes
// four spaces so columns still line up.
func clean(s string) string {
	return textsafe.Line(strings.ReplaceAll(s, "\t", "    "))
}

// chop cuts s into pieces of at most w cells, keeping every space, so
// table alignment changes stay visible.
func chop(s string, w int) []string {
	w = max(w, 1)
	if s == "" {
		return []string{""}
	}
	var out []string
	var b strings.Builder
	used := 0
	for _, c := range s {
		cw := textWidth(string(c))
		if used+cw > w && used > 0 {
			out = append(out, b.String())
			b.Reset()
			used = 0
		}
		b.WriteRune(c)
		used += cw
	}
	return append(out, b.String())
}
