package look

import (
	"fmt"
	"strings"
)

// Glyphs as words (SPEC §15.4, "screen reader"): with `[tui] screen_reader`
// or IGRIS_SCREEN_READER the CLI's text output says a status word where it
// would draw a glyph, and a count where it would draw a bar. A screen
// reader says "check mark" or nothing for ✓ and · and reads ██░░ as noise.

// Unsaid is a Cell.Word for a mark whose text already says it
// ("✓ passed"): the glyph and the space after it are left out.
const Unsaid = "-"

var glyphWords = map[string]string{
	GlyphDone:     "done",
	GlyphRunning:  "running",
	GlyphNeedsYou: "needs you",
	GlyphReady:    "next",
	GlyphBlocked:  "blocked",
	GlyphSkipped:  "skipped",
	GlyphWarn:     "warning",
	GlyphFail:     "failed",
	GlyphWaiting:  "waiting",
}

// GlyphWord is the word a screen reader hears for glyph: "" for a string
// that is not one of the status glyphs.
func GlyphWord(glyph string) string { return glyphWords[glyph] }

// MarkAs is Mark with the word to say for the glyph, where the glyph's
// own word isn't the right one (Unsaid: none).
func MarkAs(sev Severity, glyph, word, text string) Cell {
	c := Mark(sev, glyph, text)
	c.Word = word
	return c
}

// leadGlyph is the status glyph a text starts with, "" without one. The
// glyph must be followed by a space or end the text.
func leadGlyph(s string) string {
	for g := range glyphWords {
		if rest, ok := strings.CutPrefix(s, g); ok && (rest == "" || rest[0] == ' ') {
			return g
		}
	}
	return ""
}

// Spoken is the cell with its leading glyph replaced by its word, the
// painted span growing or shrinking with it; a cell without a glyph is
// returned as it is.
func (c Cell) Spoken() Cell {
	g := leadGlyph(c.Text)
	if g == "" || c.Span > 0 && c.Span < len(g) {
		return c
	}
	word := cmpOr(c.Word, glyphWords[g])
	rest := strings.TrimPrefix(c.Text[len(g):], " ")
	if word == Unsaid {
		if c.Span > 0 {
			c.Span = max(c.Span-len(g)-1, 0)
		}
		c.Text = rest
		return c
	}
	if c.Span > 0 {
		c.Span += len(word) - len(g)
	}
	c.Text = strings.TrimSuffix(word+" "+rest, " ")
	return c
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Words is a line of text with its leading glyph said: "! M1-03 …" is
// "needs you M1-03 …". A glyph whose word the line already says is just
// dropped ("✓ M1-01 done" is "M1-01 done"). Lines indented or without a
// glyph are returned as they are.
func Words(line string) string {
	g := leadGlyph(line)
	if g == "" {
		return line
	}
	word := glyphWords[g]
	rest := strings.TrimPrefix(line[len(g):], " ")
	if hasWord(rest, word) {
		return rest
	}
	return strings.TrimSuffix(word+" "+rest, " ")
}

// hasWord reports whether s contains word as whole words, ignoring case.
func hasWord(s, word string) bool {
	s, word = strings.ToLower(s), strings.ToLower(word)
	for from := 0; ; {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(word)
		if (i == 0 || !isLetter(s[i-1])) && (end == len(s) || !isLetter(s[end])) {
			return true
		}
		from = i + 1
	}
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' }

// ProgressWords is a count told in words, where Progress draws a bar:
// "3 of 7 done".
func ProgressWords(done, total int) string { return fmt.Sprintf("%d of %d done", done, total) }
