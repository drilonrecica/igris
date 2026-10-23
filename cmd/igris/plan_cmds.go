package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

// configFile is the config looked up in the working directory.
const configFile = "igris.toml"

// loaded is a parsed plan with the config it was read under.
type loaded struct {
	cfg  *config.Config
	plan *plan.Plan
	// noConfig: there is no igris.toml here, so the defaults apply.
	noConfig bool
	// parentConfig is the igris.toml of a parent directory, which these
	// commands don't read: they were run from inside the project.
	parentConfig string
}

// loadPlan resolves the plan path (--plan, else igris.toml, else tasks.md)
// and parses it. A non-zero code means the error was already reported.
func loadPlan(fs *flag.FlagSet, stderr io.Writer) (*loaded, int) {
	l := &loaded{}
	cfg, err := config.Load(configFile)
	switch {
	case errors.Is(err, config.ErrNotFound):
		cfg = config.Default()
		l.noConfig = true
		if cwd, err := os.Getwd(); err == nil {
			l.parentConfig = checks.ParentConfig(cwd)
		}
	case err != nil:
		fmt.Fprintf(stderr, "%s: %v\n", fs.Name(), err)
		return nil, exitFail
	}
	path := cfg.Plan
	if v := fs.Lookup("plan").Value.String(); v != "" {
		path = v
	}
	p, err := plan.Load(path, cfg.PlanOptions(""))
	if err != nil {
		hint := ""
		if l.parentConfig != "" {
			hint = fmt.Sprintf(" (%s)", checks.ParentHint(l.parentConfig))
		}
		fmt.Fprintf(stderr, "%s: %v; pass --plan PATH or set plan in %s%s\n", fs.Name(), err, configFile, hint)
		return nil, exitFail
	}
	if l.parentConfig != "" && fs.Name() != "igris check" { // check lists it as a warning
		fmt.Fprintf(stderr, "note: %s\n", checks.ParentHint(l.parentConfig))
	}
	l.cfg, l.plan = cfg, p
	return l, exitOK
}

func jsonFlag(fs *flag.FlagSet) bool { return fs.Lookup("json").Value.String() == "true" }

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v) // a failed write to stdout has nowhere to be reported
}

// requireValid reports an invalid plan's problems and returns exitFail.
// The status and phases commands refuse to describe a plan igris could not run.
func requireValid(fs *flag.FlagSet, l *loaded, stdout, stderr io.Writer) int {
	json := jsonFlag(fs)
	issues := l.plan.Validate(l.cfg.Rules(""))
	if len(issues) == 0 {
		return exitOK
	}
	if json {
		writeJSON(stdout, report.NewInvalid(l.plan.Path, issues))
	} else {
		fmt.Fprintf(stderr, "%s: %s is not valid; run `igris check` and fix:\n", fs.Name(), l.plan.Path)
		for _, is := range issues {
			fmt.Fprintf(stderr, "  %s\n", is)
		}
	}
	return exitFail
}

func execCheck(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	l, code := loadPlan(fs, stderr)
	if code != exitOK {
		return code
	}
	// check only warns: it lists ANTHROPIC_API_KEY but asks nothing.
	cs := checks.Run(context.Background(), checks.Options{
		IDs:    []string{checks.IDIgrisPath, checks.IDClaude, checks.IDHerdr, checks.IDTmux, checks.IDConfig, checks.IDAPIKey, checks.IDProject, checks.IDPlanHints, checks.IDDrift},
		Runner: commandRunner(), Getenv: ariseGetenv, Versions: compatWarnings, BackendName: project.BackendName(l.cfg, ariseGetenv),
		Self: igrisSelf, Version: igrisVersion(),
		Config: l.cfg, NoConfig: l.noConfig, ParentConfig: l.parentConfig, Plan: l.plan,
	})
	strict := fs.Lookup("strict").Value.String() == "true"
	r := report.Check(report.CheckInput{Plan: l.plan, Rules: l.cfg.Rules(""), Strict: strict, Checks: checks.Pick(cs,
		checks.IDIgrisPath, checks.IDClaude, checks.IDHerdr, checks.IDTmux, checks.IDConfig, checks.IDAPIKey, checks.IDProject, checks.IDPlanHints, checks.IDDrift)})
	// Under --strict a plan or config warning fails the check; a machine
	// warning never does (SPEC §14).
	strictFail := strict && r.Failing > 0

	if jsonFlag(fs) {
		writeJSON(stdout, r)
	} else {
		printCheck(newOut(stdout), r, cs, l, strictFail)
	}
	if !r.Valid || strictFail {
		return exitFail
	}
	return exitOK
}

func execPhases(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	l, code := loadPlan(fs, stderr)
	if code != exitOK {
		return code
	}
	if code := requireValid(fs, l, stdout, stderr); code != exitOK {
		return code
	}
	r := report.Phases(l.plan)
	if jsonFlag(fs) {
		writeJSON(stdout, r)
		return exitOK
	}
	printPhases(newOut(stdout), r)
	return exitOK
}

// parallelLines renders the parallelism of every phase with work left: its
// critical path and width, and the sets of tasks that could run together.
func parallelLines(rows []report.PhaseRow) []string {
	var out []string
	for _, ph := range rows {
		par := ph.Parallelism
		if par.Width == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s", ph.ID, parallelSummary(par)))
		for _, set := range par.Together {
			out = append(out, "    together: "+strings.Join(set, ", "))
		}
	}
	return out
}

// parallelSummary is "critical path A → B (2), width 3".
func parallelSummary(par report.Parallelism) string {
	return fmt.Sprintf("critical path %s (%d), width %d", strings.Join(par.CriticalPath, " → "), len(par.CriticalPath), par.Width)
}

func execStatus(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	l, code := loadPlan(fs, stderr)
	if code != exitOK {
		return code
	}
	if code := requireValid(fs, l, stdout, stderr); code != exitOK {
		return code
	}
	phaseID := ""
	if len(args) == 1 {
		phaseID = args[0]
	}
	r, err := report.Status(l.plan, phaseID)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", fs.Name(), err)
		return exitFail
	}
	r.Run = currentRun()
	var live *serve.Snapshot
	if r.Run != nil && r.Run.Lock == report.LockHere {
		live, _ = peekRun(projectRoot())
	}
	if jsonFlag(fs) {
		writeJSON(stdout, r)
		return exitOK
	}
	printStatus(newOut(stdout), r, l.plan, projectRoot(), live)
	return exitOK
}

// currentRun describes the run recorded under the project root found from
// the working directory, or returns nil when there is no root or no run.
// status reads the plan from the cwd but the state from the root (V02-P2);
// nothing here creates or changes a file.
func currentRun() *report.RunInfo {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	root, err := state.FindRoot(cwd)
	if err != nil {
		return nil
	}
	return project.RunInfo(root)
}

// projectRoot is the project root found from the working directory, or
// the working directory without one.
func projectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if root, err := state.FindRoot(cwd); err == nil {
		return root
	}
	return cwd
}

// printCheck writes check's problems and warnings, then its summary line
// and next (SPEC §14).
func printCheck(o textOut, r report.CheckReport, cs []checks.Result, l *loaded, strictFail bool) {
	for _, is := range r.Issues {
		o.println(is.String())
	}
	for _, w := range r.Warnings {
		switch {
		case w.File == "":
			o.warnf("%s", w.Message)
		case w.Line == 0:
			o.warnf("%s: %s", w.File, w.Message)
		default:
			o.warnf("%s:%d: %s", w.File, w.Line, w.Message)
		}
	}
	for _, c := range checks.Pick(cs, checks.IDProject) {
		if l.noConfig && c.Level == checks.OK { // no igris.toml: a note, not a warning
			o.printf("note: %s", c.Message)
		}
	}
	warnings := look.Count(len(r.Warnings), "warning", "warnings")
	switch {
	case l.plan.NewerFormat():
		o.printf("%s: written for a newer igris; upgrade igris and run `igris check` again", r.Plan)
		o.next("igris update")
	case !r.Valid:
		o.printf("%s: %s; fix them (or run `igris adapt`) and run `igris check` again", r.Plan, look.Count(len(r.Issues), "problem", "problems"))
		o.next("igris check")
	case strictFail:
		o.printf("%s: %s under --strict; fix them and run `igris check --strict` again", r.Plan, look.Count(r.Failing, "warning", "warnings"))
		o.next("igris check --strict")
	default:
		o.printf("%s: %s (%s, %s, %s)", r.Plan, o.paint(look.Accent, "OK"), look.Count(r.Phases, "phase", "phases"), look.Count(r.Tasks, "task", "tasks"), warnings)
		o.next("igris status")
	}
}

// printPhases writes the phases' table, their parallelism and next.
func printPhases(o textOut, r report.PhasesReport) {
	tb := &look.Table{Header: []string{"PHASE", "TITLE", "READY", "BLOCKED", "IN PROGRESS", "DONE", "SKIPPED", "TOTAL"}}
	next := ""
	for _, ph := range r.Phases {
		c := ph.Counts
		count := func(s plan.Status) look.Cell { return look.TextCell(fmt.Sprint(c[report.CountKey(s)])) }
		glyph, sev := phaseMark(ph.Phase)
		if next == "" && glyph != look.GlyphDone {
			next = "igris arise " + ph.ID
		}
		tb.Row(look.Mark(sev, glyph, ph.ID), look.TextCell(ph.Title), count(plan.Ready), count(plan.Blocked),
			count(plan.InProgress), count(plan.Done), count(plan.Skipped), look.TextCell(fmt.Sprint(ph.Total)))
	}
	o.table(tb)
	if lines := parallelLines(r.Phases); len(lines) > 0 {
		o.blank()
		for _, line := range lines {
			o.println(o.fit(line))
		}
	}
	o.blank()
	o.next(cmp.Or(next, "igris history"))
}

// phaseMark is a phase's glyph from its counts: done when every task is
// finished, running with a task in progress, ready with one to start,
// waiting on other phases otherwise.
func phaseMark(ph report.Phase) (string, look.Severity) {
	c := func(s plan.Status) int { return ph.Counts[report.CountKey(s)] }
	switch {
	case c(plan.Done)+c(plan.Skipped) == ph.Total:
		return look.GlyphDone, look.Accent
	case c(plan.InProgress) > 0:
		return look.GlyphRunning, look.Accent
	case c(plan.Ready) > 0:
		return look.GlyphReady, look.Plain
	}
	return look.GlyphWaiting, look.Plain
}
