package look

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Shade is a color for a dark and for a light ground, as "#RRGGBB".
type Shade struct{ Dark, Light string }

// Pick is the shade's color for a dark or a light ground.
func (s Shade) Pick(dark bool) string {
	if dark {
		return s.Dark
	}
	return s.Light
}

// The palette (docs/brand, SPEC §15.4). Plume and the light Glow are mark
// shades, for glyphs, edges and segments; words in those colors use the
// text shade of the same name, which is AA against the ground.
var (
	Plume     = Shade{"#E3172B", "#E3172B"} // needs you, the skip-permissions badge
	PlumeText = Shade{"#FF5C74", "#B3122E"}
	Ember     = Shade{"#FFB000", "#9A6700"} // warnings and failures
	Glow      = Shade{"#3CC8FF", "#0A86C8"} // the working accent
	GlowText  = Shade{"#3CC8FF", "#0A6FA8"}
	Text      = Shade{"#EAF4FF", "#05070F"} // everything that is read
	Muted     = Shade{"#9DB0D3", "#4A5B7A"} // dim text on the web; the terminal uses faint
	Frame     = Shade{"#2C5AA0", "#8FA9CC"} // rules, column lines, card borders
)

// Severity is how loud a piece of text is drawn.
type Severity int

// The severities. Red means you: Needs is drawn for Needs you and the
// skip-permissions badge only, every warning is Warn (Ember).
const (
	Plain  Severity = iota // the terminal's own color
	Accent                 // working, done, the focused region
	Needs                  // needs you
	Warn                   // a warning: Ember
	Fail                   // a failure: Ember in bold, a condition to fix
	Dim                    // done, skipped, blocked; secondary text
)

// Style is how a severity's words are drawn on one ground.
type Style struct {
	Color string // "" for the terminal's own
	Bold  bool
	Faint bool
}

// Mark is the severity's mark shade, for glyphs and edges; "" for one
// without a color of its own.
func (s Severity) Mark(dark bool) string {
	switch s {
	case Accent:
		return Glow.Pick(dark)
	case Needs:
		return Plume.Pick(dark)
	case Warn, Fail:
		return Ember.Pick(dark)
	}
	return ""
}

// Style is how the severity's words are drawn. Ember words are bold on
// the light ground, where Ember alone is just under AA.
func (s Severity) Style(dark bool) Style {
	switch s {
	case Accent:
		return Style{Color: GlowText.Pick(dark)}
	case Needs:
		return Style{Color: PlumeText.Pick(dark), Bold: true}
	case Warn:
		return Style{Color: Ember.Pick(dark), Bold: !dark}
	case Fail:
		return Style{Color: Ember.Pick(dark), Bold: true}
	case Dim:
		return Style{Faint: true}
	}
	return Style{}
}

// SGR is the style's select-graphic-rendition parameters, e.g.
// "1;38;2;255;176;0"; "" for a plain style. Without color only bold and
// faint are kept: they are not colors, so they stay under NO_COLOR.
func (st Style) SGR(color bool) string {
	var p []string
	if st.Bold {
		p = append(p, "1")
	}
	if st.Faint {
		p = append(p, "2")
	}
	if color && st.Color != "" {
		if r, g, b, ok := rgb(st.Color); ok {
			p = append(p, fmt.Sprintf("38;2;%d;%d;%d", r, g, b))
		}
	}
	return strings.Join(p, ";")
}

// Paint draws s with severity sev for a terminal with or without color,
// on a dark or light ground. Plain text and empty text are left as they
// are, so an uncolored output stays byte for byte the plain one.
func Paint(color, dark bool, sev Severity, s string) string {
	if s == "" {
		return s
	}
	p := sev.Style(dark).SGR(color)
	if p == "" {
		return s
	}
	return "\x1b[" + p + "m" + s + "\x1b[0m"
}

// rgb parses "#RRGGBB".
func rgb(hex string) (r, g, b uint8, ok bool) {
	if len(hex) != 7 || hex[0] != '#' {
		return 0, 0, 0, false
	}
	var c [3]uint8
	for i := range c {
		n, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return 0, 0, 0, false
		}
		c[i] = uint8(n)
	}
	return c[0], c[1], c[2], true
}

// RankColor is a stock rank's color: one scheme for the TUI, the web UI
// and the site, grey through blue and violet to orchid, never the accent's
// cyan, Ember or plume. [tui.rank_colors] overrides them in the TUI.
type RankColor struct {
	Rank, Name string
	Shade
}

// Ranks are the stock ranks' colors, smallest rank first.
var Ranks = []RankColor{
	{"haiku", "Steel", Shade{"#9FB3C8", "#52657A"}},
	{"sonnet", "Sky", Shade{"#6EA8FF", "#1D5FD1"}},
	{"opus", "Violet", Shade{"#B48CFF", "#6D3FD6"}},
	{"fable", "Orchid", Shade{"#FF8AD8", "#B0237A"}},
}

// RankShade is the stock color of rank; false for a rank without one,
// which is drawn plain.
func RankShade(rank string) (Shade, bool) {
	for _, r := range Ranks {
		if r.Rank == rank {
			return r.Shade, true
		}
	}
	return Shade{}, false
}

// ColorOn reports whether colors are drawn: only on a terminal, with
// NO_COLOR unset or empty and TERM not "dumb" (SPEC §14, §15.4). getenv
// is os.Getenv outside tests.
func ColorOn(terminal bool, getenv func(string) string) bool {
	return terminal && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}

// Terminal reports whether f is a terminal (a character device).
func Terminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
