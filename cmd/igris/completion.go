package main

import (
	"bytes"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
)

//go:embed completion/*.tmpl
var completionFS embed.FS

// completionShells are the shells `igris completion` writes a script for.
var completionShells = []string{"bash", "zsh", "fish"}

// Argument kinds a flag value or a positional argument is completed with.
const (
	argNone   = ""       // nothing to offer (a free-form value)
	argFile   = "file"   // file names
	argPhases = "phases" // phase IDs, from `igris __complete phases`
	argTasks  = "tasks"  // task IDs, from `igris __complete tasks`
	argValues = "values" // a fixed list
)

// compArg says how a flag's value, or a command's positional argument, is
// completed.
type compArg struct {
	Kind   string
	Values []string
}

type compFlag struct {
	Name  string // without dashes
	Takes bool   // takes a value
	Arg   compArg
}

// Dash is the flag as typed: --name, or -n for a single letter.
func (f compFlag) Dash() string {
	if len(f.Name) == 1 {
		return "-" + f.Name
	}
	return "--" + f.Name
}

type compCmd struct {
	Name  string
	Desc  string
	Flags []compFlag
	Pos   compArg
}

// FlagWords is the command's flags as one space-separated word list.
func (c compCmd) FlagWords() string {
	w := make([]string, len(c.Flags))
	for i, f := range c.Flags {
		w[i] = f.Dash()
	}
	return strings.Join(w, " ")
}

// ValueFlags are the flags that take a value.
func (c compCmd) ValueFlags() []compFlag {
	var out []compFlag
	for _, f := range c.Flags {
		if f.Takes {
			out = append(out, f)
		}
	}
	return out
}

// compData is what the shell templates render.
type compData struct {
	Cmds []compCmd
}

// Words is every top-level word: the commands, then help and version.
func (d compData) Words() string {
	w := make([]string, 0, len(d.Cmds)+2)
	for _, c := range d.Cmds {
		w = append(w, c.Name)
	}
	return strings.Join(append(w, "help", "version"), " ")
}

// Positional lists the commands that complete a positional argument.
func (d compData) Positional() []compCmd {
	var out []compCmd
	for _, c := range d.Cmds {
		if c.Pos.Kind != argNone {
			out = append(out, c)
		}
	}
	return out
}

var (
	compDescs = map[string]string{
		"init":       "create igris.toml, .igris/, the .gitignore entry and Claude allow rules",
		"doctor":     "read-only health check of this project and machine",
		"check":      "validate the plan",
		"phases":     "list phases with task counts per status",
		"status":     "tasks with status, rank, owner, current run and unmet deps",
		"history":    "past runs from .igris/runs.jsonl",
		"arise":      "run (or resume) with the TUI",
		"done":       "signal that a task is finished",
		"skip":       "signal that a task is skipped",
		"notify":     "send a sample of each notification",
		"adapt":      "AI-assisted conversion of a plan with diff review",
		"completion": "print a shell completion script",
		"help":       "show the help",
		"version":    "print the version",
	}
	// compFlagArgs completes flag values, keyed "command:flag"; "*:flag"
	// applies to every command. A flag that takes a value and is not listed
	// is free-form.
	compFlagArgs = map[string]compArg{
		"*:plan":        {Kind: argFile},
		"arise:through": {Kind: argPhases},
		"arise:mode":    {Kind: argValues, Values: runModes},
		"arise:only":    {Kind: argTasks},
		"arise:from":    {Kind: argTasks},
		"arise:until":   {Kind: argTasks},
		"adapt:model":   {Kind: argValues, Values: []string{"sonnet", "opus"}},
		"notify:event":  {Kind: argValues, Values: eventNames()},
		"adapt:plan":    {Kind: argFile},
	}
	compPositional = map[string]compArg{
		"arise":      {Kind: argPhases},
		"status":     {Kind: argPhases},
		"history":    {Kind: argTasks},
		"done":       {Kind: argTasks},
		"skip":       {Kind: argTasks},
		"notify":     {Kind: argValues, Values: []string{"test"}},
		"completion": {Kind: argValues, Values: completionShells},
	}
)

func eventNames() []string {
	out := make([]string, len(notify.AllEvents))
	for i, e := range notify.AllEvents {
		out[i] = string(e)
	}
	return out
}

// completionData reads the flags off each command's own FlagSet, so the
// scripts follow the commands.
func completionData() compData {
	var d compData
	for _, c := range commands() {
		fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		c.setup(fs)
		cc := compCmd{Name: c.name, Desc: compDescs[c.name], Pos: compPositional[c.name]}
		fs.VisitAll(func(f *flag.Flag) {
			cf := compFlag{Name: f.Name, Takes: !isBool(f)}
			if cf.Takes {
				if a, ok := compFlagArgs[c.name+":"+f.Name]; ok {
					cf.Arg = a
				} else {
					cf.Arg = compFlagArgs["*:"+f.Name]
				}
			}
			cc.Flags = append(cc.Flags, cf)
		})
		sort.SliceStable(cc.Flags, func(i, j int) bool { return cc.Flags[i].Name < cc.Flags[j].Name })
		d.Cmds = append(d.Cmds, cc)
	}
	return d
}

// completionScript renders the script for shell.
func completionScript(shell string) (string, error) {
	t, err := template.New(shell+".tmpl").ParseFS(completionFS, "completion/"+shell+".tmpl")
	if err != nil {
		return "", fmt.Errorf("load %s completion template: %w", shell, err)
	}
	var b bytes.Buffer
	if err := t.Execute(&b, completionData()); err != nil {
		return "", fmt.Errorf("render %s completion: %w", shell, err)
	}
	return b.String(), nil
}

// completionArgs checks `igris completion SHELL`.
func completionArgs(*flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("missing shell; use `igris completion %s`", strings.Join(completionShells, "|"))
		}
		if !contains(completionShells, args[0]) {
			return fmt.Errorf("unknown shell %q (want %s)", args[0], strings.Join(completionShells, "|"))
		}
		return atMost(args, 1)
	}
}

func execCompletion(_ *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	s, err := completionScript(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "igris completion: %v\n", err)
		return exitFail
	}
	fmt.Fprint(stdout, s)
	return exitOK
}

// execComplete is the hidden `igris __complete KIND` the scripts call for
// phase and task IDs (SPEC §14). It reads the plan only (never .igris/, never
// the network) and prints one candidate per line. Whatever goes wrong — no
// plan, an invalid plan, an unknown kind — it prints nothing and exits 0: a
// completion must never put noise on the owner's prompt.
func execComplete(args []string, stdout io.Writer) int {
	if len(args) != 1 {
		return exitOK
	}
	cfg, err := config.Load(configFile)
	switch {
	case err == nil:
	case errors.Is(err, config.ErrNotFound):
		cfg = config.Default()
	default:
		return exitOK
	}
	p, err := plan.Load(cfg.Plan, plan.Options{Columns: cfg.Columns})
	if err != nil || len(p.Validate(cfg.Rules(""))) > 0 {
		return exitOK
	}
	switch args[0] {
	case argPhases:
		for _, ph := range p.Phases {
			fmt.Fprintln(stdout, ph.ID)
		}
	case argTasks:
		for _, t := range p.Tasks {
			fmt.Fprintln(stdout, t.ID)
		}
	}
	return exitOK
}
