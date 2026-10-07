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
	"strings"
	"syscall"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/tui"
)

// Seams for tests: the clock that paces the adapt session's polling and
// stamps the backup, and the diff review.
var (
	adaptClock  engine.Clock = engine.SystemClock()
	adaptReview              = tui.Review
)

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
	proj, err := loadProject(fs.Lookup("plan").Value.String())
	if err != nil {
		return fail("%v", err)
	}
	root, cfg, planPath := proj.Root, proj.Cfg, proj.PlanPath
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
	if ariseGetenv(checks.APIKeyVar) != "" {
		fmt.Fprintf(stdout, "warning: %s\n", checks.APIKeyWarning)
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

	// The owner decides in the review (SPEC §9.5–6).
	issues := make([]string, len(res.Issues))
	for i, is := range res.Issues {
		issues[i] = is.Error()
	}
	accepted, err := adaptReview(ctx, tui.ReviewOptions{
		Mouse:        cfg.TUI.Mouse,
		Theme:        cfg.TUI.Theme,
		PlanPath:     rel(root, planPath),
		ProposalPath: res.ProposalPath,
		Review:       adapt.Compare(res.Original, res.Proposed, plan.Options{Columns: cfg.Columns}),
		Issues:       issues,
	})
	if err != nil {
		return fail("review: %v; the plan is unchanged, the proposal is in %s", err, res.ProposalPath)
	}
	if !accepted {
		fmt.Fprintf(stdout, "rejected: %s is unchanged; the proposal stays in %s\n", planPath, res.ProposalPath)
		return exitOK
	}
	backup, err := adapt.Accept(dir, res, adaptClock.Now())
	if err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(stdout, "accepted: %s replaced; the original is backed up to %s\n", planPath, backup)
	if len(res.Issues) > 0 {
		fmt.Fprintf(stdout, "the plan still has %d problem(s); fix them (models left at ?), then run `igris check`:\n", len(res.Issues))
		for _, is := range res.Issues {
			fmt.Fprintf(stdout, "  %s\n", strings.Replace(is.Error(), res.ProposalPath, planPath, 1))
		}
	}
	return exitOK
}

// rel shows path relative to the project root when it is inside it.
func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}
