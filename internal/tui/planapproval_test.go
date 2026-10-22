package tui

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/state"
)

// Plan approval from igris (SPEC §6.3): the plan view and its three
// answers.

const tuiPlan = "# Plan\n\n1. Add the parser\n   - nested: keep the indent\n2. Test it\n"

func planAsked() engine.Event {
	return engine.Event{Kind: engine.Asked, Task: "M0-03", Question: engine.QuestionPlanApproval,
		Detail: "approve the plan of M0-03? approving switches its session to mode auto", Plan: tuiPlan, Then: "auto"}
}

func TestPlanViewShowsThePlan(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), opened("M0-03"), planAsked())
	d := hs.m.dialog
	if d == nil || d.question != engine.QuestionPlanApproval {
		t.Fatalf("dialog %+v", d)
	}
	v := hs.m.View()
	for _, want := range []string{"M0-03 presents a plan", "1. Add the parser", "   - nested: keep the indent", "Approve: the session switches to auto", "Reject with feedback…", "Answer in the pane"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if hs.m.cur.state != stateQuestion {
		t.Errorf("state %v", hs.m.cur.state)
	}
	// Esc leaves it pending; Answer… brings it back.
	hs.key("esc")
	if hs.m.dialog != nil || hs.m.firstAsk() == nil {
		t.Fatal("esc dropped the plan")
	}
	hs.click(isAct(actAnswer))
	if hs.m.dialog == nil || hs.m.dialog.question != engine.QuestionPlanApproval {
		t.Fatal("Answer… did not reopen the plan")
	}
}

func TestPlanApproveAndPane(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), opened("M0-03"), planAsked())
	hs.key("enter")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdPlan, Plan: state.PlanAllow, Task: "M0-03"}) {
		t.Fatalf("sent %+v", got)
	}
	if hs.m.dialog != nil || hs.m.firstAsk() != nil {
		t.Error("approving left the plan open")
	}

	hs.events(planAsked())
	hs.key("3")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdPlan, Plan: state.PlanPane, Task: "M0-03"}) {
		t.Fatalf("sent %+v", got)
	}
	if len(hs.focus) != 1 {
		t.Errorf("the pane was not opened: %v", hs.focus)
	}
}

func TestPlanRejectNeedsFeedback(t *testing.T) {
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), opened("M0-03"), planAsked())
	hs.key("2")
	d := hs.m.dialog
	if d == nil || d.input == nil || !d.inField {
		t.Fatalf("Reject opened %+v", d)
	}
	hs.key("enter") // empty: refused
	if len(hs.s.take()) != 0 || !strings.Contains(hs.m.View(), "Type what the session should change.") {
		t.Fatalf("an empty rejection went out:\n%s", hs.m.View())
	}
	hs.key("esc") // back to the plan
	if hs.m.dialog == nil || hs.m.dialog.question != engine.QuestionPlanApproval {
		t.Fatalf("esc in the feedback did not return to the plan: %+v", hs.m.dialog)
	}
	hs.keys("2", "u", "s", "e", " ", "G", "o", "enter")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdPlan, Plan: state.PlanDeny, Text: "use Go", Task: "M0-03"}) {
		t.Fatalf("sent %+v", got)
	}
	if hs.m.dialog != nil || hs.m.firstAsk() != nil {
		t.Error("rejecting left the plan open")
	}
}

// When the plan is answered elsewhere (the pane, the hook gave up), the
// view closes, also while the feedback dialog is open.
func TestPlanClosedElsewhere(t *testing.T) {
	for _, keys := range [][]string{nil, {"2", "x"}} {
		hs := newHarness(t, 120, 30)
		hs.events(started("M0-03"), opened("M0-03"), planAsked())
		hs.keys(keys...)
		hs.events(engine.Event{Kind: engine.PlanClosed, Task: "M0-03", Detail: "the plan was answered in the session's pane, or its hook gave up"})
		if hs.m.dialog != nil || hs.m.firstAsk() != nil {
			t.Errorf("keys %v: the plan stayed open", keys)
		}
		if hs.m.cur.state != stateNeedsYou {
			t.Errorf("keys %v: state %v", keys, hs.m.cur.state)
		}
		if v := hs.m.View(); !strings.Contains(v, "answered in the session's pane") {
			t.Errorf("keys %v: the log doesn't say how it closed:\n%s", keys, v)
		}
		if len(hs.s.take()) != 0 {
			t.Errorf("keys %v: something was sent", keys)
		}
	}
}

// A long plan scrolls inside the dialog; the narrow layout shows it too.
func TestPlanViewScrollsAndNarrow(t *testing.T) {
	long := planAsked()
	var b strings.Builder
	for i := range 60 {
		b.WriteString("- step ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString("\n")
	}
	b.WriteString("- LAST STEP\n")
	long.Plan = b.String()
	hs := newHarness(t, 120, 30)
	hs.events(started("M0-03"), opened("M0-03"), long)
	if strings.Contains(hs.m.View(), "LAST STEP") {
		t.Fatal("the whole long plan fit")
	}
	for range 30 {
		hs.key("pgdown")
	}
	if !strings.Contains(hs.m.View(), "LAST STEP") {
		t.Errorf("pgdown did not reach the end:\n%s", hs.m.View())
	}
	narrow := newHarness(t, 50, 24)
	narrow.events(started("M0-03"), opened("M0-03"), planAsked())
	if v := narrow.m.View(); !strings.Contains(v, "Add the parser") || !strings.Contains(v, "Answer in the pane") {
		t.Errorf("narrow plan view:\n%s", v)
	}
}

func TestWrapPre(t *testing.T) {
	got := wrapPre("a\n\t- b c d\n    e\n          deep", 8)
	// The indent is kept, up to half the width.
	want := []string{"a", "  - b c", "  d", "    e", "    deep"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapPre = %q, want %q", got, want)
	}
}

// Approving a plan:yolo session's plan says it skips permissions from then on.
func TestPlanViewYoloBadge(t *testing.T) {
	hs := newHarness(t, 120, 30)
	ev := planAsked()
	ev.Then = "yolo"
	hs.events(started("M0-03"), opened("M0-03"), ev)
	if v := hs.m.View(); !strings.Contains(v, "Approve: the session switches to yolo [SKIP PERMISSIONS]") {
		t.Errorf("view lacks the badge:\n%s", v)
	}
}
