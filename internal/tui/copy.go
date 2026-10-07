package tui

import (
	"io"
	"os"
	"time"

	"github.com/aymanbagabas/go-osc52/v2"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// noticeFor is how long a notice such as "copied" stays in the header.
const noticeFor = 3 * time.Second

// copyText puts text on the owner's clipboard with an OSC 52 sequence
// written to out (os.Stdout when nil), the terminal the program draws on.
// Terminals without OSC 52 ignore the sequence, so success only means it
// was written. The text is base64-encoded in the sequence, so it can't
// carry escape codes of its own. Home pages reuse this helper.
func copyText(out io.Writer, text string) error {
	if out == nil {
		out = os.Stdout
	}
	_, err := osc52.New(text).WriteTo(out)
	return err
}

// copyNotice copies text and returns the notice to show for it.
func copyNotice(out io.Writer, text string) string {
	if text == "" {
		return "nothing to copy"
	}
	if err := copyText(out, text); err != nil {
		return "could not copy: " + err.Error()
	}
	return "copied"
}

// copySelected copies the item under the owner's focus: the log line at
// the bottom of the view when the log has the focus, else the command that
// resumes the current session.
func (m *model) copySelected() {
	text := ""
	switch {
	case m.focus == focusLog:
		if i := len(m.log) - 1 - min(m.scroll, max(len(m.log)-1, 0)); i >= 0 {
			text = textsafe.Line(m.log[i].text)
		}
	case m.cur != nil && m.cur.session != nil && engine.ValidSessionID(m.cur.claudeSession):
		text = "claude --resume " + m.cur.claudeSession
	}
	m.notice, m.noticeAt = copyNotice(m.opts.Out, text), m.opts.Now()
}
