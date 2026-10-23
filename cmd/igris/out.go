package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/drilonrecica/igris/internal/look"
)

// textOut draws a command's text output on its stdout through
// internal/look (SPEC §14): colors only on a terminal without NO_COLOR and
// with TERM not dumb, tables fitted only to a terminal's width.
type textOut struct {
	w     io.Writer
	color bool
	dark  bool
	width int // 0 off a terminal: nothing is cut
}

// outTerminal says whether w is a terminal and how many columns it has
// (0 when it can't tell); a seam for tests.
var outTerminal = func(w io.Writer) (bool, int) {
	f, ok := w.(*os.File)
	if !ok || !look.Terminal(f) {
		return false, 0
	}
	if n := termWidth(f); n > 0 {
		return true, n
	}
	n, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	return true, max(n, 0)
}

func newOut(w io.Writer) textOut {
	term, width := outTerminal(w)
	return textOut{w: w, color: look.ColorOn(term, os.Getenv), dark: darkGround(os.Getenv), width: width}
}

// darkGround guesses the terminal's ground from COLORFGBG ("15;0": light
// text on black), which some terminals set; without it the ground is
// dark. Asking the terminal would write to it and wait for an answer.
func darkGround(getenv func(string) string) bool {
	v := getenv("COLORFGBG")
	bg := v[strings.LastIndex(v, ";")+1:]
	return bg != "7" && bg != "15"
}

// paint draws s with sev when colors are on; s as it is otherwise, byte
// for byte (SPEC §14).
func (o textOut) paint(sev look.Severity, s string) string {
	if !o.color {
		return s
	}
	return look.Paint(true, o.dark, sev, s)
}

// painter is paint for look.Table, nil without colors.
func (o textOut) painter() look.Painter {
	if !o.color {
		return nil
	}
	return o.paint
}

// mark is a glyph drawn with sev and the plain text after it: "✓ ok".
func (o textOut) mark(sev look.Severity, glyph, text string) string {
	return look.Mark(sev, glyph, text).Draw(o.painter())
}

// printf writes a line; the format carries no "\n".
func (o textOut) printf(format string, a ...any) {
	fmt.Fprintf(o.w, format+"\n", a...)
}

// println writes s as a line.
func (o textOut) println(s string) { fmt.Fprintln(o.w, s) }

// blank writes an empty line.
func (o textOut) blank() { fmt.Fprintln(o.w) }

// table writes t fitted to the terminal.
func (o textOut) table(t *look.Table) {
	for _, l := range t.Lines(o.width, o.painter()) {
		fmt.Fprintln(o.w, l)
	}
}

// fit cuts a line of free text to the terminal's width.
func (o textOut) fit(s string) string {
	if o.width <= 0 {
		return s
	}
	return look.Cut(s, o.width)
}

// fitCell draws c cut to what is left of the terminal's width after used
// cells.
func (o textOut) fitCell(c look.Cell, used int) string {
	if o.width > 0 {
		c = look.Cell{Text: look.Cut(c.Text, o.width-used), Sev: c.Sev, Span: min(c.Span, len(look.Cut(c.Text, o.width-used)))}
	}
	return c.Draw(o.painter())
}

// next ends a text output with the commands that follow from it, bare so
// they can be copied (SPEC §14).
func (o textOut) next(cmds ...string) {
	if len(cmds) > 0 {
		fmt.Fprintf(o.w, "next: %s\n", strings.Join(cmds, "  ·  "))
	}
}

// warnf writes a warning line: "warning:" in Ember, then the text.
func (o textOut) warnf(format string, a ...any) {
	fmt.Fprintf(o.w, "%s %s\n", o.paint(look.Warn, "warning:"), fmt.Sprintf(format, a...))
}
