package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// reportArgs registers the flags of `igris report` and checks RUN: a
// positive integer or a run ID (SPEC §14).
func reportArgs(fs *flag.FlagSet) func([]string) error {
	fs.Bool("json", false, "machine-readable output")
	return func(args []string) error {
		if err := atMost(args, 1); err != nil {
			return err
		}
		if len(args) == 1 && !validRunArg(args[0]) {
			return fmt.Errorf("invalid run %q (want 1 for the newest run, 2, … or a run ID from igris history)", args[0])
		}
		return nil
	}
}

// validRunArg says s is a run index (1, 2, …) or a run ID.
func validRunArg(s string) bool {
	if state.ValidRunID(s) {
		return true
	}
	if s == "" || s[0] == '0' || len(s) > 9 {
		return false
	}
	return strings.Trim(s, "0123456789") == ""
}

// execReport prints one run from the run log (SPEC §14). It is read-only:
// it takes no lock and never creates .igris/.
func execReport(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris report: %v\n", err)
		return exitFail
	}
	var in report.HistoryInput
	name := filepath.Base(cwd)
	if root, err := state.FindRoot(cwd); err == nil {
		name = filepath.Base(root)
		if in, err = project.HistoryInput(root, 0); err != nil {
			fmt.Fprintf(stderr, "igris report: %v\n", err)
			return exitFail
		}
	}
	sel := ""
	if len(args) == 1 {
		sel = args[0]
	}
	r, err := report.NewReport(in, name, sel)
	if err != nil {
		fmt.Fprintf(stderr, "igris report: %v\n", err)
		return exitFail
	}
	if jsonFlag(fs) {
		writeJSON(stdout, r)
	} else {
		printReport(stdout, r)
	}
	return exitOK
}

// printReport writes the run as markdown, for a PR description or a
// journal. Every value the log doesn't record is "—".
func printReport(w io.Writer, r report.Report) {
	fmt.Fprintf(w, "# igris report · %s\n\n", r.Project)
	if r.Note != "" {
		fmt.Fprintf(w, "note: %s\n\n", r.Note)
	}
	fmt.Fprintf(w, "Run %s%s → %s", r.Run+sep(r.Run, " · "), r.StartedAt, dash(r.EndedAt))
	if r.DurationS > 0 {
		fmt.Fprintf(w, " (%s)", fmtSeconds(r.DurationS))
	}
	fmt.Fprintf(w, " · %s\n", r.End)
	var scope []string
	if len(r.Phases) > 0 {
		scope = append(scope, plural(len(r.Phases), "Phase ", "Phases ")+strings.Join(r.Phases, ", "))
	}
	if r.Selection != nil {
		scope = append(scope, r.Selection.String())
	}
	if len(scope) > 0 {
		fmt.Fprintln(w, strings.Join(scope, " · "))
	}
	counts := fmt.Sprintf("%d done · %d skipped · %d unfinished · %d %s",
		r.Done, r.Skipped, r.Unfinished, len(r.Commits), plural(len(r.Commits), "commit", "commits"))
	if r.NeedsYouS != nil {
		counts += " · needs you " + fmtDuration(*r.NeedsYouS)
	}
	fmt.Fprintln(w, counts)

	for _, g := range r.Groups() {
		if g.Phase == "" {
			fmt.Fprint(w, "\n## Tasks\n\n")
		} else {
			fmt.Fprintf(w, "\n## Phase %s\n\n", g.Phase)
		}
		fmt.Fprintln(w, "| Task | Result | Duration | Attempts | Verify | Commit | Needs you |")
		fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
		for _, t := range g.Tasks {
			attempts := ""
			if t.Attempts > 0 {
				attempts = fmt.Sprint(t.Attempts)
			}
			needs := ""
			if t.NeedsYouS != nil {
				needs = fmtSeconds(*t.NeedsYouS)
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s |\n",
				cell(strings.TrimSpace(t.ID+" "+t.Title)), t.Result, dash(fmtSeconds(t.DurationS)), dash(attempts),
				dash(cell(verifyMarks(t.Verify))), dash(shortSHA(t.Commit)), dash(needs))
		}
		var notes, resume []string
		for _, t := range g.Tasks {
			if t.Note != "" {
				notes = append(notes, fmt.Sprintf("- %s %s: %s", t.ID, t.Result, t.Note))
			}
			if t.Resume != "" {
				resume = append(resume, fmt.Sprintf("- %s: `%s` (run it in the project root)", t.ID, t.Resume))
			}
		}
		printList(w, "Notes", notes)
		printList(w, "Resume", resume)
	}
	var errs []string
	for _, e := range r.Errors {
		errs = append(errs, "- "+e)
	}
	printList(w, "Errors", errs)
}

func printList(w io.Writer, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n%s\n", title, strings.Join(lines, "\n"))
}

// verifyMarks is the verify results as "fast ✗ ✓": the profile, then one ✗
// per failure and ✓ per pass in order; a profile change names the new one.
func verifyMarks(vs []report.ReportVerify) string {
	var parts []string
	prev := ""
	for i, v := range vs {
		if v.Profile != "" && (i == 0 || v.Profile != prev) {
			parts = append(parts, v.Profile)
		}
		prev = v.Profile
		if v.Passed {
			parts = append(parts, "✓")
		} else {
			parts = append(parts, "✗")
		}
	}
	return strings.Join(parts, " ")
}

// cell escapes s for a markdown table cell.
func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// sep is s+suffix's suffix when s is set, "" otherwise.
func sep(s, suffix string) string {
	if s == "" {
		return ""
	}
	return suffix
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// fmtDuration is fmtSeconds, with "0s" for 0.
func fmtDuration(s int) string {
	if s <= 0 {
		return "0s"
	}
	return fmtSeconds(s)
}
