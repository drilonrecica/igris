package notify

import (
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// templated is the text a channel's template makes of m (SPEC §10):
// cleaned of control characters (newlines kept), trimmed and cut by cut.
// It is "" when there is no template, m is a digest (digests have a fixed
// format) or the result is empty: the channel then sends its default text.
func templated(t *template.Template, m Message, cut func(string) string) string {
	if t == nil || m.Event == Digest {
		return ""
	}
	at := m.At
	if !at.IsZero() {
		at = at.In(time.Local)
	}
	out, err := config.ExecuteMessageTemplate(t, config.MessageVars{
		Event: string(m.Event), Project: m.Project, Phase: m.Phase, TaskID: m.TaskID,
		Title: PlainTitle(m.Title), What: m.What, RunID: m.RunID, At: at,
	})
	if err != nil {
		// The template ran on a sample when the config loaded; should it
		// still fail, the message goes out with the default text.
		return ""
	}
	out = strings.TrimSpace(textsafe.Clean(out))
	if out == "" {
		return ""
	}
	return cut(out)
}

// cutBytes cuts s to at most n bytes on a character boundary, ending in
// "…" when it was longer.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	end := n - len("…")
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}

// cutTo returns a cut to n characters, for templated.
func cutTo(n int) func(string) string {
	return func(s string) string { return cutRunes(s, n) }
}
