package tui

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
)

// The LOG follows the §15.1 mock (V17-14): `M1-03 started · sonnet ·
// mode plan`, `! M1-04 needs you · …`, ✓ for done.
func TestLogLinesMock(t *testing.T) {
	tests := []struct {
		ev   engine.Event
		want string
	}{
		{engine.Event{Kind: engine.TaskStarted, Task: "M1-03", Title: "Parser", Rank: "sonnet", Model: "sonnet", Mode: "plan"}, "M1-03 started · sonnet · mode plan"},
		{engine.Event{Kind: engine.TaskStarted, Task: "M1-03", Rank: "sonnet", Model: "claude-sonnet-5-5", Mode: "auto"}, "M1-03 started · sonnet → claude-sonnet-5-5 · mode auto"},
		{engine.Event{Kind: engine.TaskStarted, Task: "M1-06", Title: "Buy the domain"}, "M1-06 started · user task"},
		{engine.Event{Kind: engine.NeedsYou, Task: "M1-04", Detail: "the session is idle without a signal"}, "! M1-04 needs you · the session is idle without a signal"},
		{engine.Event{Kind: engine.Asked, Task: "A-01", Question: engine.QuestionCommit, Detail: `commit the changes of A-01 as "A-01: x"?`}, "! A-01 needs you · commit?"},
		{engine.Event{Kind: engine.Asked, Task: "A-01", Question: engine.QuestionPlanApproval, Detail: "approve the plan of A-01?"}, "! A-01 needs you · a plan to approve"},
		{engine.Event{Kind: engine.TaskDone, Task: "M1-03", Detail: "parser and tests"}, "✓ M1-03 done · parser and tests"},
		{engine.Event{Kind: engine.TaskSkipped, Task: "M1-08"}, "– M1-08 skipped"},
		{engine.Event{Kind: engine.VerifyFailed, Task: "M1-03", Detail: "test failed"}, "✗ M1-03 verify failed · test failed"},
		{engine.Event{Kind: engine.ModeChanged, Detail: engine.ModeYolo}, "next sessions in mode yolo [SKIP PERMISSIONS]"},
	}
	for _, tt := range tests {
		if got := strings.Join(logLines(tt.ev), "\n"); got != tt.want {
			t.Errorf("logLines(%s) = %q, want %q", tt.ev.Kind, got, tt.want)
		}
	}
}

// Never color alone (SPEC §15.4): a line drawn as Needs you, a warning or
// a failure starts with its glyph, and `?` is no glyph (one meaning per
// glyph).
func TestLogLinesCarryTheirGlyph(t *testing.T) {
	glyph := map[tone]string{lookAlert: look.GlyphNeedsYou, lookWarn: look.GlyphWarn, lookFail: look.GlyphFail}
	kinds := []engine.EventKind{
		engine.RunStarted, engine.WorktreeReady, engine.WorktreeLeft, engine.PhaseStarted, engine.TaskStarted, engine.TaskResumed,
		engine.SessionOpened, engine.TaskDone, engine.PhaseDone, engine.PhaseStuck, engine.NotRun, engine.PauseOn, engine.PauseOff,
		engine.Paused, engine.ModeChanged, engine.TaskModeChanged, engine.ConfigChanged, engine.ConfigRestored, engine.PlanChanged,
		engine.StaleSignal, engine.StraySignal, engine.NeedsYou, engine.NeedsYouClear, engine.AgentWaiting, engine.AgentWaitingOver,
		engine.Nudged, engine.SessionLost, engine.TaskOverdue, engine.RankMismatch, engine.UsageRead, engine.QuotaRead, engine.Asked,
		engine.PlanClosed, engine.Retrying, engine.TaskSkipped, engine.TaskReset, engine.ResetDropped, engine.YourTurn,
		engine.VerifyStarted, engine.VerifyPassed, engine.VerifyFailed, engine.VerifyLimit, engine.Committed, engine.NotCommitted,
		engine.Integrated, engine.IntegrationFailed, engine.ResolverOpened, engine.ResolverDone, engine.ResolverFailed, engine.Overlap, engine.HookStarted, engine.HookFailed, engine.Warning,
		engine.RunFailed, engine.RunStopped,
	}
	for _, k := range kinds {
		lines := logLines(engine.Event{Kind: k, Task: "M1-03", Detail: "why", Output: []string{"out"}})
		if len(lines) == 0 {
			continue
		}
		if g, ok := glyph[logLook(k)]; ok && !strings.HasPrefix(lines[0], g+" ") {
			t.Errorf("%s is drawn as %v but reads %q, without %s", k, logLook(k), lines[0], g)
		}
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "?") {
				t.Errorf("%s: %q starts with ?", k, l)
			}
		}
	}
}
