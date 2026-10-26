package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/notify"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

// sent is a notification as the engine sent it, with whether its question
// had been asked (its Asked event emitted) by then.
type sent struct {
	m     notify.Message
	asked bool
}

// askNotifier records the engine's notifications (SPEC §10).
type askNotifier struct {
	h    *harness
	mu   sync.Mutex
	msgs []sent
}

func (n *askNotifier) Notify(_ context.Context, m notify.Message) []notify.Result {
	asked := false
	for _, ev := range n.h.events {
		if ev.Kind == Asked && string(ev.Question) == m.Question && ev.Task == m.TaskID {
			asked = true
		}
	}
	n.mu.Lock()
	n.msgs = append(n.msgs, sent{m: m, asked: asked})
	n.mu.Unlock()
	return nil
}

// asking returns the messages that announce question q.
func (n *askNotifier) asking(q Question) []sent {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []sent
	for _, s := range n.msgs {
		if s.m.Question == string(q) {
			out = append(out, s)
		}
	}
	return out
}

func (n *askNotifier) events(ev notify.Event) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, s := range n.msgs {
		if s.m.Event == ev {
			c++
		}
	}
	return c
}

func (n *askNotifier) use(o *Options) { o.Notifier = n }

// checkAsking checks that question q of task was notified exactly want
// times as event ev, each message after the question was asked, so serve
// knows the question when ntfy's buttons are made.
func checkAsking(t *testing.T, n *askNotifier, q Question, task string, ev notify.Event, want int) {
	t.Helper()
	got := n.asking(q)
	if len(got) != want {
		t.Fatalf("%d messages announce %s, want %d (all: %+v)", len(got), q, want, n.msgs)
	}
	for _, s := range got {
		if s.m.Event != ev || s.m.TaskID != task {
			t.Errorf("%s announced by %s for %q, want %s for %s", q, s.m.Event, s.m.TaskID, ev, task)
		}
		if !s.asked {
			t.Errorf("%s notified before it was asked: %+v", q, s.m)
		}
	}
}

// The commit question is notified as needs_input (SPEC §6.5, §10), once
// per question.
func TestCommitQuestionNotified(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.dirtyTree()
	n := &askNotifier{h: h}
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionCommit {
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true})
		}
	}
	if _, err := h.run(n.use); err != nil {
		t.Fatal(err)
	}
	if got := n.asking(QuestionCommit); len(got) != 3 || got[0].m.What != "commit question" {
		t.Fatalf("commit messages %+v, want 3 saying commit question", got)
	}
	for i, id := range []string{"A-1", "A-2", "A-3"} {
		if s := n.asking(QuestionCommit)[i]; s.m.TaskID != id || s.m.Event != notify.NeedsInput || !s.asked {
			t.Errorf("message %d = %+v (asked %v), want needs_input for %s after the question", i, s.m, s.asked, id)
		}
	}
}

// A skip request's needs_input carries its question.
func TestSkipRequestNotifiedWithQuestion(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "A-1" {
			return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionSkip, Note: "not needed"})
		}
		return h.signal(id)
	})
	n := &askNotifier{h: h}
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionConfirmSkip {
			h.eng.Send(Command{Kind: CmdAnswer, Yes: true})
		}
	}
	if _, err := h.run(n.use); err != nil {
		t.Fatal(err)
	}
	checkAsking(t, n, QuestionConfirmSkip, "A-1", notify.NeedsInput, 1)
	// Needs you still comes before the question, as the views expect.
	if i, j := h.index(NeedsYou, "A-1"), h.index(Asked, "A-1"); i < 0 || i > j {
		t.Errorf("needs_you at %d, asked at %d", i, j)
	}
}

// A lost session's session_lost carries its question.
func TestSessionLostNotifiedWithQuestion(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	opened := 0
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "A-1" {
			if opened++; opened == 1 {
				return nil
			}
		}
		return h.signal(id)
	})
	h.be.Script("A-1", backend.Working, backend.Exited)
	n := &askNotifier{h: h}
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionSessionLost {
			h.eng.Send(Command{Kind: CmdRetry})
		}
	}
	if _, err := h.run(n.use); err != nil {
		t.Fatal(err)
	}
	checkAsking(t, n, QuestionSessionLost, "A-1", notify.SessionLost, 1)
	if got := n.events(notify.SessionLost); got != 1 {
		t.Errorf("%d session_lost messages, want 1", got)
	}
}

// A failed before_task hook's run_error carries its question, and is the
// only message for it; a failed after_task hook asks nothing.
func TestHookFailureNotifiedWithQuestion(t *testing.T) {
	h := newHarness(t, chainPlan, hooksTOML)
	before, after := 1, 1
	h.recordHooks(func(c runner.Cmd) (runner.Result, error) {
		switch {
		case c.Name == "./scripts/prep" && before > 0:
			before--
			return runner.Result{ExitCode: 3}, nil
		case c.Name != "./scripts/prep" && after > 0:
			after--
			return runner.Result{ExitCode: 4}, nil
		}
		return runner.Result{}, nil
	})
	n := &askNotifier{h: h}
	h.onEvent = func(ev Event) {
		if ev.Kind == Asked && ev.Question == QuestionHookFailed {
			h.eng.Send(Command{Kind: CmdRetry})
		}
	}
	if _, err := h.run(n.use); err != nil {
		t.Fatal(err)
	}
	checkAsking(t, n, QuestionHookFailed, "A-1", notify.RunError, 1)
	if got := n.events(notify.RunError); got != 2 {
		t.Errorf("%d run_error messages, want 2: the before_task hook's question and the after_task hook's", got)
	}
}

// A plan presented for approval is notified with its question.
func TestPlanApprovalNotifiedWithQuestion(t *testing.T) {
	h, hook := planHarness(t, "")
	n := &askNotifier{h: h}
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened:
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present(planText) })
		case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
			h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanAllow})
		case ev.Kind == PlanClosed:
			hook.done()
			if err := h.signal("A-1"); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := h.run(withPlanApproval, n.use); err != nil {
		t.Fatal(err)
	}
	checkAsking(t, n, QuestionPlanApproval, "A-1", notify.NeedsInput, 1)
	if got := n.asking(QuestionPlanApproval)[0].m.What; got != "plan to approve" {
		t.Errorf("what = %q", got)
	}
}

// In a parallel phase the commit question is notified per task, and asked
// again after a decline with one message, not two.
func TestParallelCommitQuestionNotified(t *testing.T) {
	h := newParallel(t, twoThenOne(), "needs_input_after = \"1h\"\n[run]\ncommit = \"ask\"\nverify = \"make check\"\n[parallel]\nmax = 2\n")
	h.repo()
	h.commands()
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		if id == "c" {
			return h.signal(id)
		}
		return nil
	})
	n := &askNotifier{h: h}
	answer := func(at time.Duration, task string, yes bool) {
		h.clock.At(at, func() { h.eng.Send(Command{Kind: CmdAnswer, Task: task, Yes: yes}) })
	}
	h.signalAt(time.Minute, "a")
	h.signalAt(2*time.Minute, "b")
	answer(3*time.Minute, "b", true)
	answer(4*time.Minute, "a", false)
	answer(5*time.Minute, "a", true)
	answer(6*time.Minute, "c", true)
	if res, err := h.run(n.use); err != nil || res.Outcome != Completed {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	got := map[string][]string{}
	for _, s := range n.asking(QuestionCommit) {
		if !s.asked || s.m.Event != notify.NeedsInput {
			t.Errorf("message %+v (asked %v)", s.m, s.asked)
		}
		got[s.m.TaskID] = append(got[s.m.TaskID], s.m.What)
	}
	if a := strings.Join(got["a"], " | "); a != "commit question | commit question" {
		t.Errorf("a's commit messages: %q", a)
	}
	if len(got["b"]) != 1 || len(got["c"]) != 1 {
		t.Errorf("commit messages %q, want one each for b and c", got)
	}
}

// index is the position of task's first event of kind, -1 if none.
func (h *harness) index(kind EventKind, task string) int {
	for i, ev := range h.events {
		if ev.Kind == kind && ev.Task == task {
			return i
		}
	}
	return -1
}
