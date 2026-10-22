package project

import (
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/tui"
)

// AttachOptions are the run view's options for the run served in this
// project (igris attach, SPEC §14), fed by feed through c, the client
// standing in for the engine. version is this igris's, so the view can
// name another serving igris. The backend, built for this terminal,
// focuses sessions and opens side sessions; without one (an ssh shell
// outside herdr and tmux) the view does neither.
func (p *Project) AttachOptions(feed *tui.Feed, c tui.Sender, version string) tui.Options {
	cfg := p.Cfg
	o := tui.Options{
		Project:    p.Name(),
		Mouse:      cfg.TUI.Mouse,
		Theme:      cfg.TUI.Theme,
		RankColors: cfg.TUI.RankColors,
		// The task list reads the plan serve writes, as arise's does.
		PlanPath:      p.PlanPath,
		PlanOptions:   cfg.PlanOptions(p.Root),
		Feed:          feed,
		Sender:        c,
		RankDurations: rankDurations(p.Root),
		Attached:      true,
		Version:       version,
	}
	if be, err := p.Backend(); err == nil {
		o.Backend, o.Focus = be.Name(), Focus(be)
		o.Side = &SideSessions{env: p.env, load: func() (*Project, backend.Backend, error) { return p, be, nil }}
	}
	o.Tail, o.TailEvery = tui.TailOf(cfg, c)
	return o
}
