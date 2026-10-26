package look

import (
	"testing"
	"time"
)

// A quota window has a short label for the TUI and the web UI and a name
// for the notification body (SPEC §6.9, §10, §15).
func TestWindowLabels(t *testing.T) {
	for _, tt := range []struct{ window, short, name string }{
		{"five_hour", "5h", "5h limit"},
		{"seven_day", "7d", "7-day limit"},
		{"spend_limit", "spend", "spend limit"},
		{"", "limit", "usage limit"},
		{"weird\x1b[31m", "limit", "usage limit"},
	} {
		if got := Window(tt.window); got != tt.short {
			t.Errorf("Window(%q) = %q, want %q", tt.window, got, tt.short)
		}
		if got := WindowName(tt.window); got != tt.name {
			t.Errorf("WindowName(%q) = %q, want %q", tt.window, got, tt.name)
		}
	}
}

// A reset is told as a clock time with the countdown as an estimate on the
// screens and as "in" in a notification (SPEC §15.4, §10). A reset that
// has passed shows no countdown.
func TestUntil(t *testing.T) {
	now := time.Date(2026, 10, 26, 5, 48, 0, 0, time.UTC)
	reset := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	if got := Until(reset, now); got != "09:00 (≈ 3h12m)" {
		t.Errorf("Until = %q", got)
	}
	if got := UntilIn(reset, now); got != "09:00 (in 3h12m)" {
		t.Errorf("UntilIn = %q", got)
	}
	if got := Until(reset, reset.Add(time.Minute)); got != "09:00" {
		t.Errorf("Until past = %q", got)
	}
}
