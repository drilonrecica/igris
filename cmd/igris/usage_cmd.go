package main

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/price"
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
		printUsage(newOut(stdout), u)
	}
	return exitOK
}

// printUsage writes the usage as plain text. Every figure is tokens
// recorded in the transcripts, never a cost. On a terminal with colors a
// mismatch is drawn in Ember.
func printUsage(o textOut, u report.Usage) {
	o.printf("igris usage · %s", u.Project)
	if u.Note != "" {
		o.printf("note: %s", u.Note)
	}
	o.blank()
	o.println(look.Plural(len(u.Runs), "Run", "Runs"))
	for _, r := range u.Runs {
		o.printf("  %s · %s · %s", dash(r.Run), dash(when(r.StartedAt)), r.End)
	}

	if u.NoUsage != "" {
		o.blank()
		o.printf("No usage was recorded: %s.", u.NoUsage)
		printSideUsage(o, u.Side)
		o.blank()
		o.printf("Quota: %s", quotaText(u.Quota))
		o.blank()
		o.next("igris report")
		return
	}

	t := u.Totals
	o.blank()
	o.printf("Totals · %s · in %s · out %s · %s", look.Count(t.Attempts, "attempt", "attempts"), usage.Short(t.In), usage.Short(t.Output), look.Dollars(t.Estimate.USD))
	o.printf("  input %s · cache read %s · cache write %s · output %s · %d %s",
		usage.Short(t.Input), usage.Short(t.CacheRead), usage.Short(t.CacheWrite), usage.Short(t.Output),
		t.Messages, look.Plural(int(min(t.Messages, 2)), "response", "responses"))
	if sub := t.Sub; sub.Messages > 0 {
		o.printf("  subagents included: in %s · out %s", usage.Short(sub.In), usage.Short(sub.Output))
	}
	if u.Unavailable > 0 {
		o.printf("  note: %s without readable usage left out (audit: unavailable)", look.Count(u.Unavailable, "attempt", "attempts"))
	}
	if n := t.Estimate.Unpriced; n > 0 {
		o.printf("  note: %s in + out of models without a price are not in the estimate (give one in [usage.prices])", usage.Short(n))
	}

	if len(u.Tasks) > 0 {
		o.blank()
		o.println("By task")
		tb := &look.Table{Indent: "  ", Header: []string{"TASK", "ATTEMPTS", "IN", "OUT", "SUBAGENTS", "MODELS", "≈ $", "AUDIT"}}
		for _, r := range u.Tasks {
			tb.Row(look.TextCell(r.ID), look.TextCell(fmt.Sprint(r.Attempts)), look.TextCell(tokenCell(r.UsageRow, r.In)),
				look.TextCell(tokenCell(r.UsageRow, r.Output)), look.TextCell(subCell(r.Sub)),
				look.TextCell(strings.Join(allModels(r.Models, r.SubModels), ", ")), look.TextCell(dollarCell(r.UsageRow)), auditCell(r.Audit))
		}
		o.table(tb)
	}
	if len(u.Models) > 0 {
		o.blank()
		o.println("By model")
		tb := &look.Table{Indent: "  ", Header: []string{"MODEL", "ATTEMPTS", "IN", "OUT", "SUBAGENTS", "≈ $"}}
		for _, r := range u.Models {
			tb.Row(look.TextCell(r.Model), look.TextCell(fmt.Sprint(r.Attempts)), look.TextCell(usage.Short(r.In)),
				look.TextCell(usage.Short(r.Output)), look.TextCell(subCell(r.Sub)), look.TextCell(dollarCell(r.UsageRow)))
		}
		o.table(tb)
		attempts := 0
		for _, r := range u.Models {
			attempts += r.Attempts
		}
		if attempts > t.Attempts {
			o.println("  an attempt that ran on several models is counted under each, so rows can add up to more than the totals")
		}
	}
	if len(u.Ranks) > 0 {
		o.blank()
		o.println("By rank")
		tb := &look.Table{Indent: "  ", Header: []string{"RANK", "ATTEMPTS", "IN", "OUT", "SUBAGENTS", "≈ $"}}
		for _, r := range u.Ranks {
			tb.Row(look.TextCell(r.Rank), look.TextCell(fmt.Sprint(r.Attempts)), look.TextCell(usage.Short(r.In)),
				look.TextCell(usage.Short(r.Output)), look.TextCell(subCell(r.Sub)), look.TextCell(dollarCell(r.UsageRow)))
		}
		o.table(tb)
	}
	if len(u.Top) > 0 {
		o.blank()
		o.printf("Top %d by in + out", report.TopTasks)
		for i, r := range u.Top {
			o.printf("  %d. %s · %s", i+1, r.ID, usage.Short(r.Total()))
		}
	}

	printBudgets(o, u.Budgets, len(u.Runs) > 1)
	printSideUsage(o, u.Side)

	a := u.Audit
	o.blank()
	o.printf("Rank audit · %d ok · %d unchecked · %d unavailable · %d mismatch", a.OK, a.Unchecked, a.Unavailable, a.Mismatch)
	for _, m := range a.Mismatches {
		want := m.Model
		if m.Rank != "" && m.Rank != m.Model {
			want = m.Rank + " → " + m.Model
		}
		line := fmt.Sprintf("rank mismatch: %s %s ran on %s, not its rank %s", dash(m.Run), m.Task, dash(strings.Join(m.Models, ", ")), dash(want))
		o.println(o.fit("  " + o.mark(look.Warn, look.GlyphWarn, line)))
	}
	o.blank()
	o.printf("Quota: %s", quotaText(u.Quota))
	o.blank()
	o.println("Tokens recorded in the session transcripts, not a bill.")
	o.println(price.Legend())
	o.next("igris report")
}

// printBudgets writes the budgets of the runs covered with what each used
// (SPEC §6.9): `run 20M · 14.2M used · 71%`, with the run's ID first when
// several runs are covered.
func printBudgets(o textOut, budgets []report.Budget, runs bool) {
	if len(budgets) == 0 {
		return
	}
	o.blank()
	o.println("Budgets")
	for _, b := range budgets {
		line := b.Line()
		if runs {
			line = dash(b.Run) + " · " + line
		}
		o.println(o.fit("  " + line))
	}
}

// printSideUsage writes the side sessions' table (SPEC §6.10, §14): their
// own, since they are no attempt and in none of the figures above.
func printSideUsage(o textOut, side []report.UsageSide) {
	if len(side) == 0 {
		return
	}
	o.blank()
	o.println("Side sessions")
	tb := &look.Table{Indent: "  ", Header: []string{"SIDE", "RANK", "ENDED", "IN", "OUT", "SUBAGENTS", "MODELS"}}
	for _, r := range side {
		in, out := "", ""
		if !r.Unavailable {
			in, out = usage.Short(r.In), usage.Short(r.Output)
		}
		tb.Row(look.TextCell(r.Side), look.TextCell(r.Rank), look.TextCell(cmp.Or(when(r.At), r.At)), look.TextCell(in), look.TextCell(out),
			look.TextCell(subCell(r.Sub)), look.TextCell(strings.Join(allModels(r.Models, r.SubModels), ", ")))
	}
	o.table(tb)
	o.println("  not attempts, and not in the figures above")
}

// tokenCell is a count, or "—" for a task none of whose attempts could be
// read.
func tokenCell(r report.UsageRow, n int64) string {
	if r.Attempts == 0 {
		return "—"
	}
	return usage.Short(n)
}

// dollarCell is a row's estimate without the ≈ (the column header has it),
// "—" for a row none of whose attempts could be read.
func dollarCell(r report.UsageRow) string {
	if r.Attempts == 0 {
		return "—"
	}
	return strings.TrimPrefix(look.Dollars(r.Estimate.USD), "≈ ")
}

// subCell is the subagents' in + out, "—" without any.
func subCell(s report.Tokens) string {
	if s.Messages == 0 {
		return "—"
	}
	return usage.Short(s.Total())
}

// auditCell is an audit result; a mismatch is the rank mark, in Ember on a
// terminal with colors.
func auditCell(audit string) look.Cell {
	if audit == state.AuditMismatch {
		return look.Cell{Text: rankMark, Sev: look.Warn}
	}
	return look.TextCell(audit)
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
	var parts []string
	for _, w := range []struct {
		name string
		w    *state.QuotaWindow
	}{{"5h", q.FiveHour}, {"7d", q.SevenDay}} {
		if w.w != nil {
			parts = append(parts, fmt.Sprintf("%s %.0f%% (resets %s)", w.name, w.w.Used, whenTime(time.Unix(w.w.ResetsAt, 0))))
		}
	}
	return strings.Join(parts, " · ") + " · recorded " + whenTime(q.At)
}
