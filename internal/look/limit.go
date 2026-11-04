package look

import "time"

// The usage limit's words (SPEC §6.9, §10, §15; decisions.md V18-P1): a
// quota window's short label for a card or a header and its name for a
// notification body, and a reset told as a clock time with its countdown.

// Window is a quota window's short label: 5h, 7d, spend; "limit" for one
// igris doesn't know (the window comes from a file a session can write).
func Window(window string) string {
	switch window {
	case "five_hour":
		return "5h"
	case "seven_day":
		return "7d"
	case "spend_limit":
		return "spend"
	}
	return "limit"
}

// WindowName is a quota window's name in a sentence: "5h limit", "7-day
// limit", "spend limit"; "usage limit" for one igris doesn't know.
func WindowName(window string) string {
	switch window {
	case "five_hour":
		return "5h limit"
	case "seven_day":
		return "7-day limit"
	case "spend_limit":
		return "spend limit"
	}
	return "usage limit"
}

// ResumeGrace is how long after a window's reset igris resumes held starts:
// Claude Code's own continuation came 56–105 s after the minute (decisions.md
// V18-P1). The engine waits it out and every screen counts down to it.
const ResumeGrace = 60 * time.Second

// ResumeAt is when igris resumes a hold on a window that resets at reset: the
// time every screen shows, so the line never reads a past time without a
// countdown. Run log, snapshot and --json keep the reset itself.
func ResumeAt(reset time.Time) time.Time { return reset.Add(ResumeGrace) }

// Until is a reset as the screens show it: the clock time and, while it
// is ahead, the countdown as an estimate, "09:00 (≈ 3h12m)". Both times
// are taken in reset's location, so the caller passes the zone to show.
func Until(reset, now time.Time) string {
	return until(reset, now, "≈ ")
}

// UntilIn is Until for a notification body: "09:00 (in 3h12m)".
func UntilIn(reset, now time.Time) string {
	return until(reset, now, "in ")
}

func until(reset, now time.Time, lead string) string {
	s := Clock(reset)
	if d := reset.Sub(now); d > 0 {
		s += " (" + lead + Minutes(d) + ")"
	}
	return s
}
