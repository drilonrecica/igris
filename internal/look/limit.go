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
