package main

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/price"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
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

// validRunArg says s is a run index (any positive integer: one past the
// end is "only N runs recorded", not a usage error) or a run ID.
func validRunArg(s string) bool {
	if state.ValidRunID(s) {
		return true
	}
	return s != "" && s[0] != '0' && strings.Trim(s, "0123456789") == ""
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
		o := newOut(stdout)
		printReport(o, r)
		// The markdown is pasted into PRs and journals: next only on a
		// terminal, so a piped report stays the report.
		if term, _ := outTerminal(stdout); term {
			o.blank()
			o.next("igris usage " + cmp.Or(r.Run, "1"))
		}
	}
	return exitOK
}

// rankMark is the result cell's mark of a task that ran on a model its rank
// doesn't resolve to (SPEC §6.9, §14): the one wording of a rank mismatch
// is "rank mismatch", and "▲ rank" where there is little room.
const rankMark = look.GlyphWarn + " rank"

// printReport writes the run as markdown, for a PR description or a
// journal. Every value the log doesn't record is "—". On a terminal with
// colors the rank mark is drawn in Ember.
func printReport(o textOut, r report.Report) {
	w := o.w
	fmt.Fprintf(w, "# igris report · %s\n\n", md(r.Project))
	if r.Note != "" {
		fmt.Fprintf(w, "note: %s\n\n", r.Note)
	}
	fmt.Fprintf(w, "Run %s%s", r.Run+sep(r.Run, " · "), runSpan(r.StartedAt, r.EndedAt))
	if r.DurationS > 0 {
		fmt.Fprintf(w, " (%s)", fmtSeconds(r.DurationS))
	}
	fmt.Fprintf(w, " · %s\n", md(r.End))
	var scope []string
	if len(r.Phases) > 0 {
		scope = append(scope, look.Plural(len(r.Phases), "Phase ", "Phases ")+md(strings.Join(r.Phases, ", ")))
	}
	if r.Selection != nil {
		scope = append(scope, md(r.Selection.String()))
	}
	if len(scope) > 0 {
		fmt.Fprintln(w, strings.Join(scope, " · "))
	}
	counts := fmt.Sprintf("%d done · %d skipped · %d unfinished · %d %s",
		r.Done, r.Skipped, r.Unfinished, len(r.Commits), look.Plural(len(r.Commits), "commit", "commits"))
	if r.NeedsYouS != nil {
		counts += " · needs you " + fmtDuration(*r.NeedsYouS)
	}
	if r.Estimate != nil {
		counts += " · " + look.Dollars(r.Estimate.USD)
	}
	fmt.Fprintln(w, counts)
	if r.Estimate != nil {
		fmt.Fprintln(w, price.Legend())
	}

	for _, g := range r.Groups() {
		if g.Phase == "" {
			fmt.Fprint(w, "\n## Tasks\n\n")
		} else {
			fmt.Fprintf(w, "\n## Phase %s\n\n", md(g.Phase))
		}
		fmt.Fprintln(w, "| Task | Result | Duration | Agent | Attempts | Tokens | Verify | Commit | Needs you |")
		fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|")
		for _, t := range g.Tasks {
			attempts := ""
			if t.Attempts > 0 {
				attempts = fmt.Sprint(t.Attempts)
			}
			needs := ""
			if t.NeedsYouS != nil {
				needs = fmtSeconds(*t.NeedsYouS)
			}
			result := t.Result
			if t.RankMismatch() {
				result += " " + o.paint(look.Warn, rankMark)
			}
			agent, verify := "", cell(verifyMarks(t.Verify))
			if t.Time != nil {
				agent = fmtDuration(t.Time.AgentS)
				if verify != "" && t.Time.VerifyS > 0 {
					verify += " · " + fmtSeconds(t.Time.VerifyS)
				}
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
				cell(strings.TrimSpace(t.ID+" "+t.Title)), result, dash(fmtSeconds(t.DurationS)), dash(agent), dash(attempts), tokensCell(t.Usage, t.Estimate),
				dash(verify), dash(shortSHA(t.Commit)), dash(needs))
		}
		var notes, resume []string
		for _, t := range g.Tasks {
			if t.Note != "" {
				notes = append(notes, fmt.Sprintf("- %s %s: %s", md(t.ID), t.Result, md(t.Note)))
			}
			if t.Resume != "" {
				// Resume is igris's own text with a checked UUID.
				resume = append(resume, fmt.Sprintf("- %s: `%s` (run it in the project root)", md(t.ID), t.Resume))
			}
		}
		printList(w, "Notes", notes)
		printList(w, "Resume", resume)
	}
	printTimes(w, r.Time)
	var budgets []string
	for _, b := range r.Budgets {
		budgets = append(budgets, "- "+md(b.Line()))
	}
	printList(w, "Budgets", budgets)
	var errs []string
	for _, e := range r.Errors {
		errs = append(errs, "- "+md(e))
	}
	printList(w, "Errors", errs)
}

// printTimes writes the run's time breakdown (SPEC §14): a row for the
// run, and one per phase and per rank when there are several. A time
// counted per task is followed by its wall time when tasks overlapped in
// it (a parallel phase); Limit wait is a column only when there was one.
func printTimes(w io.Writer, tm *report.TimeBreakdown) {
	if tm == nil {
		return
	}
	limit := tm.Run.LimitS > 0
	cellOf := func(sum, wall int) string {
		out := fmtDuration(sum)
		if wall != sum {
			out += " (" + fmtDuration(wall) + " wall)"
		}
		return out
	}
	row := func(name string, g report.TimeGroup) {
		cells := []string{name, cellOf(g.AgentS, g.Wall.AgentS), cellOf(g.VerifyS, g.Wall.VerifyS), cellOf(g.NeedsYouS, g.Wall.NeedsYouS)}
		if limit {
			cells = append(cells, cellOf(g.LimitS, g.Wall.LimitS))
		}
		cells = append(cells, fmtDuration(g.WallS))
		fmt.Fprintf(w, "| %s |\n", strings.Join(cells, " | "))
	}
	head := []string{"Time", "Agent", "Verify", "Needs you"}
	if limit {
		head = append(head, "Limit wait")
	}
	head = append(head, "Wall")
	fmt.Fprintf(w, "\n## Time\n\n| %s |\n|%s\n", strings.Join(head, " | "), strings.Repeat("---|", len(head)))
	row("Run", tm.Run)
	if len(tm.Phases) > 1 {
		for _, g := range tm.Phases {
			row("Phase "+md(g.Phase), g)
		}
	}
	if len(tm.Ranks) > 1 {
		for _, g := range tm.Ranks {
			row("Rank "+md(g.Rank), g)
		}
	}
}

// runSpan is a run's start and end, local (SPEC §14): "Oct 08 09:15 →
// 11:02", the end's date only when it is another day's; "—" for what the
// log doesn't say.
func runSpan(started, ended string) string {
	start, err := time.Parse(time.RFC3339, started)
	if err != nil {
		return dash(started) + " → " + dash(ended)
	}
	start = start.Local()
	out := look.Date(start, textNow()) + " → "
	end, err := time.Parse(time.RFC3339, ended)
	if err != nil {
		return out + dash(ended)
	}
	end = end.Local()
	if end.YearDay() == start.YearDay() && end.Year() == start.Year() {
		return out + look.Clock(end)
	}
	return out + look.Date(end, textNow())
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

// mdEscaper escapes the characters that make markdown out of text (links,
// images, emphasis, code, headings, HTML, table cells).
var mdEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`,
	"(", `\(`, ")", `\)`, "!", `\!`, "<", `\<`, ">", `\>`, "#", `\#`, "|", `\|`,
)

// md escapes text from the log (titles, notes, IDs, errors) for the
// markdown report, so it reads as written and can't make links or
// markup; JSON output keeps it as cleaned.
func md(s string) string { return mdEscaper.Replace(s) }

// cell escapes s for a markdown table cell.
func cell(s string) string { return md(s) }

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

// fmtDuration is fmtSeconds, with "0s" for 0.
func fmtDuration(s int) string {
	if s <= 0 {
		return "0s"
	}
	return fmtSeconds(s)
}

// tokensCell is a task's tokens as "2.3M in · 41k out" (in counts cache
// reads and writes, subagents included), "—" when no usage was read.
func tokensCell(u *report.Tokens, e *report.Estimate) string {
	if u == nil {
		return "—"
	}
	cell := usage.Short(u.In) + " in · " + usage.Short(u.Output) + " out"
	if e != nil {
		cell += " · " + look.Dollars(e.USD)
	}
	return cell
}
