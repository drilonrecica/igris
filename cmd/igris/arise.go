package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// Seams for tests: where owner commands come from, which backend and
// command runner a real run uses.
var (
	ariseStdin io.Reader = os.Stdin
	// ariseBackend, when set, replaces the backend named in the config;
	// nil builds the real one for the project (project.NewBackend).
	ariseBackend func(*config.Config) (backend.Backend, error)
	ariseRunner  runner.Runner // nil means real processes
	ariseGetenv  = os.Getenv
	// compatWarnings checks the Claude Code and herdr versions (SPEC §11.4).
	compatWarnings = checks.ToolVersions
	// igrisSelf finds the running igris and the one in PATH, for the
	// igris-path check (SPEC §14).
	igrisSelf = runner.Self
	// serveSpawn starts the igris serve of a run arise or home starts;
	// nil is the running igris binary (project.SpawnServe).
	serveSpawn project.Spawn
)

// projectEnv hands this package's seams to the project layer, so the CLI
// and the home screen talk to the same (in tests: fake) world.
func projectEnv() project.Env {
	return project.Env{
		Getenv: ariseGetenv, Runner: ariseRunner, Backend: ariseBackend, Versions: compatWarnings,
		Clock: adaptClock, Format: formatEvent, Version: igrisVersion(), Self: igrisSelf,
		Build: updateBuild(), UpdateAPI: updateAPI, Environ: updateEnviron, Spawn: serveSpawn,
	}
}

type ariseFlags struct {
	phase, through, mode       string
	noTUI, dryRun, forceUnlock bool
	worktree                   bool
	parallel                   int             // --parallel N; 0 is unset
	sel                        state.Selection // --only, --from, --until
	lines                      <-chan string   // --no-tui: the owner's stdin lines
}

func (f ariseFlags) request() report.RunRequest {
	return report.RunRequest{Phase: f.phase, Through: f.through, Mode: f.mode, Selection: f.sel, Worktree: f.worktree, Parallel: f.parallel}
}

// parseSelection reads --only, --from and --until (SPEC §5.5). Combining
// --only with the others, an empty list and a malformed ID are usage errors;
// whether the IDs are in the plan is checked against it later.
func parseSelection(fs *flag.FlagSet) (state.Selection, error) {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	value := func(name string) string { return fs.Lookup(name).Value.String() }
	checkID := func(name, id string) error {
		if !plan.ValidID(id) {
			return fmt.Errorf("invalid task ID %q in --%s: IDs are letters, digits, '.', '_' and '-' (e.g. M0-01)", id, name)
		}
		return nil
	}
	var sel state.Selection
	if set["only"] {
		if set["from"] || set["until"] {
			return sel, errors.New("--only can't be combined with --from or --until; use one or the other")
		}
		if strings.Trim(value("only"), " \t,") == "" {
			return sel, errors.New("--only needs at least one task ID, e.g. --only M1-03,M1-05")
		}
		seen := map[string]bool{}
		for _, id := range strings.Split(value("only"), ",") {
			id = strings.TrimSpace(id)
			if err := checkID("only", id); err != nil {
				return sel, err
			}
			if !seen[id] {
				seen[id] = true
				sel.Only = append(sel.Only, id)
			}
		}
	}
	for _, name := range []string{"from", "until"} {
		if !set[name] {
			continue
		}
		id := strings.TrimSpace(value(name))
		if err := checkID(name, id); err != nil {
			return sel, err
		}
		if name == "from" {
			sel.From = id
		} else {
			sel.Until = id
		}
	}
	return sel, nil
}

// runFlags registers the run flags `arise` and `serve` share and returns
// their validator.
func runFlags(fs *flag.FlagSet) func(args []string) error {
	fs.String("through", "", "last phase to run")
	mode := fs.String("mode", "", "run mode: "+strings.Join(runModes, "|"))
	fs.Bool("force-unlock", false, "clear a stale lock left by a run that is no longer alive")
	fs.Bool("worktree", false, "run in a git worktree on a branch of its own, igris/<run-id>")
	fs.String("parallel", "", "tasks at once in a parallel phase, 1 to 8 (in place of [parallel] max)")
	fs.String("only", "", "run only these tasks, in plan order: ID[,ID...]")
	fs.String("from", "", "start the run at task ID")
	fs.String("until", "", "stop the run after task ID")
	return func(args []string) error {
		if *mode != "" && !contains(runModes, *mode) {
			return fmt.Errorf("invalid --mode %q (want %s)", *mode, strings.Join(runModes, "|"))
		}
		if _, err := parseSelection(fs); err != nil {
			return err
		}
		if _, err := parseParallel(fs); err != nil {
			return err
		}
		return atMost(args, 1)
	}
}

// parseParallel reads --parallel (SPEC §14): 0 when not given, else a
// whole number from 1 to config.MaxParallel; anything else is a usage error.
func parseParallel(fs *flag.FlagSet) (int, error) {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "parallel" })
	if !given {
		return 0, nil
	}
	v := fs.Lookup("parallel").Value.String()
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 || n > config.MaxParallel {
		return 0, fmt.Errorf("invalid --parallel %s: use a whole number from 1 to %d", v, config.MaxParallel)
	}
	return n, nil
}

// runFlagValues reads runFlags' flags and the phase argument.
func runFlagValues(fs *flag.FlagSet, args []string) ariseFlags {
	f := ariseFlags{
		through:     fs.Lookup("through").Value.String(),
		mode:        fs.Lookup("mode").Value.String(),
		forceUnlock: fs.Lookup("force-unlock").Value.String() == "true",
		worktree:    fs.Lookup("worktree").Value.String() == "true",
	}
	if len(args) > 0 {
		f.phase = args[0]
	}
	f.sel, _ = parseSelection(fs)     // checked when the flags were parsed
	f.parallel, _ = parseParallel(fs) // checked when the flags were parsed
	return f
}

func execArise(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	f := runFlagValues(fs, args)
	f.noTUI = fs.Lookup("no-tui").Value.String() == "true"
	f.dryRun = fs.Lookup("dry-run").Value.String() == "true"
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris arise: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	proj, err := loadProject("")
	if err != nil {
		return fail("%v", err)
	}
	if wantWizard(f, proj.Root) {
		return runApp(tui.Wizard{Mode: f.mode, Through: f.through, ForceUnlock: f.forceUnlock, Worktree: f.worktree, Parallel: f.parallel}, stdout, stderr)
	}
	out := &lockedWriter{w: stdout}
	if f.dryRun {
		return dryRun(proj, f.request(), out, stderr)
	}
	// A wrong ID in --only/--from/--until fails before anything is asked.
	if err := proj.CheckSelection(f.request()); err != nil {
		return fail("%v", err)
	}
	// The same engine the home screen builds (internal/project).
	l, err := proj.Launch(f.request())
	if err != nil {
		return fail("%v", err)
	}

	// Ctrl-C stops a --no-tui run; the session stays open (SPEC §13). With
	// the TUI the run is served: a signal, a closed terminal or a dropped
	// ssh connection included, only detaches from it.
	ctx, cancel, _ := interruptContext()
	if !f.noTUI {
		ctx, cancel, _ = signalContext(attachSignals...)
	}
	defer cancel()
	pre := l.Prelaunch(ctx)
	// With --no-tui, stdin carries the owner's commands for the whole run.
	// The TUI owns the terminal once it starts, so until then each answer
	// is read on demand and nothing keeps reading stdin.
	var ask func() (string, bool)
	if f.noTUI {
		lines := readLines(ariseStdin)
		ask = func() (string, bool) { return nextLine(ctx, lines) }
		f.lines = lines
	} else {
		r := bufio.NewReader(ariseStdin)
		ask = func() (string, bool) { return readLine(ctx, r) }
	}
	for _, c := range pre.Warnings {
		fmt.Fprintf(out, "warning: %s\n", c)
		if c.Confirm && !confirm(out, ask, "Start the run anyway?") {
			return fail("not confirmed; nothing was started")
		}
	}

	conf := report.Confirmations{ForceUnlock: f.forceUnlock}
	for {
		var res engine.Result
		var err error
		if f.noTUI {
			start := newRunStart(proj, f)
			res, err = runOnce(ctx, l.Options(conf, func(ev engine.Event) { printEvent(out, ev, start) }), f.lines, out)
		} else {
			code, started, serr := runServed(ctx, proj, l, conf, out, stderr)
			if started {
				return code
			}
			err = serr
		}
		if asked, refusal := askStartUp(err, &conf, out, ask, !f.worktree && !proj.Cfg.Run.WorktreeOn()); asked {
			if refusal != "" {
				return fail("%s", refusal)
			}
			continue
		}
		switch {
		case err != nil:
			return fail("%v%s", err, servedHint(err, proj.Root))
		case res.Outcome == engine.Stuck:
			return exitFail
		default:
			printNotRun(out, res)
			return exitOK
		}
	}
}

// askStartUp asks the owner the question a run that refused to start with
// err needs answered (SPEC §5.2 drift, §13 a changed igris.toml, §7.3
// skip-permissions) and records a yes in conf. asked is false when err is
// no such question, or was already answered; refusal says why nothing was
// started when the owner declined. tip adds the --worktree tip to the
// skip-permissions question.
func askStartUp(err error, conf *report.Confirmations, out io.Writer, ask func() (string, bool), tip bool) (asked bool, refusal string) {
	const declined = "not confirmed; nothing was started"
	var drift *engine.DriftError
	switch {
	case errors.As(err, &drift) && !conf.Drift:
		fmt.Fprintf(out, "The plan's ready/blocked cells don't match its dependencies; igris's first write would change:\n")
		for _, c := range drift.Changes {
			fmt.Fprintf(out, "  %s\n", c)
		}
		if !confirm(out, ask, "Let igris fix them?") {
			return true, declined
		}
		conf.Drift = true
	case errors.Is(err, engine.ErrConfigUnconfirmed) && !conf.Config:
		// A session can edit igris.toml; the owner looks first.
		fmt.Fprintf(out, "warning: igris.toml changed during the last run, which kept the configuration it started with; a session may have edited it.\nReview it (git diff igris.toml) before this run uses it.\n")
		if !confirm(out, ask, "Use the changed igris.toml?") {
			return true, declined
		}
		conf.Config = true
	case errors.Is(err, engine.ErrYoloUnconfirmed) && !conf.Yolo:
		// Typed confirmation, every run.
		fmt.Fprintf(out, "%v\n%s\n", err, engine.SkipNote)
		if tip {
			fmt.Fprintln(out, "Tip: --worktree keeps the run's changes on a branch of their own, away from your checkout.")
		}
		if !typed(out, ask, engine.YoloPhrase) {
			return true, "skip-permissions mode not confirmed; nothing was started"
		}
		conf.Yolo = true
	default:
		return false, ""
	}
	return true, ""
}

// interruptContext is cancelled by the first SIGINT or SIGTERM, which
// stops the run. It then stops catching them, so a second one exits igris
// at once, as the default handling does, also while the run is still
// winding down (the final notifications, SPEC §10). release stops catching
// them without ending ctx: once the owner has quit the TUI, the first
// Ctrl-C on the plain terminal exits while the run winds down.
func interruptContext() (ctx context.Context, cancel context.CancelFunc, release func()) {
	return signalContext(os.Interrupt, syscall.SIGTERM)
}

// signalContext is interruptContext for the signals in which.
func signalContext(which ...os.Signal) (ctx context.Context, cancel context.CancelFunc, release func()) {
	ctx, cancelCtx := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, which...)
	release = func() { signal.Stop(sigs) } // a no-op once stopped
	go func() {
		select {
		case <-sigs:
		case <-ctx.Done():
		}
		// Before ctx ends, so that whoever sees it end can count on the
		// default handling being back.
		release()
		cancelCtx()
	}()
	cancel = func() {
		release()
		cancelCtx()
	}
	return ctx, cancel, release
}

// stoppingText is what igris says on the plain terminal while a run the
// owner left winds down.
const stoppingText = "stopping… (Ctrl-C again to quit now)"

// windingDown is called once the TUI is gone: it stops catching Ctrl-C
// (release) and, when the run is still going (waiting), says it is
// stopping and how to quit at once.
func windingDown(release func(), waiting bool, out io.Writer) {
	release()
	if waiting {
		fmt.Fprintln(out, stoppingText)
	}
}

// ariseUI shows the TUI; a seam for tests.
var ariseUI = tui.Run

// runServed starts the run with the owner's answers conf in a background
// igris serve and shows its run view, attached (SPEC §14). started is
// false when the run refused to start (err says why, maybe a question to
// ask and try again): nothing runs then. Leaving the view detaches; the
// run goes on in serve. code is arise's exit code once started: the run's
// result when it ended while attached, else 0.
func runServed(ctx context.Context, proj *project.Project, l *project.Launch, conf report.Confirmations, out, stderr io.Writer) (code int, started bool, err error) {
	feed := tui.NewFeed()
	s, err := l.Serve(ctx, conf, feed)
	if err != nil {
		return 0, false, err
	}
	uiErr := ariseUI(ctx, proj.AttachOptions(feed, s, igrisVersion()))
	s.Close()
	if uiErr != nil {
		fmt.Fprintf(stderr, "igris arise: the TUI failed: %v; the run goes on in igris serve (`igris attach` shows it, `igris stop` stops it)\n", uiErr)
		return exitFail, true, nil
	}
	select {
	case <-feed.Ended():
		waitLockFree(ctx, proj.Root)
		res, err := feed.Result()
		return tuiExit(res, err, out, stderr), true, nil
	default:
	}
	fmt.Fprintln(out, tui.ExitText(engine.Result{}, tui.ErrDetached))
	return exitOK, true, nil
}

// servedHint adds to a lock held by a live igris serve here how to reach
// its run.
func servedHint(err error, root string) string {
	var locked *state.LockedError
	if errors.As(err, &locked) && !locked.Remote && serve.Answers(context.Background(), root) {
		return "; igris serve hosts that run: `igris attach` shows it"
	}
	return ""
}

// tuiExit reports how a run shown in the TUI ended, once the terminal is
// back to normal.
func tuiExit(res engine.Result, err error, out, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintf(stderr, "igris arise: %v\n", err)
		return exitFail
	}
	// The same words as home's status line after a run (tui.ExitText).
	fmt.Fprintln(out, tui.ExitText(res, nil))
	printNotRun(out, res)
	for _, l := range res.Worktree.Closing() {
		fmt.Fprintln(out, l)
	}
	if res.Outcome == engine.Stuck {
		return exitFail
	}
	return exitOK
}

// printNotRun is the run's final summary of the slice's tasks it left
// because their deps are unmet (SPEC §5.5).
func printNotRun(out io.Writer, res engine.Result) {
	for _, w := range res.NotRun {
		fmt.Fprintln(out, "not run · "+w.String())
	}
}

// runOnce runs one engine with opts while stdin feeds it owner commands.
// Once it returns, stdin is free again for confirmations.
func runOnce(ctx context.Context, opts engine.Options, lines <-chan string, out io.Writer) (engine.Result, error) {
	eng, err := engine.New(opts)
	if err != nil {
		return engine.Result{}, err
	}
	cmdCtx, stopCmds := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ownerCommands(cmdCtx, lines, eng, out)
	}()
	res, err := eng.Run(ctx)
	stopCmds()
	wg.Wait()
	return res, err
}

// commandRunner is the runner for igris's own checks.
func commandRunner() runner.Runner {
	if ariseRunner != nil {
		return ariseRunner
	}
	return runner.Exec{}
}

// confirm asks a y/N question on stdin; anything but y/yes is no.
func confirm(out io.Writer, ask func() (string, bool), question string) bool {
	fmt.Fprintf(out, "%s [y/N]\n", question)
	answer, ok := ask()
	answer = strings.ToLower(strings.TrimSpace(answer))
	return ok && (answer == "y" || answer == "yes")
}

// typed asks the owner to type phrase exactly.
func typed(out io.Writer, ask func() (string, bool), phrase string) bool {
	fmt.Fprintf(out, "Type %q to confirm:\n", phrase)
	answer, ok := ask()
	return ok && strings.TrimSpace(answer) == phrase
}

// nextLine returns the next stdin line; false when stdin ended or ctx is
// done.
func nextLine(ctx context.Context, lines <-chan string) (string, bool) {
	select {
	case <-ctx.Done():
		return "", false
	case line, ok := <-lines:
		return line, ok
	}
}

// readLine reads one line from r, giving up when ctx ends. The read goes
// on in the background then, but igris is about to exit.
func readLine(ctx context.Context, r *bufio.Reader) (string, bool) {
	type result struct {
		line string
		ok   bool
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		ch <- result{strings.TrimRight(line, "\r\n"), err == nil || line != ""}
	}()
	select {
	case <-ctx.Done():
		return "", false
	case res := <-ch:
		return res.line, res.ok
	}
}

// loadProject opens the project the working directory belongs to, for a
// command that works in it (see project.Load).
func loadProject(explicitPlan string) (*project.Project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return project.Load(cwd, explicitPlan, projectEnv())
}

// lockedWriter serializes the run's output and the command prompts.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// readLines delivers r's lines until it ends.
func readLines(r io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			ch <- sc.Text()
		}
	}()
	return ch
}

// ownerCommands turns stdin lines into engine commands until stdin ends or
// ctx is done (SPEC §14 --no-tui).
func ownerCommands(ctx context.Context, lines <-chan string, eng *engine.Engine, out io.Writer) {
	next := func() (string, bool) { return nextLine(ctx, lines) }
	for {
		line, ok := next()
		if !ok {
			if ctx.Err() == nil {
				// Stdin ended: nobody is left to answer the batch question,
				// which keeps the run's commits (SPEC §6.5).
				eng.Unattended()
			}
			return
		}
		c, act, err := parseCommand(line)
		if err == nil && c.Kind == engine.CmdTaskMode && eng.IsUserTask(c.Task) {
			err = fmt.Errorf("%s is a user task; it has no session", c.Task)
		}
		switch {
		case err != nil:
			fmt.Fprintf(out, "%v\n", err)
		case act == actHelp:
			fmt.Fprint(out, commandHelp)
		case act == actConfirmYolo:
			fmt.Fprintf(out, "Skip-permissions mode runs sessions with --dangerously-skip-permissions. Type %q to confirm:\n", engine.YoloPhrase)
			if answer, ok := next(); !ok || strings.TrimSpace(answer) != engine.YoloPhrase {
				fmt.Fprintln(out, "not confirmed; the mode is unchanged")
				continue
			}
			c.Yes = true
			eng.Send(c)
		case act == actSend:
			eng.Send(c)
		}
	}
}

// commandAction says what to do with a parsed stdin line.
type commandAction int

const (
	actNone        commandAction = iota // empty line
	actSend                             // send the command
	actHelp                             // print the command list
	actConfirmYolo                      // ask for the typed confirmation, then send
)

const commandHelp = `commands:
  y | n                      answer the question igris asked
  all                        answer the commit question with Commit all: commit, and no more commit questions this run
  reset yes|no [task]        answer a reset request (the oldest, or that task's)
  done [note]                mark the current task done (an agent task is not verified)
  skip <reason>              skip the current task
  retry [continue|fresh]     replace the session: continue its conversation, or start fresh (default)
  resolve [task]             open a resolver session for a task that didn't integrate (the current one without a task)
  pause                      pause after the current task (again to resume)
  start                      start the held task anyway while a usage limit holds it (a hit still holds)
  budget <value>|continue    answer a budget reached: a new value for this run (30M, $60), or let it go on
  stop                       stop igris now; the session stays open
  mode <m>                   run mode for the next sessions: default|accept|auto|plan[:m]|yolo|manual
  mode <task> <m>            mode for that task's next session, over its Mode column
  help                       this list
`

// parseCommand reads one --no-tui stdin line (SPEC §14).
func parseCommand(line string) (engine.Command, commandAction, error) {
	word, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
	rest = strings.TrimSpace(rest)
	noArgs := func(c engine.Command) (engine.Command, commandAction, error) {
		if rest != "" {
			return engine.Command{}, actNone, fmt.Errorf("%s takes no argument; type help", word)
		}
		return c, actSend, nil
	}
	switch strings.ToLower(word) {
	case "":
		return engine.Command{}, actNone, nil
	case "y", "yes":
		return noArgs(engine.Command{Kind: engine.CmdAnswer, Yes: true})
	case "n", "no":
		return noArgs(engine.Command{Kind: engine.CmdAnswer})
	case "all":
		// Commit all (SPEC §6.5): this commit, and auto for the rest of
		// the run.
		return noArgs(engine.Command{Kind: engine.CmdAnswer, Yes: true, Text: engine.CommitAllText})
	case "done":
		return engine.Command{Kind: engine.CmdDone, Text: rest}, actSend, nil
	case "skip":
		if rest == "" {
			return engine.Command{}, actNone, errors.New("skip needs a reason: skip <reason>")
		}
		return engine.Command{Kind: engine.CmdSkip, Text: rest}, actSend, nil
	case "retry":
		switch strings.ToLower(rest) {
		case "", "fresh":
			return engine.Command{Kind: engine.CmdRetry}, actSend, nil
		case "continue":
			return engine.Command{Kind: engine.CmdRetry, Continue: true}, actSend, nil
		}
		return engine.Command{}, actNone, fmt.Errorf("retry takes continue or fresh, not %q", rest)
	case "reset":
		// Answers a reset request (SPEC §6.2), which can wait beside
		// another question.
		c := engine.Command{Kind: engine.CmdAnswer, Question: engine.QuestionConfirmReset}
		args := strings.Fields(rest)
		if len(args) == 2 && plan.ValidID(args[1]) {
			c.Task = args[1]
		}
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "y", "yes":
				c.Yes = true
			case "n", "no":
			default:
				args = nil
			}
		}
		if len(args) == 0 || len(args) > 2 || (len(args) == 2 && c.Task == "") {
			return engine.Command{}, actNone, errors.New("reset takes yes or no, and the task if more than one reset waits: reset yes|no [task]")
		}
		return c, actSend, nil
	case "pause":
		return noArgs(engine.Command{Kind: engine.CmdPause})
	case "resolve":
		// The resolver session for a task that didn't integrate (SPEC
		// §6.11); the current task without one.
		if rest != "" && !plan.ValidID(rest) {
			return engine.Command{}, actNone, fmt.Errorf("resolve takes a task ID, not %q", rest)
		}
		return engine.Command{Kind: engine.CmdResolve, Task: rest}, actSend, nil
	case "start":
		return noArgs(engine.Command{Kind: engine.CmdLimitStart})
	case "budget":
		if rest == "" || strings.ContainsRune(rest, ' ') {
			return engine.Command{}, actNone, errors.New("budget takes a new value or continue: budget 30M, budget $60, budget continue")
		}
		return engine.Command{Kind: engine.CmdBudget, Text: rest}, actSend, nil
	case "stop":
		return noArgs(engine.Command{Kind: engine.CmdStop})
	case "mode":
		c := engine.Command{Kind: engine.CmdMode}
		args := strings.Fields(rest)
		switch len(args) {
		case 1:
			c.Text = strings.ToLower(args[0])
		case 2:
			c.Kind, c.Task, c.Text = engine.CmdTaskMode, args[0], strings.ToLower(args[1])
		}
		if !engine.ValidMode(c.Text) {
			return engine.Command{}, actNone, fmt.Errorf("mode takes [task] and one of %s", engine.ModeList)
		}
		m := c.Text
		if engine.SkipsPermissions(m) {
			return c, actConfirmYolo, nil
		}
		return c, actSend, nil
	case "help", "?":
		return engine.Command{}, actHelp, nil
	}
	return engine.Command{}, actNone, fmt.Errorf("unknown command %q; type help", word)
}

// limitHoldLine is the hold's line: the window, its use and the reset as a
// clock time (the line is stamped, so no countdown), `◌ paused before
// M1-04 · 5h limit 96% · resumes 09:00`.
func limitHoldLine(ev engine.Event) string {
	s := look.GlyphWaiting + " paused before " + ev.Task
	if l := ev.Limit; l != nil {
		s += " · " + l.Label() + " · resumes " + look.Clock(l.ResetsAt.Local())
	} else {
		s += " · " + ev.Detail
	}
	return s
}

// printEvent writes ev as plain log lines stamped with the local time.
func printEvent(out io.Writer, ev engine.Event, start runStart) {
	logLines(out, ev, formatEvent(ev), start)
}

// runStart is what the run's first log line adds to its scope: how many
// tasks it has to do and its mode (SPEC §14).
type runStart struct {
	proj *project.Project
	mode string // "" when the flags don't say, as for a resumed run
}

func newRunStart(proj *project.Project, f ariseFlags) runStart {
	return runStart{proj: proj, mode: runMode(f, proj.Cfg)}
}

// tasks counts the run's tasks: its slice's, else the unfinished tasks
// of its phases; -1 when the plan can't be read.
func (r runStart) tasks(ev engine.Event) int {
	if len(ev.Slice) > 0 {
		return len(ev.Slice)
	}
	p, err := plan.Load(r.proj.PlanPath, r.proj.Cfg.PlanOptions(r.proj.Root))
	if err != nil {
		return -1
	}
	scope, _, _ := strings.Cut(strings.TrimPrefix(ev.Detail, "phase "), "; ")
	n := 0
	for _, id := range strings.Split(scope, ", ") {
		if ph := p.Phase(id); ph != nil {
			for _, t := range ph.Tasks {
				if t.Status != plan.Done && t.Status != plan.Skipped {
					n++
				}
			}
		}
	}
	return n
}

// logLines writes an event's lines stamped with the local time (SPEC §14):
// the run's first line is "Arise. phase M1 · 7 tasks · mode plan".
func logLines(out io.Writer, ev engine.Event, lines []string, start runStart) {
	if ev.Kind == engine.RunStarted && len(lines) > 0 && start.proj != nil {
		if n := start.tasks(ev); n >= 0 {
			lines[0] += " · " + look.Count(n, "task", "tasks")
		}
		if start.mode != "" {
			lines[0] += " · mode " + start.mode
		}
	}
	for _, line := range lines {
		fmt.Fprintf(out, "%s %s\n", look.ClockSeconds(ev.At.Local()), line)
	}
}

// runMode is the mode a run starts in, as far as the flags tell: --mode,
// else igris.toml's for a new run; "" for a resumed run, which keeps the
// mode it had.
func runMode(f ariseFlags, cfg *config.Config) string {
	if f.mode != "" || f.phase == "" {
		return f.mode
	}
	return cfg.DefaultMode
}

// formatEvent renders ev for the plain log in the design language (SPEC
// §15.1, §15.4), the TUI log's lines with the typing hints after them.
// Most events are one line.
func formatEvent(ev engine.Event) []string {
	id := ev.Task
	switch ev.Kind {
	case engine.RunStarted:
		// "phase A; only A-2" reads "Arise. phase A · only A-2".
		return []string{"Arise. " + strings.Replace(ev.Detail, "; ", " · ", 1)}
	case engine.WorktreeReady, engine.WorktreeLeft:
		return []string{ev.Detail}
	case engine.PhaseStarted:
		return []string{"phase " + ev.Phase + " started"}
	case engine.TaskStarted:
		return []string{startedLine(ev)}
	case engine.TaskResumed:
		return []string{id + " resumed · " + ev.Detail}
	case engine.SessionOpened:
		return []string{id + " session open" + yoloBadge(ev.Mode)}
	case engine.YourTurn:
		return []string{
			needsLine(id, "your turn · "+ev.Detail),
			"  do it outside igris (no session), then type `done [note]` or `skip <reason>` (or run `igris done " + id + "` anywhere in the project)",
		}
	case engine.NeedsYou:
		switch ev.Reason {
		case state.ReasonBlocked, state.ReasonIdle, state.ReasonUsageLimit:
			// Why, in the words of SPEC §10, then the agent's state.
			return []string{needsLine(id, engine.NeedsWords(ev.Reason, ev.Cause, ev.Tool)), "  " + ev.Detail}
		}
		return []string{needsLine(id, ev.Detail)}
	case engine.SessionLost:
		return []string{needsLine(id, ev.Detail)}
	case engine.NeedsYouClear:
		return []string{id + " is working again"}
	case engine.AgentWaiting:
		return []string{look.GlyphWaiting + " " + id + " waits on background work; Claude Code resumes it when that is done"}
	case engine.AgentWaitingOver, engine.UsageRead, engine.QuotaRead, engine.BudgetRead:
		return nil // the card shows it; the next event says what followed
	case engine.Nudged:
		// The reminder, or why igris told it to continue after a usage limit.
		if ev.Detail == "" {
			return []string{id + " stopped without igris done; igris reminded it once"}
		}
		return []string{id + " " + strings.TrimPrefix(ev.Detail, "the agent ")}
	case engine.SessionLimited:
		return []string{look.GlyphWaiting + " " + id + " " + ev.Detail}
	case engine.SessionLimitOver:
		return []string{id + " " + ev.Detail}
	case engine.LimitHold:
		return []string{limitHoldLine(ev) + "; type `start` to start it anyway"}
	case engine.LimitResume:
		return []string{"resumed · " + ev.Detail}
	case engine.BudgetExceeded:
		return []string{look.GlyphNeedsYou + " " + ev.Detail}
	case engine.BudgetRaised, engine.CommitPolicy, engine.CommitsUndone:
		return []string{ev.Detail}
	case engine.TaskOverdue:
		return []string{look.GlyphWarn + " " + id + " overdue · " + ev.Detail + "; its session keeps running"}
	case engine.RankMismatch:
		return []string{look.GlyphWarn + " " + id + " rank mismatch · " + ev.Detail + "; its session keeps running"}
	case engine.Asked:
		// The line says what waits; the question itself and how to answer
		// it follow, indented.
		lines := []string{needsLine(id, ev.Question.Kind())}
		switch ev.Question {
		case engine.QuestionSessionLost:
			return append(lines, "  "+ev.Detail, "  type `retry continue`, `retry fresh`, `done [note]`, `skip <reason>` or `stop`")
		case engine.QuestionHookFailed:
			return append(lines, "  "+ev.Detail, "  type `retry`, `done [note]`, `skip <reason>` or `stop`")
		case engine.QuestionIntegration:
			lines = append(lines, "  "+ev.Detail)
			for _, l := range ev.Output {
				lines = append(lines, "  | "+l)
			}
			if ev.Resolve {
				return append(lines, "  type `resolve` (a resolver session), `retry fresh`, `done [note]`, `skip <reason>` or `stop`")
			}
			return append(lines, "  type `retry fresh`, `done [note]`, `skip <reason>` or `stop`")
		case engine.QuestionFilesInTheWay:
			lines = append(lines, "  "+ev.Detail)
			for _, l := range ev.Output {
				lines = append(lines, "  | "+l)
			}
			return append(lines, "  move them, then type `retry` to try again, or `done [note]`, `skip <reason>` or `stop`")
		case engine.QuestionConfirmReset:
			return append(lines, "  "+ev.Detail, "  type `reset yes "+id+"` or `reset no "+id+"`")
		case engine.QuestionBudget:
			// The run's question (SPEC §6.9): no task.
			return []string{needsLine("", ev.Question.Kind()), "  " + ev.Detail, "  type `budget <value>` to raise it for this run (30M, $60), `budget continue` or `stop`"}
		case engine.QuestionCommitBatch:
			// The run's, once it ended (SPEC §6.5): its commits listed.
			lines = []string{needsLine("", ev.Question.Kind()), "  " + ev.Detail}
			for _, l := range ev.Output {
				lines = append(lines, "  | "+l)
			}
			if ev.Undo {
				return append(lines, "  type `y` to keep them or `n` to undo them (git reset --soft: the changes stay, uncommitted); leaving keeps them")
			}
			return append(lines, "  type `y` to keep them; leaving keeps them")
		case engine.QuestionCommit:
			return append(lines, "  "+ev.Detail+" [y/n/all]")
		default:
			return append(lines, "  "+ev.Detail+" [y/n]")
		}
	case engine.Retrying:
		return []string{id + " new session (" + ev.Detail + ")"}
	case engine.VerifyStarted:
		return []string{id + " verifying (" + ev.Verify + ") · " + ev.Detail}
	case engine.VerifyPassed:
		return []string{look.GlyphDone + " " + id + " verify passed (" + ev.Verify + ")"}
	case engine.VerifyFailed:
		return []string{look.GlyphFail + " " + id + " verify failed · " + ev.Detail}
	case engine.VerifyLimit:
		return []string{look.GlyphFail + " " + id + " " + ev.Detail}
	case engine.Committed:
		return []string{look.GlyphDone + " " + id + " committed · " + ev.Detail}
	case engine.NotCommitted:
		return []string{id + " not committed · " + ev.Detail}
	case engine.Integrated:
		return []string{look.GlyphDone + " " + id + " integrated " + ev.Detail}
	case engine.IntegrationFailed, engine.HookFailed:
		lines := []string{look.GlyphFail + " " + id + " " + ev.Detail}
		for _, l := range ev.Output {
			lines = append(lines, "  | "+l)
		}
		return lines
	case engine.ResolverOpened:
		return nil // the didn't-integrate line said it
	case engine.ResolverDone:
		return []string{look.GlyphDone + " " + id + "'s " + ev.Detail}
	case engine.ResolverFailed:
		lines := []string{look.GlyphFail + " " + id + "'s " + ev.Detail}
		for _, l := range ev.Output {
			lines = append(lines, "  | "+l)
		}
		return lines
	case engine.Overlap:
		return []string{look.GlyphWarn + " overlap · " + ev.Detail}
	case engine.TaskDone:
		return []string{withNote(look.GlyphDone+" "+id+" done", ev.Detail)}
	case engine.TaskSkipped:
		return []string{withNote(look.GlyphSkipped+" "+id+" skipped", ev.Detail)}
	case engine.TaskReset:
		return []string{"reset " + ev.Detail}
	case engine.ResetDropped:
		return []string{ev.Detail}
	case engine.PhaseDone:
		return []string{look.GlyphDone + " phase " + ev.Phase + " complete"}
	case engine.PhaseStuck:
		lines := []string{look.GlyphWarn + " phase " + ev.Phase + " is stuck · unfinished tasks, none can start"}
		for _, w := range ev.Waiting {
			lines = append(lines, "  "+w.String())
		}
		return lines
	case engine.NotRun:
		return notRunLines(ev)
	case engine.PauseOn:
		return []string{"pause after task on (type `pause` again to turn it off)"}
	case engine.PauseOff:
		return []string{"pause after task off"}
	case engine.Paused:
		return []string{"paused before " + id + "; type `pause` to continue"}
	case engine.ModeChanged:
		return []string{"next sessions in mode " + ev.Detail + yoloBadge(ev.Detail)}
	case engine.TaskModeChanged:
		return []string{id + "'s next session in mode " + ev.Detail + yoloBadge(ev.Detail)}
	case engine.PlanChanged:
		return []string{look.GlyphWarn + " " + ev.Detail}
	case engine.ConfigChanged, engine.ConfigRestored, engine.StaleSignal, engine.StraySignal:
		return []string{ev.Detail}
	case engine.HookStarted:
		return []string{id + " running " + ev.Detail + " hook…"}
	case engine.Warning:
		return []string{look.GlyphWarn + " " + ev.Detail}
	case engine.RunFailed:
		return []string{look.GlyphFail + " run failed · " + ev.Detail}
	case engine.RunStopped:
		return []string{"run stopped · " + ev.Detail}
	}
	return []string{strings.TrimSpace(string(ev.Kind) + " " + id + " " + ev.Detail)}
}

// startedLine is a task's start: `M1-03 started · sonnet · mode plan`,
// with `rank → model` only when the model isn't the rank's own name, and
// `M1-06 started · user task` for a user task (SPEC §15.1).
func startedLine(ev engine.Event) string {
	if ev.Model == "" {
		return ev.Task + " started · user task"
	}
	model := ev.Model
	if ev.Rank != "" && ev.Rank != ev.Model {
		model = ev.Rank + " → " + ev.Model
	}
	return ev.Task + " started · " + model + " · mode " + ev.Mode
}

// needsLine is a line that waits on the owner: `! M1-04 needs you · what`.
func needsLine(id, what string) string {
	return strings.TrimSpace(look.GlyphNeedsYou+" "+id) + " needs you · " + what
}

// notRunLines are the slice's tasks a phase left (SPEC §5.5).
func notRunLines(ev engine.Event) []string {
	lines := make([]string, len(ev.Waiting))
	for i, w := range ev.Waiting {
		lines[i] = "not run · " + w.String()
	}
	return lines
}

func withNote(s, note string) string {
	if note == "" {
		return s
	}
	return s + " · " + note
}

func yoloBadge(mode string) string {
	if engine.SkipsPermissions(mode) {
		return " [SKIP PERMISSIONS]"
	}
	return ""
}

// dryRun walks the phases on a copy of the plan (engine.DryRun) and prints
// which task would launch with which model and mode (SPEC §14).
func dryRun(p *project.Project, req report.RunRequest, out, stderr io.Writer) int {
	r, err := p.DryRun(context.Background(), req)
	o := newOut(out)
	printDryRun(o, r)
	if err != nil {
		fmt.Fprintf(stderr, "igris arise --dry-run: %v\n", err)
		return exitFail
	}
	o.printf("dry run: %s, %s", look.Count(r.Sessions, "session", "sessions"), look.Count(r.Users, "user task", "user tasks"))
	next := "igris arise"
	if req.Phase != "" {
		next += " " + req.Phase
		if req.Through != "" {
			next += " --through " + req.Through
		}
	}
	o.next(next)
	return exitOK
}

// printDryRun renders a dry run, or the part of it found before it failed.
func printDryRun(o textOut, r report.DryRun) {
	if r.Interrupted != "" {
		o.printf("note: the last run stopped during %s; a real `igris arise` picks it up first", r.Interrupted)
	}
	for _, w := range r.Warnings {
		o.warnf("%s", w)
	}
	if r.Scope == "" {
		return
	}
	scope := r.Scope
	if r.Slice != "" {
		scope += "; " + r.Slice
	}
	o.printf("dry run of phase %s: nothing is written and no session starts", scope)
	if r.Worktree != "" {
		o.println(r.Worktree)
	}
	if len(r.Hooks) > 0 {
		o.printf("task hooks %s would run around each agent session; the dry run runs none", strings.Join(r.Hooks, " and "))
	}
	for _, ph := range r.Parallelism {
		if ph.Width == 0 {
			continue
		}
		o.println(o.fit(fmt.Sprintf("  phase %s: %s", ph.Phase, parallelSummary(ph.Parallelism))))
		for _, set := range ph.Together {
			o.println(o.fit("    could run together: " + strings.Join(set, ", ")))
		}
	}
	// The verify column is shown only when some task has a profile or none.
	verifies := false
	for _, s := range r.Steps {
		verifies = verifies || s.Verify != ""
	}
	header := []string{"#", "TASK", "RANK", "MODEL", "MODE"}
	if verifies {
		header = append(header, "VERIFY")
	}
	tb := &look.Table{Indent: "  ", Header: append(header, "TITLE")}
	// The tasks a parallel phase starts together share their step's
	// number (SPEC §14).
	row := func(s report.DryStep, glyph string, sev look.Severity, model, mode, title string) {
		cells := []look.Cell{look.TextCell(fmt.Sprint(s.N)), look.Mark(sev, glyph, s.Task), look.TextCell(s.Rank), look.TextCell(model), look.TextCell(mode)}
		if verifies {
			cells = append(cells, look.TextCell(s.Verify))
		}
		tb.Row(append(cells, look.TextCell(title))...)
	}
	for _, s := range r.Steps {
		switch s.Kind {
		case report.StepSession:
			title := s.Title + yoloBadge(s.Mode)
			if s.Resumed {
				title += " (resumed: fresh session)"
			}
			row(s, look.GlyphReady, look.Plain, s.Model, s.ModeText(), title)
		case report.StepUser:
			row(s, look.GlyphNeedsYou, look.Needs, "user task", "", s.Title+" (waits for you)")
		case report.StepPhaseDone:
			tb.Line(look.Mark(look.Accent, look.GlyphDone, "phase "+s.Phase+" complete"))
		case report.StepWarning:
			tb.Line(look.Mark(look.Warn, look.GlyphWarn, s.Lines[0]))
		case report.StepNote:
			for _, line := range s.Lines {
				tb.Line(look.TextCell(line))
			}
		}
	}
	if tb.Len() > 0 {
		o.table(tb)
	}
}
