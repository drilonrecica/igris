package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/drilonrecica/igris/internal/backend/herdr"
	"github.com/drilonrecica/igris/internal/checks"
)

// doctorArgs registers the flags of `igris doctor`.
func doctorArgs(fs *flag.FlagSet) func([]string) error {
	fs.Bool("json", false, "machine-readable output: the checks as a JSON array")
	return maxArgs(fs, 0)
}

// glyphs mark each level in doctor's text output.
var glyphs = map[checks.Level]string{checks.OK: "✓", checks.Warn: "!", checks.Fail: "⨯"}

// execDoctor runs the health check (SPEC §14). It is read-only: it creates
// and changes nothing, and exits 1 only if some check failed.
func execDoctor(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris doctor: %v\n", err)
		return exitFail
	}
	// herdr is only asked about when it can be: the backend is built from
	// the environment and answers from the runner.
	be := herdr.NewFromEnv(commandRunner(), ariseGetenv)
	rs := checks.Doctor(context.Background(), checks.DoctorOptions{
		Dir: cwd,
		Options: checks.Options{
			Runner: commandRunner(), Getenv: ariseGetenv, Versions: compatWarnings,
			Backend: be, Integration: be,
		},
	})
	failed := false
	for _, r := range rs {
		failed = failed || r.Level == checks.Fail
	}
	if jsonFlag(fs) {
		if rs == nil {
			rs = []checks.Result{}
		}
		writeJSON(stdout, rs)
	} else {
		for _, r := range rs {
			msg := r.Message
			if r.Line != 0 { // a located plan or config problem: file:line first
				msg = r.String()
			}
			fmt.Fprintf(stdout, "%s %-4s %s\n", glyphs[r.Level], r.Level, msg)
			if r.Level != checks.OK && r.Next != "" {
				fmt.Fprintf(stdout, "         next: %s\n", r.Next)
			}
		}
	}
	if failed {
		return exitFail
	}
	return exitOK
}
