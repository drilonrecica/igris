package herdr

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/conformance"
	"github.com/drilonrecica/igris/internal/runner"
)

// herdrWorld is a herdr server for the conformance suite: it answers each
// call with the recorded fixture (testdata/) for the session's state. It
// holds one session, the tab and pane of tab_create.json.
type herdrWorld struct {
	t       *testing.T
	mu      sync.Mutex
	gone    bool
	prompts []string
}

func (w *herdrWorld) Backend() backend.Backend {
	f := &runner.Fake{}
	f.Func(w.run)
	b := New(f, "w2B")
	b.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return b
}

func (w *herdrWorld) Kill(backend.SessionRef) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gone = true
}

func (w *herdrWorld) Prompts(backend.SessionRef) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.prompts
}

func (w *herdrWorld) run(c runner.Cmd) (runner.Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	t, a := w.t, c.Args
	answer := func(okFile, goneFile string) (runner.Result, error) {
		if w.gone {
			return fail(t, goneFile, 1), nil
		}
		return ok(t, okFile), nil
	}
	switch a[0] + " " + a[1] {
	case "tab create":
		w.gone = false
		return ok(t, "tab_create.json"), nil
	case "agent start":
		return ok(t, "agent_start_ok.json"), nil
	case "agent wait":
		return answer("agent_wait_settled.json", "error_agent_not_found.json")
	case "agent prompt":
		if !w.gone {
			w.prompts = append(w.prompts, a[3])
		}
		return answer("agent_prompt_nowait.json", "error_agent_not_found.json")
	case "agent read":
		return answer("agent_read_recent_unwrapped.txt", "error_agent_not_found.json")
	case "pane get":
		return answer("pane_get_idle.json", "error_pane_not_found.json")
	case "tab focus":
		return answer("tab_focus.json", "error_tab_get_not_found.json")
	case "tab close":
		res, err := answer("tab_close.json", "error_tab_close_not_found.json")
		w.gone = true
		return res, err
	}
	t.Errorf("unexpected herdr %q", a)
	return runner.Result{ExitCode: 1}, nil
}

func TestConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.World { return &herdrWorld{t: t} })
}
