package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The visual rules of SPEC §15.4. The layout code builds plain text; a
// theme paints the finished pieces. A piece gets one look and is never
// painted inside another piece, so no reset ends an outer style early.
// Nothing is told by color alone: glyphs, words and markers carry the
// meaning, color and weight only help the eye.

// look is how a piece of text is drawn.
type look int

const (
	lookPlain      look = iota
	lookAccent          // the working color: running, done, borders of dialogs
	lookAccentBold      // the app title, the focused region, what wants a click
	lookAlert           // needs the owner; skip permissions
	lookFrame           // box lines and rules
	lookTitle           // titles and what matters now
	lookDim             // finished or unavailable; secondary text
	lookFocus           // the focused element: reverse video
	lookCount
)

// SGR attributes. They are not colors, so they stay under NO_COLOR.
const (
	sgrBold    = "1"
	sgrFaint   = "2"
	sgrReverse = "7"
	sgrReset   = "\x1b[0m"
)

// shade is a color for dark and for light terminals.
type shade struct{ dark, light string }

func (s shade) pick(dark bool) string {
	if dark {
		return s.dark
	}
	return s.light
}

// The palette follows docs/brand: glow cyan is the working accent, plume
// red marks what needs the owner.
var (
	accentShade = shade{"#3CC8FF", "#0A86C8"}
	alertShade  = shade{"#E3172B", "#E3172B"}
	frameShade  = shade{"#2C5AA0", "#8FA9CC"}
)

// rankShades are the colors of the stock ranks; [tui.rank_colors] sets
// others or replaces these. A rank without a color is drawn plain.
var rankShades = map[string]shade{
	"haiku":  {"#9FB3C8", "#52657A"},
	"sonnet": {"#6EA8FF", "#1D5FD1"},
	"opus":   {"#B48CFF", "#6D3FD6"},
	"fable":  {"#FFC857", "#946200"},
}

// paintStyle is a look resolved for one terminal.
type paintStyle struct {
	color string // "" for the terminal's own
	attrs string // SGR parameters, e.g. "1;7"
}

// theme paints text. The zero value paints nothing: the text stays plain,
// which is what the layout tests compare.
type theme struct {
	on    bool
	r     *lipgloss.Renderer // turns colors into what the terminal takes
	looks [lookCount]paintStyle
	ranks map[string]string // rank -> color
}

// newTheme is the theme for a dark or light terminal that r writes to.
// rankColors are the owner's colors by rank ("#rrggbb" or an ANSI number).
// Under NO_COLOR r renders no colors; the attributes stay.
func newTheme(r *lipgloss.Renderer, dark bool, rankColors map[string]string) *theme {
	th := &theme{on: true, r: r, ranks: map[string]string{}}
	accent := accentShade.pick(dark)
	th.looks = [lookCount]paintStyle{
		lookAccent:     {color: accent},
		lookAccentBold: {color: accent, attrs: sgrBold},
		lookAlert:      {color: alertShade.pick(dark), attrs: sgrBold},
		lookFrame:      {color: frameShade.pick(dark)},
		lookTitle:      {attrs: sgrBold},
		lookDim:        {attrs: sgrFaint},
		lookFocus:      {attrs: sgrBold + ";" + sgrReverse},
	}
	for rank, s := range rankShades {
		th.ranks[rank] = s.pick(dark)
	}
	for rank, c := range rankColors {
		th.ranks[rank] = c
	}
	return th
}

// paint draws s in look l.
func (th *theme) paint(l look, s string) string {
	if !th.on || s == "" {
		return s
	}
	return th.draw(th.looks[l], s)
}

func (th *theme) draw(p paintStyle, s string) string {
	if p.color != "" {
		s = th.r.NewStyle().Foreground(lipgloss.Color(p.color)).Render(s)
	}
	if p.attrs == "" {
		return s
	}
	return "\x1b[" + p.attrs + "m" + s + sgrReset
}

// rank draws a rank's name in the rank's color; dim draws it faint
// instead, for rows that are over.
func (th *theme) rank(name string, dim bool) string {
	if !th.on {
		return name
	}
	if dim {
		return th.paint(lookDim, name)
	}
	return th.draw(paintStyle{color: th.ranks[name]}, name)
}

// focusLine draws s as a focus bar w cells wide.
func (th *theme) focusLine(s string, w int) string {
	if !th.on {
		return s
	}
	return th.paint(lookFocus, pad(s, w))
}

// marks paints the skip-permissions badge inside the otherwise unpainted
// s. The words are the badge; the red only helps (SPEC §7.3).
func (th *theme) marks(s string) string {
	if !th.on || !strings.Contains(s, skipBadge) {
		return s
	}
	return strings.ReplaceAll(s, skipBadge, th.paint(lookAlert, skipBadge))
}

// btnState is how a button is drawn (SPEC §15.4).
type btnState int

const (
	btnNormal    btnState = iota
	btnFocused            // has the focus: "[›Open session‹]" in reverse video
	btnAttention          // answers something pending
	btnInactive           // under a dialog or page: can't be used now
)

// button draws a button. The focused one is marked without color too.
func (th *theme) button(label string, st btnState) string {
	switch st {
	case btnFocused:
		return th.paint(lookFocus, "[›"+label+"‹]")
	case btnAttention:
		return th.paint(lookAccentBold, "["+label+"]")
	case btnInactive:
		return th.paint(lookDim, "["+label+"]")
	}
	return "[" + label + "]"
}
