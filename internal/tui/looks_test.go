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
func truecolorTheme(t *testing.T, dark bool) *theme {
	t.Helper()
	th := colorTheme(t, dark, nil)
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

// Each look of the theme is internal/look's: the severities' colors and
// weights for a dark and a light ground, the brand's plume, the frame.
func TestEachLookIsInternalLooks(t *testing.T) {
	for _, dark := range []bool{true, false} {
		th := truecolorTheme(t, dark)
		want := map[tone]paintStyle{
			lookAccent:     {color: look.Accent.Style(dark).Color},
			lookAccentBold: {color: look.Accent.Style(dark).Color, attrs: sgrBold},
			lookAlert:      {color: look.Needs.Style(dark).Color, attrs: sgrBold},
			lookWarn:       fromStyle(look.Warn.Style(dark)),
			lookFail:       {color: look.Fail.Style(dark).Color, attrs: sgrBold},
			lookFrame:      {color: look.Frame.Pick(dark)},
			lookTitle:      {attrs: sgrBold},
			lookDim:        {attrs: sgrFaint},
			lookFocus:      {attrs: sgrBold + ";" + sgrReverse},
			lookBrand:      {color: look.Plume.Pick(dark), attrs: sgrBold},
		}
		for l := tone(0); l < lookCount; l++ {
			if l == lookPlain {
				if got := th.paint(l, "x"); got != "x" {
					t.Errorf("dark %v: plain = %q", dark, got)
				}
				continue
			}
			w, ok := want[l]
			if !ok {
				t.Errorf("dark %v: look %d has no expectation", dark, l)
				continue
			}
			if got := th.looks[l]; got != w {
				t.Errorf("dark %v: look %d = %+v, want %+v", dark, l, got, w)
			}
			p := th.paint(l, "x")
			if p == "x" || strip(p) != "x" || !strings.HasSuffix(p, sgrReset) {
				t.Errorf("dark %v: look %d paints %q", dark, l, p)
			}
			if got := colorPieces(p); w.color != "" && (len(got) != 1 || !sameColor(got[0].color, sgrColor(w.color))) {
				t.Errorf("dark %v: look %d lacks its color %s: %q", dark, l, w.color, p)
			}
		}
		// Red means you: Ember is not plume, and a failure is Ember in bold.
		if th.looks[lookWarn].color == th.looks[lookAlert].color || th.looks[lookFail].color != th.looks[lookWarn].color {
			t.Errorf("dark %v: Ember and plume: warn %q, fail %q, alert %q", dark, th.looks[lookWarn].color, th.looks[lookFail].color, th.looks[lookAlert].color)
		}
		if th.looks[lookFail].attrs != sgrBold {
			t.Errorf("dark %v: a failure isn't bold", dark)
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
	// emberOK says a piece in Ember is a warning: it carries ▲ or ✗, or is
	// a quota past 80% or a model the rank doesn't expect.
	needsPiece = regexp.MustCompile(`(?i)needs you|^!|\[SKIP PERMISSIONS\]`)
)

func emberOK(text string) bool {
	return strings.ContainsAny(text, look.GlyphWarn+look.GlyphFail) || quotaPiece.MatchString(strings.TrimSpace(text)) || strings.HasPrefix(strings.TrimSpace(text), "claude-")
}

// plumeOK says a piece in plume belongs to Needs you or to skip
// permissions: the wordmark's i, the word, the badge (cut or whole), and
// what the Needs-you state line and a user task's card say.
func plumeOK(text string) bool {
	text = strings.TrimSpace(text)
	return needsPiece.MatchString(text) || text == "i" ||
		strings.HasPrefix(text, "[SKIP PERMISSION") ||
		strings.HasPrefix(text, "· o opens the session") || strings.HasPrefix(text, "o opens the session") ||
		strings.HasPrefix(text, "Yours to do outside igris") || strings.HasPrefix(text, "Then: d done")
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
			hs.m.th = truecolorTheme(t, true)
			views[fmt.Sprintf("run %dx%d %s", size[0], size[1], st.name)] = hs.m.View()
		}
		hs := loadedRun(t, size[0], size[1])
		hs.m.th = truecolorTheme(t, true)
		views[fmt.Sprintf("run %dx%d warnings", size[0], size[1])] = hs.m.View()
		hs.m.th = truecolorTheme(t, false)
		views[fmt.Sprintf("run %dx%d warnings light", size[0], size[1])] = hs.m.View()
	}
	for _, st := range homeStates {
		views["home "+st.name] = homeAt(truecolorTheme(t, true), 120, 40, st.fix()).View()
		views["home narrow "+st.name] = homeAt(truecolorTheme(t, true), 50, 20, st.fix()).View()
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
	plume, ember := map[bool]string{}, map[bool]string{}
	for _, dark := range []bool{true, false} {
		plume[dark] = sgrColor(look.PlumeText.Pick(dark))
		ember[dark] = sgrColor(look.Ember.Pick(dark))
	}
	seenPlume, seenEmber := map[string]bool{}, map[string]bool{}
	for name, view := range severityViews(t) {
		// A piece that wraps goes on in the next line, painted the same
		// and without its glyph: it belongs to the piece above.
		okPlume, okEmber := map[int]bool{}, map[int]bool{}
		for _, p := range colorPieces(view) {
			switch {
			case sameColor(p.color, plume[true]) || sameColor(p.color, plume[false]):
				seenPlume[strings.TrimSpace(p.text)] = true
				ok := plumeOK(p.text)
				if !ok && !okPlume[p.line-1] {
					t.Errorf("%s: plume on %q, which isn't Needs you or skip permissions", name, p.text)
				}
				okPlume[p.line] = okPlume[p.line] || ok
			case sameColor(p.color, ember[true]) || sameColor(p.color, ember[false]):
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
// theme on and the profile fixed to truecolor, apart from the plain layout
// goldens. The escape character is drawn as ␛.
func TestColorGoldens(t *testing.T) {
	th := func() *theme { return truecolorTheme(t, true) }
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
	}
	for _, st := range homeStates {
		if st.name != "interrupted" && st.name != "ready" {
			continue
		}
		cases = append(cases, struct {
			name string
			w, h int
			view func(w, h int) string
		}{"home_" + st.name, 120, 40, func(w, h int) string { return homeAt(th(), w, h, st.fix()).View() }})
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
