// Command igris runs a markdown task plan through Claude Code sessions.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/drilonrecica/igris/examples"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/tui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// igrisVersion is the running igris's version.
func igrisVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

// resolveVersion prefers the ldflags version; a `go install …@vX.Y.Z` build has
// none, so it falls back to the module version from the build info (SPEC §18).
func resolveVersion(ldflags string, info *debug.BuildInfo, ok bool) string {
	if ldflags != "dev" || !ok || info == nil {
		return ldflags
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return ldflags
	}
	return strings.TrimPrefix(v, "v")
}

// Exit codes shared by every subcommand (SPEC §14).
const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

const usageText = `igris - run a markdown task plan through Claude Code sessions

Usage:
  igris                                 on a terminal: the home screen; otherwise this help, exit 2
  igris <command> [flags]

Commands:
  init [--example | --template NAME]    create igris.toml, .igris/, .gitignore entry, Claude allow rules;
                                        --template a starter plan (go-cli, library, web-app)
  doctor [--json]                       read-only health check of this project and machine; exit 1 on a failure
  check [--plan PATH] [--json]          validate the plan; exit 0 valid, 1 invalid, 2 usage error
        [--strict]                      also lint the plan; exit 1 on any plan or config warning
  phases [--plan PATH] [--json]         list phases with task counts per status
  status [PHASE] [--plan PATH] [--json] tasks with status/rank/owner, current run, unmet deps
  history [TASK-ID] [-n N] [--json]     past runs from .igris/runs.jsonl; with a task ID, its attempts
  report [RUN] [--json]                 one run as markdown (or JSON): RUN is 1 (the newest, default), 2, … or a run ID
  usage [RUN | --since D] [--json]      token usage and the rank audit of one run (RUN as for report) or of the runs
                                        started since D (7d, 36h, 2026-10-01); with the last subscription quota
  arise [PHASE] [--through PHASE]       run (or resume) with the TUI
        [--mode default|accept|auto|plan[:MODE]|yolo|manual] [--no-tui]
        [--dry-run] [--force-unlock] [--worktree]
        [--only ID[,ID...] | [--from ID] [--until ID]]
                                        run only part of the phases
  serve [PHASE] [--through PHASE]       run (or resume) headless, hosting the run on .igris/igris.sock;
        [--mode MODE] [--worktree]      arise's run flags, plain log lines on stdout
        [--force-unlock]
        [--only ID[,ID...] | [--from ID] [--until ID]]
  stop                                  stop this project's live run (sessions stay open)
  done ID [--note TEXT]                 signal that a task is finished
  skip ID --reason TEXT                 signal that a task is skipped
  reset ID [--force]                    put a task back to ready/blocked; --force for done/skipped
  notify test [--event NAME]            send a sample of each notification to the configured channels
  config migrate [--yes]                remove deprecated settings from igris.toml (asks first, keeps a backup)
  adapt [--model sonnet|opus]           AI-assisted conversion with diff review
        [--plan PATH]
  draft goal [TEXT]                     draft a plan with a session and the review:
  draft spec FILE                       from a goal (interview), a spec file, an issue export,
  draft import FILE [--format github|linear|jira]
  draft extend [TEXT]                   or new phases and tasks for the current plan
        [--model sonnet|opus] [--plan PATH]
  update [--check] [--yes]              update igris with the package manager that installed it (asks first);
                                        --check only prints the current and latest versions and the method
  completion bash|zsh|fish              print a shell completion script
  help [COMMAND]                        this help, or a command's (same as igris COMMAND -h); also -h, --help
  version                               print the version; also -v, --version

Exit codes: 0 success, 1 failure, 2 usage error (SPEC §14).
Run "igris <command> -h" for the flags of a command.
`

// command describes one subcommand. setup registers its flags on fs and
// returns a validator that runs after parsing and reports usage errors.
type command struct {
	name  string
	setup func(fs *flag.FlagSet) (validate func(args []string) error)
	// exec runs the command after flags and arguments validated.
	exec func(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int
}

var runModes = engine.Modes

func commands() []command {
	return []command{
		{name: "init", setup: func(fs *flag.FlagSet) func([]string) error {
			fs.Bool("example", false, "also write the example plan when there is none")
			fs.String("template", "", "also write a starter plan when there is none: "+strings.Join(examples.TemplateNames(), ", "))
			return func(args []string) error {
				if !flagSet(fs, "template") {
					return atMost(args, 0)
				}
				if flagSet(fs, "example") {
					return errors.New("--template and --example can't be used together")
				}
				if name := fs.Lookup("template").Value.String(); name == "" {
					return fmt.Errorf("empty template name; templates: %s", strings.Join(examples.TemplateNames(), ", "))
				} else if _, ok := examples.Template(name); !ok {
					return project.UnknownTemplateError(name)
				}
				return atMost(args, 0)
			}
		}, exec: execInit},
		{name: "doctor", setup: doctorArgs, exec: execDoctor},
		{name: "check", setup: func(fs *flag.FlagSet) func([]string) error {
			fs.Bool("strict", false, "also lint the plan; exit 1 on any plan or config warning")
			return planFlags(0)(fs)
		}, exec: execCheck},
		{name: "phases", setup: planFlags(0), exec: execPhases},
		{name: "status", setup: planFlags(1), exec: execStatus},
		{name: "history", setup: historyArgs, exec: execHistory},
		{name: "report", setup: reportArgs, exec: execReport},
		{name: "usage", setup: usageArgs, exec: execUsage},
		{name: "arise", setup: func(fs *flag.FlagSet) func([]string) error {
			fs.Bool("no-tui", false, "plain log output, owner commands from stdin")
			fs.Bool("dry-run", false, "walk the phase with the fake backend, write nothing")
			return runFlags(fs)
		}, exec: execArise},
		{name: "serve", setup: runFlags, exec: execServe},
		{name: "stop", setup: func(fs *flag.FlagSet) func([]string) error { return maxArgs(fs, 0) }, exec: execStop},
		{name: "done", setup: func(fs *flag.FlagSet) func([]string) error {
			fs.String("note", "", "short note stored with the signal")
			return func(args []string) error {
				if err := exactArgs("ID", 1)(args); err != nil {
					return err
				}
				return taskIDArg(args[0])
			}
		}, exec: execDone},
		{name: "skip", setup: func(fs *flag.FlagSet) func([]string) error {
			reason := fs.String("reason", "", "why the task is skipped (required)")
			return func(args []string) error {
				if err := exactArgs("ID", 1)(args); err != nil {
					return err
				}
				if strings.TrimSpace(*reason) == "" {
					return fmt.Errorf("skip needs a reason: igris skip ID --reason TEXT")
				}
				return taskIDArg(args[0])
			}
		}, exec: execSkip},
		{name: "reset", setup: resetArgs, exec: execReset},
		{name: "notify", setup: notifyArgs, exec: execNotify},
		{name: "config", setup: configArgs, exec: execConfig},
		{name: "adapt", setup: adaptArgs, exec: execAdapt},
		{name: "draft", setup: draftArgs, exec: execDraft},
		{name: "update", setup: updateArgs, exec: execUpdate},
		{name: "completion", setup: completionArgs, exec: execCompletion},
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches args to a subcommand and returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	stderr = cleanWriter{stderr}
	if len(args) == 0 {
		if interactiveTerm() {
			return runApp(tui.Home{}, stdout, stderr)
		}
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		return execHelp(args[1:], stdout, stderr)
	case "version", "-v", "--version":
		if len(args) > 1 {
			fmt.Fprintf(stderr, "igris %s: takes no arguments; see `igris -h`\n", args[0])
			return exitUsage
		}
		fmt.Fprintf(stdout, "igris %s\n", igrisVersion())
		return exitOK
	}
	if args[0] == "__complete" { // hidden: not in the help, SPEC §14
		return execComplete(args[1:], stdout)
	}
	if args[0] == "hook" { // hidden: run by Claude Code hooks, SPEC §6.3
		return execHook(args[1:], os.Stdin, os.Stdout)
	}
	for _, c := range commands() {
		if c.name == args[0] {
			return runCommand(c, args[1:], stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "igris: unknown command %q\n\n%s", args[0], usageText)
	return exitUsage
}

// cleanWriter cleans what it writes of escape sequences and other control
// characters (textsafe.Clean). Errors carry text igris didn't write, such
// as a git hook's stderr or values read back from .igris/, so everything
// on stderr goes through it (SPEC §16, V06-07).
type cleanWriter struct{ w io.Writer }

func (c cleanWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(c.w, textsafe.Clean(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// execHelp prints the general help, or with a command's name that command's
// help, as `igris COMMAND -h` does.
func execHelp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usageText)
		return exitOK
	}
	if len(args) != 1 {
		fmt.Fprintf(stderr, "igris help: name at most one command\n\n%s", usageText)
		return exitUsage
	}
	name := args[0]
	for _, c := range commands() {
		if c.name == name {
			return runCommand(c, []string{"-h"}, stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "igris help: unknown command %q\n\n%s", name, usageText)
	return exitUsage
}

// helpRequested says args ask for the command's help: asked-for help goes
// to stdout, while a usage error's goes to stderr.
func helpRequested(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

func runCommand(c command, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("igris "+c.name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	if helpRequested(args) {
		fs.SetOutput(stdout)
	}
	synopsisUsage(fs, c.name)
	validate := c.setup(fs)
	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if err := validate(fs.Args()); err != nil {
		fmt.Fprintf(stderr, "igris %s: %v; see `igris %s -h`\n", c.name, err, c.name)
		return exitUsage
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

// synopsisUsage makes `igris NAME -h` print the command's lines from
// usageText, positional arguments included, then its flags.
func synopsisUsage(fs *flag.FlagSet, name string) {
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "Usage:\n%s\nFlags:\n", synopsis(name))
		fs.PrintDefaults()
	}
}

// synopsis is the command's entry in usageText, its line and the
// continuation lines under it, with "igris" in front.
func synopsis(name string) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(usageText, "\n") {
		switch {
		case strings.HasPrefix(l, "  "+name+" "):
			in = true
			b.WriteString("  igris " + l[2:] + "\n")
		case in && strings.HasPrefix(l, "    "):
			b.WriteString("      " + l + "\n")
		default:
			in = false
		}
	}
	return b.String()
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
