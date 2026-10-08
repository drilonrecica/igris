package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Progress and ETA in the run view (SPEC §15.1–§15.3).

// Bar widths of the phase progress: the wide header and the narrow status
// bar.
const (
	wideProgressBar   = 10
	narrowProgressBar = 6
)

// minETAPoints is how many finished tasks of a rank the ETA needs.
const minETAPoints = 3

// How much of the progress a header shows, from all of it to the least;
// with too little width the bar goes first, then the percentage.
const (
	progressFull = iota
	progressNoBar
	progressCount
	progressLevels
)

// progress counts the run's tasks of the current phase (the slice's tasks
// in it for a sliced run) and how many of them are done or skipped in the
// plan as last loaded.
func (m *model) progress() (done, total int) {
	for _, t := range m.phaseTasks() {
		if m.slice != nil && !m.slice[t.ID] {
			continue
		}
		total++
		if t.Status.Satisfied() {
			done++
		}
	}
	return done, total
}

// progressText is the progress at level, e.g. "7/12 · 58% ██████░░░░" or
// "slice 2/3 · 66%"; "" when the phase has none of the run's tasks.
func (m *model) progressText(level, cells int) string {
	done, total := m.progress()
	if total == 0 {
		return ""
	}
	s := fmt.Sprintf("%d/%d", done, total)
	if m.slice != nil {
		s = "slice " + s
	}
	if level < progressCount {
		s += fmt.Sprintf(" · %d%%", 100*done/total)
	}
	if level < progressNoBar {
		s += " " + roundBar(done, total, cells)
	}
	return s
}

// roundBar draws done of total in n cells, rounded: █ done, ░ to do. Only
// a finished phase fills it and only an untouched one leaves it empty.
func roundBar(done, total, n int) string {
	filled := (2*done*n + total) / (2 * total)
	if done > 0 && done < total {
		filled = min(max(filled, 1), n-1)
	}
	filled = min(max(filled, 0), n)
	return strings.Repeat("█", filled) + strings.Repeat("░", n-filled)
}

// eta is the current agent task's ETA after its elapsed time (SPEC
// §15.3): "≈ 14m left", or "over ≈ 18m typical" once it runs longer than
// the median of its rank's finished tasks; "" with fewer than
// minETAPoints of them.
func (m *model) eta(c *current) string {
	ds := m.opts.RankDurations[c.rank]
	if c.user || len(ds) < minETAPoints {
		return ""
	}
	typical := median(ds)
	elapsed := m.opts.Now().Sub(c.started)
	if c.started.IsZero() || elapsed < typical {
		return "≈ " + minutes(typical-max(elapsed, 0)) + " left"
	}
	return "over ≈ " + minutes(typical) + " typical"
}

func median(ds []time.Duration) time.Duration {
	s := slices.Clone(ds)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// minutes is d rounded to the minute, at least 1m: "14m", "1h05m".
func minutes(d time.Duration) string {
	n := max(int(d.Round(time.Minute)/time.Minute), 1)
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh%02dm", n/60, n%60)
}
