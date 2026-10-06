package tui

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/drilonrecica/igris/internal/engine"
)

var sgr = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// strip removes the SGR sequences from s.
func strip(s string) string { return sgr.ReplaceAllString(s, "") }

// colorTheme is a theme that draws colors although no terminal is there.
func colorTheme(t *testing.T, dark bool, ranks map[string]string) *theme {
	t.Helper()
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "")
	t.Setenv("CLICOLOR_FORCE", "1")
	return newTheme(lipgloss.NewRenderer(io.Discard), dark, ranks)
}

// noColorTheme is the theme of a terminal under NO_COLOR.
func noColorTheme(t *testing.T) *theme {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	return newTheme(lipgloss.NewRenderer(io.Discard), true, nil)
}

// styledStates are states that draw every painted part: the layout
// states, and the focus, dialogs and pages on top of a running task.
func styledStates() []struct {
	name  string
	setup func(hs *harness)
} {
	running := func(hs *harness) { hs.events(started("M0-03"), opened("M0-03")) }
	states := append([]struct {
		name  string
		setup func(hs *harness)
	}{}, layoutStates...)
	for _, st := range []struct {
		name string
		keys []string
	}{
		{"task selected", []string{"shift+tab", "down"}},
		{"log focused", []string{"tab"}},
		{"help", []string{"?"}},
		{"details", []string{"shift+tab", "enter"}},
		{"log page", []string{"tab", "enter"}},
		{"mode list", []string{"m"}},
		{"yolo phrase", []string{"m", "5"}},
		{"skip reason missing", []string{"s", "enter", "down", "enter"}},
		{"stop", []string{"x", "down"}},
	} {
		states = append(states, struct {
			name  string
			setup func(hs *harness)
		}{st.name, func(hs *harness) {
			running(hs)
			hs.events(engine.Event{Kind: engine.ModeChanged, Detail: engine.ModeYolo},
				engine.Event{Kind: engine.Warning, Detail: "the plan changed on disk"})
			hs.m.View() // the bar's buttons are known from the last frame
			hs.keys(st.keys...)
		}})
	}
	return states
}

func TestStylingKeepsTheLayout(t *testing.T) {
	themes := []struct {
		name string
		th   func(t *testing.T) *theme
	}{
		{"dark", func(t *testing.T) *theme { return colorTheme(t, true, nil) }},
		{"light", func(t *testing.T) *theme {
			return colorTheme(t, false, map[string]string{"opus": "#FF00FF", "user": "3"})
		}},
		{"no color", noColorTheme},
	}
	for _, size := range [][2]int{{120, 30}, {100, wideMinHeight}, {80, 24}, {50, 20}} {
		for _, st := range styledStates() {
			for _, tt := range themes {
				t.Run(fmt.Sprintf("%dx%d/%s/%s", size[0], size[1], st.name, tt.name), func(t *testing.T) {
					hs := newHarness(t, size[0], size[1])
					hs.withPlan(demoPlan)
					st.setup(hs)
					plain := hs.m.View()
					hs.m.th = tt.th(t)
					styled := hs.m.View()
					if styled == plain {
						t.Fatal("the theme painted nothing")
					}
					if got := strip(styled); got != plain {
						t.Errorf("styling changed the layout:\n--- styled, stripped\n%s\n--- plain\n%s", got, plain)
					}
					for i, l := range strings.Split(styled, "\n") {
						if all := sgr.FindAllString(l, -1); len(all) > 0 && all[len(all)-1] != sgrReset {
							t.Errorf("line %d leaves a style open: %q", i, l)
						}
					}
				})
			}
		}
	}
}

// colored reports whether an SGR sequence in s sets a color.
func colored(s string) bool {
	for _, m := range sgr.FindAllStringSubmatch(s, -1) {
		for _, p := range strings.Split(m[1], ";") {
			if p != "0" && p != sgrBold && p != sgrFaint && p != sgrReverse {
				return true
			}
		}
	}
	return false
}

func TestNoColorKeepsTheMarkers(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {50, 20}} {
		for _, st := range styledStates() {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], st.name), func(t *testing.T) {
				hs := newHarness(t, size[0], size[1])
				hs.withPlan(demoPlan)
				st.setup(hs)
				hs.m.th = noColorTheme(t)
				if v := hs.m.View(); colored(v) {
					t.Errorf("color under NO_COLOR:\n%q", v)
				}
			})
		}
	}
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.m.th = noColorTheme(t)
	if v := hs.m.View(); !strings.Contains(v, "\x1b[1;7m[›Open session‹]"+sgrReset) {
		t.Errorf("the focused button isn't in reverse video under NO_COLOR:\n%q", v)
	}
}

func TestColorsAreDrawn(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.m.th = colorTheme(t, true, nil)
	if v := hs.m.View(); !colored(v) {
		t.Errorf("no color in the view:\n%q", v)
	}
}

func TestFocusIsMarkedWithoutColor(t *testing.T) {
	const focus = "\x1b[1;7m"
	for _, size := range [][2]int{{120, 30}, {50, 20}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := newHarness(t, size[0], size[1])
			hs.withPlan(demoPlan)
			hs.events(started("M0-03"), opened("M0-03"))
			hs.m.th = colorTheme(t, true, nil)
			if v := hs.m.View(); !strings.Contains(v, focus+"[›Open session‹]"+sgrReset) {
				t.Errorf("focused button:\n%q", v)
			}
			hs.key("shift+tab")
			v := hs.m.View()
			if !strings.Contains(v, focus+"›● M0-03") || !strings.Contains(v, "› TASKS") {
				t.Errorf("selected task row:\n%q", v)
			}
			if strings.Contains(v, focus+"[›") {
				t.Errorf("the bar still shows the focus:\n%q", v)
			}
			hs.key("x")
			if v := hs.m.View(); !strings.Contains(v, focus+"› 1. Keep running") {
				t.Errorf("selected dialog option:\n%q", v)
			}
		})
	}
}

func TestButtonStates(t *testing.T) {
	th := noColorTheme(t)
	for st, want := range map[btnState]string{
		btnNormal:    "[Stop]",
		btnFocused:   "\x1b[1;7m[›Stop‹]" + sgrReset,
		btnAttention: "\x1b[1m[Stop]" + sgrReset,
		btnInactive:  "\x1b[2m[Stop]" + sgrReset,
	} {
		if got := th.button("Stop", st); got != want {
			t.Errorf("state %d: %q, want %q", st, got, want)
		}
	}
	plain := &theme{}
	if got := plain.button("Stop", btnFocused); got != "[›Stop‹]" {
		t.Errorf("plain focused button = %q", got)
	}

	// Under a dialog the wide bar is drawn but can't be used.
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.m.th = th
	hs.key("x")
	if v := hs.m.View(); !strings.Contains(v, "\x1b[2m[Open session]"+sgrReset) || strings.Contains(v, "[›") {
		t.Errorf("bar under a dialog:\n%q", v)
	}
}

func TestRankColors(t *testing.T) {
	th := colorTheme(t, true, map[string]string{"opus": "1", "wizard": "2"})
	for rank, want := range map[string]string{
		"opus":   "\x1b[31mopus" + sgrReset, // the owner's color replaces the built-in one
		"wizard": "\x1b[32mwizard" + sgrReset,
		"user":   "user",
		"—":      "—",
	} {
		if got := th.rank(rank, false); got != want {
			t.Errorf("rank %s = %q, want %q", rank, got, want)
		}
	}
	if got := th.rank("sonnet", false); !colored(got) || strip(got) != "sonnet" {
		t.Errorf("stock rank = %q", got)
	}
	if got := th.rank("opus", true); got != "\x1b[2mopus"+sgrReset {
		t.Errorf("dim rank = %q", got)
	}

	hs := newHarness(t, 120, 30)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"), opened("M0-03"))
	hs.m.th = th
	v := hs.m.View()
	if !strings.Contains(v, "\x1b[31mopus"+sgrReset) {
		t.Errorf("the opus task's rank isn't in its color:\n%q", v)
	}
	if !strings.Contains(v, "\x1b[2msonnet"+sgrReset) {
		t.Errorf("a done task's rank isn't dim:\n%q", v)
	}
}

func TestThemeForLightAndDark(t *testing.T) {
	dark, light := colorTheme(t, true, nil), colorTheme(t, false, nil)
	if dark.looks[lookAccent].color != "#3CC8FF" || light.looks[lookAccent].color != "#0A86C8" {
		t.Errorf("accent: dark %q, light %q", dark.looks[lookAccent].color, light.looks[lookAccent].color)
	}
	if dark.ranks["fable"] == light.ranks["fable"] || light.ranks["fable"] == "" {
		t.Errorf("fable: dark %q, light %q", dark.ranks["fable"], light.ranks["fable"])
	}

	detected := func() bool { return true }
	for setting, want := range map[string]bool{"dark": true, "light": false, "auto": true, "": true} {
		if got := darkTheme(setting, detected); got != want {
			t.Errorf("darkTheme(%q) = %v", setting, got)
		}
	}
	if darkTheme("auto", func() bool { return false }) {
		t.Error("auto ignores a light terminal")
	}
}

func TestSkipPermissionsBadgeIsPainted(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), opened("M0-03"), engine.Event{Kind: engine.ModeChanged, Detail: engine.ModeYolo})
	hs.m.th = colorTheme(t, true, nil)
	v := hs.m.View()
	if n := strings.Count(v, skipBadge+sgrReset); n < 2 { // the header and the log
		t.Errorf("%d painted badges, want the header's and the log's:\n%q", n, v)
	}
	if !strings.Contains(strip(v), "mode: yolo "+skipBadge) {
		t.Errorf("the badge lost its words:\n%s", strip(v))
	}
}

func TestFitStyledText(t *testing.T) {
	bold := "\x1b[1mabcdef" + sgrReset
	got := fit(bold, 4)
	if got != "\x1b[1mabc…"+sgrReset {
		t.Errorf("fit = %q", got)
	}
	if textWidth(got) != 4 {
		t.Errorf("width = %d", textWidth(got))
	}
	if got := fit(bold, 6); got != bold {
		t.Errorf("fit that fits = %q", got)
	}
	// Cut after the styled part: the reset is kept, nothing bleeds.
	if got := fit("\x1b[1mab"+sgrReset+"cdef", 4); got != "\x1b[1mab"+sgrReset+"c…"+sgrReset {
		t.Errorf("fit after a style = %q", got)
	}
}
