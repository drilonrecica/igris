package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/runner"
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
)

// projectEnv hands this package's seams to the project layer, so the CLI
// and the home screen talk to the same (in tests: fake) world.
func projectEnv() project.Env {
	return project.Env{
		Getenv: ariseGetenv, Runner: ariseRunner, Backend: ariseBackend, Versions: compatWarnings,
		Clock: adaptClock, Format: formatEvent,
	}
}

type ariseFlags struct {
	phase, through, mode       string
	noTUI, dryRun, forceUnlock bool
	sel                        state.Selection // --only, --from, --until
	lines                      <-chan string   // --no-tui: the owner's stdin lines
}

func (f ariseFlags) request() report.RunRequest {
	return report.RunRequest{Phase: f.phase, Through: f.through, Mode: f.mode, Selection: f.sel}
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
		if strings.TrimSpace(value("only")) == "" {
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

func execArise(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	f := ariseFlags{
		through:     fs.Lookup("through").Value.String(),
		mode:        fs.Lookup("mode").Value.String(),
		noTUI:       fs.Lookup("no-tui").Value.String() == "true",
		dryRun:      fs.Lookup("dry-run").Value.String() == "true",
		forceUnlock: fs.Lookup("force-unlock").Value.String() == "true",
	}
	if len(args) > 0 {
		f.phase = args[0]
	}
	f.sel, _ = parseSelection(fs) // checked when the flags were parsed
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris arise: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	proj, err := loadProject("")
	if err != nil {
		return fail("%v", err)
	}
	if wantWizard(f, proj.Root) {
		return runApp(tui.Wizard{Mode: f.mode, Through: f.through, ForceUnlock: f.forceUnlock}, stdout, stderr)
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel() // Ctrl-C stops the run; the session stays open (SPEC §13)
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
			res, err = runOnce(ctx, l.Options(conf, func(ev engine.Event) { printEvent(out, ev) }), f.lines, out)
		} else {
			var started bool
			res, started, err = runWithTUI(ctx, l, conf, out)
			if started {
				return tuiExit(res, err, out, stderr)
			}
		}
		var drift *engine.DriftError
		switch {
		case errors.As(err, &drift) && !conf.Drift:
			// SPEC §5.2: list the drift, ask before the first write fixes it.
			fmt.Fprintf(out, "The plan's ready/blocked cells don't match its dependencies; igris's first write would change:\n")
			for _, c := range drift.Changes {
				fmt.Fprintf(out, "  %s\n", c)
			}
			if !confirm(out, ask, "Let igris fix them?") {
				return fail("not confirmed; nothing was started")
			}
			conf.Drift = true
		case errors.Is(err, engine.ErrYoloUnconfirmed) && !conf.Yolo:
			// SPEC §7.3: typed confirmation, every run.
			fmt.Fprintf(out, "%v\nSessions in this mode run with --dangerously-skip-permissions: Claude Code acts without asking.\n", err)
			if !typed(out, ask, engine.YoloPhrase) {
				return fail("skip-permissions mode not confirmed; nothing was started")
			}
			conf.Yolo = true
		case err != nil:
			return fail("%v", err)
		case res.Outcome == engine.Stuck:
			return exitFail
		default:
			printNotRun(out, res)
			return exitOK
		}
	}
}

// ariseUI shows the TUI; a seam for tests.
var ariseUI = tui.Run

// runWithTUI starts the engine and, once the run passed its start-up checks
// (the first event arrives), hands the terminal to the TUI. A run that
// fails before that returns with started false, so its error can be
// answered on the plain terminal (drift, skip-permissions) and the run
// tried again. Quitting the TUI stops the run; the session stays open.
func runWithTUI(ctx context.Context, l *project.Launch, conf report.Confirmations, out io.Writer) (engine.Result, bool, error) {
	feed := tui.NewFeed()
	eng, err := l.Engine(conf, feed.Push)
	if err != nil {
		return engine.Result{}, false, err
	}
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	go func() { feed.End(eng.Run(runCtx)) }()
	select {
	case <-feed.Started():
	case <-feed.Ended():
		select {
		case <-feed.Started(): // it started, then ended at once
		default:
			res, err := feed.Result()
			return res, false, err
		}
	}
	uiErr := ariseUI(ctx, l.TUIOptions(feed, eng))
	stopRun()
	<-feed.Ended()
	res, err := feed.Result()
	if uiErr != nil {
		fmt.Fprintf(out, "the TUI failed: %v; run `igris arise --no-tui` to continue without it\n", uiErr)
		if err == nil {
			err = uiErr
		}
	}
	return res, true, err
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
	if res.Outcome == engine.Stuck {
		return exitFail
	}
	return exitOK
}

// printNotRun is the run's final summary of the slice's tasks it left
// because their deps are unmet (SPEC §5.5).
func printNotRun(out io.Writer, res engine.Result) {
	for _, w := range res.NotRun {
		fmt.Fprintln(out, "not run: "+w.String())
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
  done [note]                mark the current task done (an agent task is not verified)
  skip <reason>              skip the current task
  retry [continue|fresh]     replace the session: continue its conversation, or start fresh (default)
  pause                      pause after the current task (again to resume)
  stop                       stop igris now; the session stays open
  mode <m>                   run mode for the next sessions: default|accept|auto|plan|yolo
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
	case "pause":
		return noArgs(engine.Command{Kind: engine.CmdPause})
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
			return engine.Command{}, actNone, fmt.Errorf("mode takes [task] and one of default|accept|auto|plan|yolo")
		}
		m := c.Text
		if m == engine.ModeYolo {
			return c, actConfirmYolo, nil
		}
		return c, actSend, nil
	case "help", "?":
		return engine.Command{}, actHelp, nil
	}
	return engine.Command{}, actNone, fmt.Errorf("unknown command %q; type help", word)
}

// printEvent writes ev as timestamped plain log lines.
func printEvent(out io.Writer, ev engine.Event) {
	for _, line := range formatEvent(ev) {
		fmt.Fprintf(out, "%s %s\n", ev.At.Local().Format("15:04:05"), line)
	}
}

// formatEvent renders ev for the plain log. Most events are one line.
func formatEvent(ev engine.Event) []string {
	id := ev.Task
	switch ev.Kind {
	case engine.RunStarted:
		return []string{"run started: " + ev.Detail}
	case engine.PhaseStarted:
		return []string{"phase " + ev.Phase + " started"}
	case engine.TaskStarted:
		if ev.Model == "" {
			return []string{fmt.Sprintf("%s started (user task) · %s", id, ev.Title)}
		}
		return []string{fmt.Sprintf("%s started (%s → %s, mode %s) · %s", id, ev.Rank, ev.Model, ev.Mode, ev.Title)}
	case engine.TaskResumed:
		return []string{id + " resumed: " + ev.Detail}
	case engine.SessionOpened:
		return []string{id + " session open" + yoloBadge(ev.Mode)}
	case engine.YourTurn:
		return []string{
			id + " YOUR TURN: " + ev.Detail,
			"  do it outside igris (no session), then type `done [note]` or `skip <reason>` (or run `igris done " + id + "` anywhere in the project)",
		}
	case engine.NeedsYou:
		return []string{id + " NEEDS YOU: " + ev.Detail}
	case engine.NeedsYouClear:
		return []string{id + " is working again"}
	case engine.TaskOverdue:
		return []string{id + " OVERDUE: " + ev.Detail + "; its session keeps running"}
	case engine.SessionLost:
		return []string{id + " SESSION LOST: " + ev.Detail}
	case engine.Asked:
		switch ev.Question {
		case engine.QuestionSessionLost:
			return []string{"? " + ev.Detail, "  type `retry continue`, `retry fresh`, `done [note]`, `skip <reason>` or `stop`"}
		case engine.QuestionHookFailed:
			return []string{"? " + ev.Detail, "  type `retry`, `done [note]`, `skip <reason>` or `stop`"}
		default:
			return []string{"? " + ev.Detail + " [y/n]"}
		}
	case engine.Retrying:
		return []string{id + " new session (" + ev.Detail + ")"}
	case engine.VerifyStarted:
		return []string{id + " verifying (" + ev.Verify + "): " + ev.Detail}
	case engine.VerifyPassed:
		return []string{id + " verify passed (" + ev.Verify + ")"}
	case engine.VerifyFailed:
		return []string{id + " verify failed: " + ev.Detail}
	case engine.VerifyLimit:
		return []string{id + " " + ev.Detail}
	case engine.Committed:
		return []string{id + " committed: " + ev.Detail}
	case engine.NotCommitted:
		return []string{id + " not committed: " + ev.Detail}
	case engine.TaskDone:
		return []string{withNote(id+" done", ev.Detail)}
	case engine.TaskSkipped:
		return []string{withNote(id+" skipped", ev.Detail)}
	case engine.PhaseDone:
		return []string{"phase " + ev.Phase + " complete"}
	case engine.PhaseStuck:
		lines := []string{"phase " + ev.Phase + " is STUCK: unfinished tasks, none can start"}
		for _, w := range ev.Waiting {
			lines = append(lines, "  "+w.String())
		}
		return lines
	case engine.NotRun:
		return notRunLines(ev)
	case engine.PauseOn:
		return []string{"pause after task: on (type `pause` again to turn it off)"}
	case engine.PauseOff:
		return []string{"pause after task: off"}
	case engine.Paused:
		return []string{"paused before " + id + "; type `pause` to continue"}
	case engine.ModeChanged:
		return []string{"run mode for the next sessions: " + ev.Detail + yoloBadge(ev.Detail)}
	case engine.TaskModeChanged:
		return []string{"mode for " + id + "'s next session: " + ev.Detail + yoloBadge(ev.Detail)}
	case engine.ConfigChanged, engine.ConfigRestored, engine.PlanChanged, engine.StaleSignal, engine.StraySignal:
		return []string{ev.Detail}
	case engine.HookFailed:
		lines := []string{id + " " + ev.Detail}
		for _, l := range ev.Output {
			lines = append(lines, "  | "+l)
		}
		return lines
	case engine.Warning:
		return []string{"warning: " + ev.Detail}
	case engine.RunFailed:
		return []string{"error: " + ev.Detail}
	case engine.RunStopped:
		return []string{"run stopped: " + ev.Detail}
	}
	return []string{strings.TrimSpace(string(ev.Kind) + " " + id + " " + ev.Detail)}
}

// notRunLines are the slice's tasks a phase left (SPEC §5.5).
func notRunLines(ev engine.Event) []string {
	lines := make([]string, len(ev.Waiting))
	for i, w := range ev.Waiting {
		lines[i] = "not run: " + w.String()
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
	if mode == engine.ModeYolo {
		return " [SKIP PERMISSIONS]"
	}
	return ""
}

// dryRun walks the phases on a copy of the plan (engine.DryRun) and prints
// which task would launch with which model and mode (SPEC §14).
func dryRun(p *project.Project, req report.RunRequest, out, stderr io.Writer) int {
	r, err := p.DryRun(context.Background(), req)
	printDryRun(out, r)
	if err != nil {
		fmt.Fprintf(stderr, "igris arise --dry-run: %v\n", err)
		return exitFail
	}
	fmt.Fprintf(out, "dry run: %d session(s), %d user task(s)\n", r.Sessions, r.Users)
	return exitOK
}

// printDryRun renders a dry run, or the part of it found before it failed.
func printDryRun(out io.Writer, r report.DryRun) {
	if r.Interrupted != "" {
		fmt.Fprintf(out, "note: the last run stopped during %s; a real `igris arise` picks it up first\n", r.Interrupted)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	if r.Scope == "" {
		return
	}
	scope := r.Scope
	if r.Slice != "" {
		scope += "; " + r.Slice
	}
	fmt.Fprintf(out, "dry run of phase %s: nothing is written and no session starts\n", scope)
	if len(r.Hooks) > 0 {
		fmt.Fprintf(out, "task hooks %s would run around each agent session; the dry run runs none\n", strings.Join(r.Hooks, " and "))
	}
	// The verify column is shown only when some task has a profile.
	verifies := false
	for _, s := range r.Steps {
		verifies = verifies || s.Verify != ""
	}
	for _, s := range r.Steps {
		switch s.Kind {
		case report.StepSession:
			how := ""
			if s.Resumed {
				how = "  (resumed: fresh session)"
			}
			verify := ""
			if verifies {
				verify = fmt.Sprintf("verify %-7s ", cmp.Or(s.Verify, "—"))
			}
			fmt.Fprintf(out, "%3d. %-10s %-7s → model %-7s mode %-7s %s%s%s%s\n", s.N, s.Task, s.Rank, s.Model, s.Mode, verify, s.Title, yoloBadge(s.Mode), how)
		case report.StepUser:
			fmt.Fprintf(out, "%3d. %-10s user task: waits for you  %s\n", s.N, s.Task, s.Title)
		case report.StepPhaseDone:
			fmt.Fprintf(out, "     phase %s complete\n", s.Phase)
		case report.StepWarning:
			fmt.Fprintf(out, "warning: %s\n", s.Lines[0])
		case report.StepNote:
			for _, line := range s.Lines {
				fmt.Fprintln(out, "     "+line)
			}
		}
	}
}
