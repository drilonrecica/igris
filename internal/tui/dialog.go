package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
)

// dialog is a modal choice list (SPEC §15.5). The first option is the safe
// default and starts selected.
type dialog struct {
	title    string
	detail   string
	options  []option
	selected int
	// cancel is what esc picks: the non-destructive choice, or actClose
	// for a question that stays pending until answered.
	cancel action
	// question is the engine question the dialog answers; "" for dialogs
	// the TUI opens itself.
	question engine.Question
	// input is a text field above the options; nil for none. While inField
	// is set it has the focus and takes the typed keys.
	input   *field
	inField bool
	// submit is what enter in the field picks; actNone leaves the field
	// for the options instead.
	submit action
	// task is the task a dialog the TUI opened is about; it closes when
	// that task ends.
	task string
	// answers is the question a TUI dialog was opened from (the session
	// lost question's Skip); sending its command answers that question.
	answers engine.Question
	// scroll is how many detail lines are scrolled away (pgup/pgdown, the
	// wheel), for a detail longer than the box.
	scroll int
}

type option struct {
	label string
	act   action
}

// field is a one-line text field.
type field struct {
	label string
	value string
	hint  string // shown below the field, e.g. why a pick didn't work
}

// questionDialog builds the dialog for an Asked event.
func questionDialog(ev engine.Event) *dialog {
	d := &dialog{detail: ev.Detail, question: ev.Question}
	switch ev.Question {
	case engine.QuestionCommit:
		d.title = "Commit " + ev.Task + "?"
		d.options = []option{{"Commit", actAnswerYes}, {"Leave uncommitted", actAnswerNo}}
		d.cancel = actClose
	case engine.QuestionConfirmSkip:
		d.title = ev.Task + " asks to skip"
		d.options = []option{{"Keep working", actAnswerNo}, {"Skip the task", actAnswerYes}}
		d.cancel = actAnswerNo
	case engine.QuestionSessionLost:
		d.title = ev.Task + ": session lost"
		d.options = []option{
			{"Start fresh", actRetryFresh},
			{"Continue conversation", actRetryContinue},
			{"Mark done", actDone},
			{"Skip…", actSkip},
			{"Stop igris", actStop},
		}
		d.cancel = actClose
	case engine.QuestionHookFailed:
		d.title = ev.Task + ": before_task hook failed"
		d.options = []option{
			{"Retry", actRetryFresh},
			{"Mark done", actDone},
			{"Skip…", actSkip},
			{"Stop igris", actStop},
		}
		d.cancel = actClose
	default:
		d.title = "Question"
		d.options = []option{{"Yes", actAnswerYes}, {"No", actAnswerNo}}
	}
	return d
}

// stopDialog confirms the Stop action.
func stopDialog() *dialog {
	return &dialog{
		title:   "Stop igris?",
		detail:  "The session stays open; igris arise picks the run up again.",
		options: []option{{"Keep running", actClose}, {"Stop igris", actStop}},
		cancel:  actClose,
	}
}

// retryDialog asks how to retry task id.
func retryDialog(id string) *dialog {
	return &dialog{
		title:   "Retry " + id + "?",
		detail:  "The current session is closed first.",
		options: []option{{"Start fresh", actRetryFresh}, {"Continue conversation", actRetryContinue}},
		cancel:  actClose,
		task:    id,
	}
}

// skipDialog asks for the reason to skip task id. The field has the focus;
// leaving it lands on Cancel, so enter twice never skips.
func skipDialog(id string, agent bool) *dialog {
	detail := "The reason is recorded with the skip."
	if agent {
		detail += " The session is closed."
	}
	return &dialog{
		title:   "Skip " + id + "?",
		detail:  detail,
		input:   &field{label: "Reason: "},
		inField: true,
		options: []option{{"Cancel", actClose}, {"Skip", actSkipConfirm}},
		cancel:  actClose,
		task:    id,
	}
}

// doneDialog asks before marking user task id done, with an optional
// note. Done isn't destructive, so enter in the field confirms.
func doneDialog(id string) *dialog {
	return &dialog{
		title:   "Mark " + id + " done?",
		detail:  "You did it outside igris. Add a note if there is something to remember; it goes into igris history.",
		input:   &field{label: "Note (optional): "},
		inField: true,
		submit:  actDoneConfirm,
		options: []option{{"Done", actDoneConfirm}, {"Cancel", actClose}},
		cancel:  actClose,
		task:    id,
	}
}

// modeDialog lets the owner pick a mode: for the run when task is "", else
// for that task's next session. The current mode starts selected.
func modeDialog(task, current string) *dialog {
	d := &dialog{
		title:  "Run mode for the next sessions",
		detail: "A running session keeps its mode.",
		cancel: actClose,
		task:   task,
	}
	if task != "" {
		d.title = "Mode for " + task + "'s next session"
	}
	for i, m := range modeActs {
		label := m.label
		if m.mode == current {
			label += " (current)"
			d.selected = i
		}
		d.options = append(d.options, option{label, m.act})
	}
	return d
}

// yoloDialog asks for the typed phrase before switching to skip
// permissions (SPEC §7.3); task is as for modeDialog.
func yoloDialog(task string) *dialog {
	what := "the next sessions"
	if task != "" {
		what = task + "'s next session"
	}
	return &dialog{
		title:   "Skip permissions?",
		detail:  "Claude Code runs " + what + " with --dangerously-skip-permissions: no permission prompts at all. Type \"" + engine.YoloPhrase + "\" to confirm.",
		input:   &field{label: "Phrase: "},
		inField: true,
		options: []option{{"Cancel", actClose}, {"Skip permissions", actYoloConfirm}},
		cancel:  actClose,
		task:    task,
	}
}

// key handles a key press. It returns the action to run, if the key picked
// one, and whether the key was used.
func (d *dialog) key(msg tea.KeyMsg) (action, bool) {
	k := msg.String()
	if d.inField {
		return d.fieldKey(msg)
	}
	switch k {
	case "up", "k", "shift+tab":
		if d.input != nil && (d.selected == 0 || k == "shift+tab") {
			d.inField = true
			return actNone, true
		}
		d.move(-1)
	case "down", "j", "tab":
		d.move(1)
	case "enter", " ", "space":
		return d.options[d.selected].act, true
	case "esc":
		return d.cancel, true
	case "pgup":
		d.scrollBy(-3)
	case "pgdown":
		d.scrollBy(3)
	default:
		n, err := strconv.Atoi(k)
		if err != nil || n < 1 || n > len(d.options) || n > 9 {
			return actNone, false
		}
		d.selected = n - 1
		return d.options[d.selected].act, true
	}
	return actNone, true
}

// fieldKey handles a key while the text field has the focus: every
// printable key types.
func (d *dialog) fieldKey(msg tea.KeyMsg) (action, bool) {
	f := d.input
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		f.value += string(msg.Runes)
		if msg.Type == tea.KeySpace && len(msg.Runes) == 0 {
			f.value += " "
		}
		f.hint = ""
		return actNone, true
	case tea.KeyBackspace:
		if r := []rune(f.value); len(r) > 0 {
			f.value = string(r[:len(r)-1])
		}
		return actNone, true
	case tea.KeyEnter, tea.KeyTab, tea.KeyDown:
		if msg.Type == tea.KeyEnter && d.submit != actNone {
			return d.submit, true
		}
		d.inField, d.selected = false, 0
		return actNone, true
	case tea.KeyEsc:
		return d.cancel, true
	}
	return actNone, false
}

// scrollBy scrolls the detail by n lines; render keeps it in range.
func (d *dialog) scrollBy(n int) { d.scroll = max(d.scroll+n, 0) }

func (d *dialog) move(by int) {
	d.selected = (d.selected + by + len(d.options)) % len(d.options)
}

// pick selects option i (a click) and returns its action.
func (d *dialog) pick(i int) action {
	if i < 0 || i >= len(d.options) {
		return actNone
	}
	d.inField = false
	d.selected = i
	return d.options[i].act
}

// render draws the dialog as a box at most w cells wide and h lines high
// and records the option and field zones relative to the box's top-left
// corner. When space is short the detail text is cut, never the field or
// the options. The selected option is a focus bar across the box.
func (d *dialog) render(th *theme, w, h int) ([]string, zones) {
	inner := max(w-4, 8) // "│ " + text + " │"
	title := wrap(d.title, inner)
	var opts [][]string
	optLines := 0
	for i, o := range d.options {
		mark := "  "
		if i == d.selected && !d.inField {
			mark = "› "
		}
		lines := wrap(o.label, inner-5) // "› 1. "
		lines[0] = mark + strconv.Itoa(i+1) + ". " + lines[0]
		for j := 1; j < len(lines); j++ {
			lines[j] = "     " + lines[j]
		}
		opts = append(opts, lines)
		optLines += len(lines)
	}
	var input []string
	if d.input != nil {
		input = append(input, d.fieldLine(inner))
		if d.input.hint != "" {
			input = append(input, fit(d.input.hint, inner))
		}
	}
	var detail []string
	if d.detail != "" {
		detail = wrap(d.detail, inner)
		// Borders, the title, a blank line before the detail and before the
		// options, the field and the blank line after it.
		room := h - 2 - len(title) - 2 - optLines
		if len(input) > 0 {
			room -= len(input) + 1
		}
		if room < 1 {
			detail = nil
		} else if len(detail) > room {
			// A long detail scrolls; "…" marks the lines cut on either side.
			d.scroll = min(d.scroll, len(detail)-room)
			cutTop, cutBottom := d.scroll > 0, d.scroll+room < len(detail)
			detail = detail[d.scroll : d.scroll+room]
			if cutTop {
				detail[0] = fit("… "+detail[0], inner)
			}
			if cutBottom {
				detail[room-1] = fit(detail[room-1]+" …", inner)
			}
		} else {
			d.scroll = 0
		}
	}

	var body []string
	var looks []look // of each body line
	add := func(l look, lines ...string) {
		for _, s := range lines {
			body, looks = append(body, s), append(looks, l)
		}
	}
	add(lookTitle, title...)
	if len(detail) > 0 {
		add(lookPlain, "")
		add(lookPlain, detail...)
	}
	add(lookPlain, "")
	var z zones
	if len(input) > 0 {
		z.add(rect{x: 0, y: len(body) + 1, w: 1, h: 1}, target{act: actField})
		add(lookPlain, input[0])
		add(lookAlert, input[1:]...) // the hint
		add(lookPlain, "")
	}
	for i, lines := range opts {
		z.add(rect{x: 0, y: len(body) + 1, w: 1, h: len(lines)}, target{act: actOption, option: i})
		if i == d.selected && !d.inField {
			add(lookFocus, lines...)
		} else {
			add(lookPlain, lines...)
		}
	}
	width := 0
	for _, l := range body {
		width = max(width, textWidth(l))
	}
	v := th.paint(lookAccent, "│")
	out := make([]string, 0, len(body)+2)
	out = append(out, th.paint(lookAccent, "╭"+strings.Repeat("─", width+2)+"╮"))
	for i, l := range body {
		if looks[i] == lookPlain {
			l = pad(th.marks(l), width)
		} else {
			l = th.paint(looks[i], pad(l, width))
		}
		out = append(out, v+" "+l+" "+v)
	}
	out = append(out, th.paint(lookAccent, "╰"+strings.Repeat("─", width+2)+"╯"))
	for i := range z.list {
		z.list[i].r.w = width + 4
	}
	return out, z
}

// fieldLine draws the text field in w cells: its label, the end of its
// value that fits, and a cursor while it has the focus. A field without
// the focus is drawn in brackets so it reads as a field.
func (d *dialog) fieldLine(w int) string {
	f := d.input
	cursor := " "
	if d.inField {
		cursor = "▏"
	}
	room := max(w-textWidth(f.label)-3, 1) // "[" + value + cursor + "]"
	v := []rune(f.value)
	for textWidth(string(v)) > room {
		v = v[1:]
	}
	val := string(v)
	if len(v) < len([]rune(f.value)) {
		val = "…" + string(v[1:])
	}
	return fit(f.label+"["+pad(val+cursor, room+1)+"]", w)
}
