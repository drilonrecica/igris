package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
)

// doctorArgs registers the flags of `igris doctor`.
func doctorArgs(fs *flag.FlagSet) func([]string) error {
	fs.Bool("json", false, "machine-readable output: a JSON object with the checks")
	return maxArgs(fs, 0)
}

// levelMarks are each level's glyph and how it is drawn in doctor's text
// output (SPEC §14).
var levelMarks = map[checks.Level]struct {
	glyph string
	sev   look.Severity
}{checks.OK: {look.GlyphDone, look.Accent}, checks.Warn: {look.GlyphWarn, look.Warn}, checks.Fail: {look.GlyphFail, look.Fail}}

// execDoctor runs the health check (SPEC §14). It is read-only: it creates
// and changes nothing, and exits 1 only if some check failed.
func execDoctor(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris doctor: %v\n", err)
		return exitFail
	}
	rs := project.Doctor(context.Background(), cwd, projectEnv())
	failed := false
	for _, r := range rs {
		failed = failed || r.Level == checks.Fail
	}
	if jsonFlag(fs) {
		writeJSON(stdout, report.NewDoctor(rs))
	} else {
		printDoctor(newOut(stdout), rs)
	}
	if failed {
		return exitFail
	}
	return exitOK
}

// printDoctor writes one line per check, a problem's next command under
// it, then the summary line and next: the first failure's command, else
// the first warning's.
func printDoctor(o textOut, rs []checks.Result) {
	// next is the first failure's command, else the first warning's.
	warns, fails, next, warnNext := 0, 0, "", ""
	for _, r := range rs {
		msg := r.Message
		if r.Line != 0 { // a located plan or config problem: file:line first
			msg = r.String()
		}
		m := levelMarks[r.Level]
		level := fmt.Sprintf("%-4s", r.Level)
		o.println(look.Cell{Text: m.glyph + " " + level + " " + msg, Sev: m.sev, Span: len(m.glyph) + 1 + len(level)}.Draw(o.painter()))
		if r.Level == checks.OK {
			continue
		}
		if r.Level == checks.Warn {
			warns++
		} else {
			fails++
		}
		if r.Next != "" {
			o.printf("       next: %s", r.Next)
			if r.Level == checks.Fail && next == "" {
				next = r.Next
			}
			if warnNext == "" {
				warnNext = r.Next
			}
		}
	}
	o.blank()
	switch {
	case fails > 0:
		summary := look.Count(fails, "failure", "failures")
		if warns > 0 {
			summary += " · " + look.Count(warns, "warning", "warnings")
		}
		o.println(o.mark(look.Fail, look.GlyphFail, "doctor: "+summary))
	case warns > 0:
		o.println(o.mark(look.Warn, look.GlyphWarn, "doctor: "+look.Count(warns, "warning", "warnings")))
	default:
		o.println(o.mark(look.Accent, look.GlyphDone, "doctor: ok"))
	}
	o.next(cmp.Or(next, warnNext, "igris status"))
}
