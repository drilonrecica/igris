package look

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/plan"
)

func TestTaskGlyph(t *testing.T) {
	for _, tt := range []struct {
		s    plan.Status
		want string
	}{
		{plan.Done, "✓"}, {plan.InProgress, "●"}, {plan.Ready, "·"},
		{plan.Blocked, "⨯"}, {plan.Skipped, "–"}, {plan.StatusUnknown, "?"},
	} {
		if got := TaskGlyph(tt.s); got != tt.want {
			t.Errorf("TaskGlyph(%v) = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// Each glyph has one meaning: no two names share one.
func TestGlyphsAreDistinct(t *testing.T) {
	all := []string{GlyphDone, GlyphRunning, GlyphNeedsYou, GlyphReady, GlyphBlocked, GlyphSkipped, GlyphWarn, GlyphFail, GlyphWaiting}
	seen := map[string]bool{}
	for _, g := range all {
		if seen[g] {
			t.Errorf("glyph %q used twice", g)
		}
		seen[g] = true
	}
}

func TestStatusWords(t *testing.T) {
	for _, tt := range []struct {
		s           Status
		word, label string
		sev         Severity
	}{
		{Working, "Working", "● Working", Accent},
		{NeedsYou, "Needs you", "! Needs you", Needs},
		{Paused, "Paused", "Paused", Plain},
		{Stopping, "Stopping", "Stopping", Plain},
		{Done, "Done", "Done", Plain},
		{Idle, "Idle", "Idle", Dim},
		{Interrupted, "Interrupted", "▲ Interrupted", Warn},
	} {
		if got := tt.s.String(); got != tt.word {
			t.Errorf("%d: word %q, want %q", tt.s, got, tt.word)
		}
		if got := tt.s.Label(); got != tt.label {
			t.Errorf("%d: label %q, want %q", tt.s, got, tt.label)
		}
		if got := tt.s.Severity(); got != tt.sev {
			t.Errorf("%d: severity %d, want %d", tt.s, got, tt.sev)
		}
	}
	if got := Status(42).String(); got != "Idle" {
		t.Errorf("an unknown status reads %q", got)
	}
}

func TestOutcomes(t *testing.T) {
	for _, tt := range []struct {
		o    Outcome
		line string
		sev  Severity
	}{
		{Completed, "Done ✓ completed", Accent},
		{Stopped, "Done – stopped", Plain},
		{Stuck, "Done ▲ stuck", Warn},
		{Failed, "Done ✗ failed", Fail},
	} {
		if got := tt.o.Line(); got != tt.line {
			t.Errorf("%d: %q, want %q", tt.o, got, tt.line)
		}
		if got := tt.o.Severity(); got != tt.sev {
			t.Errorf("%d: severity %d, want %d", tt.o, got, tt.sev)
		}
	}
}

func TestSeverityStyles(t *testing.T) {
	for _, tt := range []struct {
		sev        Severity
		dark       bool
		want       Style
		mark       string
		sgr, plain string
	}{
		{Plain, true, Style{}, "", "", ""},
		{Accent, true, Style{Color: "#3CC8FF"}, "#3CC8FF", "38;2;60;200;255", ""},
		{Accent, false, Style{Color: "#0A6FA8"}, "#0A86C8", "38;2;10;111;168", ""},
		{Needs, true, Style{Color: "#FF5C74", Bold: true}, "#E3172B", "1;38;2;255;92;116", "1"},
		{Needs, false, Style{Color: "#B3122E", Bold: true}, "#E3172B", "1;38;2;179;18;46", "1"},
		{Warn, true, Style{Color: "#FFB000"}, "#FFB000", "38;2;255;176;0", ""},
		{Warn, false, Style{Color: "#9A6700", Bold: true}, "#9A6700", "1;38;2;154;103;0", "1"},
		{Fail, true, Style{Color: "#FFB000", Bold: true}, "#FFB000", "1;38;2;255;176;0", "1"},
		{Dim, false, Style{Faint: true}, "", "2", "2"},
	} {
		st := tt.sev.Style(tt.dark)
		if st != tt.want {
			t.Errorf("%d dark=%v: style %+v, want %+v", tt.sev, tt.dark, st, tt.want)
		}
		if got := tt.sev.Mark(tt.dark); got != tt.mark {
			t.Errorf("%d dark=%v: mark %q, want %q", tt.sev, tt.dark, got, tt.mark)
		}
		if got := st.SGR(true); got != tt.sgr {
			t.Errorf("%d dark=%v: SGR %q, want %q", tt.sev, tt.dark, got, tt.sgr)
		}
		if got := st.SGR(false); got != tt.plain {
			t.Errorf("%d dark=%v: SGR without color %q, want %q", tt.sev, tt.dark, got, tt.plain)
		}
	}
}

// Red means you: plume is the mark of Needs and nothing else.
func TestOnlyNeedsIsPlume(t *testing.T) {
	for sev := Plain; sev <= Dim; sev++ {
		for _, dark := range []bool{true, false} {
			isPlume := sev.Mark(dark) == Plume.Pick(dark) || sev.Style(dark).Color == PlumeText.Pick(dark)
			if isPlume != (sev == Needs) {
				t.Errorf("severity %d dark=%v: plume %v", sev, dark, isPlume)
			}
		}
	}
}

func TestPaint(t *testing.T) {
	for _, tt := range []struct {
		color bool
		sev   Severity
		in    string
		want  string
	}{
		{true, Warn, "▲ rank", "\x1b[38;2;255;176;0m▲ rank\x1b[0m"},
		{false, Warn, "▲ rank", "▲ rank"},
		{false, Fail, "✗ failed", "\x1b[1m✗ failed\x1b[0m"},
		{true, Plain, "plain", "plain"},
		{true, Needs, "", ""},
	} {
		if got := Paint(tt.color, true, tt.sev, tt.in); got != tt.want {
			t.Errorf("Paint(%v, %d, %q) = %q, want %q", tt.color, tt.sev, tt.in, got, tt.want)
		}
	}
}

var hexColor = regexp.MustCompile(`^#[0-9A-F]{6}$`)

func TestPaletteIsHex(t *testing.T) {
	shades := map[string]Shade{
		"plume": Plume, "plume text": PlumeText, "ember": Ember, "glow": Glow,
		"glow text": GlowText, "text": Text, "muted": Muted, "frame": Frame,
	}
	for _, r := range Ranks {
		shades["rank "+r.Rank] = r.Shade
	}
	for name, s := range shades {
		for _, c := range []string{s.Dark, s.Light} {
			if !hexColor.MatchString(c) {
				t.Errorf("%s: %q is not #RRGGBB", name, c)
			}
		}
	}
}

// The ranks never take the accent, Ember or plume, and Ember is the one
// warning color in both themes.
func TestRanks(t *testing.T) {
	want := map[string]Shade{
		"haiku": {"#9FB3C8", "#52657A"}, "sonnet": {"#6EA8FF", "#1D5FD1"},
		"opus": {"#B48CFF", "#6D3FD6"}, "fable": {"#FF8AD8", "#B0237A"},
	}
	for rank, s := range want {
		got, ok := RankShade(rank)
		if !ok || got != s {
			t.Errorf("%s: %+v %v, want %+v", rank, got, ok, s)
		}
	}
	if _, ok := RankShade("custom"); ok {
		t.Error("a rank without a stock color has one")
	}
	reserved := []Shade{Glow, GlowText, Ember, Plume, PlumeText}
	for _, r := range Ranks {
		for _, s := range reserved {
			if r.Dark == s.Dark || r.Light == s.Light {
				t.Errorf("rank %s uses a reserved color %+v", r.Rank, s)
			}
		}
	}
}

func TestColorOn(t *testing.T) {
	for _, tt := range []struct {
		name     string
		terminal bool
		env      map[string]string
		want     bool
	}{
		{"terminal", true, map[string]string{"TERM": "xterm-256color"}, true},
		{"no TERM", true, nil, true},
		{"not a terminal", false, map[string]string{"TERM": "xterm"}, false},
		{"NO_COLOR", true, map[string]string{"TERM": "xterm", "NO_COLOR": "1"}, false},
		{"empty NO_COLOR", true, map[string]string{"TERM": "xterm", "NO_COLOR": ""}, true},
		{"dumb", true, map[string]string{"TERM": "dumb"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			if got := ColorOn(tt.terminal, getenv); got != tt.want {
				t.Errorf("ColorOn = %v, want %v", got, tt.want)
			}
		})
	}
	if Terminal(nil) {
		t.Error("nil is a terminal")
	}
}

func TestDuration(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"}, {0, "0s"}, {time.Millisecond, "1s"},
		{45 * time.Second, "45s"}, {59*time.Second + 900*time.Millisecond, "59s"},
		{time.Minute, "1m00s"}, {4*time.Minute + 12*time.Second, "4m12s"},
		{14*time.Minute + 2*time.Second, "14m02s"},
		{time.Hour, "1h00m"}, {time.Hour + 47*time.Minute + 59*time.Second, "1h47m"},
		{23*time.Hour + 59*time.Minute, "23h59m"},
		{51 * time.Hour, "2d3h"}, {48 * time.Hour, "2d0h"},
	} {
		if got := Duration(tt.d); got != tt.want {
			t.Errorf("Duration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestMinutesAndEstimate(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{0, "1m"}, {20 * time.Second, "1m"}, {14*time.Minute + 29*time.Second, "14m"},
		{14*time.Minute + 30*time.Second, "15m"}, {65 * time.Minute, "1h05m"},
	} {
		if got := Minutes(tt.d); got != tt.want {
			t.Errorf("Minutes(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
	if got := Estimate(18 * time.Minute); got != "≈ 18m" {
		t.Errorf("Estimate = %q", got)
	}
}

func TestAge(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{-time.Minute, "0s ago"}, {45 * time.Second, "45s ago"}, {25 * time.Minute, "25m ago"},
		{3*time.Hour + 59*time.Minute, "3h ago"}, {50 * time.Hour, "2d ago"},
	} {
		if got := Age(tt.d); got != tt.want {
			t.Errorf("Age(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestTimes(t *testing.T) {
	zone := time.FixedZone("CEST", 2*3600)
	at := time.Date(2026, 10, 6, 19, 10, 7, 0, time.UTC).In(zone)
	if got := Clock(at); got != "21:10" {
		t.Errorf("Clock = %q", got)
	}
	if got := ClockSeconds(at); got != "21:10:07" {
		t.Errorf("ClockSeconds = %q", got)
	}
	for _, tt := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 10, 23, 9, 0, 0, 0, zone), "Oct 06 21:10"},
		{time.Date(2027, 1, 2, 9, 0, 0, 0, zone), "Oct 06 2026 21:10"},
		// still 2026 in UTC, but already 2027 in t's zone
		{time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC), "Oct 06 2026 21:10"},
	} {
		if got := Date(at, tt.now); got != tt.want {
			t.Errorf("Date(%v) = %q, want %q", tt.now, got, tt.want)
		}
	}
}

func TestTokens(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0", 812: "812", 999: "999", 1000: "1k", 1234: "1.2k", 9999: "9.9k",
		10_000: "10k", 41_900: "41k", 2_345_678: "2.3M", 14_000_000: "14M",
		1_250_000_000: "1.2G",
	} {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %q, want %q", n, got, want)
		}
	}
	if got := InOut(2_300_000, 41_000); got != "2.3M in · 41k out" {
		t.Errorf("InOut = %q", got)
	}
	if got := InOutNarrow(2_300_000, 41_000); got != "2.3M/41k" {
		t.Errorf("InOutNarrow = %q", got)
	}
}

func TestCount(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want string
	}{{0, "0 tasks"}, {1, "1 task"}, {2, "2 tasks"}} {
		if got := Count(tt.n, "task", "tasks"); got != tt.want {
			t.Errorf("Count(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
	if got := Count(2, "task", "tasks") + " " + Plural(2, "needs", "need") + " you"; got != "2 tasks need you" {
		t.Errorf("%q", got)
	}
}

func TestProgress(t *testing.T) {
	for _, tt := range []struct {
		done, total, n int
		want           string
	}{
		{7, 12, 10, "7/12 · 58% ██████░░░░"},
		{0, 3, 10, "0/3 · 0% ░░░░░░░░░░"},
		{3, 3, 6, "3/3 · 100% ██████"},
		{1, 100, 10, "1/100 · 1% █░░░░░░░░░"},
		{99, 100, 10, "99/100 · 99% █████████░"},
		{2, 4, 0, "2/4 · 50%"},
		{0, 0, 4, "0/0 · 0% ░░░░"},
	} {
		if got := Progress(tt.done, tt.total, tt.n); got != tt.want {
			t.Errorf("Progress(%d, %d, %d) = %q, want %q", tt.done, tt.total, tt.n, got, tt.want)
		}
		if n := strings.Count(Bar(tt.done, tt.total, tt.n), "") - 1; n != tt.n {
			t.Errorf("Bar(%d, %d, %d) has %d cells", tt.done, tt.total, tt.n, n)
		}
	}
}
