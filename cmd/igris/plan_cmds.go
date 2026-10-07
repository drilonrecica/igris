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
	"github.com/drilonrecica/igris/internal/state"
)

// configFile is the config looked up in the working directory.
const configFile = "igris.toml"

// statuses lists the statuses in the order the commands report them.
var statuses = []plan.Status{plan.Ready, plan.Blocked, plan.InProgress, plan.Done, plan.Skipped}

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

type issueJSON struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

func issuesJSON(issues []plan.Issue) []issueJSON {
	out := make([]issueJSON, len(issues)) // never null in JSON
	for i, is := range issues {
		out[i] = issueJSON{File: is.File, Line: is.Line, Message: is.Msg}
	}
	return out
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
		writeJSON(stdout, map[string]any{"plan": l.plan.Path, "valid": false, "issues": issuesJSON(issues)})
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
	issues := l.plan.Validate(l.cfg.Models)
	valid := len(issues) == 0

	// Drift is only meaningful when the plan is valid: unknown or cyclic
	// dependencies make readiness undefined.
	type warningJSON struct {
		File    string `json:"file,omitempty"` // empty for a tool version, igris.toml for the config
		Line    int    `json:"line,omitempty"`
		Task    string `json:"task,omitempty"`
		From    string `json:"from,omitempty"`
		To      string `json:"to,omitempty"`
		Message string `json:"message"`
	}
	warnings := []warningJSON{}
	for _, w := range compatWarnings(context.Background(), commandRunner()) {
		warnings = append(warnings, warningJSON{Message: w})
	}
	for _, w := range l.cfg.Warnings() {
		warnings = append(warnings, warningJSON{File: configFile, Message: w})
	}
	if ariseGetenv(engine.APIKeyVar) != "" {
		warnings = append(warnings, warningJSON{Message: engine.APIKeyWarning})
	}
	if l.parentConfig != "" {
		warnings = append(warnings, warningJSON{Message: parentHint(l.parentConfig)})
	}
	if valid {
		for _, h := range l.plan.Hints() {
			warnings = append(warnings, warningJSON{File: h.File, Line: h.Line, Message: h.Msg})
		}
		for _, c := range l.plan.Readiness() {
			t := l.plan.Task(c.ID)
			warnings = append(warnings, warningJSON{
				File: l.plan.Path, Line: t.Line, Task: c.ID, From: c.From.String(), To: c.To.String(),
				Message: fmt.Sprintf("%s is %s but %s; igris will set it to %s", c.ID, c.From, driftReason(l.plan, t, c.To), c.To),
			})
		}
	}

	if jsonFlag(fs) {
		writeJSON(stdout, map[string]any{
			"plan": l.plan.Path, "valid": valid, "phases": len(l.plan.Phases), "tasks": len(l.plan.Tasks),
			"issues": issuesJSON(issues), "warnings": warnings,
		})
	} else {
		for _, is := range issues {
			fmt.Fprintln(stdout, is)
		}
		for _, w := range warnings {
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
		if valid {
			fmt.Fprintf(stdout, "%s: OK (%d phases, %d tasks, %d warnings)\n", l.plan.Path, len(l.plan.Phases), len(l.plan.Tasks), len(warnings))
		} else {
			fmt.Fprintf(stdout, "%s: %d problem(s); fix them (or run `igris adapt`) and run `igris check` again\n", l.plan.Path, len(issues))
		}
	}
	if !valid {
		return exitFail
	}
	return exitOK
}

// driftReason says why the readiness sync would flip a task's status.
func driftReason(p *plan.Plan, t *plan.Task, to plan.Status) string {
	if to == plan.Ready {
		return "all its dependencies are satisfied"
	}
	return "waits on " + strings.Join(p.WaitingOn(t).Unmet, ", ")
}

type phaseJSON struct {
	ID     string         `json:"id"`
	Title  string         `json:"title"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

func countStatuses(ph *plan.Phase) map[string]int {
	counts := make(map[string]int, len(statuses))
	for _, s := range statuses {
		counts[s.String()] = 0
	}
	for _, t := range ph.Tasks {
		counts[t.Status.String()]++
	}
	return counts
}

func execPhases(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	l, code := loadPlan(fs, stderr)
	if code != exitOK {
		return code
	}
	if code := requireValid(fs, l, stdout, stderr); code != exitOK {
		return code
	}
	phases := make([]phaseJSON, len(l.plan.Phases))
	for i, ph := range l.plan.Phases {
		phases[i] = phaseJSON{ID: ph.ID, Title: ph.Title, Total: len(ph.Tasks), Counts: countStatuses(ph)}
	}
	if jsonFlag(fs) {
		writeJSON(stdout, map[string]any{"plan": l.plan.Path, "phases": phases})
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PHASE\tTITLE\tREADY\tBLOCKED\tIN PROGRESS\tDONE\tSKIPPED\tTOTAL")
	for _, ph := range phases {
		c := ph.Counts
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\n", ph.ID, ph.Title,
			c["ready"], c["blocked"], c["in progress"], c["done"], c["skipped"], ph.Total)
	}
	_ = tw.Flush()
	return exitOK
}

type waitJSON struct {
	ID     string `json:"id"`
	Status string `json:"status,omitempty"`
	Phase  string `json:"phase,omitempty"`
}

type taskJSON struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Status  string     `json:"status"`
	Rank    string     `json:"rank"`
	Owner   string     `json:"owner"`
	Mode    string     `json:"mode"`
	WaitsOn []waitJSON `json:"waits_on"`
}

type phaseStatusJSON struct {
	phaseJSON
	Outcome string     `json:"outcome"`
	Next    string     `json:"next,omitempty"`
	Tasks   []taskJSON `json:"tasks"`
}

func execStatus(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	l, code := loadPlan(fs, stderr)
	if code != exitOK {
		return code
	}
	if code := requireValid(fs, l, stdout, stderr); code != exitOK {
		return code
	}
	phases := l.plan.Phases
	if len(args) == 1 {
		ph := l.plan.Phase(args[0])
		if ph == nil {
			_, err := l.plan.Select(args[0]) // builds the "unknown phase" message
			fmt.Fprintf(stderr, "%s: %v\n", fs.Name(), err)
			return exitFail
		}
		phases = []*plan.Phase{ph}
	}

	report := make([]phaseStatusJSON, len(phases))
	for i, ph := range phases {
		sel, err := l.plan.Select(ph.ID)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", fs.Name(), err)
			return exitFail
		}
		r := phaseStatusJSON{
			phaseJSON: phaseJSON{ID: ph.ID, Title: ph.Title, Total: len(ph.Tasks), Counts: countStatuses(ph)},
			Outcome:   sel.Outcome.String(), Tasks: make([]taskJSON, len(ph.Tasks)),
		}
		if sel.Task != nil {
			r.Next = sel.Task.ID
		}
		for j, t := range ph.Tasks {
			r.Tasks[j] = taskReport(l.plan, t)
		}
		report[i] = r
	}

	if jsonFlag(fs) {
		writeJSON(stdout, map[string]any{"plan": l.plan.Path, "phases": report})
		return exitOK
	}
	for i, r := range report {
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printPhaseStatus(stdout, r)
	}
	return exitOK
}

func taskReport(p *plan.Plan, t *plan.Task) taskJSON {
	r := taskJSON{ID: t.ID, Title: t.Title, Status: t.Status.String(), Rank: orDash(t.Rank), Owner: string(t.Owner), Mode: orDash(t.Mode), WaitsOn: []waitJSON{}}
	if w := p.WaitingOn(t); w != nil {
		for _, id := range w.Unmet {
			if d := p.Task(id); d != nil && t.Status != plan.Done && t.Status != plan.Skipped {
				r.WaitsOn = append(r.WaitsOn, waitJSON{ID: id, Status: d.Status.String(), Phase: d.Phase.ID})
			}
		}
	}
	return r
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func printPhaseStatus(w io.Writer, r phaseStatusJSON) {
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
