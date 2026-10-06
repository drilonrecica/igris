// Command igris runs a markdown task plan through Claude Code sessions.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// Exit codes shared by every subcommand (SPEC §14).
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

const usageText = `igris - run a markdown task plan through Claude Code sessions

Usage:
  igris <command> [flags]

Commands:
  init                                  create igris.toml, .igris/, .gitignore entry, Claude allow rules
  check [--plan PATH] [--json]          validate the plan; exit 0 valid, 1 invalid, 2 usage error
  phases [--plan PATH] [--json]         list phases with task counts per status
  status [PHASE] [--plan PATH] [--json] tasks with status/rank/owner, current run, unmet deps
  arise [PHASE] [--through PHASE]       run (or resume) with the TUI
        [--mode default|accept|auto|plan|yolo] [--no-tui] [--dry-run]
        [--force-unlock]
  done ID [--note TEXT]                 signal that a task is finished
  skip ID --reason TEXT                 signal that a task is skipped
  notify test [--event NAME]            send a sample of each notification to the configured channels
  adapt [--model sonnet|opus]           AI-assisted conversion with diff review
        [--plan PATH]
  version                               print the version

Run "igris <command> -h" for the flags of a command.
`

// command describes one subcommand. setup registers its flags on fs and
// returns a validator that runs after parsing and reports usage errors.
type command struct {
	name  string
	setup func(fs *flag.FlagSet) (validate func(args []string) error)
	// exec runs the command after flags and arguments validated. Commands
	// without it are not implemented yet.
	exec func(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int
}

var runModes = []string{"default", "accept", "auto", "plan", "yolo"}

func commands() []command {
	return []command{
		{name: "init", setup: noFlags(0), exec: execInit},
		{name: "check", setup: planFlags(0), exec: execCheck},
		{name: "phases", setup: planFlags(0), exec: execPhases},
		{name: "status", setup: planFlags(1), exec: execStatus},
		{name: "arise", setup: func(fs *flag.FlagSet) func([]string) error {
			fs.String("through", "", "last phase to run")
			mode := fs.String("mode", "", "run mode: "+strings.Join(runModes, "|"))
			fs.Bool("no-tui", false, "plain log output, owner commands from stdin")
			fs.Bool("dry-run", false, "walk the phase with the fake backend, write nothing")
			fs.Bool("force-unlock", false, "clear a stale lock left by a run that is no longer alive")
			return func(args []string) error {
				if *mode != "" && !contains(runModes, *mode) {
					return fmt.Errorf("invalid --mode %q (want %s)", *mode, strings.Join(runModes, "|"))
				}
				return atMost(args, 1)
			}
		}, exec: execArise},
		{name: "done", setup: func(fs *flag.FlagSet) func([]string) error {
			fs.String("note", "", "short note stored with the signal")
			return exactArgs("ID", 1)
		}, exec: execDone},
		{name: "skip", setup: func(fs *flag.FlagSet) func([]string) error {
			reason := fs.String("reason", "", "why the task is skipped (required)")
			return func(args []string) error {
				if err := exactArgs("ID", 1)(args); err != nil {
					return err
				}
				if strings.TrimSpace(*reason) == "" {
					return fmt.Errorf("--reason is required")
				}
				return nil
			}
		}, exec: execSkip},
		{name: "notify", setup: notifyArgs, exec: execNotify},
		{name: "adapt", setup: adaptArgs, exec: execAdapt},
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches args to a subcommand and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "igris %s\n", version)
		return exitOK
	}
	for _, c := range commands() {
		if c.name == args[0] {
			return runCommand(c, args[1:], stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "igris: unknown command %q\n\n%s", args[0], usageText)
	return exitUsage
}

func runCommand(c command, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("igris "+c.name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	validate := c.setup(fs)
	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if err := validate(fs.Args()); err != nil {
		fmt.Fprintf(stderr, "igris %s: %v\n", c.name, err)
		return exitUsage
	}
	if c.exec == nil {
		fmt.Fprintf(stderr, "igris %s: not implemented yet\n", c.name)
		return exitFail
	}
	return c.exec(fs, fs.Args(), stdout, stderr)
}

// reorder moves flags ahead of positional arguments so that both
// "igris done ID --note x" and "igris done --note x ID" work with the
// standard flag package, which stops at the first positional argument.
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil && !isBool(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, append([]string{"--"}, rest...)...)
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// planFlags registers the flags shared by the plan commands and allows up
// to max positional arguments.
func planFlags(max int) func(*flag.FlagSet) func([]string) error {
	return func(fs *flag.FlagSet) func([]string) error {
		fs.String("plan", "", "path to the plan file (default: plan from igris.toml, else tasks.md)")
		fs.Bool("json", false, "machine-readable output")
		return maxArgs(fs, max)
	}
}

func noFlags(max int) func(*flag.FlagSet) func([]string) error {
	return func(fs *flag.FlagSet) func([]string) error { return maxArgs(fs, max) }
}

func maxArgs(_ *flag.FlagSet, n int) func([]string) error {
	return func(args []string) error { return atMost(args, n) }
}

func atMost(args []string, n int) error {
	if len(args) > n {
		return fmt.Errorf("unexpected argument %q", args[n])
	}
	return nil
}

func exactArgs(name string, n int) func([]string) error {
	return func(args []string) error {
		if len(args) < n {
			return fmt.Errorf("missing %s argument", name)
		}
		return atMost(args, n)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
