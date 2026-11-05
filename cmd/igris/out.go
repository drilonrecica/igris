package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/state"
)

// textOut draws a command's text output on its stdout through
// internal/look (SPEC §14): colors only on a terminal without NO_COLOR and
// with TERM not dumb, tables fitted only to a terminal's width.
type textOut struct {
	w     io.Writer
	color bool
	theme look.Theme // the ground colors are picked for
	width int        // 0 off a terminal: nothing is cut
	// words says glyphs and bars in words, one fact per line (SPEC §15.4).
	words bool
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
	o := textOut{w: w, color: look.ColorOn(term, os.Getenv), width: width, words: screenReader(os.Getenv, configScreenReader)}
	if o.color { // without colors there is no ground to pick for
		o.theme = ground(os.Getenv, configTheme)
	}
	return o
}

// screenReader says the text output speaks (SPEC §15.4): IGRIS_SCREEN_READER
// decides when it is set (0, false, no or off: no; anything else: yes),
// else igris.toml's `[tui] screen_reader`, read through fromConfig only
// then.
func screenReader(getenv func(string) string, fromConfig func() bool) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("IGRIS_SCREEN_READER"))) {
	case "":
		return fromConfig()
	case "0", "false", "no", "off":
		return false
	}
	return true
}

// configScreenReader is `[tui] screen_reader` of the igris.toml of the
// project the working directory is in; false without one, or with one that
// can't be read (doctor and check say what is wrong with it).
func configScreenReader() bool {
	tui, ok := projectTUI()
	return ok && tui.ScreenReader
}

// configTheme is `[tui] theme` of the project the working directory is in,
// as configScreenReader reads it; "" (auto) without one.
func configTheme() string {
	tui, _ := projectTUI()
	return tui.Theme
}

// projectTUI is the [tui] section of the igris.toml of the project the
// working directory is in; false without one, or with one that can't be
// read.
func projectTUI() (config.TUI, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.TUI{}, false
	}
	root, err := state.FindRoot(cwd)
	if err != nil {
		root = cwd
	}
	cfg, err := config.Load(filepath.Join(root, configFile))
	if err != nil {
		return config.TUI{}, false
	}
	return cfg.TUI, true
}

// ground is the theme the text output's colors are picked for (SPEC
// §15.4): `[tui] theme` as the TUI takes it, read through fromConfig; for
// auto the ground guessed from COLORFGBG ("15;0": light text on black),
// which some terminals set, and dark without it. Asking the terminal would
// write to it and wait for an answer.
func ground(getenv func(string) string, fromConfig func() string) look.Theme {
	return look.ThemeFor(fromConfig(), func() bool {
		v := getenv("COLORFGBG")
		bg := v[strings.LastIndex(v, ";")+1:]
		return bg != "7" && bg != "15"
	})
}

// paint draws s with sev when colors are on; s as it is otherwise, byte
// for byte (SPEC §14).
func (o textOut) paint(sev look.Severity, s string) string {
	if !o.color {
		return s
	}
	return look.Paint(true, o.theme, sev, s)
}

// painter is paint for look.Table, nil without colors.
func (o textOut) painter() look.Painter {
	if !o.color {
		return nil
	}
	return o.paint
}

// marked is a glyph drawn with sev and the plain text after it: "✓ ok",
// with the word to say for the glyph when the output speaks (look.Unsaid:
// none, the text says it).
func (o textOut) marked(sev look.Severity, glyph, word, text string) string {
	return o.draw(look.MarkAs(sev, glyph, word, text))
}

// draw is c painted, its glyph said as a word when the output speaks.
func (o textOut) draw(c look.Cell) string {
	if o.words {
		c = c.Spoken()
	}
	return c.Draw(o.painter())
}

// rankMark is the mark of a rank mismatch: "▲ rank", or "rank mismatch"
// when the output speaks.
func (o textOut) rankMark() string {
	if o.words {
		return "rank mismatch"
	}
	return rankMark
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
	t.Spoken = o.words
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
	if o.words {
		c = c.Spoken()
	}
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
