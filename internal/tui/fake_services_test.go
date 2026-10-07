package tui

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
	"github.com/drilonrecica/igris/internal/report"
)

// fakeServices is a Services for tests: each call returns what its field
// holds and is counted. A nil func field gives the zero answer. Calls are
// safe from the program's goroutines.
type fakeServices struct {
	mu    sync.Mutex
	calls map[string]int

	snapshot   func(ctx context.Context) (*report.Snapshot, error)
	stamp      report.Stamp
	backendErr error
	doctor     []checks.Result
	history    report.History
	historyErr error
	preview    func(req report.RunRequest) (*report.DryRun, error)
	prelaunch  report.Prelaunch
	start      func(req report.RunRequest, c report.Confirmations, events func(engine.Event)) (Runner, error)
	initSteps  []report.Step
	initErr    error
	onInit     func(example bool) // runs inside Init, to change what Snapshot reads next
	notify     []report.NotifyResult
	notifyErr  error
	adapt      func(ctx context.Context, model string, out io.Writer, opened func(backend.SessionRef)) (*adapt.Result, error)
	backup     string
	acceptErr  error
	edit       func(path string) (tea.ExecCommand, error)
	vi         func(path string) tea.ExecCommand
	focusErr   error
}

var _ Services = (*fakeServices)(nil)

// fakeSnapshot is a project as Snapshot reads it.
func fakeSnapshot(project string) *report.Snapshot {
	return &report.Snapshot{Root: "/src/" + project, Project: project, Found: true}
}

func (f *fakeServices) count(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

// called is how often name was called.
func (f *fakeServices) called(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *fakeServices) Snapshot(ctx context.Context) (*report.Snapshot, error) {
	f.count("Snapshot")
	if f.snapshot == nil {
		return fakeSnapshot("sinjal"), nil
	}
	return f.snapshot(ctx)
}

func (f *fakeServices) Stamp() report.Stamp {
	f.count("Stamp")
	return f.stamp
}

func (f *fakeServices) BackendAvailable(context.Context) error {
	f.count("BackendAvailable")
	return f.backendErr
}

func (f *fakeServices) Doctor(context.Context) []checks.Result {
	f.count("Doctor")
	return f.doctor
}

func (f *fakeServices) History(context.Context, int) (report.History, error) {
	f.count("History")
	return f.history, f.historyErr
}

func (f *fakeServices) Preview(_ context.Context, req report.RunRequest) (*report.DryRun, error) {
	f.count("Preview")
	if f.preview == nil {
		return &report.DryRun{}, nil
	}
	return f.preview(req)
}

func (f *fakeServices) Prelaunch(context.Context, report.RunRequest) report.Prelaunch {
	f.count("Prelaunch")
	return f.prelaunch
}

func (f *fakeServices) Start(_ context.Context, req report.RunRequest, c report.Confirmations, events func(engine.Event)) (Runner, error) {
	f.count("Start")
	if f.start == nil {
		return nil, errors.New("fake: no run")
	}
	return f.start(req, c, events)
}

func (f *fakeServices) Init(_ context.Context, example bool) ([]report.Step, error) {
	f.count("Init")
	if f.onInit != nil {
		f.onInit(example)
	}
	return f.initSteps, f.initErr
}

func (f *fakeServices) InitFiles(example bool) []string {
	f.count("InitFiles")
	files := []string{"igris.toml", ".igris/", ".gitignore", ".claude/settings.local.json"}
	if example {
		files = append(files, "tasks.md")
	}
	return files
}

func (f *fakeServices) NotifyTest(_ context.Context, emit func(report.NotifyResult)) error {
	f.count("NotifyTest")
	for _, r := range f.notify {
		emit(r)
	}
	return f.notifyErr
}

func (f *fakeServices) Adapt(ctx context.Context, model string, out io.Writer, opened func(backend.SessionRef)) (*adapt.Result, error) {
	f.count("Adapt")
	f.count("Adapt " + model)
	if f.adapt == nil {
		return nil, errors.New("fake: no adapt")
	}
	return f.adapt(ctx, model, out, opened)
}

func (f *fakeServices) AcceptAdapt(*adapt.Result) (string, error) {
	f.count("AcceptAdapt")
	return f.backup, f.acceptErr
}

func (f *fakeServices) EditCommand(path string) (tea.ExecCommand, error) {
	f.count("EditCommand")
	if f.edit == nil {
		return nil, errors.New("fake: no editor")
	}
	return f.edit(path)
}

func (f *fakeServices) ViCommand(path string) tea.ExecCommand {
	f.count("ViCommand")
	if f.vi == nil {
		return nil
	}
	return f.vi(path)
}

func (f *fakeServices) Focus(context.Context, backend.SessionRef) error {
	f.count("Focus")
	return f.focusErr
}
