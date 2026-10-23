package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/serve"
)

// at is t0 plus d.
func at(d time.Duration) time.Time { return t0.Add(d) }

// runEnd drives a whole run of M0 to its end: two tasks done (one after a
// failed verify), a user task skipped, usage read, two commits, an idle
// wait of 12m30s, and the run completed after 1h47m.
func runEnd(hs *harness) {
	hs.events(
		engine.Event{Kind: engine.RunStarted, Run: "20261006-094100-3fa2", Detail: "phase M0", At: at(0)},
		engine.Event{Kind: engine.PhaseStarted, Phase: "M0", At: at(0)},
		started("M0-03"), opened("M0-03"),
		engine.Event{Kind: engine.UsageRead, Task: "M0-03", Tokens: &engine.Tokens{In: 1_200_000, Out: 20_000, Models: []string{"claude-sonnet-5-5"}}},
		engine.Event{Kind: engine.NeedsYou, Task: "M0-03", Detail: "the session is idle without a signal", At: at(30 * time.Minute)},
		engine.Event{Kind: engine.NeedsYouClear, Task: "M0-03", At: at(42*time.Minute + 30*time.Second)},
		engine.Event{Kind: engine.VerifyStarted, Task: "M0-03", Verify: "fast", At: at(50 * time.Minute)},
		engine.Event{Kind: engine.VerifyFailed, Task: "M0-03", Detail: "fast failed", At: at(51 * time.Minute)},
		engine.Event{Kind: engine.VerifyPassed, Task: "M0-03", Verify: "fast", At: at(55 * time.Minute)},
		engine.Event{Kind: engine.Committed, Task: "M0-03", Detail: "M0-03: Makefile", At: at(55 * time.Minute)},
		engine.Event{Kind: engine.TaskDone, Task: "M0-03", Detail: "ok", At: at(55 * time.Minute)},
		engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: "M0-04", Title: "Config loader", Rank: "opus", Model: "opus", Mode: "plan", At: at(56 * time.Minute)},
		engine.Event{Kind: engine.UsageRead, Task: "M0-04", Tokens: &engine.Tokens{In: 800_000, Out: 9_000}},
		engine.Event{Kind: engine.VerifyPassed, Task: "M0-04", Verify: "fast", At: at(80 * time.Minute)},
		engine.Event{Kind: engine.Committed, Task: "M0-04", Detail: "M0-04: Config loader", At: at(80 * time.Minute)},
		engine.Event{Kind: engine.TaskDone, Task: "M0-04", At: at(80 * time.Minute)},
		engine.Event{Kind: engine.TaskStarted, Phase: "M0", Task: "M0-05", Title: "Buy the domain", At: at(81 * time.Minute)},
		engine.Event{Kind: engine.YourTurn, Task: "M0-05", Detail: "**Buy the domain**", At: at(81 * time.Minute)},
		engine.Event{Kind: engine.TaskSkipped, Task: "M0-05", Detail: "domain bought by hand", At: at(82 * time.Minute)},
		engine.Event{Kind: engine.RunStopped, Detail: "completed", At: at(107 * time.Minute)},
	)
}

// The end-of-run summary (SPEC §15.3) at the three sizes, and the stuck
// and failed outcomes.
func TestSummaryGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 20}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			hs := newHarness(t, size[0], size[1])
			hs.m.loc = time.UTC
			hs.withPlan(demoPlan)
			runEnd(hs)
			v := hs.m.View()
			checkFits(t, v, size[0], size[1])
			golden(t, fmt.Sprintf("summary_%dx%d", size[0], size[1]), v)
		})
	}
	t.Run("stuck", func(t *testing.T) {
		hs := newHarness(t, 120, 40)
		hs.withPlan(demoPlan)
		hs.events(
			engine.Event{Kind: engine.RunStarted, Run: "20261006-094100-3fa2", Detail: "phase M0", At: at(0)},
			engine.Event{Kind: engine.PhaseStarted, Phase: "M0", At: at(0)},
			engine.Event{Kind: engine.PhaseStuck, Phase: "M0", Waiting: []plan.Waiting{plan.WaitingText("M0-13", []string{"P0-05"}, "M0-13 waits on P0-05 (ready, phase P0)")}, At: at(time.Minute)},
			engine.Event{Kind: engine.RunStopped, Detail: "stuck", At: at(time.Minute)},
		)
		v := hs.m.View()
		checkFits(t, v, 120, 40)
		golden(t, "summary_stuck_120x40", v)
	})
	t.Run("failed", func(t *testing.T) {
		hs := newHarness(t, 120, 40)
		hs.withPlan(demoPlan)
		hs.events(
			engine.Event{Kind: engine.RunStarted, Run: "20261006-094100-3fa2", Detail: "phase M0", At: at(0)},
			started("M0-03"), opened("M0-03"),
			engine.Event{Kind: engine.HookFailed, Task: "M0-03", Detail: "before_task hook failed: exit status 1", At: at(time.Minute)},
			engine.Event{Kind: engine.RunFailed, Detail: "before_task hook failed: exit status 1", At: at(time.Minute)},
			engine.Event{Kind: engine.RunStopped, Detail: "error", At: at(time.Minute)},
		)
		v := hs.m.View()
		checkFits(t, v, 120, 40)
		golden(t, "summary_failed_120x40", v)
	})
}

// The summary adds the run's events up: counts, tokens of every attempt,
// commits, verify, the needs-you time as one union, failed tasks whose
// last attempt hit the limit, the outcome and what to run next.
func TestSummaryAddsUp(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.withPlan(demoPlan)
	runEnd(hs)
	s := hs.m.sum
	if s.run != "20261006-094100-3fa2" || !slices.Equal(s.phases, []string{"M0"}) {
		t.Errorf("run %q phases %v", s.run, s.phases)
	}
	if s.done != 2 || s.skipped != 1 || len(s.failed) != 0 {
		t.Errorf("done %d skipped %d failed %v", s.done, s.skipped, s.failed)
	}
	if s.in != 2_000_000 || s.out != 29_000 || !s.usage {
		t.Errorf("tokens %d/%d usage %v, want every attempt folded once", s.in, s.out, s.usage)
	}
	if s.commits != 2 || s.lastCommit != "M0-04" || s.verifyOK != 2 || s.verifyFail != 1 {
		t.Errorf("commits %d (%q) verify %d/%d", s.commits, s.lastCommit, s.verifyOK, s.verifyFail)
	}
	if want := 12*time.Minute + 30*time.Second + time.Minute; s.needsYou != want {
		t.Errorf("needs you %v, want %v (the idle wait and the user task)", s.needsYou, want)
	}
	if s.end.Sub(s.start) != 107*time.Minute || s.outcome != look.Completed {
		t.Errorf("wall %v outcome %v", s.end.Sub(s.start), s.outcome)
	}
	if !slices.Equal(s.skips, []string{"M0-05 skipped · domain bought by hand"}) {
		t.Errorf("skips %v", s.skips)
	}
	if got := hs.m.nextCommands(); !slices.Equal(got, []string{"igris arise P0", "igris report 20261006-094100-3fa2"}) {
		t.Errorf("next %v", got)
	}
	if got := s.reportCommand(); got != "igris report 20261006-094100-3fa2" {
		t.Errorf("report command %q", got)
	}
}

func TestSummaryFailedAndNext(t *testing.T) {
	// A verify limit marks the task failed until a new attempt or its end.
	var s summary
	s.note(engine.Event{Kind: engine.VerifyLimit, Task: "A"})
	s.note(engine.Event{Kind: engine.VerifyLimit, Task: "B"})
	s.note(engine.Event{Kind: engine.Retrying, Task: "A"})
	if len(s.failed) != 1 || !s.failed["B"] {
		t.Errorf("failed %v, want B only", s.failed)
	}
	s.note(engine.Event{Kind: engine.TaskDone, Task: "B"})
	if len(s.failed) != 0 {
		t.Errorf("failed %v after done", s.failed)
	}
	// An unavailable count adds nothing.
	s.fold(&engine.Tokens{Unavailable: true})
	s.fold(nil)
	if s.usage || s.in != 0 {
		t.Errorf("unavailable usage counted: %+v", s)
	}
	for _, tt := range []struct {
		detail string
		want   look.Outcome
	}{{"completed", look.Completed}, {"stopped", look.Stopped}, {"stuck", look.Stuck}, {"error", look.Failed}, {"", look.Failed}} {
		if got := outcomeOf(tt.detail); got != tt.want {
			t.Errorf("outcomeOf(%q) = %v, want %v", tt.detail, got, tt.want)
		}
	}
	if got := scopePhases("phase M0, M1; only A-2"); !slices.Equal(got, []string{"M0", "M1"}) {
		t.Errorf("scopePhases = %v", got)
	}
	// The next commands follow the outcome.
	for _, tt := range []struct {
		outcome look.Outcome
		want    []string
	}{
		{look.Stopped, []string{"igris arise", "igris report"}},
		{look.Stuck, []string{"igris status M0", "igris report"}},
		{look.Failed, []string{"igris doctor", "igris report"}},
		{look.InterruptedRun, []string{"igris status", "igris report"}},
	} {
		hs := newHarness(t, 120, 40)
		hs.withPlan(demoPlan)
		hs.m.phase = "M0"
		hs.m.sum.outcome = tt.outcome
		if got := hs.m.nextCommands(); !slices.Equal(got, tt.want) {
			t.Errorf("%v: next %v, want %v", tt.outcome, got, tt.want)
		}
	}
	// A completed run whose later phases are done has only the report.
	hs := newHarness(t, 120, 40)
	hs.withPlan(strings.ReplaceAll(demoPlan, "| P0-05 | **Create repo** | — | ready |", "| P0-05 | **Create repo** | — | done |"))
	hs.events(engine.Event{Kind: engine.RunStarted, Detail: "phase M0"}, engine.Event{Kind: engine.RunStopped, Detail: "completed"})
	if got := hs.m.nextCommands(); !slices.Equal(got, []string{"igris report"}) {
		t.Errorf("next %v, want the report alone", got)
	}
}

// The feed may end without a RunStopped event (the engine failed before
// one): the summary then takes the feed's result.
func TestSummaryWithoutRunStopped(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.events(engine.Event{Kind: engine.RunStarted, Detail: "phase M0"})
	hs.m.opts.Feed.End(engine.Result{Outcome: engine.Stuck, Phase: "M0"}, nil)
	hs.m.Update(batch{ended: true})
	if v := hs.m.View(); !strings.Contains(v, "Done ▲ stuck") {
		t.Errorf("summary lacks the feed's outcome:\n%s", v)
	}
}

// A view whose serve was killed (the V17 re-gate: SIGKILL by PID) ends
// with serve.ErrGone. The run didn't end itself, so it is interrupted,
// as home, status and history call it (SPEC §15.4), not failed, and the
// header no longer says it is reconnecting.
func TestSummaryOfAKilledServe(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.events(engine.Event{Kind: engine.RunStarted, Detail: "phase M0"})
	hs.m.Update(batch{lost: true})
	hs.m.opts.Feed.End(engine.Result{}, serve.ErrGone)
	hs.m.Update(batch{ended: true})
	v := hs.m.View()
	for _, want := range []string{"Done ▲ interrupted", "next: igris status", "Copy report command"} {
		if !strings.Contains(v, want) {
			t.Errorf("summary lacks %q:\n%s", want, v)
		}
	}
	for _, bad := range []string{"failed", "reconnecting", "igris doctor"} {
		if strings.Contains(v, bad) {
			t.Errorf("summary says %q:\n%s", bad, v)
		}
	}
}

// A view that attaches while a question waits counts the wait from when
// it was asked, and a reconnect keeps what was counted (the V17 gate saw
// 3m07s in an attached view and 6m16s in the one that saw it all).
func TestSummaryNeedsYouOfAnAttachedView(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.withPlan(multiPlan)
	q := serve.Event{V: 1, Type: string(engine.Asked), Task: "M1-05", Question: string(engine.QuestionCommit), At: t0.Add(time.Minute)}
	asking := []serve.Current{{Task: "M1-05", State: serve.StateQuestion, Model: "opus"}}
	hs.now = t0.Add(4 * time.Minute)
	hs.m.restore(serve.Snapshot{Run: "r1", Started: t0, Phase: "M1", Active: asking, Questions: []serve.Event{q}})
	// The question was answered while the view was cut off.
	hs.now = t0.Add(5 * time.Minute)
	hs.m.restore(serve.Snapshot{Run: "r1", Started: t0, Phase: "M1", Active: []serve.Current{{Task: "M1-05", State: serve.StateWorking, Model: "opus"}}})
	hs.events(engine.Event{Kind: engine.RunStopped, At: t0.Add(6 * time.Minute), Detail: "completed"})
	if got := hs.m.sum.needsYou; got != 4*time.Minute {
		t.Errorf("needs you %v, want 4m0s: from the question to the reconnect that found it answered", got)
	}
}

// Arise. opens the log (SPEC §15.1): the phase, the task count once the
// plan is read, the mode; a sliced run counts the slice's tasks.
func TestAriseLine(t *testing.T) {
	first := func(hs *harness) string {
		for _, e := range hs.m.log {
			if e.arise {
				return e.text
			}
		}
		return ""
	}
	hs := newHarness(t, 120, 40)
	hs.m.loc = time.UTC
	hs.events(engine.Event{Kind: engine.RunStarted, Detail: "phase M0"})
	if got := first(hs); got != "Arise. phase M0 · mode plan" {
		t.Errorf("before the plan: %q", got)
	}
	if got := hs.m.log[0].tone; got != lookAccentBold {
		t.Errorf("Arise. drawn %v, want bold accent", got)
	}
	// The unfinished tasks, as serve and --no-tui count them (SPEC §14):
	// M0's done and skipped tasks aren't the run's (the V17 re-gate saw
	// "2 tasks" here and "1 task" in serve.log for one resumed run).
	hs.withPlan(demoPlan)
	if got := first(hs); got != "Arise. phase M0 · 4 tasks · mode plan" {
		t.Errorf("after the plan: %q", got)
	}
	if v := hs.m.View(); !strings.Contains(v, "09:41 Arise. phase M0 · 4 tasks · mode plan") || strings.Contains(v, "run started") {
		t.Errorf("log:\n%s", v)
	}
	// A task the run finishes doesn't shrink the count as the plan is read
	// again.
	hs.events(engine.Event{Kind: engine.TaskDone, Task: "M0-03"})
	hs.withPlan(strings.Replace(demoPlan, "fmt, lint, test | M0-01 | in progress |", "fmt, lint, test | M0-01 | done |", 1))
	if got := first(hs); got != "Arise. phase M0 · 4 tasks · mode plan" {
		t.Errorf("after a task is done: %q", got)
	}
	hs = newHarness(t, 120, 40)
	hs.m.mode = ""
	hs.withPlan(demoPlan)
	hs.events(engine.Event{Kind: engine.RunStarted, Detail: "phase M0, P0; from M0-02 until P0-05", Slice: []string{"M0-02", "M0-03", "P0-05"}})
	if got := first(hs); got != "Arise. phase M0, P0 · 3 tasks" {
		t.Errorf("sliced run: %q", got)
	}
}

// The card that needs you is selected when Needs you starts, unless the
// selected card itself needs you; the selection never jumps away from a
// card that needs you (SPEC §15.1).
func TestNeedsYouSelectsItsCard(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	if hs.m.cur.id != "M1-05" {
		t.Fatalf("selected %s at the start", hs.m.cur.id)
	}
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-04", Detail: "idle"})
	if hs.m.cur.id != "M1-04" {
		t.Fatalf("selected %s, want M1-04, which needs you", hs.m.cur.id)
	}
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-03", Detail: "idle"})
	if hs.m.cur.id != "M1-04" {
		t.Errorf("selected %s, want M1-04 kept: it needs you too", hs.m.cur.id)
	}
	// The owner looks at M1-05, which works; another event of M1-03 (still
	// needing you, not starting to) doesn't pull the selection.
	hs.keys("]")
	if hs.m.cur.id != "M1-05" {
		t.Fatalf("] selected %s", hs.m.cur.id)
	}
	hs.events(engine.Event{Kind: engine.UsageRead, Task: "M1-03", Tokens: &engine.Tokens{In: 1}})
	if hs.m.cur.id != "M1-05" {
		t.Errorf("an event of a task that needs you pulled the selection to %s", hs.m.cur.id)
	}
	// Needs you starting again on M1-03 does.
	hs.events(engine.Event{Kind: engine.NeedsYouClear, Phase: "M1", Task: "M1-03"}, engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-03", Detail: "idle"})
	if hs.m.cur.id != "M1-03" {
		t.Errorf("selected %s, want M1-03", hs.m.cur.id)
	}
	// Narrow too: the switcher leads with the selected card that needs you.
	hs.m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	if v := hs.m.View(); !strings.Contains(v, "‹ ! M1-03 · 1 of 3 › ! M1-04 ● M1-05") {
		t.Errorf("narrow switcher:\n%s", v)
	}
}

// The banner names the first task in plan order that needs you and the
// kind of question, with Answer… while a question waits (SPEC §15.1).
func TestBannerNamesTheFirstTask(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-05", Detail: "the session is idle without a signal"})
	v := hs.m.View()
	if !strings.Contains(v, "! M1-05 needs you · idle without a signal") || strings.Contains(v, "[Answer…]") {
		t.Errorf("banner:\n%s", v)
	}
	hs.events(engine.Event{Kind: engine.Asked, Phase: "M1", Task: "M1-03", Question: engine.QuestionCommit, Detail: "commit?"})
	hs.keys("esc")
	v = hs.m.View()
	if !strings.Contains(v, "! M1-03 needs you · commit? · 2 tasks need you") {
		t.Errorf("banner with two tasks:\n%s", v)
	}
	// The banner's Answer… opens the question; it sits on the banner's row.
	var z *zone
	for i := range hs.m.zones.list {
		if c := &hs.m.zones.list[i]; c.t.act == actAnswer && c.r.y == 2 {
			z = c
		}
	}
	if z == nil {
		t.Fatalf("no Answer… zone on the banner row; zones %+v", hs.m.zones.list)
	}
	hs.m.Update(tea.MouseMsg{X: z.r.x, Y: z.r.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if d := hs.m.dialog; d == nil || d.task != "M1-03" {
		t.Errorf("the banner's Answer… opened %+v", d)
	}
	// Ended, no banner.
	hs.keys("esc")
	hs.events(engine.Event{Kind: engine.RunStopped, Detail: "stopped"})
	if v := hs.m.View(); strings.Contains(v, "needs you ·") {
		t.Errorf("banner after the end:\n%s", v)
	}
}

// On the summary h opens History and y copies the report command; neither
// does anything while the run goes on.
func TestSummaryKeys(t *testing.T) {
	hs := newHarness(t, 120, 40)
	out := &lockedBuf{}
	hs.m.opts.Out = out
	hs.m.opts.History = func() tea.Msg { return "history" }
	hs.m.opts.Leave = func() tea.Msg { return "home" }
	hs.events(engine.Event{Kind: engine.RunStarted, Run: "20261006-094100-3fa2", Detail: "phase M0"}, started("M0-03"), opened("M0-03"))
	if msg := hs.key("h"); msg != nil {
		t.Errorf("h during the run returned %v", msg)
	}
	if v := hs.m.View(); strings.Contains(v, "[h History]") {
		t.Errorf("History offered during the run:\n%s", v)
	}
	hs.events(engine.Event{Kind: engine.RunStopped, Detail: "completed"})
	v := hs.m.View()
	if !strings.Contains(v, "[›q Home‹] [h History] [y Copy report command] [?]") {
		t.Errorf("summary bar:\n%s", v)
	}
	if hs.m.focus != focusBar || hs.m.focusedButton() != actQuit {
		t.Errorf("focus %v on %v, want Home on the bar", hs.m.focus, hs.m.focusedButton())
	}
	hs.key("y")
	if want := osc52Of("igris report 20261006-094100-3fa2"); out.String() != want || !strings.Contains(hs.m.View(), "copied") {
		t.Errorf("y wrote %q, want %q", out.String(), want)
	}
	if msg := hs.key("h"); msg != "history" {
		t.Errorf("h returned %v, want the History message", msg)
	}
	// tab stays on the bar: the summary has no other region.
	hs.keys("tab")
	if hs.m.focus != focusBar {
		t.Errorf("tab moved the focus to %v", hs.m.focus)
	}
}

// TASKS is as wide as its longest row, within its bounds (SPEC §15.1).
func TestTasksColumnIsAsWideAsItsRows(t *testing.T) {
	divider := func(v string) int {
		return textWidth(strings.SplitN(strings.Split(v, "\n")[2], "│", 2)[0])
	}
	hs := newHarness(t, 120, 40)
	hs.withPlan(demoPlan)
	hs.events(started("M0-03"))
	if got, want := divider(hs.m.View()), hs.m.rows().natural()+1; got != want {
		t.Errorf("divider at %d, want %d (the longest row and a space)", got, want)
	}
	long := strings.ReplaceAll(demoPlan, "**Buy the domain**", "**Buy the domain and set up its DNS, mail and a landing page that explains the product**")
	hs.withPlan(long)
	if got, want := divider(hs.m.View()), (120-3)/tasksMaxShare+1; got != want {
		t.Errorf("divider at %d, want %d (half the screen)", got, want)
	}
	hs = newHarness(t, 120, 40)
	hs.events(started("M0-03"))
	if got, want := divider(hs.m.View()), tasksMinWidth+1; got != want {
		t.Errorf("divider at %d without a plan, want %d", got, want)
	}
}
