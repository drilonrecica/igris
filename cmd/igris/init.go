package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/drilonrecica/igris/internal/project"
)

const claudeSettingsPath = project.ClaudeSettingsPath

// execInit sets up the current directory as an igris project. It is safe to
// run again: it only adds what is missing and never overwrites.
func execInit(fs *flag.FlagSet, _ []string, stdout, stderr io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "igris init: %v\n", err)
		return exitFail
	}
	starter := ""
	if f := fs.Lookup("example"); f != nil && f.Value.String() == "true" {
		starter = project.StarterExample
	}
	if f := fs.Lookup("template"); f != nil && f.Value.String() != "" {
		starter = f.Value.String()
	}
	steps, err := project.Init(context.Background(), root, starter, projectEnv())
	for _, s := range steps {
		fmt.Fprintln(stdout, s.Message)
	}
	if err != nil {
		fmt.Fprintf(stderr, "igris init: %v\n", err)
		return exitFail
	}
	return exitOK
}
