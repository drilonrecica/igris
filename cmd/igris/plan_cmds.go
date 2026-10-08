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
	"text/tabwriter"

	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
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
	p, err := plan.Load(path, plan.Options{Columns: cfg.Columns})
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
		IDs:    []string{checks.IDClaude, checks.IDHerdr, checks.IDTmux, checks.IDConfig, checks.IDAPIKey, checks.IDProject, checks.IDPlanHints, checks.IDDrift},
		Runner: commandRunner(), Getenv: ariseGetenv, Versions: compatWarnings, BackendName: project.BackendName(l.cfg, ariseGetenv),
		Config: l.cfg, NoConfig: l.noConfig, ParentConfig: l.parentConfig, Plan: l.plan,
	})
	strict := fs.Lookup("strict").Value.String() == "true"
	r := report.Check(report.CheckInput{Plan: l.plan, Rules: l.cfg.Rules(""), Strict: strict, Checks: checks.Pick(cs,
		checks.IDClaude, checks.IDHerdr, checks.IDTmux, checks.IDConfig, checks.IDAPIKey, checks.IDProject, checks.IDPlanHints, checks.IDDrift)})
	// Under --strict a plan or config warning fails the check; a machine
	// warning never does (SPEC §14).
	strictFail := strict && r.Failing > 0

	if jsonFlag(fs) {
		writeJSON(stdout, r)
	} else {
		for _, is := range r.Issues {
			fmt.Fprintln(stdout, is)
		}
		for _, w := range r.Warnings {
			switch {
			case w.File == "":
				fmt.Fprintf(stdout, "warning: %s\n", w.Message)
				continue
			case w.Line == 0:
				fmt.Fprintf(stdout, "warning: %s: %s\n", w.File, w.Message)
				continue
			}
			fmt.Fprintf(stdout, "warning: %s:%d: %s\n", w.File, w.Line, w.Message)
		}
		for _, c := range checks.Pick(cs, checks.IDProject) {
			if l.noConfig && c.Level == checks.OK { // no igris.toml: a note, not a warning
				fmt.Fprintf(stdout, "note: %s\n", c.Message)
			}
		}
		switch {
		case !r.Valid:
			fmt.Fprintf(stdout, "%s: %d problem(s); fix them (or run `igris adapt`) and run `igris check` again\n", r.Plan, len(r.Issues))
		case strictFail:
			fmt.Fprintf(stdout, "%s: %d warning(s) under --strict; fix them and run igris check --strict again\n", r.Plan, r.Failing)
		default:
			fmt.Fprintf(stdout, "%s: OK (%d phases, %d tasks, %d warnings)\n", r.Plan, r.Phases, r.Tasks, len(r.Warnings))
		}
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
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PHASE\tTITLE\tREADY\tBLOCKED\tIN PROGRESS\tDONE\tSKIPPED\tTOTAL")
	for _, ph := range r.Phases {
		c := ph.Counts
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n", ph.ID, ph.Title,
			c["ready"], c["blocked"], c["in progress"], c["done"], c["skipped"], ph.Total)
	}
	_ = tw.Flush()
	return exitOK
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
	if jsonFlag(fs) {
		writeJSON(stdout, r)
		return exitOK
	}
	if r.Run != nil {
		printRun(stdout, r.Run)
		fmt.Fprintln(stdout)
	}
	for i, ph := range r.Phases {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printPhaseStatus(stdout, ph, r.Optional)
	}
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

func printRun(w io.Writer, r *report.RunInfo) {
	fmt.Fprintln(w, "Run")
	row := func(label, value string) {
		if value != "" {
			fmt.Fprintf(w, "  %-8s %s\n", label, value)
		}
	}
	if r.Unreadable != "" {
		row("State", r.Unreadable)
	} else {
		phases := strings.Join(r.Phases, ", ")
		if r.Through != "" {
			phases += " (through " + r.Through + ")"
		}
		row("Phases", phases)
		row("Started", r.StartedAt)
		task := r.Task
		if task == "" {
			task = "between tasks"
		}
		row("Task", task)
		row("Mode", r.Mode)
		row("Since", r.Since)
		row("Session", r.Session)
	}
	lock := r.Lock
	if r.LockDetail != "" {
		lock += " (" + r.LockDetail + ")"
	}
	row("Lock", lock)
	row("Signals", strings.Join(r.Signals, ", "))
}

func printPhaseStatus(w io.Writer, r report.PhaseStatus, opt report.Optional) {
	satisfied := r.Counts["done"] + r.Counts["skipped"]
	name := r.ID
	if r.Title != "" {
		name += " — " + r.Title
	}
	fmt.Fprintf(w, "%s: %d/%d finished, %s", name, satisfied, r.Total, r.Outcome)
	if r.Next != "" {
		fmt.Fprintf(w, " (%s)", r.Next)
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	// The optional columns are shown only when some task of the plan sets
	// them (SPEC §14).
	header := "  ID\tSTATUS\tRANK\tOWNER\tMODE\t"
	optional := func(t report.Task) string {
		var b strings.Builder
		if opt.Verify {
			b.WriteString(cmp.Or(t.Verify, "—") + "\t")
		}
		if opt.Timeout {
			b.WriteString(cmp.Or(t.Timeout, "—") + "\t")
		}
		if opt.Context {
			b.WriteString(cmp.Or(strings.Join(t.Context, ", "), "—") + "\t")
		}
		return b.String()
	}
	if opt.Verify {
		header += "VERIFY\t"
	}
	if opt.Timeout {
		header += "TIMEOUT\t"
	}
	if opt.Context {
		header += "CONTEXT\t"
	}
	fmt.Fprintln(tw, header+"WAITS ON")
	for _, t := range r.Tasks {
		waits := make([]string, len(t.WaitsOn))
		for i, x := range t.WaitsOn {
			waits[i] = fmt.Sprintf("%s (%s, phase %s)", x.ID, x.Status, x.Phase)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s%s\n", t.ID, t.Status, t.Rank, t.Owner, t.Mode, optional(t), strings.Join(waits, ", "))
	}
	_ = tw.Flush()
}
