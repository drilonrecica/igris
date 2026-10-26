package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/engine"
)

// The batch question (SPEC §6.5, §15.5): a batch run ended with commits,
// and asks once whether to keep them. Keep is the default; Undo, `git
// reset --soft` to where the run began, takes a second confirmation like
// Stop: it moves the owner's checkout.

// batchEvent folds the batch question and its outcome into the view; it
// reports whether ev was one of them.
func (m *model) batchEvent(ev engine.Event) bool {
	switch {
	case ev.Kind == engine.Asked && ev.Question == engine.QuestionCommitBatch:
		e := ev
		m.batchAsk = &e
		if d := m.dialog; d == nil || (d.question == "" && d.answers == "") {
			m.dialog = batchDialog(ev)
		}
	case ev.Kind == engine.CommitsUndone:
		m.closeBatch()
	default:
		return false
	}
	return false // the log shows both
}

// closeBatch forgets the batch question once it is answered.
func (m *model) closeBatch() {
	m.batchAsk = nil
	if d := m.dialog; d != nil && (d.question == engine.QuestionCommitBatch || d.answers == engine.QuestionCommitBatch) {
		m.dialog = nil
	}
}

// batchDialog asks whether to keep the run's commits, listing them. Undo
// is offered only while they are all the run branch has since the base;
// esc keeps asking.
func batchDialog(ev engine.Event) *dialog {
	title, detail := engine.BatchTitle(ev.Detail)
	if len(ev.Output) > 0 {
		detail = strings.TrimSpace(detail + "\n\n" + strings.Join(ev.Output, "\n"))
	}
	d := &dialog{
		title:    title,
		detail:   detail,
		pre:      true,
		options:  []option{{"Keep", actBatchKeep}},
		cancel:   actClose,
		question: engine.QuestionCommitBatch,
	}
	if ev.Undo {
		d.options = append(d.options, option{"Undo… (git reset --soft; the changes stay)", actBatchUndo})
	}
	return d
}

// batchUndoDialog confirms Undo: Cancel first and focused, so enter
// twice never undoes. Back and esc return to the question.
func batchUndoDialog(ev engine.Event) *dialog {
	return &dialog{
		title:    "Undo the run's commits?",
		detail:   "git reset --soft to where the run began: the commits go, every change stays in the tree and the index, uncommitted.",
		options:  []option{{"Undo the commits", actBatchUndoConfirm}, {"Back", actAnswer}},
		selected: 1,
		cancel:   actAnswer,
		answers:  engine.QuestionCommitBatch,
	}
}

// answerBatch sends the answer to the batch question: keep, or undo.
func (m *model) answerBatch(keep bool) {
	m.dialog = nil
	m.opts.Sender.Send(engine.Command{Kind: engine.CmdAnswer, Question: engine.QuestionCommitBatch, Yes: keep})
	if keep {
		m.closeBatch()
	}
}
