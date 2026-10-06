package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/state"
)

// adaptClock paces the adapt session's polling; tests use a fake clock.
var adaptClock engine.Clock = engine.SystemClock()

func adaptArgs(fs *flag.FlagSet) func([]string) error {
	model := fs.String("model", "", "model to use: sonnet|opus (default: adapt.model in igris.toml)")
	fs.String("plan", "", "plan file to adapt (default: plan in igris.toml, else tasks.md)")
	return func(args []string) error {
		if *model != "" && *model != "sonnet" && *model != "opus" {
			return fmt.Errorf("invalid --model %q (want sonnet|opus)", *model)
		}
		return atMost(args, 0)
	}
}

func execAdapt(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris adapt: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	root, cfg, err := loadProject()
	if err != nil {
		return fail("%v", err)
	}
	planPath := rootPath(root, cfg.Plan)
	if p := fs.Lookup("plan").Value.String(); p != "" {
		if planPath, err = filepath.Abs(p); err != nil {
			return fail("%v", err)
		}
	}
	model := fs.Lookup("model").Value.String()
	if model == "" {
		model = cfg.Adapt.Model
	}
	secrets, err := cfg.Resolve(ariseGetenv)
	if err != nil {
		return fail("%v", err)
	}
	be, err := ariseBackend(cfg)
	if err != nil {
		return fail("%v", err)
	}
	dir, err := state.Open(root, state.Options{Now: adaptClock.Now})
	if err != nil {
		return fail("%v", err)
	}
	if ariseGetenv(engine.APIKeyVar) != "" {
		fmt.Fprintf(stdout, "warning: %s is set: Claude Code bills the API instead of your subscription login.\n", engine.APIKeyVar)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel() // Ctrl-C stops waiting; the session stays open
	res, err := adapt.Run(ctx, adapt.Options{
		Config:   cfg,
		PlanPath: planPath,
		Model:    model,
		Backend:  be,
		State:    dir,
		Notifier: notify.FromConfig(cfg.Notify, secrets, be),
		Clock:    adaptClock,
		Out:      stdout,
	})
	if errors.Is(err, adapt.ErrAlreadyValid) {
		fmt.Fprintf(stdout, "%s passes `igris check`; nothing to adapt\n", planPath)
		return exitOK
	}
	if err != nil {
		return fail("%v", err)
	}
	return reviewProposal(res, stdout)
}

// reviewProposal reports the proposal and its validation result.
func reviewProposal(res *adapt.Result, stdout io.Writer) int {
	fmt.Fprintf(stdout, "proposal: %s\n", res.ProposalPath)
	if len(res.Issues) == 0 {
		fmt.Fprintln(stdout, "✓ the proposal passes `igris check`")
	} else {
		fmt.Fprintf(stdout, "⨯ the proposal has %d problem(s) left to fix after accepting it:\n", len(res.Issues))
		for _, is := range res.Issues {
			fmt.Fprintf(stdout, "  %s\n", is)
		}
	}
	fmt.Fprintf(stdout, "%s is unchanged; compare the two files and copy the proposal over it if it looks right\n", res.PlanPath)
	return exitOK
}
