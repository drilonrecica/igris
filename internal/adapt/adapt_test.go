package adapt

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/state"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// A synthetic non-canonical plan: renamed columns and a prose status.
const oldPlan = `# Widgets

| Key | What | State | LLM |
|---|---|---|---|
| W-1 | Parse widgets | todo | sonnet |
`

const goodProposal = `## W — Widgets

| ID | Task | Status | Model |
|---|---|---|---|
| W-1 | Parse widgets | ready | sonnet |
`

const openModelProposal = `## W — Widgets

| ID | Task | Status | Model |
|---|---|---|---|
| W-1 | Parse widgets | ready | ? |

## Adapt notes

- W-1: unknown model "LLM"
`

type notes struct {
	mu   sync.Mutex
	msgs []notify.Message
}

func (n *notes) Notify(_ context.Context, m notify.Message) []notify.Result {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, m)
	return nil
}

func (n *notes) events() []notify.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []notify.Event
	for _, m := range n.msgs {
		out = append(out, m.Event)
	}
	return out
}

type harness struct {
	t     *testing.T
	root  string
	plan  string
	be    *fake.Backend
	clock *engine.FakeClock
	dir   *state.Dir
	notes *notes
	out   bytes.Buffer
}

func newHarness(t *testing.T, planText string) *harness {
	t.Helper()
	h := &harness{t: t, root: t.TempDir(), be: fake.New(), clock: engine.NewFakeClock(t0), notes: &notes{}}
	h.plan = filepath.Join(h.root, "plan.md")
	if err := os.WriteFile(h.plan, []byte(planText), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	h.dir, err = state.Open(h.root, state.Options{Now: h.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// finish makes the session write proposal (unless empty) and signal done.
func (h *harness) finish(proposal string) error {
	if proposal != "" {
		if err := os.WriteFile(ProposalPath(h.dir, h.plan), []byte(proposal), 0o600); err != nil {
			return err
		}
	}
	return h.dir.WriteSignal(state.Signal{ID: ID, Action: state.ActionDone, Note: "converted"})
}

// finishOnPrompt finishes as soon as the adapt prompt arrives.
func (h *harness) finishOnPrompt(proposal string) {
	h.be.SetAutoSignal(func(_ context.Context, _ string) error { return h.finish(proposal) })
}

func (h *harness) run(ctx context.Context, model string) (*Result, error) {
	cfg := config.Default()
	cfg.Models["opus"] = "claude-opus-5-5"
	return Run(ctx, Options{
		Config:   cfg,
		PlanPath: h.plan,
		Model:    model,
		Backend:  h.be,
		State:    h.dir,
		Notifier: h.notes,
		Clock:    h.clock,
		Out:      &h.out,
	})
}

func TestRunValidProposal(t *testing.T) {
	h := newHarness(t, oldPlan)
	h.finishOnPrompt(goodProposal)

	res, err := h.run(context.Background(), "opus")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 0 {
		t.Errorf("issues = %v, want none", res.Issues)
	}
	if string(res.Original) != oldPlan || string(res.Proposed) != goodProposal || res.Note != "converted" {
		t.Errorf("result = %+v", res)
	}
	if want := filepath.Join(h.root, ".igris", "adapt", "plan.proposed.md"); res.ProposalPath != want {
		t.Errorf("proposal = %s, want %s", res.ProposalPath, want)
	}
	if data, _ := os.ReadFile(h.plan); string(data) != oldPlan { //nolint:gosec // test temp dir
		t.Error("adapt changed the plan")
	}

	opened := h.be.Opened()
	if len(opened) != 1 || opened[0].TaskID != ID || opened[0].Dir != h.root || opened[0].Label != "ADAPT · opus" {
		t.Fatalf("opened = %+v", opened)
	}
	args := opened[0].Args
	if i := slices.Index(args, "--model"); i < 0 || args[i+1] != "claude-opus-5-5" {
		t.Errorf("args = %v, want --model claude-opus-5-5", args)
	}
	for _, flag := range []string{"--permission-mode", "--dangerously-skip-permissions"} {
		if slices.Contains(args, flag) {
			t.Errorf("args = %v: mode default adds no %s", args, flag)
		}
	}
	if i := slices.Index(args, "--append-system-prompt-file"); i < 0 || !strings.HasSuffix(args[i+1], "ADAPT.rules.md") {
		t.Errorf("args = %v, want the adapt rules file", args)
	}

	prompts := h.be.Prompts(ID)
	if len(prompts) != 1 {
		t.Fatalf("prompts = %d, want 1", len(prompts))
	}
	for _, want := range []string{"`plan.md`", ".igris/adapt/plan.proposed.md", "no task table found", "`opus` → claude-opus-5-5"} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if len(h.be.Closed()) != 1 {
		t.Error("the session was not closed after done")
	}
	if s, _ := h.dir.ReadSignal(ID); s != nil {
		t.Error("the done signal was not consumed")
	}
	if _, err := os.Stat(filepath.Join(h.root, ".igris", "igris.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the run lock was not released")
	}
}

func TestRunProposalWithOpenModels(t *testing.T) {
	h := newHarness(t, oldPlan)
	h.finishOnPrompt(openModelProposal)

	res, err := h.run(context.Background(), "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 1 || !strings.Contains(res.Issues[0].Msg, `model not set yet ("?")`) {
		t.Errorf("issues = %v, want the open model", res.Issues)
	}
	if res.Issues[0].File != res.ProposalPath {
		t.Errorf("issue file = %s, want the proposal", res.Issues[0].File)
	}
}

func TestRunAlreadyValid(t *testing.T) {
	h := newHarness(t, goodProposal)
	if _, err := h.run(context.Background(), "sonnet"); !errors.Is(err, ErrAlreadyValid) {
		t.Fatalf("err = %v, want ErrAlreadyValid", err)
	}
	if len(h.be.Opened()) != 0 {
		t.Error("a session was opened for a valid plan")
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
		want  string
	}{
		{"no proposal written", func(h *harness) { h.finishOnPrompt("") }, "without writing"},
		{"session gone", func(h *harness) { h.be.Script(ID, backend.Working, backend.Exited) }, "ended without `igris done ADAPT`"},
		{"backend unavailable", func(h *harness) { h.be.SetAvailable(errors.New("herdr is not running")) }, "herdr is not running"},
		{"open fails", func(h *harness) { h.be.FailOpen(errors.New("no pane")) }, "no pane"},
		{"skipped", func(h *harness) {
			h.be.SetAutoSignal(func(context.Context, string) error {
				return h.dir.WriteSignal(state.Signal{ID: ID, Action: state.ActionSkip, Note: "not now"})
			})
		}, "skipped (not now)"},
		{"locked", func(h *harness) {
			l, err := h.dir.Lock(false)
			if err != nil {
				h.t.Fatal(err)
			}
			h.t.Cleanup(func() { _ = l.Release() })
		}, "adapt can't run while igris arise runs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, oldPlan)
			tt.setup(h)
			_, err := h.run(context.Background(), "sonnet")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRunSessionLostNotifies(t *testing.T) {
	h := newHarness(t, oldPlan)
	h.be.Script(ID, backend.Exited)
	if _, err := h.run(context.Background(), "sonnet"); err == nil {
		t.Fatal("want an error")
	}
	if got := h.notes.events(); !slices.Equal(got, []notify.Event{notify.SessionLost}) {
		t.Errorf("notifications = %v", got)
	}
}

func TestRunNeedsYouOncePerIdleStretch(t *testing.T) {
	h := newHarness(t, oldPlan)
	// Idle long enough to ask, working again, idle again, then done.
	h.be.Script(ID, backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle,
		backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle, backend.Idle,
		backend.Working,
		backend.Idle)
	h.clock.At(10*time.Minute, func() {
		if err := h.finish(goodProposal); err != nil {
			t.Error(err)
		}
	})
	if _, err := h.run(context.Background(), "sonnet"); err != nil {
		t.Fatal(err)
	}
	if got := h.notes.events(); !slices.Equal(got, []notify.Event{notify.NeedsInput, notify.NeedsInput}) {
		t.Errorf("notifications = %v, want needs_input twice (one per idle stretch)", got)
	}
	out := h.out.String()
	if strings.Count(out, "needs you") != 2 || !strings.Contains(out, "working again") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunIgnoresStaleSignal(t *testing.T) {
	h := newHarness(t, oldPlan)
	// A done signal left over from an earlier adapt, before this one started.
	if err := h.dir.WriteSignal(state.Signal{ID: ID, Action: state.ActionDone, At: t0.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h.clock.At(10*time.Second, func() { // before needs_input_after
		if err := h.finish(goodProposal); err != nil {
			t.Error(err)
		}
	})
	if _, err := h.run(context.Background(), "sonnet"); err != nil {
		t.Fatal(err)
	}
	if len(h.notes.events()) != 0 {
		t.Errorf("notifications = %v", h.notes.events())
	}
}

func TestRunCanceledLeavesSessionOpen(t *testing.T) {
	h := newHarness(t, oldPlan)
	ctx, cancel := context.WithCancel(context.Background())
	h.clock.At(time.Minute, cancel)
	_, err := h.run(ctx, "sonnet")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "stays open") {
		t.Fatalf("err = %v", err)
	}
	if len(h.be.Closed()) != 0 {
		t.Error("a canceled adapt closed the session")
	}
}

func TestRunProposalWithoutTasks(t *testing.T) {
	h := newHarness(t, oldPlan)
	h.finishOnPrompt("# Widgets\n\nNothing here.\n")
	res, err := h.run(context.Background(), "sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 1 || !strings.Contains(res.Issues[0].Msg, "no task table") {
		t.Errorf("issues = %v: a proposal without tasks must not pass", res.Issues)
	}
}

func TestRunRemovesEarlierProposal(t *testing.T) {
	h := newHarness(t, oldPlan)
	if err := os.WriteFile(ProposalPath(h.dir, h.plan), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.finishOnPrompt("") // the session writes nothing this time
	if _, err := h.run(context.Background(), "sonnet"); err == nil || !strings.Contains(err.Error(), "without writing") {
		t.Fatalf("err = %v: an earlier proposal must not count", err)
	}
}
