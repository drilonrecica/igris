package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

// Plan approval from igris (SPEC §6.3, V11-P1), end to end with the fake
// backend: the tests play the plan-approve hook through the exchange
// files, the owner through engine commands.

const onePlanTask = `# Plan

## A — Phase

| ID | Task | Deps | Status | Model | Owner | Mode |
|---|---|---|---|---|---|---|
| A-1 | **One** — the thing | — | ready | sonnet | agent | plan |
`

const planText = "# Plan\n\n1. Write `hello.txt` with <hi>.\n   - nested step\n"

// planHook plays `igris hook plan-approve` for task id's session: present
// writes a plan request, decision reads igris's answer, done removes both
// files as the hook does on its way out.
type planHook struct {
	h  *harness
	id string
	n  string
}

func (p *planHook) uuid() string {
	p.h.t.Helper()
	for _, s := range p.h.be.Opened() {
		if s.TaskID == p.id {
			return s.ClaudeSession
		}
	}
	p.h.t.Fatalf("no session for %s", p.id)
	return ""
}

func (p *planHook) present(text string) {
	p.h.t.Helper()
	n, err := state.NewPlanN()
	if err != nil {
		p.h.t.Fatal(err)
	}
	p.n = n
	if err := state.WritePlanRequest(p.h.root, p.uuid(), state.PlanRequest{N: n, At: p.h.clock.Now(), Plan: text, PID: os.Getpid()}); err != nil {
		p.h.t.Fatal(err)
	}
}

func (p *planHook) decision() state.PlanDecision {
	p.h.t.Helper()
	d, ok, err := state.ReadPlanDecision(p.h.root, p.uuid())
	if err != nil || !ok {
		p.h.t.Fatalf("no decision: %v", err)
	}
	if d.N != p.n {
		p.h.t.Errorf("decision for %s, want %s", d.N, p.n)
	}
	return d
}

func (p *planHook) done() {
	p.h.t.Helper()
	if err := state.RemovePlanApproval(p.h.root, p.uuid(), p.n); err != nil {
		p.h.t.Fatal(err)
	}
}

// planHarness runs A-1 in plan mode with the TUI's plan approval on; the
// session is blocked at its plan until the test signals done.
func planHarness(t *testing.T, toml string) (*harness, *planHook) {
	h := newHarness(t, onePlanTask, toml)
	h.autoSignalExcept("A-1")
	h.be.Script("A-1", states(backend.Blocked, 200)...)
	return h, &planHook{h: h, id: "A-1"}
}

func withPlanApproval(o *Options) { o.PlanApproval = true }

func TestPlanApprovedFromIgris(t *testing.T) {
	h, hook := planHarness(t, "[run]\ncommit = \"auto\"\n[plan_mode]\nthen = \"accept\"\n")
	git := h.dirtyTree()
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened:
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present(planText) })
		case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
			if ev.Plan != planText || ev.Then != ModeAccept {
				t.Errorf("asked with plan %q, then %q", ev.Plan, ev.Then)
			}
			h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanAllow})
		case ev.Kind == PlanClosed:
			if d := hook.decision(); d.Behavior != state.PlanAllow || d.Message != "" {
				t.Errorf("decision %+v", d)
			}
			hook.done()
			if err := h.signal("A-1"); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := h.run(withPlanApproval); err != nil {
		t.Fatal(err)
	}

	// The session's settings file waits for igris and switches to accept.
	settings := h.read(".igris/hooks/A-1.settings.json")
	if !strings.Contains(settings, `plan-approve --mode 'acceptEdits' --wait '30m0s'`) || !strings.Contains(settings, `"timeout": 1830`) {
		t.Errorf("settings file lacks the plan-approve entry:\n%s", settings)
	}
	if got := h.read(".igris/plans/A-1-a1-1.md"); got != planText {
		t.Errorf("kept plan = %q", got)
	}
	info, err := os.Stat(h.path(".igris/plans/A-1-a1-1.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("plan file mode %v, %v", info, err)
	}
	if got, want := h.waits(), "A-1#1 plan_approval, clear A-1#1 plan_approval"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
	events, err := h.dir.Events()
	if err != nil {
		t.Fatal(err)
	}
	var plans []string
	for _, e := range events {
		if strings.HasPrefix(string(e.Type), "plan_") {
			plans = append(plans, string(e.Type)+" "+e.PlanFile+" "+e.Mode)
			if len(e.PlanSHA256) != 64 || e.Attempt != 1 || e.Task != "A-1" {
				t.Errorf("%s: sha %q attempt %d task %q", e.Type, e.PlanSHA256, e.Attempt, e.Task)
			}
		}
	}
	if got, want := strings.Join(plans, ", "), "plan_presented .igris/plans/A-1-a1-1.md , plan_approved .igris/plans/A-1-a1-1.md accept"; got != want {
		t.Errorf("plan events = %q, want %q", got, want)
	}
	// Never the plan text in the run log.
	if log := h.read(".igris/runs.jsonl"); strings.Contains(log, "hello.txt") {
		t.Error("the run log holds the plan text")
	}
	// The commit body carries the approved plan.
	if len(*git) != 2 || !strings.Contains((*git)[1], "-m did A-1 -m Approved plan:\n\n"+strings.TrimSpace(planText)) {
		t.Errorf("git calls %q", *git)
	}
}

// A rejection sends the owner's feedback, cleaned; the session presents a
// revised plan, which is kept as the attempt's second.
func TestPlanRejectedThenApproved(t *testing.T) {
	h, hook := planHarness(t, "")
	asked := 0
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened:
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present("first plan") })
		case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
			asked++
			if asked == 1 {
				h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanDeny})                                     // no feedback: refused
				h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanDeny, Text: "  use \x1b[31mGo\x1b[0m \n"}) // cleaned
				return
			}
			h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanAllow})
		case ev.Kind == PlanClosed && asked == 1:
			if d := hook.decision(); d.Behavior != state.PlanDeny || d.Message != "use Go" {
				t.Errorf("decision %+v", d)
			}
			hook.done()
			h.clock.At(ev.At.Sub(t0)+10*time.Second, func() { hook.present("second plan") })
		case ev.Kind == PlanClosed:
			hook.done()
			_ = h.signal("A-1")
		}
	}
	if _, err := h.run(withPlanApproval); err != nil {
		t.Fatal(err)
	}
	if asked != 2 {
		t.Fatalf("asked %d times", asked)
	}
	if got := h.read(".igris/plans/A-1-a1-2.md"); got != "second plan\n" {
		t.Errorf("second plan file = %q", got)
	}
	if !strings.Contains(h.event(Warning, "").Detail, "a rejection needs feedback") {
		t.Errorf("warning %q", h.event(Warning, "").Detail)
	}
	var plans []string
	for _, typ := range strings.Fields(h.logged()) {
		if strings.HasPrefix(typ, "plan_") {
			plans = append(plans, typ)
		}
	}
	if got, want := strings.Join(plans, " "), "plan_presented plan_rejected plan_presented plan_approved"; got != want {
		t.Errorf("plan events = %s, want %s", got, want)
	}
}

// An answer in the pane makes Claude Code cancel the hook, which removes
// its request: igris closes the view, and the wait ends.
func TestPlanAnsweredInThePane(t *testing.T) {
	h, hook := planHarness(t, "")
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened:
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present(planText) })
		case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
			h.clock.At(ev.At.Sub(t0)+7*time.Second, hook.done)
			h.signalAt(ev.At.Sub(t0)+30*time.Second, "A-1")
		}
	}
	if _, err := h.run(withPlanApproval); err != nil {
		t.Fatal(err)
	}
	if got := h.event(PlanClosed, "A-1").Detail; !strings.Contains(got, "pane") {
		t.Errorf("closed: %q", got)
	}
	if strings.Contains(h.logged(), "plan_approved") || strings.Contains(h.logged(), "plan_rejected") {
		t.Errorf("run log = %s", h.logged())
	}
	// The scripted session stays blocked: from then on it waits like any
	// other blocked session.
	if got, want := h.waits(), "A-1#1 plan_approval, clear A-1#1 plan_approval, A-1#1 blocked, clear A-1#1 blocked"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
}

// Choosing the pane tells the hook to leave the decision to it; the
// session still waits on the owner there, so Needs you follows as for any
// blocked session.
func TestPlanAnswerInPaneChoice(t *testing.T) {
	h, hook := planHarness(t, "")
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened:
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present(planText) })
		case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
			h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanPane})
		case ev.Kind == PlanClosed:
			if d := hook.decision(); d.Behavior != state.PlanPane {
				t.Errorf("decision %+v", d)
			}
			hook.done()
		case ev.Kind == NeedsYou && strings.Contains(ev.Detail, "blocked"):
			_ = h.signal("A-1")
		}
	}
	if _, err := h.run(withPlanApproval); err != nil {
		t.Fatal(err)
	}
	if got, want := h.waits(), "A-1#1 plan_approval, clear A-1#1 plan_approval, A-1#1 blocked, clear A-1#1 blocked"; got != want {
		t.Errorf("waits = %q, want %q", got, want)
	}
}

// Without a TUI (--no-tui) there is no plan-approve entry, and a request
// is never shown.
func TestPlanApprovalOffWithoutTUI(t *testing.T) {
	for _, tc := range []struct {
		name, toml string
		on         bool
	}{
		{"no tui", "", false},
		{"approve_timeout 0", "[plan_mode]\napprove_timeout = \"0\"\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, hook := planHarness(t, tc.toml)
			h.onEvent = func(ev Event) {
				if ev.Kind == SessionOpened {
					h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present(planText) })
					h.signalAt(ev.At.Sub(t0)+60*time.Second, "A-1")
				}
			}
			if _, err := h.run(func(o *Options) { o.PlanApproval = tc.on }); err != nil {
				t.Fatal(err)
			}
			if s := h.read(".igris/hooks/A-1.settings.json"); strings.Contains(s, "plan-approve") {
				t.Errorf("settings file has plan-approve:\n%s", s)
			}
			if h.count(Asked) != 0 || strings.Contains(h.logged(), "plan_") {
				t.Errorf("events %s, log %s", h.kinds(), h.logged())
			}
		})
	}
}

// A session in another mode gets no plan-approve entry, and a request
// whose hook is gone is not shown.
func TestPlanApprovalOnlyForLivePlanSessions(t *testing.T) {
	h, hook := planHarness(t, "")
	h.write("tasks.md", strings.Replace(onePlanTask, "| plan |", "| auto |", 1))
	h.onEvent = func(ev Event) {
		if ev.Kind == SessionOpened {
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() {
				// A hook that is gone (no such process).
				n, _ := state.NewPlanN()
				hook.n = n
				if err := state.WritePlanRequest(h.root, hook.uuid(), state.PlanRequest{N: n, Plan: "x", PID: 1 << 30}); err != nil {
					t.Error(err)
				}
			})
			h.signalAt(ev.At.Sub(t0)+60*time.Second, "A-1")
		}
	}
	if _, err := h.run(withPlanApproval); err != nil {
		t.Fatal(err)
	}
	if s := h.read(".igris/hooks/A-1.settings.json"); strings.Contains(s, "plan-approve") {
		t.Errorf("an auto session got plan-approve:\n%s", s)
	}
	if h.count(Asked) != 0 {
		t.Errorf("events %s", h.kinds())
	}
}

// The Mode cell names the mode after approval and beats [plan_mode] then;
// plain plan takes then. The mode travels in the settings file, is named
// when the owner is asked and logged with the approval (SPEC §7.1).
func TestPlanModeAfterApproval(t *testing.T) {
	for _, tc := range []struct {
		cell, then, setMode string
		yolo                bool // the run's typed confirmation
	}{
		{"plan", "accept", "acceptEdits", false},
		{"plan:default", "default", "default", false},
		{"plan:accept", "accept", "acceptEdits", false},
		{"plan:auto", "auto", "auto", false},
		{"Plan:Yolo", "yolo", "bypassPermissions", true},
	} {
		t.Run(tc.cell, func(t *testing.T) {
			h, hook := planHarness(t, "[plan_mode]\nthen = \"accept\"\n")
			h.write("tasks.md", strings.Replace(onePlanTask, "| plan |", "| "+tc.cell+" |", 1))
			h.onEvent = func(ev Event) {
				switch {
				case ev.Kind == SessionOpened:
					h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present(planText) })
				case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
					if ev.Then != tc.then || !strings.HasSuffix(ev.Detail, "mode "+tc.then) {
						t.Errorf("asked with then %q: %s", ev.Then, ev.Detail)
					}
					h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanAllow})
				case ev.Kind == PlanClosed:
					hook.done()
					_ = h.signal("A-1")
				}
			}
			if _, err := h.run(withPlanApproval, func(o *Options) { o.ConfirmedYolo = tc.yolo }); err != nil {
				t.Fatal(err)
			}
			if s := h.read(".igris/hooks/A-1.settings.json"); !strings.Contains(s, "plan-approve --mode '"+tc.setMode+"'") {
				t.Errorf("settings file:\n%s", s)
			}
			args := h.be.Opened()[0].Args
			if !slices.Contains(args, "plan") || slices.Contains(args, "--dangerously-skip-permissions") {
				t.Errorf("args %q", args)
			}
			if got := slices.Contains(args, "--allow-dangerously-skip-permissions"); got != tc.yolo {
				t.Errorf("--allow-dangerously-skip-permissions passed: %v, want %v", got, tc.yolo)
			}
			if !strings.Contains(h.read(".igris/runs.jsonl"), `"mode":"`+tc.then+`"`) {
				t.Errorf("run log lacks the mode: %s", h.read(".igris/runs.jsonl"))
			}
		})
	}
}

// plan:yolo is refused without the run's typed confirmation, like yolo
// (SPEC §7.3): no session, no settings file.
func TestPlanYoloNeedsConfirmation(t *testing.T) {
	h, _ := planHarness(t, "")
	h.write("tasks.md", strings.Replace(onePlanTask, "| plan |", "| plan:yolo |", 1))
	_, err := h.run(withPlanApproval)
	if !errors.Is(err, ErrYoloUnconfirmed) || !strings.Contains(err.Error(), "plan:yolo mode") {
		t.Fatalf("err = %v, want ErrYoloUnconfirmed", err)
	}
	if got := h.opened(); got != "" {
		t.Errorf("sessions opened for %q", got)
	}
}

// The settings file is valid JSON with both PermissionRequest entries.
func TestPlanSettingsEntries(t *testing.T) {
	h, _ := planHarness(t, "[plan_mode]\napprove_timeout = \"2h\"\n")
	h.autoSignalExcept()
	if _, err := h.run(withPlanApproval); err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct {
				Command string
				Timeout int
			}
		}
	}
	if err := json.Unmarshal([]byte(h.read(filepath.Join(".igris", "hooks", "A-1.settings.json"))), &s); err != nil {
		t.Fatal(err)
	}
	pr := s.Hooks["PermissionRequest"]
	if len(pr) != 2 || pr[0].Matcher != "*" || pr[1].Matcher != "ExitPlanMode" || pr[1].Hooks[0].Timeout != 7230 {
		t.Errorf("PermissionRequest entries %+v", pr)
	}
	if !strings.HasSuffix(pr[1].Hooks[0].Command, ` hook --root '`+h.root+`' plan-approve --mode 'auto' --wait '2h0m0s'`) {
		t.Errorf("command %q", pr[1].Hooks[0].Command)
	}
}

func TestFeedback(t *testing.T) {
	long := strings.Repeat("é", state.MaxPlanFeedback)
	for _, tc := range []struct{ in, want string }{
		{"  fine ", "fine"},
		{"a\x1b]0;title\x07b", "ab"},
		{"two\nlines", "two\nlines"},
	} {
		if got := Feedback(tc.in); got != tc.want {
			t.Errorf("Feedback(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := Feedback(long); len(got) > state.MaxPlanFeedback || !strings.HasPrefix(long, got) {
		t.Errorf("long feedback: %d bytes", len(got))
	}
}

// A resumed run that reattaches a plan session shows the plan its hook
// still waits with (SPEC §6.3).
func TestPlanShownAfterResume(t *testing.T) {
	h, hook := planHarness(t, "")
	h.stopMidTask(6*time.Second, withPlanApproval)
	hook.present(planText) // while igris was down
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
			h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanAllow})
		case ev.Kind == PlanClosed:
			hook.done()
			_ = h.signal("A-1")
		}
	}
	res, err := h.run(resumeLast, withPlanApproval)
	if err != nil || res.Outcome != Completed {
		t.Fatalf("resumed run = %s, %v", res.Outcome, err)
	}
	if h.count(Asked) != 1 || !strings.Contains(h.logged(), "task_resumed plan_presented") {
		t.Errorf("events %s, log %s", h.kinds(), h.logged())
	}
}

// The commit body carries at most 8 KiB of the approved plan.
func TestApprovedPlanCappedInCommit(t *testing.T) {
	h, hook := planHarness(t, "[run]\ncommit = \"auto\"\n")
	git := h.dirtyTree()
	long := strings.Repeat("ü", maxCommitPlan) // twice the cap in bytes
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == SessionOpened:
			h.clock.At(ev.At.Sub(t0)+5*time.Second, func() { hook.present(long) })
		case ev.Kind == Asked && ev.Question == QuestionPlanApproval:
			h.eng.Send(Command{Kind: CmdPlan, Plan: state.PlanAllow})
		case ev.Kind == PlanClosed:
			hook.done()
			_ = h.signal("A-1")
		}
	}
	if _, err := h.run(withPlanApproval); err != nil {
		t.Fatal(err)
	}
	if len(*git) != 2 {
		t.Fatalf("git calls %q", *git)
	}
	body := (*git)[1]
	if !strings.Contains(body, "[plan cut at 8 KiB]") || strings.Count(body, "ü") != maxCommitPlan/2 {
		t.Errorf("commit body has %d of the plan's characters", strings.Count(body, "ü"))
	}
}
