package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/usage"
)

// usageNow is the clock `igris usage --since` measures a duration from.
var usageNow = time.Now

// usageArgs registers the flags of `igris usage` and checks RUN and
// --since: RUN as for report, --since a duration or a date, not both
// (SPEC §14).
func usageArgs(fs *flag.FlagSet) func([]string) error {
	since := fs.String("since", "", "cover every run started since D: a duration (7d, 36h) or a date (2026-10-01)")
	fs.Bool("json", false, "machine-readable output")
	return func(args []string) error {
		if err := atMost(args, 1); err != nil {
			return err
		}
		if len(args) == 1 && !validRunArg(args[0]) {
			return fmt.Errorf("invalid run %q (want 1 for the newest run, 2, … or a run ID from igris history)", args[0])
		}
		if *since == "" {
			if flagSet(fs, "since") {
				return fmt.Errorf("invalid --since %q (want a duration such as 7d or 36h, or a date such as 2026-10-01)", *since)
			}
			return nil
		}
		if len(args) == 1 {
			return fmt.Errorf("give either a RUN or --since, not both")
		}
		_, err := parseSince(*since, usageNow())
		return err
	}
}

// flagSet says the flag was given on the command line.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// parseSince turns D into the time it names relative to now: a positive
// duration (`d` is 24 h) or a date, local midnight (SPEC §14).
func parseSince(s string, now time.Time) (time.Time, error) {
	bad := fmt.Errorf("invalid --since %q (want a duration such as 7d or 36h, or a date such as 2026-10-01)", s)
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	var d time.Duration
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseInt(n, 10, 64)
		if err != nil || days < 1 || days > 36500 {
			return time.Time{}, bad
		}
		d = time.Duration(days) * 24 * time.Hour
	} else if p, err := time.ParseDuration(s); err == nil && p >= time.Hour/60 && p <= 36500*24*time.Hour {
		d = p
	} else {
		return time.Time{}, bad
	}
	return now.Add(-d), nil
}

// execUsage prints the token usage and rank audit from the run log (SPEC
// §14). It is read-only: it takes no lock, never creates .igris/ and never
// reads a transcript.
func execUsage(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris usage: %v\n", err)
		return exitFail
	}
	var in report.HistoryInput
	var quota *state.Quota
	name := filepath.Base(cwd)
	if root, err := state.FindRoot(cwd); err == nil {
		name = filepath.Base(root)
		if q, ok, _ := state.ReadQuota(root); ok { // an unusable file is no quota
			quota = &q
		}
		if in, err = project.HistoryInput(root, 0); err != nil {
			fmt.Fprintf(stderr, "igris usage: %v\n", err)
			return exitFail
		}
	}
	sel := ""
	if len(args) == 1 {
		sel = args[0]
	}
	var since time.Time
	rawSince := fs.Lookup("since").Value.String()
	if rawSince != "" {
		since, _ = parseSince(rawSince, usageNow()) // checked before the project was read
	}
	u, err := report.NewUsage(in, name, sel, since)
	if err != nil {
		if err == report.ErrNoRunsSince {
			err = fmt.Errorf("no run since %s; igris history lists the runs", rawSince)
		}
		fmt.Fprintf(stderr, "igris usage: %v\n", err)
		return exitFail
	}
	u.Quota = quota
	if jsonFlag(fs) {
		writeJSON(stdout, u)
	} else {
		f, _ := stdout.(*os.File)
		printUsage(stdout, u, isCharDevice(f) && os.Getenv("TERM") != "dumb")
	}
	return exitOK
}

// printUsage writes the usage as plain text. Every figure is tokens
// recorded in the transcripts, never a cost. color paints a mismatch red,
// on a terminal.
func printUsage(w io.Writer, u report.Usage, color bool) {
	fmt.Fprintf(w, "igris usage · %s\n", u.Project)
	if u.Note != "" {
		fmt.Fprintf(w, "note: %s\n", u.Note)
	}
	fmt.Fprintf(w, "\n%s\n", plural(len(u.Runs), "Run", "Runs"))
	for _, r := range u.Runs {
		fmt.Fprintf(w, "  %s · %s · %s\n", dash(r.Run), r.StartedAt, r.End)
	}

	if u.NoUsage != "" {
		fmt.Fprintf(w, "\nNo usage was recorded: %s.\n", u.NoUsage)
		printSideUsage(w, u.Side)
		fmt.Fprintf(w, "\nQuota: %s\n", quotaText(u.Quota))
		return
	}

	t := u.Totals
	fmt.Fprintf(w, "\nTotals · %d %s · in %s · out %s\n", t.Attempts, plural(t.Attempts, "attempt", "attempts"), usage.Short(t.In), usage.Short(t.Output))
	fmt.Fprintf(w, "  input %s · cache read %s · cache write %s · output %s · %d %s\n",
		usage.Short(t.Input), usage.Short(t.CacheRead), usage.Short(t.CacheWrite), usage.Short(t.Output),
		t.Messages, plural(int(min(t.Messages, 2)), "response", "responses"))
	if sub := t.Sub; sub.Messages > 0 {
		fmt.Fprintf(w, "  subagents included: in %s · out %s\n", usage.Short(sub.In), usage.Short(sub.Output))
	}
	if u.Unavailable > 0 {
		fmt.Fprintf(w, "  note: %d %s without readable usage left out (audit: unavailable)\n",
			u.Unavailable, plural(u.Unavailable, "attempt", "attempts"))
	}

	if len(u.Tasks) > 0 {
		fmt.Fprint(w, "\nBy task\n")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  Task\tAttempts\tIn\tOut\tSubagents\tModels\tAudit")
		for _, r := range u.Tasks {
			fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Attempts, tokenCell(r.UsageRow, r.In),
				tokenCell(r.UsageRow, r.Output), subCell(r.Sub), dash(strings.Join(allModels(r.Models, r.SubModels), ", ")),
				auditCell(r.Audit, color))
		}
		_ = tw.Flush() // a failed write to stdout has nowhere to be reported
	}
	if len(u.Models) > 0 {
		fmt.Fprint(w, "\nBy model\n")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  Model\tAttempts\tIn\tOut\tSubagents")
		for _, r := range u.Models {
			fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\n", r.Model, r.Attempts, usage.Short(r.In), usage.Short(r.Output), subCell(r.Sub))
		}
		_ = tw.Flush() // a failed write to stdout has nowhere to be reported
		attempts := 0
		for _, r := range u.Models {
			attempts += r.Attempts
		}
		if attempts > t.Attempts {
			fmt.Fprint(w, "  an attempt that ran on several models is counted under each, so rows can add up to more than the totals\n")
		}
	}
	if len(u.Ranks) > 0 {
		fmt.Fprint(w, "\nBy rank\n")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  Rank\tAttempts\tIn\tOut\tSubagents")
		for _, r := range u.Ranks {
			fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\n", r.Rank, r.Attempts, usage.Short(r.In), usage.Short(r.Output), subCell(r.Sub))
		}
		_ = tw.Flush() // a failed write to stdout has nowhere to be reported
	}
	if len(u.Top) > 0 {
		fmt.Fprintf(w, "\nTop %d by in + out\n", report.TopTasks)
		for i, r := range u.Top {
			fmt.Fprintf(w, "  %d. %s · %s\n", i+1, r.ID, usage.Short(r.Total()))
		}
	}

	printSideUsage(w, u.Side)

	a := u.Audit
	fmt.Fprintf(w, "\nRank audit · %d ok · %d unchecked · %d unavailable · %d mismatch\n", a.OK, a.Unchecked, a.Unavailable, a.Mismatch)
	for _, m := range a.Mismatches {
		want := m.Model
		if m.Rank != "" && m.Rank != m.Model {
			want = m.Rank + " → " + m.Model
		}
		line := fmt.Sprintf("%s %s ran on %s, not its rank %s", dash(m.Run), m.Task, dash(strings.Join(m.Models, ", ")), dash(want))
		if color {
			line = "\x1b[31m" + line + "\x1b[0m"
		}
		fmt.Fprintf(w, "  ⚠ %s\n", line)
	}
	fmt.Fprintf(w, "\nQuota: %s\n", quotaText(u.Quota))
	fmt.Fprint(w, "\nTokens recorded in the session transcripts, not a bill.\n")
}

// printSideUsage writes the side sessions' table (SPEC §6.10, §14): their
// own, since they are no attempt and in none of the figures above.
func printSideUsage(w io.Writer, side []report.UsageSide) {
	if len(side) == 0 {
		return
	}
	fmt.Fprint(w, "\nSide sessions\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  Side\tRank\tEnded\tIn\tOut\tSubagents\tModels")
	for _, r := range side {
		in, out, sub := dash(""), dash(""), subCell(r.Sub)
		if !r.Unavailable {
			in, out = usage.Short(r.In), usage.Short(r.Output)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Side, dash(r.Rank), r.At, in, out, sub,
			dash(strings.Join(allModels(r.Models, r.SubModels), ", ")))
	}
	_ = tw.Flush() // a failed write to stdout has nowhere to be reported
	fmt.Fprint(w, "  not attempts, and not in the figures above\n")
}

// tokenCell is a count, or "—" for a task none of whose attempts could be
// read.
func tokenCell(r report.UsageRow, n int64) string {
	if r.Attempts == 0 {
		return "—"
	}
	return usage.Short(n)
}

// subCell is the subagents' in + out, "—" without any.
func subCell(s report.Tokens) string {
	if s.Messages == 0 {
		return "—"
	}
	return usage.Short(s.Total())
}

// auditCell is an audit result, red for a mismatch on a terminal.
func auditCell(audit string, color bool) string {
	if audit == state.AuditMismatch && color {
		return "\x1b[31m" + audit + "\x1b[0m"
	}
	return dash(audit)
}

// allModels joins model id lists without repeats.
func allModels(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, m := range l {
			if !contains(out, m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// quotaText is the subscription quota as `igris usage` prints it: each
// window with its reset time, and when it was recorded, or `quota
// unavailable` when the status line never recorded one (SPEC §6.3, §14).
func quotaText(q *state.Quota) string {
	if q == nil {
		return "quota unavailable"
	}
	const layout = "2006-01-02 15:04"
	var parts []string
	for _, w := range []struct {
		name string
		w    *state.QuotaWindow
	}{{"5h", q.FiveHour}, {"7d", q.SevenDay}} {
		if w.w != nil {
			parts = append(parts, fmt.Sprintf("%s %.0f%% (resets %s)", w.name, w.w.Used, time.Unix(w.w.ResetsAt, 0).Format(layout)))
		}
	}
	return strings.Join(parts, " · ") + " · recorded " + q.At.Local().Format(layout)
}
