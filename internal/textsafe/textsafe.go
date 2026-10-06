// Package textsafe cleans text igris did not write (plan cells, done notes,
// command output) before it is drawn on the owner's terminal or typed into a
// session's pane, so it can't carry escape sequences (SPEC §16).
package textsafe

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	esc = 0x1b
	bel = 0x07
	csi = 0x9b // the one-rune form of ESC [
)

// Clean removes ANSI escape sequences and every control character except
// newline and tab. Bytes that are not UTF-8 become U+FFFD.
func Clean(s string) string {
	if !dirty(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == esc:
			i += escapeLen(s[i:])
		case r == csi:
			i += size + csiLen(s[i+size:])
		case r == '\n' || r == '\t':
			b.WriteRune(r)
			i += size
		case unicode.IsControl(r):
			i += size
		default:
			b.WriteRune(r) // an invalid byte decodes to U+FFFD
			i += size
		}
	}
	return b.String()
}

// Line is Clean for text shown on one line: newlines and tabs become spaces.
func Line(s string) string {
	s = Clean(s)
	if !strings.ContainsAny(s, "\n\t") {
		return s
	}
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(s)
}

// HasControl reports whether s holds a control character other than tab.
func HasControl(s string) bool {
	for _, r := range s {
		if r != '\t' && unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// dirty reports whether Clean would change s.
func dirty(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && size == 1) || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			return true
		}
		i += size
	}
	return false
}

// escapeLen is the length of the escape sequence s starts with; s[0] is ESC.
func escapeLen(s string) int {
	if len(s) == 1 {
		return 1
	}
	switch s[1] {
	case '[':
		return 2 + csiLen(s[2:])
	case ']', 'P', 'X', '^', '_':
		// OSC, DCS, SOS, PM, APC: a string up to BEL or ST (ESC \). One that
		// is never closed ends at the line's end rather than eating the rest.
		for i := 2; i < len(s); i++ {
			switch {
			case s[i] == bel:
				return i + 1
			case s[i] == esc && i+1 < len(s) && s[i+1] == '\\':
				return i + 2
			case s[i] == '\n':
				return i
			}
		}
		return len(s)
	}
	// Two-character sequences such as ESC c, with optional intermediates.
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// csiLen is the length of a CSI sequence's parameters and final byte, which
// s starts with.
func csiLen(s string) int {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 0x40 && c <= 0x7e:
			return i + 1
		case c < 0x20 || c > 0x7e:
			return i // not part of a sequence: stop before it
		}
	}
	return len(s)
}
