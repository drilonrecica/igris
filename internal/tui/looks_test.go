package tui

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
)

// truecolorTheme is a theme with the profile fixed to 24-bit color, so a
// colored golden is the same on every machine and in CI.
func truecolorTheme(t *testing.T, kind look.Theme) *theme {
	t.Helper()
	th := colorTheme(t, kind, nil)
	th.r = lipgloss.NewRenderer(io.Discard)
	th.r.SetColorProfile(termenv.TrueColor)
	return th
}

// sgrColor is the "38;2;r;g;b" parameters of a "#RRGGBB" color.
func sgrColor(hex string) string {
	var r, g, b int
	_, _ = fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
}

// themeKinds are the TUI's theme kinds (SPEC §15.4).
var themeKinds = []look.Theme{look.Dark, look.Light, look.High}

// Each look of the theme is internal/look's: the severities' colors and
// weights for a dark, a light and a high-contrast ground, the brand's
// plume, the frame.
func TestEachLookIsInternalLooks(t *testing.T) {
	for _, kind := range themeKinds {
		th := truecolorTheme(t, kind)
		want := map[tone]paintStyle{
			lookAccent:     {color: look.Accent.Style(kind).Color},
			lookAccentBold: {color: look.Accent.Style(kind).Color, attrs: sgrBold},
			lookAlert:      {color: look.Needs.Style(kind).Color, attrs: sgrBold},
			lookWarn:       fromStyle(look.Warn.Style(kind)),
			lookFail:       {color: look.Fail.Style(kind).Color, attrs: sgrBold},
			lookFrame:      {color: look.Frame.Pick(kind)},
			lookTitle:      {attrs: sgrBold},
			lookDim:        fromStyle(look.Dim.Style(kind)),
			lookFocus:      {attrs: sgrBold + ";" + sgrReverse},
			lookBrand:      {color: look.Plume.Pick(kind), attrs: sgrBold},
		}
		for l := tone(0); l < lookCount; l++ {
			if l == lookPlain {
				if got := th.paint(l, "x"); got != "x" {
					t.Errorf("theme %d: plain = %q", kind, got)
				}
				continue
			}
			w, ok := want[l]
			if !ok {
				t.Errorf("theme %d: look %d has no expectation", kind, l)
				continue
			}
			if got := th.looks[l]; got != w {
				t.Errorf("theme %d: look %d = %+v, want %+v", kind, l, got, w)
			}
			p := th.paint(l, "x")
			if p == "x" || strip(p) != "x" || !strings.HasSuffix(p, sgrReset) {
				t.Errorf("theme %d: look %d paints %q", kind, l, p)
			}
			if got := colorPieces(p); w.color != "" && (len(got) != 1 || !sameColor(got[0].color, sgrColor(w.color))) {
				t.Errorf("theme %d: look %d lacks its color %s: %q", kind, l, w.color, p)
			}
		}
		// Red means you: Ember is not plume, and a failure is Ember in bold.
		if th.looks[lookWarn].color == th.looks[lookAlert].color || th.looks[lookFail].color != th.looks[lookWarn].color {
			t.Errorf("theme %d: Ember and plume: warn %q, fail %q, alert %q", kind, th.looks[lookWarn].color, th.looks[lookFail].color, th.looks[lookAlert].color)
		}
		if th.looks[lookFail].attrs != sgrBold {
			t.Errorf("theme %d: a failure isn't bold", kind)
		}
		// High contrast draws dim words in Muted, not faint (below 7:1),
		// and still marks the focus without color.
		if kind == look.High && th.looks[lookDim] != (paintStyle{color: look.Muted.High}) {
			t.Errorf("high contrast: dim is %+v, want Muted", th.looks[lookDim])
		}
		if th.looks[lookFocus].color != "" {
			t.Errorf("theme %d: focus is a color", kind)
		}
	}
	// Under NO_COLOR every look keeps its weight and loses its color.
	nc := noColorTheme(t)
	for l := tone(1); l < lookCount; l++ {
		if p := nc.paint(l, "x"); colored(p) {
			t.Errorf("look %d is colored under NO_COLOR: %q", l, p)
		}
	}
}

// piece is a run of text drawn in one color.
type piece struct {
	color, text string
	line        int
}

// sameColor reports whether two "38;2;r;g;b" parameter strings are the
// same color, to the rounding a terminal profile does.
func sameColor(a, b string) bool {
	var ar, ag, ab, br, bg, bb int
	if _, err := fmt.Sscanf(a, "38;2;%d;%d;%d", &ar, &ag, &ab); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(b, "38;2;%d;%d;%d", &br, &bg, &bb); err != nil {
		return false
	}
	d := func(x, y int) bool { return x-y <= 1 && y-x <= 1 }
	return d(ar, br) && d(ag, bg) && d(ab, bb)
}

var sgrRun = regexp.MustCompile(`\x1b\[([0-9;]*)m([^\x1b]*)`)

// colorPieces splits a colored view into the texts drawn in a color, with
// the color's "38;2;r;g;b" parameters.
func colorPieces(view string) []piece {
	var out []piece
	color, line := "", 0
	for _, m := range sgrRun.FindAllStringSubmatch(view, -1) {
		params := strings.Split(m[1], ";")
		for i := 0; i < len(params); i++ {
			switch {
			case params[i] == "0" || params[i] == "":
				color = ""
			case params[i] == "38" && i+4 < len(params)+0 && params[i+1] == "2":
				color = strings.Join(params[i:i+5], ";")
				i += 4
			case params[i] == "48" && i+4 < len(params) && params[i+1] == "2":
				i += 4 // a background, the helm's bottom pixel
			}
		}
		for i, part := range strings.Split(m[2], "\n") {
			if i > 0 {
				line++
			}
			if color != "" && part != "" {
				out = append(out, piece{color, part, line})
			}
		}
	}
	return out
}

var (
	quotaPiece = regexp.MustCompile(`^\d+[hd] \d+%$`)
	// budgetPiece is a budget line (SPEC §6.9), Ember from 80% like the
	// quota: `budget 110k / 100k · 110%`, `rank opus budget $33.10 / $40 · 82%`.
	budgetPiece = regexp.MustCompile(`^(?:(?:run|phase \S+|rank \S+) )?budget \S+ / \S+ · \d+%$`)
	// emberOK says a piece in Ember is a warning: it carries ▲ or ✗, or is
	// a quota or a budget past 80% or a model the rank doesn't expect.
	needsPiece = regexp.MustCompile(`(?i)needs you|^!|\[SKIP PERMISSIONS\]`)
)

func emberOK(text string) bool {
	return strings.ContainsAny(text, look.GlyphWarn+look.GlyphFail) || quotaPiece.MatchString(strings.TrimSpace(text)) ||
		budgetPiece.MatchString(strings.TrimSpace(text)) || strings.HasPrefix(strings.TrimSpace(text), "claude-")
}

// plumeOK says a piece in plume belongs to Needs you or to skip
// permissions: the wordmark's i, the word, the badge (cut or whole), and
// what the Needs-you state line and a user task's card say.
func plumeOK(text string) bool {
	text = strings.TrimSpace(text)
	return needsPiece.MatchString(text) || text == "i" || helmPlume(text) ||
		strings.HasPrefix(text, "[SKIP PERMISSION") ||
		strings.HasPrefix(text, "· o opens the session") || strings.HasPrefix(text, "o opens the session") ||
		strings.HasPrefix(text, "Yours to do outside igris") || strings.HasPrefix(text, "Then: d done")
}

// helmPlume says text is one of the helm's pixels (look.HelmL, look.HelmS),
// half blocks only: the plume is the brand mark's other named exception.
func helmPlume(text string) bool {
	return text != "" && strings.Trim(text, "▀▄") == ""
}

// severityViews are colored screens that draw every severity: the run
// screen with a needs-you task in skip-permissions mode, an overdue
// attempt, a rank mismatch and a quota to watch; the dialogs; the home
// states.
func severityViews(t *testing.T) map[string]string {
	t.Helper()
	views := map[string]string{}
	for _, size := range [][2]int{{120, 40}, {60, 24}} {
		for _, st := range styledStates() {
			hs := newHarness(t, size[0], size[1])
			hs.m.loc = time.UTC
			hs.withPlan(demoPlan)
			st.setup(hs)
			hs.m.th = truecolorTheme(t, look.Dark)
			views[fmt.Sprintf("run %dx%d %s", size[0], size[1], st.name)] = hs.m.View()
		}
		hs := loadedRun(t, size[0], size[1])
		hs.m.th = truecolorTheme(t, look.Dark)
		views[fmt.Sprintf("run %dx%d warnings", size[0], size[1])] = hs.m.View()
		hs.m.th = truecolorTheme(t, look.Light)
		views[fmt.Sprintf("run %dx%d warnings light", size[0], size[1])] = hs.m.View()
		hs.m.th = truecolorTheme(t, look.High)
		views[fmt.Sprintf("run %dx%d warnings high", size[0], size[1])] = hs.m.View()
		// Working again, the warnings are lines of their own: while the
		// task needed the owner they were on its Needs-you line.
		hs.events(engine.Event{Kind: engine.NeedsYouClear, Task: "M0-03"})
		hs.m.th = truecolorTheme(t, look.Dark)
		views[fmt.Sprintf("run %dx%d warnings working", size[0], size[1])] = hs.m.View()
	}
	for _, st := range homeStates {
		views["home "+st.name] = homeAt(truecolorTheme(t, look.Dark), 120, 40, st.fix()).View()
		views["home narrow "+st.name] = homeAt(truecolorTheme(t, look.Dark), 50, 20, st.fix()).View()
	}
	return views
}

// loadedRun is a run screen that draws every warning at once: a task that
// needs the owner in skip-permissions mode, overdue, on the wrong model,
// with the quota running low and the view reconnecting.
func loadedRun(t *testing.T, w, h int) *harness {
	t.Helper()
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"),
		engine.Event{Kind: engine.ModeChanged, Detail: engine.ModeYolo},
		engine.Event{Kind: engine.UsageRead, Task: "M0-03", Tokens: &engine.Tokens{In: 5, Out: 1, Models: []string{"claude-opus-5-5"}}},
		engine.Event{Kind: engine.RankMismatch, Task: "M0-03", Detail: "on claude-opus-5-5, not its rank sonnet"},
		engine.Event{Kind: engine.TaskOverdue, Task: "M0-03", Detail: "52m of 45m"},
		engine.Event{Kind: engine.NeedsYou, Task: "M0-03", Detail: "idle", At: t0})
	hs.now = t0.Add(4*time.Minute + 12*time.Second)
	hs.m.lost = true
	return hs
}

// Plume appears only on Needs you and skip-permissions pieces, and Ember
// only on warnings and failures (SPEC §15.4): red means you.
func TestPlumeOnlyForNeedsYouAndEmberOnlyForWarnings(t *testing.T) {
	var plume, ember []string
	for _, kind := range themeKinds {
		plume = append(plume, sgrColor(look.PlumeText.Pick(kind)))
		ember = append(ember, sgrColor(look.Ember.Pick(kind)))
	}
	in := func(c string, set []string) bool {
		for _, s := range set {
			if sameColor(c, s) {
				return true
			}
		}
		return false
	}
	seenPlume, seenEmber := map[string]bool{}, map[string]bool{}
	for name, view := range severityViews(t) {
		// A piece that wraps goes on in the next line, painted the same
		// and without its glyph: it belongs to the piece above.
		okPlume, okEmber := map[int]bool{}, map[int]bool{}
		for _, p := range colorPieces(view) {
			switch {
			case in(p.color, plume):
				seenPlume[strings.TrimSpace(p.text)] = true
				ok := plumeOK(p.text)
				if !ok && !okPlume[p.line-1] {
					t.Errorf("%s: plume on %q, which isn't Needs you or skip permissions", name, p.text)
				}
				okPlume[p.line] = okPlume[p.line] || ok
			case in(p.color, ember):
				seenEmber[strings.TrimSpace(p.text)] = true
				ok := emberOK(p.text)
				if !ok && !okEmber[p.line-1] {
					t.Errorf("%s: Ember on %q, which isn't a warning", name, p.text)
				}
				okEmber[p.line] = okEmber[p.line] || ok
			}
		}
	}
	// The scan is only worth something if it saw both.
	for _, want := range []string{"! Needs you", "[SKIP PERMISSIONS]"} {
		if !seenPlume[want] {
			t.Errorf("no plume piece %q was drawn; saw %v", want, keys(seenPlume))
		}
	}
	for _, want := range []string{"▲ Overdue", "▲ Rank mismatch", "▲ reconnecting…", "▲ Interrupted"} {
		found := false
		for s := range seenEmber {
			found = found || strings.HasPrefix(s, want)
		}
		if !found {
			t.Errorf("no Ember piece %q was drawn; saw %v", want, keys(seenEmber))
		}
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// escVisible makes a colored view readable in a golden and a diff.
func escVisible(s string) string { return strings.ReplaceAll(s, "\x1b", "␛") }

// The colored goldens (SPEC §15.4): the run screen wide and narrow, home,
// a floating dialog and the end-of-run summary, each at one size with the
// theme on (and a few again in high contrast) and the profile fixed to truecolor, apart from the plain layout
// goldens. The escape character is drawn as ␛.
func TestColorGoldens(t *testing.T) {
	th := func() *theme { return truecolorTheme(t, look.Dark) }
	cases := []struct {
		name string
		w, h int
		view func(w, h int) string
	}{
		{"wide_needs_you", 120, 40, func(w, h int) string {
			hs := loadedRun(t, w, h)
			hs.m.lost = false
			hs.m.th = th()
			return hs.m.View()
		}},
		{"wide_reconnecting", 120, 40, func(w, h int) string {
			hs := loadedRun(t, w, h)
			hs.m.th = th()
			return hs.m.View()
		}},
		{"narrow_needs_you", 60, 24, func(w, h int) string {
			hs := loadedRun(t, w, h)
			hs.m.lost = false
			hs.m.th = th()
			return hs.m.View()
		}},
		{"dialog", 120, 40, func(w, h int) string {
			hs := newHarness(t, w, h)
			hs.m.loc = time.UTC
			hs.withPlan(demoPlan)
			layoutStates[4].setup(hs)
			hs.m.th = th()
			return hs.m.View()
		}},
		{"ended", 120, 40, func(w, h int) string {
			hs := newHarness(t, w, h)
			hs.m.loc = time.UTC
			hs.withPlan(demoPlan)
			hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.TaskDone, Task: "M0-03", Detail: "ok"},
				engine.Event{Kind: engine.RunStopped, Detail: "completed"})
			hs.m.th = th()
			return hs.m.View()
		}},
		{"ended_small", 60, 24, func(w, h int) string {
			hs := newHarness(t, w, h)
			hs.m.loc = time.UTC
			hs.withPlan(demoPlan)
			hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.TaskDone, Task: "M0-03", Detail: "ok"},
				engine.Event{Kind: engine.RunStopped, Detail: "completed"})
			hs.m.th = th()
			return hs.m.View()
		}},
	}
	for _, st := range homeStates {
		if st.name != "interrupted" && st.name != "stopped_task" && st.name != "ready" && st.name != "get_started" {
			continue
		}
		cases = append(cases, struct {
			name string
			w, h int
			view func(w, h int) string
		}{"home_" + st.name, 120, 40, func(w, h int) string { return homeAt(th(), w, h, st.fix()).View() }})
	}
	// The high-contrast theme (SPEC §15.4) on the screens that draw the
	// most: every severity, the end-of-run helm, GET STARTED.
	high := func() *theme { return truecolorTheme(t, look.High) }
	cases = append(cases, []struct {
		name string
		w, h int
		view func(w, h int) string
	}{
		{"wide_needs_you_high", 120, 40, func(w, h int) string {
			hs := loadedRun(t, w, h)
			hs.m.lost = false
			hs.m.th = high()
			return hs.m.View()
		}},
		{"ended_high", 120, 40, func(w, h int) string {
			hs := newHarness(t, w, h)
			hs.m.loc = time.UTC
			hs.withPlan(demoPlan)
			hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.TaskDone, Task: "M0-03", Detail: "ok"},
				engine.Event{Kind: engine.RunStopped, Detail: "completed"})
			hs.m.th = high()
			return hs.m.View()
		}},
	}...)
	for _, st := range homeStates {
		if st.name == "get_started" || st.name == "interrupted" {
			cases = append(cases, struct {
				name string
				w, h int
				view func(w, h int) string
			}{"home_" + st.name + "_high", 120, 40, func(w, h int) string { return homeAt(high(), w, h, st.fix()).View() }})
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			view := c.view(c.w, c.h)
			if !colored(view) {
				t.Fatal("the view isn't colored")
			}
			checkFits(t, strip(view), c.w, c.h)
			golden(t, c.name+"_color", escVisible(view))
		})
	}
}

// helmColor is a foreground or background color's "38;2;r;g;b" or
// "48;2;r;g;b" parameters, without the 38 or 48.
var helmColor = regexp.MustCompile(`[34]8(;2;\d+;\d+;\d+)`)

// The helm (SPEC §15.4) is drawn pixel for pixel in internal/look's
// shades: the mark in the ground's, the S tile in the dark ones on a light
// ground (it carries its own) and in high contrast's own on black; 256 colors take the nearest; without color it
// is the plain S shape.
func TestHelmDrawing(t *testing.T) {
	for _, kind := range themeKinds {
		th := truecolorTheme(t, kind)
		for _, grid := range [][]string{look.HelmL, look.HelmS} {
			lines := th.helm(grid)
			if len(lines) != look.HelmRows(grid) {
				t.Fatalf("theme %d: %d lines, want %d", kind, len(lines), look.HelmRows(grid))
			}
			view := strings.Join(lines, "\n")
			for i, l := range lines {
				if n := textWidth(strip(l)); n != look.HelmWidth(grid) {
					t.Errorf("theme %d: line %d is %d cells, want %d", kind, i, n, look.HelmWidth(grid))
				}
			}
			ground := kind
			if kind == look.Light && len(grid) == len(look.HelmS) {
				ground = look.Dark
			}
			for _, p := range []byte(strings.Join(grid, "")) {
				s, ok := look.HelmShade(p)
				if !ok {
					continue
				}
				drawn := false
				for _, c := range helmColor.FindAllStringSubmatch(view, -1) {
					drawn = drawn || sameColor("38"+c[1], sgrColor(s.Pick(ground)))
				}
				if !drawn {
					t.Errorf("theme %d: %q isn't drawn in %s", kind, p, s.Pick(ground))
				}
			}
		}
	}
	th := truecolorTheme(t, look.Dark)
	th.r.SetColorProfile(termenv.ANSI256)
	if v := strings.Join(th.helm(look.HelmL), ""); !strings.Contains(v, "38;5;") || strings.Contains(v, "38;2;") {
		t.Errorf("256 colors aren't the nearest: %q", v)
	}
	for _, th := range []*theme{noColorTheme(t), {}} {
		if got := th.helm(look.HelmL); strings.Join(got, "\n") != strings.Join(look.HelmPlain(), "\n") {
			t.Errorf("without color the helm is %q, want the plain S", got)
		}
	}
}
