package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
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
		l.parentConfig = parentConfig()
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
			hint = fmt.Sprintf(" (%s)", parentHint(l.parentConfig))
		}
		fmt.Fprintf(stderr, "%s: %v; pass --plan PATH or set plan in %s%s\n", fs.Name(), err, configFile, hint)
		return nil, exitFail
	}
	if l.parentConfig != "" && fs.Name() != "igris check" { // check lists it as a warning
		fmt.Fprintf(stderr, "note: %s\n", parentHint(l.parentConfig))
	}
	l.cfg, l.plan = cfg, p
	return l, exitOK
}

// parentConfig returns the igris.toml of the nearest parent directory that
// has one, or "".
func parentConfig() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	root, err := state.FindRoot(cwd)
	if err != nil || root == cwd {
		return ""
	}
	path := filepath.Join(root, configFile)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func parentHint(path string) string {
	return fmt.Sprintf("%s found in %s; run igris from there (this directory uses the defaults)", configFile, filepath.Dir(path))
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
	issues := l.plan.Validate(l.cfg.Models)
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
	var leading []report.Warning
	for _, w := range compatWarnings(context.Background(), commandRunner()) {
		leading = append(leading, report.Warning{Message: w})
	}
	for _, w := range l.cfg.Warnings() {
		leading = append(leading, report.Warning{File: configFile, Message: w})
	}
	if ariseGetenv(engine.APIKeyVar) != "" {
		leading = append(leading, report.Warning{Message: engine.APIKeyWarning})
	}
	if l.parentConfig != "" {
		leading = append(leading, report.Warning{Message: parentHint(l.parentConfig)})
	}
	r := report.Check(report.CheckInput{Plan: l.plan, Models: l.cfg.Models, Leading: leading})

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
		if l.noConfig && l.parentConfig == "" {
			// Not a warning: a directory with just a plan is a valid project.
			fmt.Fprintf(stdout, "note: no %s here; using the defaults (`igris init` creates one)\n", configFile)
		}
		if r.Valid {
			fmt.Fprintf(stdout, "%s: OK (%d phases, %d tasks, %d warnings)\n", r.Plan, r.Phases, r.Tasks, len(r.Warnings))
		} else {
			fmt.Fprintf(stdout, "%s: %d problem(s); fix them (or run `igris adapt`) and run `igris check` again\n", r.Plan, len(r.Issues))
		}
	}
	if !r.Valid {
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
	if jsonFlag(fs) {
		writeJSON(stdout, r)
		return exitOK
	}
	for i, ph := range r.Phases {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printPhaseStatus(stdout, ph)
	}
	return exitOK
}

func printPhaseStatus(w io.Writer, r report.PhaseStatus) {
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
	fmt.Fprintln(tw, "  ID\tSTATUS\tRANK\tOWNER\tMODE\tWAITS ON")
	for _, t := range r.Tasks {
		waits := make([]string, len(t.WaitsOn))
		for i, x := range t.WaitsOn {
			waits[i] = fmt.Sprintf("%s (%s, phase %s)", x.ID, x.Status, x.Phase)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Rank, t.Owner, t.Mode, strings.Join(waits, ", "))
	}
	_ = tw.Flush()
}
