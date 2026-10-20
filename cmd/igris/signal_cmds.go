package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/side"
	"github.com/drilonrecica/igris/internal/state"
)

// errSideSession is why done, skip and reset refuse inside a side session
// (SPEC §6.2, §6.10): its environment holds IGRIS_SIDE. It stops a
// mistake, not a session that unsets the variable.
const errSideSession = "this is a side session; it doesn't finish, skip or reset tasks"

// inSideSession reports whether igris runs inside a side session.
func inSideSession() bool { return os.Getenv(side.Env) != "" }

func execDone(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	return sendSignal(state.ActionDone, args[0], fs.Lookup("note").Value.String(), stdout, stderr)
}

func execSkip(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	return sendSignal(state.ActionSkip, args[0], fs.Lookup("reason").Value.String(), stdout, stderr)
}

// taskIDArg checks a command's task ID argument: a malformed one is a usage
// error (exit 2, SPEC §14), found before the project is read.
func taskIDArg(id string) error {
	if !plan.ValidID(id) {
		return fmt.Errorf("invalid task ID %q: IDs are letters, digits, '.', '_' and '-' (e.g. M0-01); check it with `igris status`", id)
	}
	return nil
}

// sendSignal writes the signal file for task id. It never edits the plan:
// only the running igris consumes signals (SPEC §6.2).
func sendSignal(action, id, note string, stdout, stderr io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris %s: %s\n", action, fmt.Sprintf(format, a...))
		return exitFail
	}
	if inSideSession() {
		return fail(errSideSession)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail("%v", err)
	}
	root, err := state.FindRoot(cwd)
	if err != nil {
		return fail("%v", err)
	}
	cfg, err := config.Load(filepath.Join(root, state.ConfigFile))
	switch {
	case errors.Is(err, config.ErrNotFound):
		cfg = config.Default()
	case err != nil:
		return fail("%v", err)
	}
	// The adapt session finishes with `igris done ADAPT` (SPEC §9). Its
	// plan doesn't validate and may not be the configured one, so ADAPT is
	// accepted without the plan, unless the plan has a task of that ID.
	p, err := plan.Load(rootPath(root, cfg.Plan), cfg.PlanOptions(root))
	adapting := action == state.ActionDone && id == prompt.AdaptID && (err != nil || p.Task(id) == nil)
	switch {
	case adapting:
	case err != nil:
		return fail("%v; set plan in %s", err, state.ConfigFile)
	case p.Task(id) == nil:
		return fail("task %q is not in %s; check the ID with `igris status`", id, p.Path)
	}
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		return fail("%v", err)
	}
	if err := dir.WriteSignal(state.Signal{ID: id, Action: action, Note: note}); err != nil {
		return fail("%v", err)
	}
	idle := ""
	if !adapting && !dir.Running() {
		idle = "; no igris run is active in this project, the next `igris arise` applies it"
	}
	if action == state.ActionSkip {
		fmt.Fprintf(stdout, "igris: skip signal recorded for %s; the owner confirms it in igris%s\n", id, idle)
	} else {
		fmt.Fprintf(stdout, "igris: done signal recorded for %s%s\n", id, idle)
	}
	return exitOK
}

// rootPath resolves a config path relative to the project root.
func rootPath(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
