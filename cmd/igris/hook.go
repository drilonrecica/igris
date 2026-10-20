package main

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/drilonrecica/igris/internal/hook"
	"github.com/drilonrecica/igris/internal/state"
)

// execHook runs the hidden `igris hook [--root DIR] <event>` (SPEC §6.3).
// Claude Code runs it from the hooks file igris passes each session. It
// prints nothing and always exits 0: a failing hook must never disturb or
// block the session. The event name argument is informational; the event
// on stdin decides. `igris hook [--root DIR] plan-approve --mode M --wait D`
// is the plan approval hook: it waits for igris and prints Claude Code's
// answer, or nothing. `igris hook [--root DIR] statusline ID` is the status
// line command ([usage] statusline): it prints the one status line and
// records the subscription quota.
func execHook(args []string, stdin io.Reader, stdout io.Writer) int {
	root := ""
	if len(args) >= 2 && args[0] == "--root" {
		root, args = args[1], args[2:]
	}
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return exitOK
		}
		if root, err = state.FindRoot(cwd); err != nil {
			return exitOK
		}
	}
	if len(args) > 0 && args[0] == hook.PlanCommand {
		approvePlan(root, args[1:], stdin, stdout)
		return exitOK
	}
	if len(args) > 0 && args[0] == hook.StatusLineCommand {
		task := ""
		if len(args) > 1 {
			task = args[1]
		}
		_ = hook.StatusLine(context.Background(), root, task, stdin, stdout, time.Now)
		return exitOK
	}
	_ = hook.Run(context.Background(), root, stdin, time.Now)
	return exitOK
}

// approvePlan runs plan-approve with its --mode and --wait arguments. Claude
// Code cancels a waiting hook when the owner answers in the pane; the
// first SIGTERM or SIGINT ends the wait, so the request is removed on the
// way out. Any other end leaves it to igris, which sees the hook's process
// is gone.
func approvePlan(root string, args []string, stdin io.Reader, stdout io.Writer) {
	opt := hook.PlanOptions{}
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--mode":
			opt.Mode = args[i+1]
		case "--wait":
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				return
			}
			opt.Wait = d
		}
	}
	ctx, cancel, _ := interruptContext()
	defer cancel()
	_ = hook.ApprovePlan(ctx, root, stdin, stdout, opt)
}
