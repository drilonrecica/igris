package serve

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/look"
)

var cssToken = regexp.MustCompile(`(--[a-z-]+):\s*([^;]+);`)

// cssBlock is the custom properties of the first block that opens with
// head.
func cssBlock(t *testing.T, css, head string) map[string]string {
	t.Helper()
	i := strings.Index(css, head)
	if i < 0 {
		t.Fatalf("app.css has no %q", head)
	}
	body := css[i+len(head):]
	body = body[:strings.Index(body, "}")]
	out := map[string]string{}
	for _, m := range cssToken.FindAllStringSubmatch(body, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// The web UI's color tokens are internal/look's palette (docs/design.md,
// rule 1): one vocabulary on three surfaces, from one source.
func TestCSSTokensMatchLook(t *testing.T) {
	css := appCSS(t)
	themes := []struct {
		name   string
		dark   bool
		tokens map[string]string
	}{
		{"dark", true, cssBlock(t, css, ":root {")},
		{"light", false, cssBlock(t, css, "@media (prefers-color-scheme: light) {\n  :root {")},
	}
	want := map[string]look.Shade{
		"--text":        look.Text,
		"--muted":       look.Muted,
		"--accent":      look.Glow,
		"--accent-text": look.GlowText,
		"--plume":       look.Plume,
		"--needs-text":  look.PlumeText,
		"--ember":       look.Ember,
		// the helm's shades
		"--helm-face":         look.HelmFace,
		"--helm-face-shadow":  look.HelmFaceShadow,
		"--helm-ridge":        look.HelmRidge,
		"--helm-visor-core":   look.HelmVisorCore,
		"--helm-chin":         look.HelmChin,
		"--helm-plume-stroke": look.HelmPlumeStroke,
	}
	for _, r := range look.Ranks {
		want["--rank-"+r.Rank] = r.Shade
	}
	for _, th := range themes {
		for token, shade := range want {
			got, ok := th.tokens[token]
			if !ok {
				t.Errorf("%s: app.css has no %s", th.name, token)
				continue
			}
			if w := shade.Pick(th.dark); !strings.EqualFold(got, w) {
				t.Errorf("%s: %s is %s, internal/look has %s", th.name, token, got, w)
			}
		}
	}
}

// appCSS is the stylesheet.
func appCSS(t *testing.T) string {
	t.Helper()
	b, err := uiFiles.ReadFile("ui/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var cssRule = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)

// cssRules are the stylesheet's rules, selector → declarations (the rules
// of an @media block included, each selector its last rule's).
func cssRules(css string) map[string]string {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	out := map[string]string{}
	for _, m := range cssRule.FindAllStringSubmatch(css, -1) {
		sel := strings.TrimSpace(m[1])
		if i := strings.LastIndex(sel, "\n"); i >= 0 && strings.HasPrefix(strings.TrimSpace(sel[:i]), "@") {
			sel = strings.TrimSpace(sel[i:])
		}
		out[sel] = m[2]
	}
	return out
}

// Red means you (docs/design.md, rule 2): plume and its text shade are
// drawn for what needs the owner and the wordmark's i, nothing else.
func TestCSSRedIsNeedsYouOnly(t *testing.T) {
	needs := []string{".wordmark .i", "needs", ".question", "to-sheet", ".card.asks", ".peek"}
	for sel, decl := range cssRules(appCSS(t)) {
		if !strings.Contains(decl, "var(--plume)") && !strings.Contains(decl, "var(--needs-text)") && !strings.Contains(decl, "var(--needs-glow)") {
			continue
		}
		ok := false
		for _, n := range needs {
			ok = ok || strings.Contains(sel, n)
		}
		if !ok {
			t.Errorf("%s is drawn red, and it isn't Needs you", sel)
		}
	}
}

// One radius scale (SPEC §15.8): 8 px fields, 12 px buttons, 14 px cards,
// 22 px the sheet's top corners; a circle is a circle.
func TestCSSRadii(t *testing.T) {
	scale := map[string]string{"--r-field": "8px", "--r-button": "12px", "--r-card": "14px", "--r-sheet": "22px"}
	root := cssBlock(t, appCSS(t), ":root {")
	for k, v := range scale {
		if root[k] != v {
			t.Errorf("%s is %q, want %s", k, root[k], v)
		}
	}
	radius := regexp.MustCompile(`border(?:-[a-z]+)*-radius:\s*([^;]+);`)
	for _, m := range radius.FindAllStringSubmatch(appCSS(t), -1) {
		for _, v := range strings.Fields(m[1]) {
			if v != "0" && v != "50%" && !regexp.MustCompile(`^var\(--r-(field|button|card|sheet)\)$`).MatchString(v) {
				t.Errorf("a radius of %s is off the scale: %s", v, m[0])
			}
		}
	}
}

// Text is AA in both themes (SPEC §15.8): every text token measures 4.5:1
// on the ground and on a card, the primary button's text on the accent
// too. Ember on the light ground is drawn bold, where 4.3 is enough.
func TestCSSContrastAA(t *testing.T) {
	css := appCSS(t)
	themes := map[string]map[string]string{
		"dark":  cssBlock(t, css, ":root {"),
		"light": cssBlock(t, css, "@media (prefers-color-scheme: light) {\n  :root {"),
	}
	text := []string{"--text", "--muted", "--accent-text", "--needs-text", "--ember", "--rank-haiku", "--rank-sonnet", "--rank-opus", "--rank-fable"}
	for name, th := range themes {
		for _, fg := range text {
			for _, bg := range []string{"--ground", "--card"} {
				want := 4.5
				if fg == "--ember" && th["--ember-weight"] == "700" {
					want = 4.3
				}
				if c := contrast(t, th[fg], th[bg]); c < want {
					t.Errorf("%s: %s on %s is %.2f:1, want %.1f", name, fg, bg, c, want)
				}
			}
		}
		if c := contrast(t, th["--on-accent"], th["--accent"]); c < 4.5 {
			t.Errorf("%s: the primary button's text is %.2f:1", name, c)
		}
	}
	// A colour used for words is a text token: the mark shades are not.
	for sel, decl := range cssRules(css) {
		if regexp.MustCompile(`(^|[^-])color: var\(--(accent|plume)\)`).MatchString(decl) && !strings.Contains(sel, "glyph") && !strings.Contains(sel, ".wordmark .i") {
			t.Errorf("%s colours text with a mark shade: %s", sel, decl)
		}
	}
	if strings.Contains(css, "opacity: .7") {
		t.Error("app.css fades text with opacity, below AA")
	}
}

// contrast is the WCAG contrast ratio of two "#RRGGBB" colours.
func contrast(t *testing.T, a, b string) float64 {
	t.Helper()
	lum := func(hex string) float64 {
		if len(hex) != 7 || hex[0] != '#' {
			t.Fatalf("not a colour: %q", hex)
		}
		var c [3]float64
		for i := range c {
			n, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
			if err != nil {
				t.Fatal(err)
			}
			v := float64(n) / 255
			if v <= 0.03928 {
				c[i] = v / 12.92
			} else {
				c[i] = math.Pow((v+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
	}
	la, lb := lum(a), lum(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}
