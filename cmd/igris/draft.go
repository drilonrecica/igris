package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/draft"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// Seams for tests: the review and the context Ctrl-C cancels. The clock is
// adapt's, which paces both sessions.
var (
	draftReview  = tui.Review
	draftContext = interruptContext
)

// draftSources are the draft subcommands as words.
func draftSources() []string {
	out := make([]string, len(draft.Sources))
	for i, s := range draft.Sources {
		out[i] = string(s)
	}
	return out
}

// draftArgs checks `igris draft SUBCOMMAND [TEXT | FILE]` (SPEC §9.1, §14):
// every usage error is found before the project is read.
func draftArgs(fs *flag.FlagSet) func([]string) error {
	model := fs.String("model", "", "model to use: sonnet|opus (default: draft.model in igris.toml, else opus)")
	fs.String("plan", "", "the plan to write, or with extend to add to (default: plan in igris.toml, else tasks.md)")
	format := fs.String("format", "", "import only: the export's format, "+strings.Join(draft.Formats, "|")+" (default: detected)")
	return func(args []string) error {
		want := strings.Join(draftSources(), "|")
		if len(args) == 0 {
			return fmt.Errorf("missing subcommand; use `igris draft %s`", want)
		}
		src := draft.Source(args[0])
		if !contains(draftSources(), args[0]) {
			return fmt.Errorf("unknown subcommand %q (want %s)", args[0], want)
		}
		if src.HasFile() && (len(args) < 2 || args[1] == "") {
			return fmt.Errorf("missing FILE argument; use `igris draft %s FILE`", src)
		}
		if *model != "" && *model != "sonnet" && *model != "opus" {
			return fmt.Errorf("invalid --model %q (want sonnet|opus)", *model)
		}
		if flagSet(fs, "format") {
			if src != draft.Import {
				return errors.New("--format is only for `igris draft import`")
			}
			if !contains(draft.Formats, *format) {
				return fmt.Errorf("invalid --format %q (want %s)", *format, strings.Join(draft.Formats, "|"))
			}
		}
		return atMost(args, 2)
	}
}

func execDraft(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris draft: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	src := draft.Source(args[0])
	var text, file string
	if len(args) > 1 {
		if src.HasFile() {
			file = args[1]
		} else {
			text = args[1]
		}
	}
	if inSideSession() {
		return fail("this is a side session; draft plans from igris outside it")
	}

	flagPlan := fs.Lookup("plan").Value.String()
	proj, err := draftProject(src, flagPlan)
	if err != nil {
		return fail("%v", err)
	}
	root, cfg, planPath := proj.Root, proj.Cfg, proj.PlanPath
	if flagPlan != "" {
		if planPath, err = filepath.Abs(flagPlan); err != nil {
			return fail("%v", err)
		}
	}
	// Refused before anything is created, a session least of all.
	if err := draft.Check(cfg, root, src, planPath, file, fs.Lookup("format").Value.String()); err != nil {
		return fail("%v", err)
	}
	model := fs.Lookup("model").Value.String()
	if model == "" {
		model = cfg.Draft.ModelOrDefault()
	}
	secrets, err := cfg.Resolve(ariseGetenv)
	if err != nil {
		return fail("%v", err)
	}
	be, err := proj.Backend()
	if err != nil {
		return fail("%v", err)
	}
	dir, err := state.Open(root, state.Options{Now: adaptClock.Now})
	if err != nil {
		return fail("%v", err)
	}
	if ariseGetenv(checks.APIKeyVar) != "" {
		fmt.Fprintf(stdout, "warning: %s\n", checks.APIKeyWarning)
	}

	ctx, cancel, _ := draftContext() // a second Ctrl-C exits at once
	defer cancel()                   // Ctrl-C stops waiting; the session stays open
	res, err := draft.Run(ctx, draft.Options{
		SessionOptions: adapt.SessionOptions{
			Config:   cfg,
			Backend:  be,
			State:    dir,
			Notifier: notify.FromConfig(cfg.Notify, secrets, be, notify.Immediate), // draft never flushes, so nothing may be held
			Clock:    adaptClock,
			Out:      stdout,
		},
		Source:   src,
		PlanPath: planPath,
		Model:    model,
		Text:     text,
		File:     file,
		Format:   fs.Lookup("format").Value.String(),
	})
	if errors.Is(err, context.Canceled) {
		// The owner stopped waiting (Ctrl-C): a choice, not a failure,
		// as for adapt (SPEC §14).
		fmt.Fprintln(stdout, err)
		return exitOK
	}
	if err != nil {
		return fail("%v", err)
	}

	// The owner decides in the review (SPEC §9.1).
	issues := make([]string, len(res.Issues))
	for i, is := range res.Issues {
		issues[i] = is.Error()
	}
	accepted, err := draftReview(ctx, tui.ReviewOptions{
		Mouse:        cfg.TUI.Mouse,
		Theme:        cfg.TUI.Theme,
		Command:      "igris draft",
		Note:         res.Ranks.String(),
		NewPlan:      src.NewPlan(),
		Extend:       res.Extension != nil,
		PlanPath:     rel(root, planPath),
		ProposalPath: res.ProposalPath,
		Review:       draft.Review(res),
		Issues:       issues,
	})
	if err != nil {
		return fail("review: %v; nothing was written, the proposal is in %s", err, res.ProposalPath)
	}
	if !accepted {
		fmt.Fprintf(stdout, "rejected: nothing was written; the proposal stays in %s\n", res.ProposalPath)
		return exitOK
	}
	backups, err := draft.Accept(dir, res, adaptClock.Now())
	if err != nil {
		return fail("%v", err)
	}
	if res.Extension != nil {
		n := 0
		for _, a := range res.Extension.Additions {
			n += len(a.Tasks)
		}
		fmt.Fprintf(stdout, "accepted: %d task(s) added to %s; the files it changed are backed up to %s\n", n, planPath, strings.Join(backups, ", "))
		fmt.Fprintf(stdout, "the new ranks are suggestions (%d set, %d left at ?), change them as you like\n", res.Ranks.Set, res.Ranks.Open)
	} else {
		fmt.Fprintf(stdout, "accepted: %s written; its ranks are suggestions (%d set, %d left at ?), change them as you like\n", planPath, res.Ranks.Set, res.Ranks.Open)
	}
	if len(res.Issues) > 0 {
		fmt.Fprintf(stdout, "the plan still has %d problem(s); fix them (ranks left at ?), then run `igris check`:\n", len(res.Issues))
		for _, is := range res.Issues {
			fmt.Fprintf(stdout, "  %s\n", strings.Replace(is.Error(), res.ProposalPath, planPath, 1))
		}
	}
	return exitOK
}

// draftProject opens the project for draft. A new plan needs no project:
// outside one the working directory is the root and the config is the
// default, as for check (SPEC §9.1); extend works on the current plan, so
// it needs one as adapt does.
func draftProject(src draft.Source, flagPlan string) (*project.Project, error) {
	if !src.NewPlan() {
		return loadProject(flagPlan)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	proj, err := project.Open(cwd, projectEnv())
	if err != nil {
		return nil, err
	}
	if proj.CfgErr != nil {
		return nil, proj.CfgErr
	}
	return proj, nil
}
