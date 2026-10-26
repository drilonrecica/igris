package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
)

// dialog is a modal choice list (SPEC §15.5). The default starts
// selected: the first option, or Cancel, which is always the last.
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
	// def is the option the focus lands on whenever it leaves the field:
	// the default, Cancel where that is the default, so enter twice never
	// confirms.
	def int
	// task is the task a dialog the TUI opened is about; it closes when
	// that task ends.
	task string
	// mode is the mode a skip-permissions dialog switches to.
	mode string
	// answers is the question a TUI dialog was opened from (the session
	// lost question's Skip); sending its command answers that question.
	answers engine.Question
	// scroll is how many detail lines are scrolled away (pgup/pgdown, the
	// wheel), for a detail longer than the box.
	scroll int
	// pre keeps the detail's line breaks and indentation (a plan in
	// markdown); wide lets the box grow past the usual width.
	pre, wide bool
	// echo, when set, makes the detail the field's whole value, its end in
	// view while the field has the focus (a side session's first prompt,
	// longer than one line); echo itself is the detail while the value is
	// empty.
	echo string
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
	// The task it asks about: the answer goes to it, and it closes when
	// that task's question is settled (SPEC §15.1).
	d := &dialog{detail: ev.Detail, question: ev.Question, task: ev.Task}
	switch ev.Question {
	case engine.QuestionCommit:
		d.title = "Commit " + ev.Task + "?"
		d.options = []option{{"Commit", actAnswerYes}, {"Leave uncommitted", actAnswerNo}, {"Commit all (no more commit questions this run)", actCommitAll}}
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
	case engine.QuestionConfirmReset:
		// A session can write the request too: keeping is the default.
		d.title = "Reset " + ev.Task + "?"
		d.options = []option{{"Keep it as it is", actAnswerNo}, {"Reset " + ev.Task, actAnswerYes}}
		d.cancel = actClose
	case engine.QuestionPlanApproval:
		return planDialog(ev)
	case engine.QuestionBudget:
		return budgetDialog(ev)
	case engine.QuestionCommitBatch:
		return batchDialog(ev)
	case engine.QuestionHookFailed:
		d.title = ev.Task + ": before_task hook failed"
		d.options = []option{
			{"Retry", actRetryFresh},
			{"Mark done", actDone},
			{"Skip…", actSkip},
			{"Stop igris", actStop},
		}
		d.cancel = actClose
	case engine.QuestionIntegration:
		// The task didn't integrate (SPEC §6.11, §15.5); its worktree is
		// kept. The conflicting files or the verify output's last lines
		// follow the question.
		d.title = ev.Task + " didn't integrate"
		if len(ev.Output) > 0 {
			d.detail += "\n\n" + strings.Join(ev.Output, "\n")
			d.pre = true
		}
		d.options = []option{
			{"Rerun fresh", actRetryFresh},
			{"Mark done (integrated by hand)", actDone},
			{"Skip…", actSkip},
			{"Stop igris", actStop},
		}
		if ev.Resolve {
			// A resolver session for this one task, first (SPEC §6.11).
			d.options = append([]option{{"Resolve (a resolver session)", actResolve}}, d.options...)
		}
		d.cancel = actClose
	case engine.QuestionFilesInTheWay:
		// Files where the run branch is checked out are in the way of the
		// fast-forward (SPEC §6.11): a rerun can't help. Once the owner
		// moved them, Try again fast-forwards the committed branch.
		d.title = ev.Task + " didn't integrate"
		if len(ev.Output) > 0 {
			d.detail += "\n\n" + strings.Join(ev.Output, "\n")
			d.pre = true
		}
		d.options = []option{
			{"Try again", actRetryFresh},
			{"Mark done (integrated by hand)", actDone},
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

// planDialog shows the plan a session presents for approval (SPEC §6.3):
// approve, switching the session to the mode after approval, reject with
// feedback, or answer in the pane, where Claude Code's own dialog waits
// too. Esc leaves the question pending.
func planDialog(ev engine.Event) *dialog {
	then := ev.Then + badge(ev.Then) // yolo: no permission prompts from then on
	if ev.Then == "" {
		then = "the mode after approval"
	}
	return &dialog{
		title:  ev.Task + " presents a plan",
		detail: ev.Plan,
		pre:    true,
		wide:   true,
		options: []option{
			{"Approve: the session switches to " + then, actPlanApprove},
			{"Reject with feedback…", actPlanReject},
			{"Answer in the pane", actPlanPane},
		},
		cancel:   actClose,
		question: engine.QuestionPlanApproval,
		task:     ev.Task,
	}
}

// planRejectDialog asks for the feedback that rejects task id's plan. The
// session revises the plan with it and presents it again. Back and esc
// return to the plan.
func planRejectDialog(id string) *dialog {
	return &dialog{
		title:   "Reject the plan of " + id + "?",
		detail:  "Your feedback goes to the session, which revises the plan and presents it again.",
		input:   &field{label: "Feedback: "},
		inField: true,
		submit:  actPlanRejectConfirm,
		options: []option{{"Send feedback", actPlanRejectConfirm}, {"Back to the plan", actAnswer}},
		cancel:  actAnswer,
		task:    id,
		answers: engine.QuestionPlanApproval,
	}
}

// confirmDialog asks before act, which can't be taken back: yes first,
// Cancel last and focused, as every dialog with a Cancel (SPEC §15.5).
func confirmDialog(title, detail, yes string, act action) *dialog {
	return &dialog{
		title:    title,
		detail:   detail,
		options:  []option{{yes, act}, {"Cancel", actClose}},
		selected: 1,
		cancel:   actClose,
	}
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
// leaving it lands on Cancel, the last option, so enter twice never skips.
// session says the task has an open session, which the skip closes.
func skipDialog(id string, session bool) *dialog {
	detail := "The reason is recorded with the skip."
	if session {
		detail += " The session is closed."
	}
	return &dialog{
		title:    "Skip " + id + "?",
		detail:   detail,
		input:    &field{label: "Reason: "},
		inField:  true,
		options:  []option{{"Skip", actSkipConfirm}, {"Cancel", actClose}},
		selected: 1,
		def:      1,
		cancel:   actClose,
		task:     id,
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
	d.options, d.selected = modeOptions(current)
	if d.selected >= 0 {
		d.options[d.selected].label += " (current)"
	}
	d.selected = max(d.selected, 0)
	return d
}

// yoloDialog asks for the typed phrase before switching to mode, yolo or
// plan:yolo (SPEC §7.3); task is as for modeDialog.
func yoloDialog(task, mode string) *dialog {
	what := "the next sessions"
	if task != "" {
		what = task + "'s next session"
	}
	detail := "Claude Code runs " + what + " with --dangerously-skip-permissions: no permission prompts at all."
	if mode == engine.ModePlanYolo {
		detail = "Claude Code starts " + what + " in plan mode with --allow-dangerously-skip-permissions; once you approve the plan in igris, it runs with no permission prompts at all."
	}
	return &dialog{
		title:    "Skip permissions?",
		detail:   detail + " Type \"" + engine.YoloPhrase + "\" to confirm.",
		input:    &field{label: "Phrase: "},
		inField:  true,
		options:  []option{{"Skip permissions", actYoloConfirm}, {"Cancel", actClose}},
		selected: 1,
		def:      1,
		cancel:   actClose,
		task:     task,
		mode:     mode,
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
		return d.choose(d.selected), true
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
		return d.choose(n - 1), true
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
		d.inField, d.selected = false, d.def
		return actNone, true
	case tea.KeyEsc:
		return d.cancel, true
	case tea.KeyPgUp:
		d.scrollBy(-3)
		return actNone, true
	case tea.KeyPgDown:
		d.scrollBy(3)
		return actNone, true
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
	return d.choose(i)
}

// choose selects option i and returns its action. More modes… is the
// dialog's own: it lists the rest of the modes and picks nothing.
func (d *dialog) choose(i int) action {
	d.selected = i
	if d.options[i].act == actModeMore {
		d.moreModes()
		return actNone
	}
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
		num := strconv.Itoa(i+1) + ". "
		lines := wrap(o.label, inner-2-len(num)) // "› 1. "
		lines[0] = mark + num + lines[0]
		for j := 1; j < len(lines); j++ {
			lines[j] = strings.Repeat(" ", 2+len(num)) + lines[j]
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
	text := d.detail
	if d.echo != "" && d.input != nil {
		text = d.echo
		if d.input.value != "" {
			text = d.input.value
		}
		if d.inField {
			d.scroll = maxTop // what is typed shows; render clamps
		}
	}
	var detail []string
	if text != "" {
		detail = wrap(text, inner)
		if d.pre {
			detail = wrapPre(text, inner)
		}
		// Borders, the title, a blank line before the detail and before the
		// options, the field and the blank line after it, and the blank
		// line and the key hint after the options.
		room := h - 2 - len(title) - 2 - optLines - 2
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
	var looks []tone // of each body line
	add := func(l tone, lines ...string) {
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
		add(lookPlain, input[1:]...) // the hint
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
	add(lookPlain, "")
	add(lookDim, fit(d.hint(), inner))
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

// hint is the dialog's last line, its keys (SPEC §15.5): what enter does,
// that a number picks an option, and what esc does: the non-destructive
// choice it picks, or "keep asking" for a question that stays pending.
func (d *dialog) hint() string {
	esc := "close"
	switch {
	case d.question != "" && d.cancel == actClose:
		esc = "keep asking"
	default:
		for _, o := range d.options {
			if o.act == d.cancel {
				esc = lowerFirst(strings.TrimSuffix(o.label, "…"))
			}
		}
	}
	if d.inField {
		enter := "next"
		for _, o := range d.options {
			if d.submit != actNone && o.act == d.submit {
				enter = lowerFirst(strings.TrimSuffix(o.label, "…"))
			}
		}
		return "type · enter " + enter + " · esc " + esc
	}
	parts := []string{"enter choose"}
	if n := min(len(d.options), 9); n >= 2 {
		parts = append(parts, "1–"+strconv.Itoa(n)+" pick")
	}
	return strings.Join(append(parts, "esc "+esc), " · ")
}

// lowerFirst lowers the first letter of s, for a label used mid-sentence;
// an ID or an acronym (a capital second letter) keeps its case.
func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 || len(r) > 1 && r[1] >= 'A' && r[1] <= 'Z' {
		return s
	}
	return strings.ToLower(string(r[:1])) + string(r[1:])
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
	// A pasted value may hold line breaks and tabs: one line here.
	full := []rune(strings.NewReplacer("\r\n", "↵", "\n", "↵", "\r", "↵", "\t", " ").Replace(f.value))
	v := full
	for textWidth(string(v)) > room {
		v = v[1:]
	}
	val := string(v)
	if len(v) < len(full) {
		val = "…" + string(v[1:])
	}
	return fit(f.label+"["+pad(val+cursor, room+1)+"]", w)
}
