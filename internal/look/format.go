package look

import (
	"fmt"
	"strings"
	"time"
)

const day = 24 * time.Hour

// Duration is d in its two largest units, no spaces: 45s, 4m12s, 1h47m,
// 2d3h. A known duration under a second is 1s, never unknown; 0 or less
// is 0s.
func Duration(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Second:
		return "1s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	case d < day:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dd%dh", int(d/day), int(d%day/time.Hour))
}

// Minutes is d rounded to the minute, at least 1m: 14m, 1h05m. Estimates
// are told in minutes.
func Minutes(d time.Duration) string {
	n := max(int(d.Round(time.Minute)/time.Minute), 1)
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh%02dm", n/60, n%60)
}

// Estimate is an approximate duration: "≈ 14m".
func Estimate(d time.Duration) string { return "≈ " + Minutes(d) }

// Age is how long ago something was, in one unit: 45s ago, 25m ago, 3h
// ago, 2d ago.
func Age(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < day:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/day))
}

// Clock is a time of day as HH:MM, for the TUI, the web UI and `status`.
// Text surfaces show local time: t is formatted in its own location, so
// the caller passes it in the zone to show (t.Local(), or a TUI's zone).
// --json, the run log and the protocol keep RFC 3339 UTC.
func Clock(t time.Time) string { return t.Format("15:04") }

// ClockSeconds is a time of day as HH:MM:SS, for headless log lines.
func ClockSeconds(t time.Time) string { return t.Format("15:04:05") }

// Date is a date and time as "Oct 06 21:10", with the year when it isn't
// now's: "Oct 06 2025 21:10". Both are taken in their own locations.
func Date(t, now time.Time) string {
	if t.Year() != now.In(t.Location()).Year() {
		return t.Format("Jan 02 2006 15:04")
	}
	return t.Format("Jan 02 15:04")
}

// Tokens is a token count short, rounded down: 812, 1.2k, 41k, 2.3M, 14M,
// 1.2G; a decimal only under 10 units.
func Tokens(n int64) string {
	for _, u := range []struct {
		at   int64
		unit string
	}{{1_000_000_000, "G"}, {1_000_000, "M"}, {1_000, "k"}} {
		if n < u.at {
			continue
		}
		if n >= 10*u.at {
			return fmt.Sprintf("%d%s", n/u.at, u.unit)
		}
		tenths := n * 10 / u.at
		return strings.TrimSuffix(fmt.Sprintf("%d.%d", tenths/10, tenths%10), ".0") + u.unit
	}
	return fmt.Sprint(n)
}

// InOut is a session's tokens: "2.3M in · 41k out".
func InOut(in, out int64) string { return Tokens(in) + " in · " + Tokens(out) + " out" }

// InOutNarrow is InOut where there is little room: "2.3M/41k".
func InOutNarrow(in, out int64) string { return Tokens(in) + "/" + Tokens(out) }

// Plural is one when n is 1, else many: Plural(n, "needs", "need").
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Count is n and its noun: "1 task", "3 warnings". A number never appears
// without its noun.
func Count(n int, one, many string) string { return fmt.Sprintf("%d %s", n, Plural(n, one, many)) }

// Percent is done of total in percent, rounded down; 0 for no total.
func Percent(done, total int) int {
	if total <= 0 {
		return 0
	}
	return 100 * done / total
}

// Bar draws done of total in n cells, █ done and ░ to do, rounded: only
// a finished whole fills it and only an untouched one leaves it empty.
func Bar(done, total, n int) string {
	if n <= 0 {
		return ""
	}
	if total <= 0 {
		return strings.Repeat("░", n)
	}
	filled := (2*done*n + total) / (2 * total)
	if done > 0 && done < total {
		filled = min(max(filled, 1), n-1)
	}
	filled = min(max(filled, 0), n)
	return strings.Repeat("█", filled) + strings.Repeat("░", n-filled)
}

// Progress is "7/12 · 58% ██████░░░░": the count, the percentage rounded
// down and a bar of n cells; no bar for n of 0.
func Progress(done, total, n int) string {
	s := fmt.Sprintf("%d/%d · %d%%", done, total, Percent(done, total))
	if b := Bar(done, total, n); b != "" {
		s += " " + b
	}
	return s
}
