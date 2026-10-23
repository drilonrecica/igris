// Command igris runs a markdown task plan through Claude Code sessions.
package main

import (
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
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

// usageText is the help (SPEC §14): the commands by what you are doing,
// each with one line and its main flags.
const usageText = `igris · run a markdown task plan through Claude Code, one fresh session per task

Usage
  igris                        the home screen (on a terminal)
  igris <command> [flags]      ` + "`igris <command> -h`" + ` lists its flags

Start
  init           create igris.toml, .igris/, the allow rules    --example  --template NAME
  doctor         check this project and machine, read-only      --json

Inspect
  check          validate the plan                              --strict  --plan PATH  --json
  phases         phases, counts and parallelism                 --json
  status         tasks, the current run, unmet deps             [PHASE]  --json
  history        past runs, or one task's attempts              [TASK-ID]  -n N  --json
  report         one run as markdown                            [RUN]  --json
  usage          tokens and the rank audit                      [RUN | --since D]  --json

Run
  arise          run or resume with the TUI                     [PHASE]  --through  --mode  …
  serve          run headless, for attach and the web UI        [PHASE]  --http ADDR:PORT
  serve token    print the web UI's pairing URL                 --rotate
  attach         open this project's live run
  stop           stop the live run (sessions stay open)

Signal (from a session)
  done ID        the task is finished                           --note TEXT
  skip ID        the task is skipped                            --reason TEXT
  reset ID       back to ready or blocked                       --force

Plan with AI
  draft          write a plan from a goal, a spec or an export  goal | spec | import | extend
  adapt          convert a plan in another format               --model sonnet|opus

Maintain
  notify test    send a sample of each notification             --event NAME
  config migrate drop deprecated settings from igris.toml       --yes
  update         update igris through its package manager       --check  --yes
  completion     print a completion script                      bash | zsh | fish
  help, version

Exit codes  0 ok · 1 failure · 2 usage error
`

// synopses are the commands' full synopses, which `igris COMMAND -h`
// prints: a command's line and the lines under it.
const synopses = `
  init [--example | --template NAME]    create igris.toml, .igris/, .gitignore entry, Claude allow rules;
                                        --template a starter plan (go-cli, library, web-app)
  doctor [--json]                       read-only health check of this project and machine; exit 1 on a failure
  check [--plan PATH] [--json]          validate the plan; exit 0 valid, 1 invalid, 2 usage error
        [--strict]                      also lint the plan; exit 1 on any plan or config warning
  phases [--plan PATH] [--json]         list phases with task counts per status and their parallelism
  status [PHASE] [--plan PATH] [--json] tasks with status/rank/owner, current run, unmet deps
  history [TASK-ID] [-n N] [--json]     past runs from .igris/runs.jsonl; with a task ID, its attempts
  report [RUN] [--json]                 one run as markdown (or JSON): RUN is 1 (the newest, default), 2, … or a run ID
  usage [RUN | --since D] [--json]      token usage and the rank audit of one run (RUN as for report) or of the runs
                                        started since D (7d, 36h, 2026-10-01); with the last subscription quota
  arise [PHASE] [--through PHASE]       run (or resume) with the TUI
        [--mode default|accept|auto|plan[:MODE]|yolo|manual] [--no-tui]
        [--dry-run] [--force-unlock] [--worktree] [--parallel N]
        [--only ID[,ID...] | [--from ID] [--until ID]]
                                        run only part of the phases
  serve [PHASE] [--through PHASE]       run (or resume) headless, hosting the run on .igris/igris.sock;
        [--mode MODE] [--worktree]      arise's run flags, plain log lines on stdout
        [--force-unlock] [--parallel N]
        [--only ID[,ID...] | [--from ID] [--until ID]]
        [--http ADDR:PORT] [--listen-public]
                                        also on HTTP for the web UI and ntfy answers (in place of [serve] http)
  serve token [--rotate]                print the pairing URL of the HTTP listener (creating the token);
                                        --rotate replaces the token, unpairing every device
  attach                                open the run view of this project's live run
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
		{name: "serve", setup: serveFlags, exec: execServe},
		{name: "attach", setup: func(fs *flag.FlagSet) func([]string) error { return maxArgs(fs, 0) }, exec: execAttach},
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
			fmt.Fprintf(stderr, "igris %s: unexpected argument %q; run `igris help`\n", args[0], args[1])
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
	fmt.Fprintf(stderr, "igris: %s\n", unknownCommand(args[0]))
	return exitUsage
}

// unknownCommand answers a command igris doesn't have with the closest
// one within two edits, not the whole help (SPEC §14).
func unknownCommand(name string) string {
	best, dist := "", 3
	for _, c := range append(commandNames(), "help", "version") {
		if d := editDistance(name, c); d < dist {
			best, dist = c, d
		}
	}
	if best == "" {
		return fmt.Sprintf("unknown command %q; run `igris help`", name)
	}
	return fmt.Sprintf("unknown command %q; did you mean %s?", name, best)
}

func commandNames() []string {
	var out []string
	for _, c := range commands() {
		out = append(out, c.name)
	}
	return out
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
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
		fmt.Fprintf(stderr, "igris help: unexpected argument %q; name one command, e.g. `igris help status`\n", args[1])
		return exitUsage
	}
	name := args[0]
	for _, c := range commands() {
		if c.name == name {
			return runCommand(c, []string{"-h"}, stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "igris help: %s\n", unknownCommand(name))
	return exitUsage
}

func runCommand(c command, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("igris "+c.name, flag.ContinueOnError)
	// The flag package's own messages and usage are replaced: a usage
	// error is one line on stderr, asked-for help goes to stdout.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	validate := c.setup(fs)
	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandHelp(stdout, fs, c.name)
			return exitOK
		}
		fmt.Fprintf(stderr, "igris %s: %s; see `igris %s -h`\n", c.name, flagProblem(err), c.name)
		return exitUsage
	}
	if err := validate(fs.Args()); err != nil {
		fmt.Fprintf(stderr, "igris %s: %v; see `igris %s -h`\n", c.name, err, c.name)
		return exitUsage
	}
	return c.exec(fs, fs.Args(), stdout, stderr)
}

// flagNames finds the flag package's "-name" in its error messages.
var flagNames = regexp.MustCompile(`(^|[ :])-([A-Za-z][A-Za-z0-9-]*)`)

// flagProblem words a flag package error as igris writes flags: "unknown
// flag --jsn", "--plan needs a value".
func flagProblem(err error) string {
	msg := err.Error()
	if name, ok := strings.CutPrefix(msg, "flag provided but not defined: "); ok {
		return "unknown flag " + dashed(strings.TrimLeft(name, "-"))
	}
	if name, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
		return dashed(strings.TrimLeft(name, "-")) + " needs a value"
	}
	msg = strings.Replace(msg, " for flag ", " for ", 1)
	return flagNames.ReplaceAllStringFunc(msg, func(m string) string {
		lead, name, _ := strings.Cut(m, "-")
		return lead + dashed(name)
	})
}

// dashed is a flag's name as the help writes it: -n, --plan; quoted when
// it holds more than a flag name can, so the rest of the line survives.
func dashed(name string) string {
	d := "--" + name
	if len([]rune(name)) == 1 {
		d = "-" + name
	}
	if strings.Trim(name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.=") != "" {
		return fmt.Sprintf("%q", d)
	}
	return d
}

// flagArgs name the value each flag takes in the help.
var flagArgs = map[string]string{
	"plan": "PATH", "template": "NAME", "event": "NAME", "note": "TEXT", "reason": "TEXT",
	"mode": "MODE", "through": "PHASE", "only": "ID[,ID...]", "from": "ID", "until": "ID",
	"n": "N", "parallel": "N", "since": "D", "model": "MODEL", "format": "FORMAT", "http": "ADDR:PORT",
}

// printCommandHelp is `igris NAME -h`: the command's synopsis, then its
// flags written --flag.
func printCommandHelp(w io.Writer, fs *flag.FlagSet, name string) {
	fmt.Fprintf(w, "Usage\n%s", synopsis(name))
	var names []string
	var usages []string
	fs.VisitAll(func(f *flag.Flag) {
		n := dashed(f.Name)
		if !isBool(f) {
			n += " " + cmp.Or(flagArgs[f.Name], "VALUE")
		}
		u := f.Usage
		if f.DefValue != "" && f.DefValue != "0" && !isBool(f) {
			u += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		names, usages = append(names, n), append(usages, u)
	})
	if len(names) == 0 {
		return
	}
	width := 0
	for _, n := range names {
		width = max(width, len(n))
	}
	fmt.Fprint(w, "\nFlags\n")
	for i, n := range names {
		fmt.Fprintf(w, "  %-*s  %s\n", width, n, usages[i])
	}
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
		if f := fs.Lookup(name); f != nil && !isBool(f) {
			if i+1 == len(args) { // no value: the parser says so
				return flags
			}
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

// synopsis is the command's entry in synopses, its lines and the
// continuation lines under them, with "igris" in front.
func synopsis(name string) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(synopses, "\n") {
		switch {
		case strings.HasPrefix(l, "  "+name+" ") || l == "  "+name:
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
