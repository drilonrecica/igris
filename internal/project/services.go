package project

import (
	"context"
	"errors"
	"io"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/tui"
)

// Services is the home screen's view of the project in a working directory
// (tui.Services). Every call opens the project afresh, so an edited
// igris.toml counts at once; only Stamp reuses what the last Snapshot found.
type Services struct {
	cwd string
	env Env

	mu   sync.Mutex
	last *Project // the project of the last Snapshot, for Stamp
}

var _ tui.Services = (*Services)(nil)

// NewServices returns the services for the project cwd belongs to.
func NewServices(cwd string, env Env) *Services {
	return &Services{cwd: cwd, env: env}
}

func (s *Services) open() (*Project, error) { return Open(s.cwd, s.env) }

// Snapshot implements tui.Services.
func (s *Services) Snapshot(context.Context) (*report.Snapshot, error) {
	p, err := s.open()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.last = p
	s.mu.Unlock()
	return p.Snapshot(), nil
}

// Stamp implements tui.Services: the watched files of the project the last
// Snapshot read (a changed igris.toml, which may move the plan, shows in
// the stamp and leads to a new Snapshot).
func (s *Services) Stamp() report.Stamp {
	s.mu.Lock()
	p := s.last
	s.mu.Unlock()
	if p == nil {
		var err error
		if p, err = s.open(); err != nil {
			return report.Stamp{}
		}
	}
	return p.Stamp()
}

// BackendAvailable implements tui.Services.
func (s *Services) BackendAvailable(ctx context.Context) error {
	p, err := s.open()
	if err != nil {
		return err
	}
	be, err := p.Backend()
	if err != nil {
		return err
	}
	return be.Available(ctx)
}

// Doctor implements tui.Services.
func (s *Services) Doctor(ctx context.Context) []checks.Result {
	return Doctor(ctx, s.cwd, s.env)
}

// History implements tui.Services.
func (s *Services) History(_ context.Context, n int) (report.History, error) {
	p, err := s.open()
	if err != nil {
		return report.History{}, err
	}
	in := report.HistoryInput{N: n}
	if p.Found {
		if in, err = HistoryInput(p.Root, n); err != nil {
			return report.History{}, err
		}
	}
	return report.NewHistory(in), nil
}

// Preview implements tui.Services.
func (s *Services) Preview(ctx context.Context, req report.RunRequest) (*report.DryRun, error) {
	p, err := Load(s.cwd, "", s.env)
	if err != nil {
		return nil, err
	}
	r, err := p.DryRun(ctx, req)
	return &r, err
}

// Prelaunch implements tui.Services. It creates nothing; without a usable
// backend the herdr integration check is left out.
func (s *Services) Prelaunch(ctx context.Context, _ report.RunRequest) report.Prelaunch {
	p, err := s.open()
	if err != nil {
		return report.Prelaunch{}
	}
	be, _ := p.Backend()
	return p.Prelaunch(ctx, be)
}

// Start implements tui.Services: the engine `igris arise` would build for
// req with the answers c.
func (s *Services) Start(_ context.Context, req report.RunRequest, c report.Confirmations, events func(engine.Event)) (tui.Runner, error) {
	p, err := Load(s.cwd, "", s.env)
	if err != nil {
		return nil, err
	}
	l, err := p.Launch(req)
	if err != nil {
		return nil, err
	}
	return l.Engine(c, events)
}

// Init implements tui.Services: `igris init` in the project root (the
// working directory outside a project).
func (s *Services) Init(ctx context.Context, example bool) ([]report.Step, error) {
	p, err := s.open()
	if err != nil {
		return nil, err
	}
	return Init(ctx, p.Root, example, s.env)
}

// InitFiles implements tui.Services: what Init would touch.
func (s *Services) InitFiles(example bool) []string { return InitFiles(example) }

// NotifyTest implements tui.Services: `igris notify test` for every event,
// each delivery reported as soon as it is over. The herdr toast is left out
// when herdr can't be reached.
func (s *Services) NotifyTest(ctx context.Context, emit func(report.NotifyResult)) error {
	p, err := Load(s.cwd, "", s.env)
	if err != nil {
		return err
	}
	router, _, err := p.NotifyRouter(ctx)
	if err != nil {
		return err
	}
	msgs := TestMessages(router, p.Name(), "")
	if len(msgs) == 0 {
		return errors.New(NoChannels)
	}
	for _, m := range msgs {
		router.NotifyEach(ctx, m, func(res notify.Result) {
			r := report.NotifyResult{Event: string(res.Event), Channel: res.Channel}
			if res.Err != nil {
				r.Err = textsafe.Line(res.Err.Error()) // the router already removed secrets
			}
			emit(r)
		})
	}
	return ctx.Err()
}

// Adapt implements tui.Services: `igris adapt` on the configured plan; model
// "" is adapt.model in igris.toml.
func (s *Services) Adapt(ctx context.Context, model string, out io.Writer, opened func(backend.SessionRef)) (*adapt.Result, error) {
	p, err := Load(s.cwd, "", s.env)
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = p.Cfg.Adapt.Model
	}
	secrets, err := p.Cfg.Resolve(s.env.getenv())
	if err != nil {
		return nil, err
	}
	be, err := p.Backend()
	if err != nil {
		return nil, err
	}
	clock := s.env.clock()
	dir, err := state.Open(p.Root, state.Options{Now: clock.Now})
	if err != nil {
		return nil, err
	}
	return adapt.Run(ctx, adapt.Options{
		Config:   p.Cfg,
		PlanPath: p.PlanPath,
		Model:    model,
		Backend:  be,
		State:    dir,
		Notifier: notify.FromConfig(p.Cfg.Notify, secrets, be),
		Clock:    clock,
		Out:      out,
		Opened:   opened,
	})
}

// AcceptAdapt implements tui.Services.
func (s *Services) AcceptAdapt(res *adapt.Result) (string, error) {
	p, err := Load(s.cwd, "", s.env)
	if err != nil {
		return "", err
	}
	clock := s.env.clock()
	dir, err := state.Open(p.Root, state.Options{Now: clock.Now})
	if err != nil {
		return "", err
	}
	return adapt.Accept(dir, res, clock.Now())
}

// EditCommand implements tui.Services (see EditorArgv).
func (s *Services) EditCommand(path string) (tea.ExecCommand, error) {
	argv, err := EditorArgv(s.env.getenv(), path)
	if err != nil {
		return nil, err
	}
	return newEditorCmd(argv), nil
}

// ViCommand implements tui.Services.
func (s *Services) ViCommand(path string) tea.ExecCommand {
	argv := viArgv(s.env.lookPath(), path)
	if argv == nil {
		return nil
	}
	return newEditorCmd(argv)
}

// Focus implements tui.Services.
func (s *Services) Focus(ctx context.Context, ref backend.SessionRef) error {
	p, err := s.open()
	if err != nil {
		return err
	}
	be, err := p.Backend()
	if err != nil {
		return err
	}
	return Focus(be)(ctx, ref)
}
