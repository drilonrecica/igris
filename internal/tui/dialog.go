package tui

import (
	"strconv"
	"strings"

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
}

type option struct {
	label string
	act   action
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
			{"Stop igris", actStop},
		}
		d.cancel = actClose
	default:
		d.title = "Question"
		d.options = []option{{"Yes", actAnswerYes}, {"No", actAnswerNo}}
	}
	return d
}

// key handles a key press. It returns the action to run, if the key picked
// one, and whether the key was used.
func (d *dialog) key(k string) (action, bool) {
	switch k {
	case "up", "k", "shift+tab":
		d.move(-1)
	case "down", "j", "tab":
		d.move(1)
	case "enter", " ", "space":
		return d.options[d.selected].act, true
	case "esc":
		return d.cancel, true
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

func (d *dialog) move(by int) {
	d.selected = (d.selected + by + len(d.options)) % len(d.options)
}

// pick selects option i (a click) and returns its action.
func (d *dialog) pick(i int) action {
	if i < 0 || i >= len(d.options) {
		return actNone
	}
	d.selected = i
	return d.options[i].act
}

// render draws the dialog as a box at most w cells wide and h lines high
// and records the option zones relative to the box's top-left corner. When
// space is short the detail text is cut, never the options.
func (d *dialog) render(w, h int) ([]string, zones) {
	inner := max(w-4, 8) // "│ " + text + " │"
	title := wrap(d.title, inner)
	var opts [][]string
	optLines := 0
	for i, o := range d.options {
		mark := "  "
		if i == d.selected {
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
	var detail []string
	if d.detail != "" {
		detail = wrap(d.detail, inner)
		// Borders, the title, a blank line before and after the detail.
		room := h - 2 - len(title) - 2 - optLines
		if room < 1 {
			detail = nil
		} else if len(detail) > room {
			detail = detail[:room]
			detail[room-1] = fit(detail[room-1]+" …", inner)
		}
	}

	body := append([]string{}, title...)
	if len(detail) > 0 {
		body = append(body, "")
		body = append(body, detail...)
	}
	body = append(body, "")
	var z zones
	for i, lines := range opts {
		z.add(rect{x: 0, y: len(body) + 1, w: 1, h: len(lines)}, target{act: actOption, option: i})
		body = append(body, lines...)
	}
	width := 0
	for _, l := range body {
		width = max(width, textWidth(l))
	}
	out := make([]string, 0, len(body)+2)
	out = append(out, "╭"+strings.Repeat("─", width+2)+"╮")
	for _, l := range body {
		out = append(out, "│ "+pad(l, width)+" │")
	}
	out = append(out, "╰"+strings.Repeat("─", width+2)+"╯")
	for i := range z.list {
		z.list[i].r.w = width + 4
	}
	return out, z
}
