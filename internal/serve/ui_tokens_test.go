package serve

import (
	"regexp"
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
	b, err := uiFiles.ReadFile("ui/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(b)
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
