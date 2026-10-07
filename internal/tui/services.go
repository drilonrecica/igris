package tui

import (
	"context"
	"errors"
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/adapt"
	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/report"
)

// Services is everything the home screen asks of the project (SPEC §15.6):
// the I/O seam between the screens and the files, herdr and the engine.
// internal/project implements it on the real project; tests use a fake.
// Text in the results is already cleaned (SPEC §16).
type Services interface {
	// Snapshot reads the project: config and its problems, the plan and
	// its issues, status, the current run, the lock and the recent runs.
	Snapshot(ctx context.Context) (*report.Snapshot, error)
	// Stamp is a cheap stat of the watched files, to tell whether a new
	// Snapshot is needed.
	Stamp() report.Stamp
	// BackendAvailable says whether the backend can host sessions (nil) or
	// why not. It runs herdr, so it is slow.
	BackendAvailable(ctx context.Context) error
	// Doctor runs `igris doctor`'s checks.
	Doctor(ctx context.Context) []checks.Result
	// History is `igris history -n n`.
	History(ctx context.Context, n int) (report.History, error)
	// Preview is `igris arise --dry-run`; on an error the report holds
	// what was found before it.
	Preview(ctx context.Context, req report.RunRequest) (*report.DryRun, error)
	// Prelaunch is what arise warns about and asks before a run starts.
	Prelaunch(ctx context.Context, req report.RunRequest) report.Prelaunch
	// Start builds the engine for req with the owner's answers, exactly as
	// `igris arise` does; events receives the run's events. The run starts
	// when the caller calls Run.
	Start(ctx context.Context, req report.RunRequest, c report.Confirmations, events func(engine.Event)) (Runner, error)
	// Init is `igris init` (--example with example). On an error the steps
	// finished before it are returned with it.
	Init(ctx context.Context, example bool) ([]report.Step, error)
	// NotifyTest is `igris notify test`: emit gets each delivery as soon
	// as it is over. The error says why nothing could be sent.
	NotifyTest(ctx context.Context, emit func(report.NotifyResult)) error
	// Adapt runs `igris adapt`'s session with model, writing its progress
	// to out; AcceptAdapt replaces the plan with the proposal and returns
	// the backup's path.
	Adapt(ctx context.Context, model string, out io.Writer) (*adapt.Result, error)
	AcceptAdapt(res *adapt.Result) (string, error)
	// EditCommand is the owner's editor on path ($VISUAL, else $EDITOR);
	// ErrNoEditor when neither is set.
	EditCommand(path string) (tea.ExecCommand, error)
	// ViCommand is vi on path, offered when no editor is set; nil when vi
	// isn't on PATH.
	ViCommand(path string) tea.ExecCommand
	// Focus brings a session's pane to the front.
	Focus(ctx context.Context, ref backend.SessionRef) error
}

// ErrNoEditor says neither $VISUAL nor $EDITOR is set: home then asks,
// and never falls back to vi on its own (SPEC §15.6).
var ErrNoEditor = errors.New("neither $VISUAL nor $EDITOR is set")

// Runner is a built engine: Run runs it until it ends, Send takes the
// owner's commands meanwhile. *engine.Engine is one.
type Runner interface {
	Run(ctx context.Context) (engine.Result, error)
	Sender
}
