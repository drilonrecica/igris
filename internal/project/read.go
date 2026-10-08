package project

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drilonrecica/igris/internal/backend/herdr"
	"github.com/drilonrecica/igris/internal/backend/tmux"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Everything here only reads: no lock is taken and .igris/ is never
// created (SPEC §13).

// RecentRuns is how many runs the home screen's RECENT shows.
const RecentRuns = 3

// RunInfo describes the run recorded under root, or returns nil when there
// is none (see report.NewRunInfo).
func RunInfo(root string) *report.RunInfo {
	in := report.RunInput{}
	in.Run, in.RunErr = state.PeekRun(root)
	in.Lock, in.LockErr = state.PeekLock(root)
	var bad []error
	in.Signals, bad, _ = state.PeekSignals(root) // an unlistable directory shows as no signals
	in.SignalsBad = len(bad)
	return report.NewRunInfo(in)
}

// HistoryInput reads root's run log for report.NewHistory and friends,
// keeping the last n runs. No log is no runs, not an error.
func HistoryInput(root string, n int) (report.HistoryInput, error) {
	in := report.HistoryInput{N: n}
	log, err := state.PeekLog(root)
	if err != nil && !errors.Is(err, state.ErrNoLog) {
		return in, err
	}
	in.Events, in.Unreadable = log.Events, log.Unreadable
	if l, err := state.PeekLock(root); err == nil {
		in.Live = l.Alive && !l.Stale && !l.Unreadable
	}
	return in, nil
}

// Doctor runs `igris doctor`'s checks for the project dir belongs to
// (SPEC §14). It is read-only.
func Doctor(ctx context.Context, dir string, env Env) []checks.Result {
	// The backend is the one the project's config chooses here (SPEC
	// §11.3), built from the environment; it answers from the runner.
	cfg := config.Default()
	if root, err := state.FindRoot(dir); err == nil {
		if c, err := config.Load(filepath.Join(root, state.ConfigFile)); err == nil {
			cfg = c
		}
	}
	o := checks.Options{Runner: env.runner(), Getenv: env.getenv(), Versions: env.versions()}
	name, err := ResolveBackend(cfg.Backend, env.getenv())
	switch {
	case err != nil:
		o.Backend = unavailable{err}
	case name == herdr.Name:
		be := herdr.NewFromEnv(env.runner(), env.getenv())
		o.BackendName, o.Backend, o.Integration = name, be, be
	default:
		o.BackendName, o.Backend = name, tmux.NewFromEnv(env.runner(), env.getenv())
	}
	return checks.Doctor(ctx, checks.DoctorOptions{Dir: dir, Options: o})
}

// unavailable is the availability answer when no backend can be chosen.
type unavailable struct{ err error }

func (u unavailable) Name() string                    { return config.BackendAuto }
func (u unavailable) Available(context.Context) error { return u.err }

// Snapshot reads the project as the home screen shows it. It never fails:
// whatever can't be read is described in the result.
func (p *Project) Snapshot() *report.Snapshot {
	s := &report.Snapshot{
		Root: p.Root, Project: textsafe.Line(p.Name()), Found: p.Found,
		Config: p.Cfg, NoConfig: p.NoConfig, ConfigPath: p.ConfigPath(), PlanPath: p.PlanPath,
		Settings:  report.NewSettings(p.Cfg, p.CfgKeys, p.env.getenv()),
		Stamp:     p.Stamp(),
		APIKeySet: p.env.getenv()(checks.APIKeyVar) != "",
		Backend:   BackendName(p.Cfg, p.env.getenv()),
	}
	if s.Backend == "" {
		s.Backend = herdr.Name + "/" + tmux.Name
	}
	if p.CfgErr != nil {
		s.ConfigProblems = problemLines(p.CfgErr)
	}
	for _, w := range p.Cfg.Warnings() {
		s.ConfigWarnings = append(s.ConfigWarnings, textsafe.Line(w))
	}
	pl, err := plan.Load(p.PlanPath, plan.Options{Columns: p.Cfg.Columns})
	switch {
	case errors.Is(err, fs.ErrNotExist):
		s.PlanMissing = true
	case err != nil:
		s.PlanErr = textsafe.Line(err.Error())
	default:
		s.Plan = pl
		s.Issues = report.Issues(pl.Validate(p.Cfg.Rules(p.Root)))
		if len(s.Issues) == 0 {
			if st, err := report.Status(pl, ""); err == nil {
				s.Status = &st
			}
		}
	}
	if !p.Found {
		return s // no .igris/ to read
	}
	s.Run = RunInfo(p.Root)
	if l, err := state.PeekLock(p.Root); err == nil {
		l.Info.Host, l.Reason, l.Path = textsafe.Line(l.Info.Host), textsafe.Line(l.Reason), textsafe.Line(l.Path)
		s.Lock = l
	}
	if in, err := HistoryInput(p.Root, RecentRuns); err == nil {
		s.Recent = report.NewHistory(in).Runs
	}
	return s
}

// problemLines splits a config error into its problems, one cleaned line
// each.
func problemLines(err error) []string {
	var out []string
	for _, line := range strings.Split(err.Error(), "\n") {
		if line = textsafe.Line(strings.TrimSpace(line)); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Stamp stats the files the home screen watches: igris.toml, the plan, and
// .igris/state.json, igris.lock and runs.jsonl.
func (p *Project) Stamp() report.Stamp {
	dir := filepath.Join(p.Root, state.DirName)
	return report.Stamp{
		Config: fileStamp(filepath.Join(p.Root, state.ConfigFile)),
		Plan:   fileStamp(p.PlanPath),
		State:  fileStamp(filepath.Join(dir, "state.json")),
		Lock:   fileStamp(filepath.Join(dir, "igris.lock")),
		Log:    fileStamp(filepath.Join(dir, "runs.jsonl")),
	}
}

func fileStamp(path string) report.FileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return report.FileStamp{}
	}
	return report.FileStamp{Exists: true, Size: fi.Size(), ModTime: fi.ModTime()}
}
