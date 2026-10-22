// Package look is the igris design language as data (SPEC §14, §15.4,
// docs/design.md): the glyphs, the status words, the severities and their
// colors, the rank colors, and how counts, durations, times and tokens are
// written. The TUI and the CLI draw with it and the web UI's CSS tokens
// mirror it, so a word or a color is spelled one way on every surface.
//
// Nothing here prints or reads the terminal; ColorOn is the one function
// that looks at the environment, and it takes what it needs.
package look

import "github.com/drilonrecica/igris/internal/plan"

// The status glyphs (SPEC §15.4). Each has one meaning on every surface.
const (
	GlyphDone     = "✓" // done, ok, passed
	GlyphRunning  = "●" // a task with a live session; the current phase
	GlyphNeedsYou = "!" // waits on the owner; nothing else
	GlyphReady    = "·" // could run next; a step not reached yet
	GlyphBlocked  = "⨯" // a task whose dependencies aren't met; nothing else
	GlyphSkipped  = "–" // skipped; an unavailable value
	GlyphWarn     = "▲" // a warning
	GlyphFail     = "✗" // a failure
	GlyphWaiting  = "◌" // waits on other phases, background work, a send
)

// TaskGlyph is the glyph of a task status in the plan: "?" for a status
// igris doesn't know. A task that waits on the owner is GlyphNeedsYou
// whatever its status; the caller knows that, the plan doesn't.
func TaskGlyph(s plan.Status) string {
	switch s {
	case plan.Done:
		return GlyphDone
	case plan.InProgress:
		return GlyphRunning
	case plan.Ready:
		return GlyphReady
	case plan.Blocked:
		return GlyphBlocked
	case plan.Skipped:
		return GlyphSkipped
	}
	return "?"
}

// Status is what igris is doing, in one of seven words (SPEC §15.4): the
// header's word, the web UI's big word, the Run line of `igris status`
// and the first word of a notification's title.
type Status int

// The status words.
const (
	Working     Status = iota // a run is going and nothing waits on the owner
	NeedsYou                  // anything waits on the owner
	Paused                    // the pause took effect; igris waits for Resume
	Stopping                  // Stop was confirmed; igris winds down
	Done                      // the run has ended; an Outcome follows the word
	Idle                      // no run in this project
	Interrupted               // the last run ended without ending itself
)

var statusWords = [...]string{
	Working:     "Working",
	NeedsYou:    "Needs you",
	Paused:      "Paused",
	Stopping:    "Stopping",
	Done:        "Done",
	Idle:        "Idle",
	Interrupted: "Interrupted",
}

// String is the status word.
func (s Status) String() string {
	if s < 0 || int(s) >= len(statusWords) {
		return "Idle"
	}
	return statusWords[s]
}

// Glyph is the glyph drawn before the word, "" for a word that has none.
// Done takes its outcome's glyph (Outcome.Line).
func (s Status) Glyph() string {
	switch s {
	case Working:
		return GlyphRunning
	case NeedsYou:
		return GlyphNeedsYou
	case Interrupted:
		return GlyphWarn
	}
	return ""
}

// Severity is how loud the word is drawn. Done is Plain here; its outcome
// has the severity (Outcome.Severity).
func (s Status) Severity() Severity {
	switch s {
	case Working:
		return Accent
	case NeedsYou:
		return Needs
	case Idle:
		return Dim
	case Interrupted:
		return Warn
	}
	return Plain
}

// Label is the glyph and the word: "! Needs you", "● Working", "Idle".
func (s Status) Label() string {
	if g := s.Glyph(); g != "" {
		return g + " " + s.String()
	}
	return s.String()
}

// Outcome is how a run ended; it follows the word Done.
type Outcome int

// The outcomes of a run.
const (
	Completed Outcome = iota
	Stopped
	Stuck
	Failed
)

var outcomes = [...]struct{ word, glyph string }{
	Completed: {"completed", GlyphDone},
	Stopped:   {"stopped", GlyphSkipped},
	Stuck:     {"stuck", GlyphWarn},
	Failed:    {"failed", GlyphFail},
}

// String is the outcome's word: completed, stopped, stuck, failed.
func (o Outcome) String() string {
	if o < 0 || int(o) >= len(outcomes) {
		return "failed"
	}
	return outcomes[o].word
}

// Glyph is the outcome's glyph.
func (o Outcome) Glyph() string {
	if o < 0 || int(o) >= len(outcomes) {
		return GlyphFail
	}
	return outcomes[o].glyph
}

// Severity is how the outcome is drawn: a completed run in the accent, a
// stop plain, a stuck run a warning, a failed one a failure.
func (o Outcome) Severity() Severity {
	switch o {
	case Completed:
		return Accent
	case Stopped:
		return Plain
	case Stuck:
		return Warn
	}
	return Fail
}

// Line is the ended run's headline: "Done ✓ completed", "Done – stopped".
func (o Outcome) Line() string {
	return Done.String() + " " + o.Glyph() + " " + o.String()
}

// SkipBadge marks a task or a mode that runs without permission prompts
// (SPEC §7.3). With Needs you, the only thing drawn in plume.
const SkipBadge = "[SKIP PERMISSIONS]"
