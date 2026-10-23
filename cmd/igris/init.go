package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/project"
	"github.com/drilonrecica/igris/internal/state"
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
	o := newOut(stdout)
	for _, s := range steps {
		o.println(s.Message)
	}
	if err != nil {
		fmt.Fprintf(stderr, "igris init: %v\n", err)
		return exitFail
	}
	// With a plan the next step is checking it; without one, writing one.
	cfg, err := config.Load(filepath.Join(root, state.ConfigFile))
	if err != nil {
		cfg = config.Default()
	}
	o.blank()
	if _, err := os.Stat(rootPath(root, cfg.Plan)); err == nil {
		o.next("igris check")
	} else {
		o.next("igris draft goal", "igris init --example")
	}
	return exitOK
}
