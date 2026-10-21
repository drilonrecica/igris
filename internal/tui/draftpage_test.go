package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/checks"
	"github.com/drilonrecica/igris/internal/draft"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/report"
)

const draftProposed = `## W — Widgets

| ID | Task | Status | Model |
|---|---|---|---|
| W-1 | Write the spec | ready | |
| W-2 | Parse widgets | blocked | ? |
`

// draftResult is a finished draft of a new plan on the sinjal project; the
// plan has one rank left at ?.
func draftResult() *draft.Result {
	return &draft.Result{
		Source:       draft.Goal,
		PlanPath:     "/src/sinjal/tasks.md",
		ProposalPath: "/src/sinjal/.igris/draft/tasks.proposed.md",
		Proposed:     []byte(draftProposed),
		Issues:       []plan.Issue{{File: "/src/sinjal/.igris/draft/tasks.proposed.md", Line: 6, Msg: `W-2: unknown model rank "?"`}},
		Ranks:        draft.Ranks{Set: 3, Open: 1},
	}
}

// extendResult is a finished extend: two new tasks for M2.
func extendResult() *draft.Result {
	return &draft.Result{
		Source:       draft.Extend,
		PlanPath:     "/src/sinjal/tasks.md",
		ProposalPath: "/src/sinjal/.igris/draft/tasks.extend.md",
		Proposed:     []byte("## M2\n"),
		Ranks:        draft.Ranks{Set: 2},
		Extension: &plan.Extension{Additions: []plan.Addition{{
			File: "/src/sinjal/tasks.md", Phase: "M2", Heading: "M2 Config & state",
			Tasks: []*plan.Task{{ID: "M2-05", Title: "Docs", Rank: "sonnet", OwnerText: "agent"}, {ID: "M2-06", Title: "Release notes", Rank: "sonnet", OwnerText: "agent"}},
		}}},
	}
}

type draftFunc = func(context.Context, DraftRequest, io.Writer, func(backend.SessionRef)) (*draft.Result, error)

// finishedDraft reports, opens its session and returns res and err at once.
func finishedDraft(res *draft.Result, err error) draftFunc {
	return func(_ context.Context, req DraftRequest, out io.Writer, opened func(backend.SessionRef)) (*draft.Result, error) {
		fmt.Fprintf(out, "12:00:00 draft session started (%s); answer its questions in the DRAFT pane\n", req.Model)
		opened(backend.SessionRef{Backend: "fake", PaneID: "p1"})
		fmt.Fprint(out, "12:00:09 draft session done: \x1b[31mproposal written")
		return res, err
	}
}

// workingDraft opens its session and works until it is cancelled.
func workingDraft(ctx context.Context, req DraftRequest, out io.Writer, opened func(backend.SessionRef)) (*draft.Result, error) {
	fmt.Fprintf(out, "12:00:00 draft session started (%s); answer its questions in the DRAFT pane\n", req.Model)
	opened(backend.SessionRef{Backend: "fake", PaneID: "p1"})
	<-ctx.Done()
	return nil, fmt.Errorf("draft stopped; the DRAFT session stays open: %w", ctx.Err())
}

// newPlanSnap is the sinjal project with no plan file; extendSnap is
// the same project with a valid one.
func newPlanSnap(apiKey bool) *report.Snapshot {
	s := homeSnap("ready")
	s.Plan, s.Status, s.Issues, s.PlanMissing, s.Recent = nil, nil, nil, true, nil
	s.APIKeySet = apiKey
	return s
}

func extendSnap(apiKey bool) *report.Snapshot {
	s := homeSnap("ready")
	s.APIKeySet = apiKey
	return s
}

func draftApp(t *testing.T, svc *fakeServices, snap *report.Snapshot) (*appModel, *homeScreen) {
	t.Helper()
	svc.snapshot = func(context.Context) (*report.Snapshot, error) { return snap, nil }
	return pollApp(t, svc)
}

func TestDraftPicker(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap *report.Snapshot
		want string
	}{
		{"new plan", newPlanSnap(false), "From a goal — Claude interviews you, then plans · From a spec file… — a spec or PRD in this project · Import an export… — GitHub, Linear or Jira issues · Cancel"},
		{"valid plan", extendSnap(false), "Extend the plan… — add phases or tasks · Cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeServices{}
			a, home := draftApp(t, svc, tc.snap)
			press(a, "D")
			d := home.dialog
			if d == nil || home.drafting == nil {
				t.Fatal("D didn't open the draft picker")
			}
			if got := strings.Join(labels(d), " · "); got != tc.want {
				t.Errorf("options = %s", got)
			}
			if !strings.Contains(d.detail, "nothing reaches tasks.md until you accept it in the review") {
				t.Errorf("detail = %q", d.detail)
			}
			press(a, "esc")
			if home.dialog != nil || home.drafting != nil || svc.called("Draft") != 0 {
				t.Error("esc didn't cancel the picker")
			}
		})
	}
}

func TestDraftAPIKeyWarning(t *testing.T) {
	a, home := draftApp(t, &fakeServices{}, newPlanSnap(true))
	press(a, "D")
	if !strings.Contains(home.dialog.detail, checks.APIKeyWarning) {
		t.Errorf("picker lacks the API key warning: %q", home.dialog.detail)
	}
	press(a, "1")
	if !strings.Contains(home.dialog.detail, checks.APIKeyWarning) {
		t.Errorf("dialog lacks the API key warning: %q", home.dialog.detail)
	}
}

// Draft… is offered only where SPEC §15.6 says: the backend is reachable,
// no run holds the project, igris.toml is valid or missing, and there is a
// slot for a plan or a valid plan to extend.
func TestDraftOnlyWhenItApplies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		snap    func() *report.Snapshot
		backend error
		want    bool
	}{
		{"plan missing", func() *report.Snapshot { return newPlanSnap(false) }, nil, true},
		{"valid plan", func() *report.Snapshot { return extendSnap(false) }, nil, true},
		{"no igris.toml", func() *report.Snapshot {
			s := newPlanSnap(false)
			s.NoConfig = true
			return s
		}, nil, true},
		{"no backend", func() *report.Snapshot { return extendSnap(false) }, errors.New("not inside a herdr pane"), false},
		{"config invalid", func() *report.Snapshot {
			s := newPlanSnap(false)
			s.ConfigProblems = []string{"igris.toml:3: default_mode: unknown mode"}
			return s
		}, nil, false},
		{"plan invalid", func() *report.Snapshot {
			s := extendSnap(false)
			s.Plan = plan.Parse("tasks.md", []byte(homeInvalidPlan), plan.Options{})
			s.Issues = report.Issues(s.Plan.Validate(s.Config.Rules("")))
			return s
		}, nil, false},
		{"run holds the project", func() *report.Snapshot {
			s := extendSnap(false)
			s.Lock.Alive = true
			return s
		}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeServices{backendErr: tc.backend}
			a, home := draftApp(t, svc, tc.snap())
			press(a, "D")
			if got := home.dialog != nil; got != tc.want {
				t.Errorf("D opened a dialog: %v, want %v", got, tc.want)
			}
			offered := false
			for _, b := range home.buttons() {
				offered = offered || b.act == actDraft
			}
			if offered != tc.want {
				t.Errorf("Draft… in the bar: %v, want %v", offered, tc.want)
			}
		})
	}
}

func TestDraftGoalAccept(t *testing.T) {
	svc := &fakeServices{draft: finishedDraft(draftResult(), nil)}
	a, home := draftApp(t, svc, newPlanSnap(false))
	press(a, "D", "1")
	d := home.dialog
	if d == nil || d.input != nil {
		t.Fatalf("the goal dialog = %+v, want a model choice without a field", d)
	}
	if got := strings.Join(labels(d), " · "); got != "Cancel · Draft with sonnet · Draft with opus" {
		t.Errorf("options = %s", got)
	}
	if !strings.Contains(d.detail, "draft.model in igris.toml is opus") {
		t.Errorf("detail = %q", d.detail)
	}
	press(a, "3")
	if len(svc.drafted) != 1 || svc.drafted[0] != (DraftRequest{Source: draft.Goal, Model: "opus"}) {
		t.Fatalf("draft ran with %+v", svc.drafted)
	}
	rv, ok := top(a).(*review)
	if !ok || len(a.stack) != 2 {
		t.Fatalf("the review didn't replace the progress screen: %d screens, top %T", len(a.stack), top(a))
	}
	v := rv.View()
	for _, want := range []string{"igris draft · review tasks.md", "ranks are suggestions: 3 set, 1 left at ?", "W-2"} {
		if !strings.Contains(v, want) {
			t.Errorf("review lacks %q:\n%s", want, v)
		}
	}
	press(a, "a") // the proposal doesn't pass check: asks again
	if rv.dialog == nil || svc.called("AcceptDraft") != 0 {
		t.Fatal("accepting a proposal with problems didn't ask again")
	}
	press(a, "2")
	if len(a.stack) != 1 || svc.called("AcceptDraft") != 1 {
		t.Fatalf("Write anyway: %d screens, accepted %d times", len(a.stack), svc.called("AcceptDraft"))
	}
	for _, want := range []string{"draft accepted: tasks.md written", "ranks are suggestions (3 set, 1 left at ?)", "1 problem left", "Check shows them"} {
		if !strings.Contains(home.status, want) {
			t.Errorf("status %q lacks %q", home.status, want)
		}
	}
}

func TestDraftReject(t *testing.T) {
	svc := &fakeServices{draft: finishedDraft(draftResult(), nil)}
	a, home := draftApp(t, svc, newPlanSnap(false))
	press(a, "D", "1", "2", "r")
	if len(a.stack) != 1 || svc.called("AcceptDraft") != 0 || svc.called("Draft goal sonnet") != 1 {
		t.Fatalf("reject: %d screens, accepted %d times", len(a.stack), svc.called("AcceptDraft"))
	}
	if want := "draft rejected: nothing was written to tasks.md; the proposal stays in .igris/draft/tasks.proposed.md"; home.status != want {
		t.Errorf("status %q, want %q", home.status, want)
	}
}

func TestDraftAcceptFails(t *testing.T) {
	res := draftResult()
	res.Issues = nil
	svc := &fakeServices{draft: finishedDraft(res, nil), draftErr: errors.New("not writing tasks.md: a file appeared there after the review")}
	a, home := draftApp(t, svc, newPlanSnap(false))
	press(a, "D", "1", "2", "a")
	if len(a.stack) != 1 || svc.called("AcceptDraft") != 1 || home.status != "draft: not writing tasks.md: a file appeared there after the review" {
		t.Errorf("%d screens, status %q", len(a.stack), home.status)
	}
}

func TestDraftSpecAsksForThePath(t *testing.T) {
	svc := &fakeServices{draft: finishedDraft(draftResult(), nil)}
	snap := newPlanSnap(false)
	snap.Config.Draft.Model = "sonnet"
	a, home := draftApp(t, svc, snap)
	press(a, "D", "2")
	d := home.dialog
	if d == nil || d.input == nil || !d.inField || d.input.label != "Spec file: " {
		t.Fatalf("the spec dialog = %+v, want a focused path field", d)
	}
	if !strings.Contains(d.detail, "draft.model in igris.toml is sonnet") {
		t.Errorf("detail = %q", d.detail)
	}

	press(a, "enter") // nothing typed: the dialog stays and says why
	if home.dialog != d || !d.inField || d.input.hint == "" || svc.called("Draft") != 0 {
		t.Fatalf("an empty path started draft or closed the dialog (hint %q)", d.input.hint)
	}

	press(a, "docs/spec.md", "enter") // enter in the field is the configured model
	if len(svc.drafted) != 1 || svc.drafted[0] != (DraftRequest{Source: draft.Spec, Model: "sonnet", File: "docs/spec.md"}) {
		t.Fatalf("draft ran with %+v", svc.drafted)
	}
}

func TestDraftImportAndExtendRequests(t *testing.T) {
	svc := &fakeServices{draft: finishedDraft(draftResult(), nil)}
	a, _ := draftApp(t, svc, newPlanSnap(false))
	press(a, "D", "3", "export.json", "enter")
	press(a, "q") // leaves the review
	if len(svc.drafted) != 1 || svc.drafted[0] != (DraftRequest{Source: draft.Import, Model: "opus", File: "export.json"}) {
		t.Fatalf("import ran with %+v", svc.drafted)
	}

	svc = &fakeServices{draft: finishedDraft(extendResult(), nil), backups: []string{"/src/sinjal/.igris/draft/tasks.md.20261022-120000.bak.md"}}
	a, home := draftApp(t, svc, extendSnap(false))
	press(a, "D", "1")
	if d := home.dialog; d == nil || d.input == nil || d.input.label != "Request (optional): " {
		t.Fatalf("the extend dialog = %+v", d)
	}
	press(a, "a phase for the release", "down", "3") // down to the options: Draft with opus
	if len(svc.drafted) != 1 || svc.drafted[0] != (DraftRequest{Source: draft.Extend, Model: "opus", Text: "a phase for the release"}) {
		t.Fatalf("extend ran with %+v", svc.drafted)
	}
	rv, ok := top(a).(*review)
	if !ok {
		t.Fatalf("top screen %T, want the review", top(a))
	}
	if v := rv.View(); !strings.Contains(v, "M2-05") || !strings.Contains(v, "ranks are suggestions: 2 set, 0 left at ?") {
		t.Errorf("review:\n%s", v)
	}
	press(a, "a")
	for _, want := range []string{"draft accepted: 2 tasks added to tasks.md", "backed up to .igris/draft/tasks.md.20261022-120000.bak.md", "ranks are suggestions (2 set, 0 left at ?)"} {
		if !strings.Contains(home.status, want) {
			t.Errorf("status %q lacks %q", home.status, want)
		}
	}
	if strings.Contains(home.status, "problem") {
		t.Errorf("status mentions problems: %q", home.status)
	}
}

// An extend request is optional: left empty, the session asks.
func TestDraftExtendWithoutARequest(t *testing.T) {
	svc := &fakeServices{draft: finishedDraft(extendResult(), nil)}
	a, _ := draftApp(t, svc, extendSnap(false))
	press(a, "D", "1", "enter")
	if len(svc.drafted) != 1 || svc.drafted[0] != (DraftRequest{Source: draft.Extend, Model: "opus"}) {
		t.Fatalf("extend ran with %+v", svc.drafted)
	}
}

// A draft that can't start (a plan in the way, a file that can't be used)
// stays on its screen with the reason; nothing was written.
func TestDraftFailureStaysOnTheScreen(t *testing.T) {
	svc := &fakeServices{draft: func(context.Context, DraftRequest, io.Writer, func(backend.SessionRef)) (*draft.Result, error) {
		return nil, errors.New("spec file docs/spec.md doesn't exist; pass the path of a spec or PRD in the project")
	}}
	a, home := draftApp(t, svc, newPlanSnap(false))
	press(a, "D", "2", "docs/spec.md", "enter")
	s, ok := top(a).(*draftScreen)
	if !ok || len(a.stack) != 2 {
		t.Fatalf("%d screens, top %T, want the progress screen", len(a.stack), top(a))
	}
	v := s.View()
	for _, want := range []string{"Draft · from a spec file · opus · failed", "draft failed: spec file docs/spec.md doesn't exist", "Nothing was written."} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if !strings.HasPrefix(home.status, "draft failed: spec file") {
		t.Errorf("status %q", home.status)
	}
	press(a, "q")
	if len(a.stack) != 1 {
		t.Errorf("q left %d screens", len(a.stack))
	}
}

// draftProgress starts draft on a progress screen over a home on a missing
// plan and takes its news until the session is open.
func draftProgress(t *testing.T, svc *fakeServices, w, h int) (*draftScreen, *homeScreen) {
	t.Helper()
	home := newHome(context.Background(), svc, &theme{})
	home.now = func() time.Time { return homeNow }
	home.Update(snapshotMsg{newPlanSnap(false), nil})
	home.Update(backendMsg{nil})
	s := newDraftScreen(home, DraftRequest{Source: draft.Goal, Model: "opus"})
	s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	s.Init()
	for s.ref == nil {
		s.Update(s.run.next())
	}
	return s, home
}

func TestDraftOpenSessionAndCancel(t *testing.T) {
	svc := &fakeServices{draft: workingDraft}
	s, home := draftProgress(t, svc, 80, 24)
	v := s.View()
	for _, want := range []string{"Draft · from a goal · opus · working…", "draft session started (opus)", "[Open session]", "[Cancel]"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}

	_, cmd := s.Update(adaptKey("o"))
	req, ok := cmd().(asyncReq)
	if !ok {
		t.Fatal("o didn't ask to open the session")
	}
	s.Update(req.fn())
	if svc.called("Focus") != 1 {
		t.Errorf("Focus called %d times", svc.called("Focus"))
	}

	s.Update(adaptKey("esc"))
	if v := s.View(); !strings.Contains(v, "cancelling…") {
		t.Errorf("view after cancel:\n%s", v)
	}
	var msg adaptNewsMsg
	for !msg.ended {
		msg = s.run.next()
	}
	_, cmd = s.Update(msg)
	if _, ok := cmd().(popMsg); !ok {
		t.Error("the cancelled screen didn't close")
	}
	if want := "draft cancelled; nothing changed, and the DRAFT session stays open"; home.status != want {
		t.Errorf("status %q, want %q", home.status, want)
	}
}

func TestDraftOpenSessionFails(t *testing.T) {
	svc := &fakeServices{draft: workingDraft, focusErr: errors.New("session is gone")}
	s, _ := draftProgress(t, svc, 80, 24)
	_, cmd := s.Update(adaptKey("o"))
	s.Update(cmd().(asyncReq).fn())
	if v := s.View(); !strings.Contains(v, "open session: session is gone") {
		t.Errorf("view:\n%s", v)
	}
	s.Update(adaptKey("q"))
	<-s.run.done
}

// The picker, the field dialog and the progress screen at the three sizes
// (SPEC §17).
func TestDraftGolden(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 20}} {
		w, h := size[0], size[1]
		home := func(snap *report.Snapshot) *homeScreen {
			m := homeAt(&theme{}, w, h, homeFixture{snap: snap, doctor: homeDoctor, doctorDone: true})
			m.svc = &fakeServices{}
			return m
		}
		t.Run(fmt.Sprintf("picker_%dx%d", w, h), func(t *testing.T) {
			m := home(newPlanSnap(true))
			m.activate(actDraft)
			view := m.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("draft_picker_%dx%d", w, h), view)
		})
		t.Run(fmt.Sprintf("extend_picker_%dx%d", w, h), func(t *testing.T) {
			m := home(extendSnap(false))
			m.activate(actDraft)
			view := m.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("draft_extend_picker_%dx%d", w, h), view)
		})
		t.Run(fmt.Sprintf("spec_%dx%d", w, h), func(t *testing.T) {
			m := home(newPlanSnap(false))
			m.activate(actDraft)
			m.draftPick(actDraftSpec)
			m.dialog.input.value = "docs/spec.md"
			view := m.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("draft_spec_%dx%d", w, h), view)
		})
		t.Run(fmt.Sprintf("progress_%dx%d", w, h), func(t *testing.T) {
			s, _ := draftProgress(t, &fakeServices{draft: workingDraft}, w, h)
			view := s.View()
			checkFits(t, view, w, h)
			golden(t, fmt.Sprintf("draft_progress_%dx%d", w, h), view)
			s.Update(adaptKey("esc"))
			<-s.run.done
		})
	}
}
