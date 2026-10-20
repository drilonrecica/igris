package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/hook"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// Plan approval from igris (SPEC §6.3, V11-P1). A session in plan mode
// whose settings file has the plan-approve hook hands each plan it
// presents to igris through .igris/plan-approval/; igris shows it, and
// the owner approves, rejects with feedback or answers in the pane.

// maxCommitPlan caps the approved plan in a commit body (SPEC §6.5).
const maxCommitPlan = 8 << 10

// setModeOf maps the mode after approval to the Claude Code permission
// mode setMode takes (SPEC §7.1). bypassPermissions takes effect only in a
// session started with --allow-dangerously-skip-permissions, which
// plan:yolo, and only it, gets (modeFlags).
var setModeOf = map[string]string{ModeDefault: "default", ModeAccept: "acceptEdits", ModeAuto: "auto", ModeYolo: "bypassPermissions"}

// planShown is the plan request the owner is shown for l's session.
type planShown struct {
	n    string // the request's ID; "" while none is shown
	file string // where the plan is kept, relative to the root; "" if saving failed
	sum  string // sha256 of the plan text
	// answered is the request igris answered; its file stays until the
	// hook has read the decision, and is not shown again meanwhile.
	answered string
}

// planThen is the mode a session in plan mode switches to once igris
// approves its plan (SPEC §7.1): the one its mode names, or [plan_mode]
// then for plain plan.
func (e *Engine) planThen(mode string) string { return After(mode, e.cfg.PlanMode.ThenOrDefault()) }

// planApproval is the plan approval entry for a session in mode, or nil:
// only a plan session gets it, only with a TUI to approve from, and not
// with approve_timeout = "0" (SPEC §6.3).
func (e *Engine) planApproval(mode string) *hook.PlanApproval {
	wait := e.cfg.PlanMode.ApproveTimeoutOrDefault()
	if !IsPlan(mode) || !e.opts.PlanApproval || wait <= 0 {
		return nil
	}
	sm, ok := setModeOf[e.planThen(mode)]
	if !ok {
		return nil // validation keeps the modes to the ones above
	}
	return &hook.PlanApproval{Mode: sm, Wait: wait}
}

// checkPlanRequest looks for a plan l's session presents for approval, and for
// one shown that is gone: the owner answered in the pane (Claude Code
// cancels the hook), the hook gave up or igris answered it.
func (e *Engine) checkPlanRequest(ctx context.Context, l *launch) {
	if e.planApproval(l.mode) == nil || !ValidSessionID(l.cur.ClaudeSession) {
		return // the session has no plan-approve hook; nothing to show
	}
	p := &l.plan
	r, ok, err := state.ReadPlanRequest(e.dir.Root(), l.cur.ClaudeSession)
	if err != nil {
		e.reportOnce("plan|"+err.Error(), Warning, err.Error())
	}
	if ok && r.PID > 0 && !state.ProcessAlive(r.PID) {
		ok = false // the hook is gone; its request answers nothing
	}
	if p.n != "" && (!ok || r.N != p.n) {
		e.closePlan(l, "the plan was answered in the session's pane, or its hook gave up")
	}
	if !ok {
		p.answered = ""
		return
	}
	if r.N == p.n || r.N == p.answered {
		return
	}
	e.presentPlan(ctx, l, r)
}

// presentPlan keeps the plan of request r, logs it and asks the owner.
func (e *Engine) presentPlan(ctx context.Context, l *launch, r state.PlanRequest) {
	t := l.t
	sum := sha256.Sum256([]byte(r.Plan))
	l.plan = planShown{n: r.N, sum: hex.EncodeToString(sum[:])}
	file, err := e.dir.SavePlan(t.ID, max(l.attempt, 1), r.Plan)
	if err != nil {
		e.warn(fmt.Sprintf("keep the plan of %s: %v", t.ID, err))
	}
	l.plan.file = file
	e.log(state.Event{Type: state.EventPlanPresented, PlanFile: file, PlanSHA256: l.plan.sum})
	e.needsYou(ctx, notifyNeedsInput, state.ReasonPlanApproval, "the session presents a plan for approval", "plan awaiting approval")
	text := r.Plan
	if strings.TrimSpace(text) == "" {
		text = "(the session sent an empty plan)"
	}
	e.emit(Event{
		Kind:     Asked,
		Question: QuestionPlanApproval,
		Detail:   fmt.Sprintf("approve the plan of %s? approving switches its session to mode %s", t.ID, e.planThen(l.mode)),
		Plan:     text,
		Then:     e.planThen(l.mode),
	})
}

// decidePlan sends the owner's decision c on the plan shown for l's
// session to its hook.
func (e *Engine) decidePlan(l *launch, c Command) {
	p := &l.plan
	if p.n == "" {
		e.reject(c, "no plan is waiting for approval")
		return
	}
	d := state.PlanDecision{N: p.n, Behavior: c.Plan}
	if d.Behavior == state.PlanDeny {
		d.Message = Feedback(c.Text)
		if d.Message == "" {
			e.reject(c, "a rejection needs feedback for the session")
			return
		}
	}
	if err := state.WritePlanDecision(e.dir.Root(), l.cur.ClaudeSession, d); err != nil {
		e.warn(fmt.Sprintf("answer the plan of %s: %v; answer it in the session's pane", l.t.ID, err))
		return
	}
	ev := state.Event{PlanFile: p.file, PlanSHA256: p.sum}
	why := "you answer the plan in the session's pane"
	switch d.Behavior {
	case state.PlanAllow:
		ev.Type, ev.Mode = state.EventPlanApproved, e.planThen(l.mode)
		why = "plan approved; the session switches to mode " + e.planThen(l.mode)
		l.cur.ApprovedPlan = p.file
		if err := e.dir.SaveRun(e.run); err != nil {
			e.warn(err.Error())
		}
	case state.PlanDeny:
		ev.Type = state.EventPlanRejected
		why = "plan rejected; the session revises it"
	}
	if ev.Type != "" {
		e.log(ev)
	}
	p.answered = p.n
	if d.Behavior == state.PlanPane {
		// The session still waits on the owner, in its pane now: the
		// blocked episode's Needs you takes over.
		l.plan.n, l.plan.file, l.plan.sum = "", "", ""
		e.emit(Event{Kind: PlanClosed, Detail: why})
		e.waitOver(state.ReasonPlanApproval)
		return
	}
	e.closePlan(l, why)
}

// closePlan ends the wait of the plan shown for l and closes its view; why
// says how. Nothing is shown: nothing happens.
func (e *Engine) closePlan(l *launch, why string) {
	if l.plan.n == "" {
		return
	}
	l.plan.n, l.plan.file, l.plan.sum = "", "", ""
	e.emit(Event{Kind: PlanClosed, Detail: why})
	e.emit(Event{Kind: NeedsYouClear})
	e.waitOver(state.ReasonPlanApproval)
}

// dropPlan forgets the plan shown for l's session: the session is lost,
// replaced or done.
func (e *Engine) dropPlan(l *launch, why string) {
	e.closePlan(l, why)
	l.plan = planShown{}
}

// Feedback cleans a rejection's feedback for the session (SPEC §6.3):
// without escape sequences, trimmed, at most MaxPlanFeedback bytes, cut
// at a character boundary.
func Feedback(s string) string {
	s = strings.TrimSpace(textsafe.Clean(s))
	if len(s) <= state.MaxPlanFeedback {
		return s
	}
	s = s[:state.MaxPlanFeedback]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// approvedPlan is the plan igris approved for l's task, for the commit
// body, capped at maxCommitPlan bytes; "" without one.
func (e *Engine) approvedPlan(l *launch) string {
	if l.cur == nil || l.cur.ApprovedPlan == "" {
		return ""
	}
	text, err := state.ReadPlanFile(e.dir.Root(), l.cur.ApprovedPlan)
	if err != nil {
		e.warn(fmt.Sprintf("the approved plan is left out of the commit: %v", err))
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) > maxCommitPlan {
		text = text[:maxCommitPlan]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += "\n[plan cut at 8 KiB]"
	}
	return text
}
