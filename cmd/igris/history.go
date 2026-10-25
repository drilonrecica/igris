package main

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// historyArgs registers the flags of `igris history`.
func historyArgs(fs *flag.FlagSet) func([]string) error {
	n := fs.Int("n", report.DefaultHistoryRuns, "number of runs to show")
	fs.Bool("json", false, "machine-readable output")
	return func(args []string) error {
		if *n < 1 {
			return fmt.Errorf("invalid -n %d (want 1 or more)", *n)
		}
		if err := atMost(args, 1); err != nil {
			return err
		}
		if len(args) == 1 && !plan.ValidID(args[0]) {
			return fmt.Errorf("invalid task ID %q", args[0])
		}
		return nil
	}
}

// execHistory shows the run log (SPEC §14). It is read-only: it takes no
// lock and never creates .igris/.
func execHistory(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	n := fs.Lookup("n").Value.(flag.Getter).Get().(int)
	in := report.HistoryInput{N: n}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris history: %v\n", err)
		return exitFail
	}
	if root, err := state.FindRoot(cwd); err == nil {
		if in, err = project.HistoryInput(root, n); err != nil {
			fmt.Fprintf(stderr, "igris history: %v\n", err)
			return exitFail
		}
	}
	if len(args) == 1 {
		h := report.NewTaskHistory(in, args[0])
		if jsonFlag(fs) {
			writeJSON(stdout, h)
		} else {
			printTaskHistory(stdout, h)
		}
		return exitOK
	}
	h := report.NewHistory(in)
	if jsonFlag(fs) {
		writeJSON(stdout, h)
	} else {
		printHistory(stdout, h)
	}
	return exitOK
}

func printHistory(w io.Writer, h report.History) {
	o := newOut(w)
	if len(h.Runs) == 0 {
		o.println("no runs recorded yet")
		o.next("igris status")
		return
	}
	printNote(o, h.Note)
	for i, r := range h.Runs {
		if i > 0 {
			o.blank()
		}
		// The run ID, when the log has one, is what `igris report` takes.
		head := []string{"Run " + cmp.Or(r.Run, when(r.StartedAt))}
		if r.Run != "" {
			head = append(head, when(r.StartedAt))
		}
		if len(r.Phases) > 0 {
			head = append(head, look.Plural(len(r.Phases), "phase ", "phases ")+strings.Join(r.Phases, ", "))
		}
		glyph, sev := endMark(r.End)
		end := look.Mark(sev, glyph, r.End).Draw(o.painter())
		if r.DurationS > 0 {
			end += " · " + fmtSeconds(r.DurationS)
		}
		o.println(strings.Join(head, " · ") + " · " + end)
		o.printf("%d done · %d skipped", r.Done, r.Skipped)
		if len(r.Tasks) > 0 {
			// Agent and needs-you time only where the log records them (v1).
			timed := r.Time != nil
			header := []string{"TASK", "RESULT", "RANK", "MODEL", "DURATION", "VERIFY"}
			if timed {
				header = []string{"TASK", "RESULT", "RANK", "MODEL", "DURATION", "AGENT", "VERIFY", "NEEDS YOU"}
			}
			tb := &look.Table{Indent: "  ", Header: header}
			for _, t := range r.Tasks {
				glyph, sev := resultMark(t.Result)
				cells := []look.Cell{look.Mark(sev, glyph, t.ID), look.TextCell(t.Result), look.TextCell(t.Rank), look.TextCell(t.Model),
					look.TextCell(fmtSeconds(t.DurationS))}
				switch {
				case !timed:
					cells = append(cells, look.TextCell(verifyText(t)))
				case t.Time == nil: // a user task
					cells = append(cells, look.TextCell(""), look.TextCell(verifyText(t)), look.TextCell(""))
				default:
					cells = append(cells, look.TextCell(fmtDuration(t.Time.AgentS)), look.TextCell(verifyText(t)),
						look.TextCell(fmtDuration(t.Time.NeedsYouS)))
				}
				tb.Row(cells...)
			}
			o.table(tb)
		}
		if tm := r.Time; tm != nil {
			o.println(o.fit("  time: " + timeLine(tm.Run)))
			if len(tm.Phases) > 1 {
				for _, g := range tm.Phases {
					o.println(o.fit("  phase " + g.Phase + ": " + timeLine(g)))
				}
			}
			if len(tm.Ranks) > 1 {
				for _, g := range tm.Ranks {
					o.println(o.fit("  rank " + g.Rank + ": " + timeLine(g)))
				}
			}
		}
		for _, c := range r.Commits {
			o.println(o.fit("  commit: " + c))
		}
		for _, e := range r.Errors {
			o.println(o.fit("  " + look.Mark(look.Fail, look.GlyphFail, "error: "+e).Draw(o.painter())))
		}
	}
	o.blank()
	o.next("igris report")
}

// endMark is the glyph of how a run ended and how it is drawn.
func endMark(end string) (string, look.Severity) {
	switch end {
	case report.EndRunning:
		return look.GlyphRunning, look.Accent
	case report.EndInterrupted:
		return look.GlyphWarn, look.Warn
	}
	o := endedOutcome(end)
	return o.Glyph(), o.Severity()
}

// resultMark is the glyph of a task's result in a run: done, skipped,
// running, or unfinished when its run ended without one.
func resultMark(result string) (string, look.Severity) {
	switch result {
	case "done":
		return look.GlyphDone, look.Accent
	case "skipped":
		return look.GlyphSkipped, look.Plain
	case "running":
		return look.GlyphRunning, look.Accent
	}
	return look.GlyphWarn, look.Warn
}

func printTaskHistory(w io.Writer, h report.TaskHistory) {
	o := newOut(w)
	if len(h.Attempts) == 0 {
		o.printf("no attempts of %s recorded", h.Task)
		o.next("igris status")
		return
	}
	printNote(o, h.Note)
	for i, a := range h.Attempts {
		if i > 0 {
			o.blank()
		}
		head := []string{h.Task, "run " + cmp.Or(a.Run, when(a.RunStartedAt))}
		glyph, sev := resultMark(a.Result)
		res := look.Mark(sev, glyph, a.Result).Draw(o.painter())
		if a.DurationS > 0 {
			res += " · " + fmtSeconds(a.DurationS)
		}
		o.println(strings.Join(head, " · ") + " · " + res)
		var about []string
		if a.StartedAt != "" {
			about = append(about, "started "+when(a.StartedAt))
		}
		if a.Rank != "" {
			about = append(about, "rank "+a.Rank)
		}
		if a.Model != "" {
			about = append(about, "model "+a.Model)
		}
		if len(about) > 0 {
			o.println(o.fit("  " + strings.Join(about, " · ")))
		}
		if a.Note != "" {
			o.println(o.fit("  note: " + a.Note))
		}
		for _, v := range a.Verify {
			glyph, sev := look.GlyphDone, look.Accent
			if strings.HasPrefix(v, "failed") {
				glyph, sev = look.GlyphFail, look.Fail
			}
			o.println(o.fit("  " + look.Mark(sev, glyph, "verify "+v).Draw(o.painter())))
		}
	}
	o.blank()
	if run := h.Attempts[0].Run; run != "" {
		o.next("igris report " + run)
		return
	}
	o.next("igris report")
}

// printNote prints a note about the log (a newer igris wrote some of it)
// ahead of the runs.
func printNote(o textOut, note string) {
	if note != "" {
		o.printf("note: %s", note)
		o.blank()
	}
}

// verifyText is a task's verify results and, when the log says, the time
// verify took: "1 failed · 1 passed · 45s".
func verifyText(t report.TaskRun) string {
	if t.VerifyFailed == 0 && t.VerifyPassed == 0 {
		return ""
	}
	out := fmt.Sprintf("%d failed · %d passed", t.VerifyFailed, t.VerifyPassed)
	if t.Time != nil && t.Time.VerifyS > 0 {
		out += " · " + fmtSeconds(t.Time.VerifyS)
	}
	return out
}

// timeLine is a group's time breakdown (SPEC §14): "agent 30m00s (12m00s
// wall) · verify 1m10s · needs you 2m00s", a time counted per task
// followed by its wall time when tasks overlapped in it, the limit wait
// only when there was one.
func timeLine(g report.TimeGroup) string {
	part := func(name string, sum, wall int) string {
		out := name + " " + fmtDuration(sum)
		if wall != sum {
			out += " (" + fmtDuration(wall) + " wall)"
		}
		return out
	}
	parts := []string{
		part("agent", g.AgentS, g.Wall.AgentS),
		part("verify", g.VerifyS, g.Wall.VerifyS),
		part("needs you", g.NeedsYouS, g.Wall.NeedsYouS),
	}
	if g.LimitS > 0 {
		parts = append(parts, part("limit wait", g.LimitS, g.Wall.LimitS))
	}
	return strings.Join(parts, " · ")
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// fmtSeconds is look.Duration of s seconds: "45s", "2m05s", "1h02m"; ""
// for 0.
func fmtSeconds(s int) string {
	if s <= 0 {
		return ""
	}
	return look.Duration(time.Duration(s) * time.Second)
}
