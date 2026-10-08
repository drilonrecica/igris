package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/state"
)

// Needs-you time is the time igris waited on the owner (SPEC §13): these
// tests check where each wait of the run log starts and ends.

// runReport is `igris report` of the harness's run log.
func (h *harness) runReport() report.Report {
	h.t.Helper()
	r, err := report.NewReport(report.HistoryInput{Events: h.logEvents()}, "p", "")
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) taskNeedsYou(r report.Report, id string) int {
	h.t.Helper()
	for _, tk := range r.Tasks {
		if tk.ID == id && tk.NeedsYouS != nil {
			return *tk.NeedsYouS
		}
	}
	h.t.Fatalf("no needs-you time for %s in the report", id)
	return 0
}

// An idle wait ends with the session's done signal, also when its verify
// then fails and the agent works on the failure for a long time.
func TestIdleWaitEndsAtDoneSignal(t *testing.T) {
	h := newHarness(t, chainPlan, "[run]\nverify = \"make test\"\n")
	h.verifyResults(failWith(2, "boom\n"), pass)
	h.autoSignalExcept("A-1")
	// Idle for a minute (needs you at 30s), then working for 30 minutes.
	h.be.Script("A-1", append(states(backend.Idle, 30), states(backend.Working, 900)...)...)
	h.signalAt(59*time.Second, "A-1")
	h.signalAt(30*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got, want := h.waits(), "A-1#1 idle, clear A-1#1 idle"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
	if got := h.taskNeedsYou(h.runReport(), "A-1"); got > 30 {
		t.Errorf("A-1 needs you %ds, want at most the 30s up to the done signal", got)
	}
}

// A session that writes its skip request twice waits once.
func TestSkipRequestedTwiceIsOneWait(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "A-1" {
			return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionSkip, Note: "not needed"})
		}
		return h.signal(id)
	})
	h.clock.At(5*time.Second, func() {
		_ = h.dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionSkip, Note: "really not needed"})
	})
	asked := 0
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionConfirmSkip {
			if asked++; asked == 2 {
				h.clock.At(h.clock.Now().Sub(t0)+10*time.Second, func() { h.eng.Send(Command{Kind: CmdAnswer, Yes: false}) })
			}
		}
	}
	h.be.Script("A-1", backend.Working)
	h.signalAt(30*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if asked != 2 {
		t.Fatalf("asked %d times, want 2", asked)
	}
	if got, want := h.waits(), "A-1#1 skip_request, clear A-1#1 skip_request"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
	if got := h.taskNeedsYou(h.runReport(), "A-1"); got > 20 {
		t.Errorf("A-1 needs you %ds, want only the time until the owner declined", got)
	}
}

// A task past its Timeout waits on the owner only until the agent is seen
// working (Decision W, V05-P1).
func TestOverdueWaitEndsWhileWorking(t *testing.T) {
	h := newHarness(t, timeoutPlan, "")
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", backend.Working)
	h.signalAt(30*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got, want := h.waits(), "A-1#1 task_overdue, clear A-1#1 task_overdue"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
	if got := h.taskNeedsYou(h.runReport(), "A-1"); got > 2 {
		t.Errorf("A-1 needs you %ds while the agent worked throughout, want a poll at most", got)
	}
}

// The verify limit's wait ends when the agent works again: the owner told
// it what to do.
func TestVerifyLimitWaitEndsWhenWorking(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(failWith(1, "boom\n"), failWith(1, "boom\n"), pass)
	h.autoSignalExcept("A-1")
	h.resignal(1)
	// Idle while igris verifies; at 1m the owner has the agent work again.
	h.be.Script("A-1", append(states(backend.Done, 10), states(backend.Working, 600)...)...)
	h.signalAt(5*time.Second, "A-1")
	h.signalAt(2*time.Minute, "A-1")
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got, want := h.waits(), "A-1#1 verify_limit, clear A-1#1 verify_limit"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
}

// A second plan edit while the run already holds is not a second wait.
func TestPlanChangedWhileHeldIsOneWait(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.onEvent = func(ev Event) {
		if ev.Kind == SessionOpened && ev.Task == "A-1" {
			h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| A-2 | **Two** — the second thing | A-1 | blocked | opus |", "| A-2 | **Two** — the second thing | A-1 | blocked | fable |", 1))
		}
	}
	h.clock.At(20*time.Second, func() {
		h.write("tasks.md", strings.Replace(h.read("tasks.md"), "| B-1 | **Four** | A-3 | blocked | sonnet |", "| B-1 | **Four** | A-3 | blocked | opus |", 1))
	})
	h.clock.At(time.Minute, func() { h.eng.Send(Command{Kind: CmdPause}) })
	h.clock.At(2*time.Minute, func() { h.eng.Send(Command{Kind: CmdStop}) })
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if got := h.count(PlanChanged); got != 2 {
		t.Errorf("%d plan_changed events, want one per edit", got)
	}
	if got, want := h.waits(), "#0 plan_changed, clear #0 plan_changed"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
	if r := h.runReport(); r.NeedsYouS == nil || *r.NeedsYouS > 60 {
		t.Errorf("run needs you %v, want at most the minute until the owner resumed", r.NeedsYouS)
	}
}

// Attempts are counted per task over the whole run: a task reset and
// started again goes on from its last attempt (SPEC §13).
func TestAttemptsContinueAfterReset(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.confirmResets(true)
	h.continueWhenPaused()
	h.resetAt(10*time.Second, "A-1", false)
	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, e := range h.logEvents() {
		if e.Task == "A-1" && (e.Type == state.EventTaskStarted || e.Type == state.EventTaskResumed || e.Type == state.EventTaskRetried) {
			got = append(got, e.Attempt)
		}
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("A-1 attempts %v, want [1 2]", got)
	}
}
