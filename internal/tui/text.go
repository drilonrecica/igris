package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Text helpers for plain (unstyled) strings. Widths are terminal cells.

func textWidth(s string) int { return lipgloss.Width(s) }

// pad fills s with spaces up to w cells.
func pad(s string, w int) string {
	if n := w - textWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// fit shortens s to at most w cells, ending in "…" when it was cut.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := textWidth(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
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
