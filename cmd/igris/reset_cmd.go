package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

func resetArgs(fs *flag.FlagSet) func([]string) error {
	fs.Bool("force", false, "also reset a done or skipped task")
	return exactArgs("ID", 1)
}

// execReset puts a task back to ready/blocked (SPEC §14 `reset`): through
// the running igris when one holds the lock here, else by writing the
// Status cell itself.
func execReset(fs *flag.FlagSet, args []string, stdout, stderr io.Writer) int {
	id, force := args[0], fs.Lookup("force").Value.String() == "true"
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "igris reset: %s\n", fmt.Sprintf(format, a...))
		return exitFail
	}
	if !plan.ValidID(id) {
		return fail("invalid task ID %q: IDs are letters, digits, '.', '_' and '-' (e.g. M0-01); check it with `igris status`", id)
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
	opts, rules := plan.Options{Columns: cfg.Columns}, cfg.Rules(root)
	p, err := plan.Load(rootPath(root, cfg.Plan), opts)
	if err != nil {
		return fail("%v; set plan in %s", err, state.ConfigFile)
	}
	if issues := p.Validate(rules); len(issues) > 0 {
		fmt.Fprintf(stderr, "igris reset: %s is not valid; run `igris check` and fix:\n", p.Path)
		for _, is := range issues {
			fmt.Fprintf(stderr, "  %s\n", is)
		}
		return exitFail
	}
	t := p.Task(id)
	if t == nil {
		return fail("task %q is not in %s; check the ID with `igris status`", id, p.Path)
	}
	switch ok, err := plan.Resettable(t, force); {
	case err != nil:
		return fail("%v", err)
	case !ok:
		fmt.Fprintf(stdout, "%s is %s: nothing to reset\n", id, t.Status)
		return exitOK
	}

	lock, err := state.PeekLock(root)
	if err != nil {
		return fail("%v", err)
	}
	switch {
	case lock.Remote:
		return fail("igris is running for this project on another host (%s); run `igris reset %s` there, or, if that run is gone, clear the lock with `igris arise --force-unlock`", textsafe.Line(lock.Info.String()), id)
	case lock.Alive:
		// The running igris is the plan's only writer while it runs. Sessions
		// can write signals too, so it asks the owner first (SPEC §6.2).
		dir, err := state.Open(root, state.Options{})
		if err != nil {
			return fail("%v", err)
		}
		if err := dir.WriteSignal(state.Signal{ID: id, Action: state.ActionReset, Force: force}); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(stdout, "%s: reset requested; confirm it in the running igris (if none is running, the next `igris arise` asks)\n", id)
	default:
		if code := resetDirect(root, rootPath(root, cfg.Plan), opts, rules, t, force, lock.Held, stdout, stderr); code != exitOK {
			return code
		}
	}
	for _, d := range p.Dependents(id) {
		if d.Status == plan.InProgress {
			fmt.Fprintf(stdout, "%s depends on %s and is in progress; reset leaves it as it is\n", d.ID, id)
		}
	}
	return exitOK
}

// resetDirect writes t's Status cell (and the readiness it moves) with the
// surgical writer, while no igris runs here, and logs it. It holds the run
// lock meanwhile, so an `igris arise` starting at the same moment can't
// write the plan too; held says a stale lock file is there, which only
// `--force-unlock` clears and which keeps arise out just the same.
func resetDirect(root, path string, opts plan.Options, rules plan.Rules, t *plan.Task, force, held bool, stdout, stderr io.Writer) int {
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		fmt.Fprintf(stderr, "igris reset: %v\n", err)
		return exitFail
	}
	if !held {
		lock, err := dir.Lock(false)
		if err != nil {
			fmt.Fprintf(stderr, "igris reset: %v; run `igris reset %s` again\n", err, t.ID)
			return exitFail
		}
		defer func() {
			if err := lock.Release(); err != nil {
				fmt.Fprintf(stderr, "igris reset: warning: %v\n", err)
			}
		}()
	}
	changes, err := plan.NewWriter(path, opts, rules).Update(context.Background(), func(p *plan.Plan) ([]plan.Change, error) {
		return p.Reset(t.ID, force)
	})
	if err != nil {
		fmt.Fprintf(stderr, "igris reset: %v\n", err)
		return exitFail
	}
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "%s: nothing to reset\n", t.ID) // changed since it was read
		return exitOK
	}
	for _, c := range changes {
		fmt.Fprintf(stdout, "%s\n", c)
	}
	if err := dir.Append(state.Event{Type: state.EventTaskReset, Task: t.ID, Rank: t.Rank, Detail: t.Status.String()}); err != nil {
		// The plan is written; the log is a record, not state.
		fmt.Fprintf(stderr, "igris reset: warning: %v\n", err)
	}
	if run, err := state.PeekRun(root); err == nil && run.Current != nil && run.Current.TaskID == t.ID {
		fmt.Fprintf(stdout, "%s was the task of the interrupted run; its session may still be open: close it by hand\n", t.ID)
	}
	return exitOK
}
