package project

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/side"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/tui"
)

// SideSessions is the project's side sessions for the TUI (tui.SideSessions,
// SPEC §6.10) over one side.Manager, kept while the project root and the
// backend stay the same, so the sessions it opened or reattached are held
// across calls.
type SideSessions struct {
	// load is the project and its backend: as they are now (home), or as
	// the run started (a run view's).
	load func() (*Project, backend.Backend, error)
	env  Env

	mu  sync.Mutex
	mgr *side.Manager
	key string // the root and backend mgr is for
}

var _ tui.SideSessions = (*SideSessions)(nil)

// Side implements tui.Services: the project as it is at each call.
func (s *Services) Side() tui.SideSessions {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.side == nil {
		s.side = &SideSessions{env: s.env, load: func() (*Project, backend.Backend, error) {
			p, err := s.open()
			if err != nil {
				return nil, nil, err
			}
			be, err := p.Backend()
			return p, be, err
		}}
	}
	return s.side
}

// Side is the side sessions of this run's project and backend.
func (l *Launch) Side() *SideSessions {
	return &SideSessions{env: l.p.env, load: func() (*Project, backend.Backend, error) { return l.p, l.Backend, nil }}
}

// manager is the project, its backend and the Manager for them.
func (s *SideSessions) manager() (*Project, backend.Backend, *side.Manager, error) {
	p, be, err := s.load()
	if err != nil {
		return nil, nil, nil, err
	}
	if !p.Found {
		return nil, nil, nil, fmt.Errorf("no igris project in %s; run `igris init` first", p.Root)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key := p.Root + "\x00" + be.Name(); s.mgr == nil || s.key != key {
		s.mgr, s.key = side.New(side.Options{
			Root: p.Root, Backend: be, Runner: s.env.runner(),
			Log: sideLog(p.Root), Usage: p.CfgErr == nil && p.Cfg.Usage.On(),
		}), key
	}
	return p, be, s.mgr, nil
}

// Setup implements tui.SideSessions: the [models] ranks with [side] model
// first, [side] mode, and the run a live igris holds here.
func (s *SideSessions) Setup(context.Context) (tui.SideSetup, error) {
	p, _, _, err := s.manager()
	if err != nil {
		return tui.SideSetup{}, err
	}
	if p.CfgErr != nil {
		return tui.SideSetup{}, fmt.Errorf("igris.toml is invalid; fix it first (Settings or `igris doctor` shows why)")
	}
	cfg := p.Cfg
	ranks := slices.Sorted(maps.Keys(cfg.Models))
	if i := slices.Index(ranks, cfg.Side.ModelOrDefault()); i > 0 {
		ranks = append(append([]string{ranks[i]}, ranks[:i]...), ranks[i+1:]...)
	}
	out := tui.SideSetup{Mode: cfg.Side.ModeOrDefault()}
	for _, r := range ranks {
		out.Ranks = append(out.Ranks, tui.SideRank{Rank: textsafe.Line(r), Model: textsafe.Line(cfg.Models[r])})
	}
	if lock, err := state.PeekLock(p.Root); err == nil && lock.Alive {
		if r, err := state.PeekRun(p.Root); err == nil {
			out.Run = &tui.SideRun{Worktree: r.Worktree != nil}
			if r.Current != nil {
				out.Run.Task = r.Current.TaskID
			}
		}
	}
	return out, nil
}

// List implements tui.SideSessions: Refresh, then each open session's state
// from its hooks.
func (s *SideSessions) List(ctx context.Context) (tui.SideList, error) {
	_, _, mgr, err := s.manager()
	if err != nil {
		return tui.SideList{}, err
	}
	sync, err := mgr.Refresh(ctx)
	if err != nil {
		return tui.SideList{}, err
	}
	var out tui.SideList
	for _, e := range sync.Open {
		r := e.Record
		row := tui.SideRow{ID: r.ID, State: sideState(ctx, e.Session), At: r.At, Rank: textsafe.Line(r.Rank), Mode: r.Mode, Worktree: r.Worktree != nil}
		switch r.Preset {
		case state.SidePresetTask:
			row.Subject = r.Task
		case state.SidePresetIgris:
			row.Subject = "igris"
		}
		out.Rows = append(out.Rows, row)
	}
	for _, e := range sync.Ended {
		out.Lines = append(out.Lines, endedLine(e))
	}
	for _, w := range sync.Warnings {
		out.Lines = append(out.Lines, textsafe.Line(w))
	}
	return out, nil
}

// sideState is a side session's state as the page shows it (SPEC §15.7).
func sideState(ctx context.Context, sess backend.Session) string {
	st, err := sess.State(ctx)
	switch {
	case err != nil:
		return string(backend.Unknown)
	case st == backend.Done:
		return string(backend.Idle)
	case st == backend.Working, st == backend.Idle, st == backend.Blocked, st == backend.Waiting:
		return string(st)
	}
	return string(backend.Unknown)
}

// endedLine says, for the feed, that a side session ended and what became
// of its worktree.
func endedLine(e side.Ended) string {
	id := e.Record.ID
	out := "side " + id + " ended"
	if e.Closed {
		out = "side " + id + " closed"
	}
	switch {
	case e.WorktreeRemoved:
		out += "; its worktree was removed, branch " + state.SideWorktree(id).Branch + " stays"
	case e.Worktree != nil && e.Closed:
		out += "; its worktree " + e.Worktree.Path + " has changes and is kept"
	case e.Worktree != nil:
		out += "; its worktree " + e.Worktree.Path + " is kept"
	}
	return out
}

// Prefill implements tui.SideSessions: the preset's first prompt (SPEC
// §6.10).
func (s *SideSessions) Prefill(ctx context.Context, req tui.SidePrefill) (string, error) {
	p, be, _, err := s.manager()
	if err != nil {
		return "", err
	}
	switch req.Preset {
	case state.SidePresetTask:
		pl, err := plan.Load(p.PlanPath, plan.Options{Columns: p.Cfg.Columns})
		if err != nil {
			return "", err
		}
		t := pl.Task(req.Task)
		if t == nil {
			return "", fmt.Errorf("task %s is not in the plan any more", req.Task)
		}
		info := side.TaskInfo{ID: t.ID, Title: t.Title, PlanPath: p.PlanPath, Text: t.Text, Running: req.Running}
		if t.Phase != nil {
			info.Phase, info.PhaseName = t.Phase.ID, t.Phase.Title
		}
		return side.TaskPrefill(info), nil
	case state.SidePresetIgris:
		// A log that can't be read leaves the events out; the session is
		// told where to read it.
		events, _ := state.PeekEvents(p.Root)
		return side.IgrisPrefill(side.IgrisInfo{
			Version: s.env.Version,
			Backend: be.Name(),
			Run:     req.Run,
			Events:  events,
			Doctor:  Doctor(ctx, p.Root, s.env),
		}), nil
	}
	return "", nil
}

// Open implements tui.SideSessions.
func (s *SideSessions) Open(ctx context.Context, req tui.SideRequest) (string, error) {
	p, _, mgr, err := s.manager()
	if err != nil {
		return "", err
	}
	model, ok := p.Cfg.Models[req.Rank]
	if !ok {
		return "", fmt.Errorf("rank %q is not in [models] any more", req.Rank)
	}
	e, err := mgr.Open(ctx, side.Spec{
		Preset: req.Preset, Task: req.Task, Rank: req.Rank, Model: model, Mode: req.Mode,
		ExtraArgs: p.Cfg.Claude.ExtraArgs,
		Worktree:  req.Worktree,
		PlanPath:  p.PlanPath,
		RunTask:   req.RunTask,
		Prompt:    req.Prompt,
	})
	if e.Session == nil {
		return "", err
	}
	return e.Record.ID, err
}

// Focus implements tui.SideSessions.
func (s *SideSessions) Focus(ctx context.Context, id string) error {
	_, _, mgr, err := s.manager()
	if err != nil {
		return err
	}
	return mgr.Focus(ctx, id)
}

// Close implements tui.SideSessions.
func (s *SideSessions) Close(ctx context.Context, id string) (string, error) {
	_, _, mgr, err := s.manager()
	if err != nil {
		return "", err
	}
	e, ok, err := mgr.Close(ctx, id)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "side " + id + " had ended already", nil
	}
	return endedLine(e), nil
}

// sideLog appends a side session's run-log line (SPEC §13), with the run
// this igris is running, if it is. A log that can't be written is passed
// over: the session's own work matters more.
func sideLog(root string) func(state.Event) {
	return func(ev state.Event) {
		d, err := state.Open(root, state.Options{})
		if err != nil {
			return
		}
		ev.Run = ownRun(root)
		_ = d.Append(ev)
	}
}

// ownRun is the ID of the run this process is running in root, "" when it
// runs none: a read-only home beside another igris's run has none (SPEC
// §6.10). It is the newest run_started of the log without a run_stopped,
// while this process holds the run lock.
func ownRun(root string) string {
	lock, err := state.PeekLock(root)
	if err != nil || !lock.Alive || lock.Info.PID != os.Getpid() {
		return ""
	}
	events, err := state.PeekEvents(root)
	if err != nil {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		switch e := events[i]; {
		case e.Run == "":
		case e.Type == state.EventRunStopped:
			return ""
		case e.Type == state.EventRunStarted:
			return e.Run
		}
	}
	return ""
}
