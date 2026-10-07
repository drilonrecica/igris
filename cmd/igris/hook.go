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
// on stdin decides.
func execHook(args []string, stdin io.Reader) int {
	root := ""
	if len(args) >= 2 && args[0] == "--root" {
		root = args[1]
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
	_ = hook.Run(context.Background(), root, stdin, time.Now)
	return exitOK
}
