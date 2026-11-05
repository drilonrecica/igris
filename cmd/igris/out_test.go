package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/look"
)

// The text output's ground (SPEC §15.4): `[tui] theme` as set, the TUI's
// own values; auto by COLORFGBG, dark without it.
func TestGround(t *testing.T) {
	for _, tc := range []struct {
		theme, colorfgbg string
		want             look.Theme
	}{
		{"", "", look.Dark}, {"auto", "", look.Dark},
		{"", "15;0", look.Dark}, {"auto", "0;15", look.Light}, {"", "0;default;7", look.Light},
		{"dark", "0;15", look.Dark}, {"light", "15;0", look.Light},
		{"high-contrast", "", look.High}, {"high-contrast", "0;15", look.High},
	} {
		env := func(k string) string {
			if k == "COLORFGBG" {
				return tc.colorfgbg
			}
			return ""
		}
		if got := ground(env, func() string { return tc.theme }); got != tc.want {
			t.Errorf("theme %q, COLORFGBG %q: %d, want %d", tc.theme, tc.colorfgbg, got, tc.want)
		}
	}
}

func TestConfigTheme(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if got := configTheme(); got != "" {
		t.Errorf("%q without an igris.toml", got)
	}
	for src, want := range map[string]string{
		"[tui]\ntheme = \"high-contrast\"\n": "high-contrast",
		"[tui]\ntheme = \"light\"\n":         "light",
		"[tui]\ntheme = ":                    "", // unreadable
	} {
		if err := os.WriteFile(filepath.Join(dir, "igris.toml"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := configTheme(); got != want {
			t.Errorf("%q: %q, want %q", src, got, want)
		}
	}
}

// A project in high contrast draws the CLI's text in the high shades.
func TestHighContrastOutput(t *testing.T) {
	writeProject(t, map[string]string{"tasks.md": goldenPlan, "igris.toml": "[tui]\ntheme = \"high-contrast\"\n"})
	onTerminal(t, 0, false)
	code, out, errb := runCmd("check")
	if code != exitOK {
		t.Fatalf("check: %d %s", code, errb)
	}
	high, dark := sgrOf(look.Accent.Style(look.High).Color), sgrOf(look.Accent.Style(look.Dark).Color)
	if !strings.Contains(out, high) || strings.Contains(out, dark) {
		t.Errorf("check isn't drawn in the high-contrast accent %s:\n%q", high, out)
	}
}

// sgrOf is the "38;2;r;g;b" parameters of a "#RRGGBB" color.
func sgrOf(hex string) string {
	return look.Style{Color: hex}.SGR(true)
}
