package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// Text helpers. Widths are terminal cells; SGR sequences take none. wrap
// takes plain text only.

func textWidth(s string) int { return lipgloss.Width(s) }

// pad fills s with spaces up to w cells.
func pad(s string, w int) string {
	if n := w - textWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// fit shortens s to at most w cells, ending in "…" when it was cut. Styled
// text is cut between its SGR sequences and reset after the cut.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used, styled := 0, false
	for i := 0; i < len(s); {
		if n := sgrLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			styled = true
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := textWidth(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
		i += size
	}
	b.WriteString("…")
	if styled {
		b.WriteString(sgrReset)
	}
	return b.String()
}

// sgrLen is the length of the escape sequence s starts with ("\x1b[1;7m"),
// or 0 if it starts with none.
func sgrLen(s string) int {
	if !strings.HasPrefix(s, "\x1b[") {
		return 0
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return 0
}

// wrap breaks s into lines of at most w cells at spaces; words longer than
// w are cut.
func wrap(s string, w int) []string {
	w = max(w, 2)
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			for textWidth(word) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				head := fit(word, w)
				head = strings.TrimSuffix(head, "…")
				out = append(out, head)
				word = word[len(head):]
			}
			switch {
			case line == "":
				line = word
			case textWidth(line)+1+textWidth(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}
