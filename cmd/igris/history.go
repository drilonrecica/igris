package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

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
	if len(h.Runs) == 0 {
		fmt.Fprintln(w, "no runs recorded yet")
		return
	}
	for i, r := range h.Runs {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "Run %s  phase %s  %s", r.StartedAt, strings.Join(r.Phases, ", "), r.End)
		if r.DurationS > 0 {
			fmt.Fprintf(w, "  (%s)", fmtSeconds(r.DurationS))
		}
		fmt.Fprintf(w, "\n  %d done, %d skipped\n", r.Done, r.Skipped)
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		for _, t := range r.Tasks {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Result, dash(t.Rank), dash(t.Model), dash(fmtSeconds(t.DurationS)), verifyText(t))
		}
		_ = tw.Flush()
		for _, c := range r.Commits {
			fmt.Fprintf(w, "  commit: %s\n", c)
		}
		for _, e := range r.Errors {
			fmt.Fprintf(w, "  error: %s\n", e)
		}
	}
}

func printTaskHistory(w io.Writer, h report.TaskHistory) {
	if len(h.Attempts) == 0 {
		fmt.Fprintf(w, "no attempts of %s recorded\n", h.Task)
		return
	}
	for i, a := range h.Attempts {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  run %s  %s", h.Task, a.RunStartedAt, a.Result)
		if a.DurationS > 0 {
			fmt.Fprintf(w, "  (%s)", fmtSeconds(a.DurationS))
		}
		fmt.Fprintln(w)
		if a.StartedAt != "" {
			fmt.Fprintf(w, "  started %s\n", a.StartedAt)
		}
		if a.Rank != "" || a.Model != "" {
			fmt.Fprintf(w, "  rank %s, model %s\n", dash(a.Rank), dash(a.Model))
		}
		if a.Note != "" {
			fmt.Fprintf(w, "  note: %s\n", a.Note)
		}
		for _, v := range a.Verify {
			fmt.Fprintf(w, "  verify %s\n", v)
		}
	}
}

func verifyText(t report.TaskRun) string {
	if t.VerifyFailed == 0 && t.VerifyPassed == 0 {
		return ""
	}
	return fmt.Sprintf("verify %d failed, %d passed", t.VerifyFailed, t.VerifyPassed)
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// fmtSeconds is "45s", "2m05s" or "1h02m"; "" for 0.
func fmtSeconds(s int) string {
	switch {
	case s <= 0:
		return ""
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
}
