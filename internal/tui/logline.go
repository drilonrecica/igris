package tui

import (
	"strings"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
)

// logLines renders an event for the TUI log in the design language (SPEC
// §15.1, §15.4): `M1-03 started · sonnet · mode plan`, `! M1-04 needs you
// · commit?`, `✓ M1-03 done`. Unlike the --no-tui log it carries no
// typing hints: the TUI asks with buttons and dialogs.
func logLines(ev engine.Event) []string {
	id := ev.Task
	switch ev.Kind {
	case engine.RunStarted:
		return []string{"run started · " + ev.Detail}
	case engine.WorktreeReady, engine.WorktreeLeft:
		return []string{ev.Detail}
	case engine.PhaseStarted:
		return []string{"phase " + ev.Phase + " started"}
	case engine.TaskStarted:
		return []string{startedLine(ev)}
	case engine.TaskResumed:
		return []string{id + " resumed · " + ev.Detail}
	case engine.SessionOpened:
		return []string{id + " session open" + badge(ev.Mode)}
	case engine.YourTurn:
		return []string{needsLine(id, "your turn · do it outside igris, then d done or s skip")}
	case engine.NeedsYou:
		return []string{needsLine(id, ev.Detail)}
	case engine.NeedsYouClear:
		return []string{id + " is working again"}
	case engine.AgentWaiting:
		return []string{look.GlyphWaiting + " " + id + " waits on background work; Claude Code resumes it when that is done"}
	case engine.AgentWaitingOver, engine.UsageRead, engine.QuotaRead:
		return nil // the card shows it; the next event says what followed
	case engine.Nudged:
		// "the agent stopped without igris done; igris reminded it once",
		// or why igris told it to continue after a usage limit.
		if ev.Detail == "" {
			return []string{id + " stopped without igris done; igris reminded it once"}
		}
		return []string{id + " " + strings.TrimPrefix(ev.Detail, "the agent ")}
	case engine.SessionLimited:
		return []string{look.GlyphWaiting + " " + id + " " + ev.Detail}
	case engine.SessionLimitOver:
		return []string{id + " " + ev.Detail}
	case engine.LimitHold:
		return []string{look.GlyphWaiting + " paused before " + id + " · " + ev.Detail}
	case engine.LimitResume:
		return []string{"resumed · " + ev.Detail}
	case engine.TaskOverdue:
		return []string{look.GlyphWarn + " " + id + " overdue · " + ev.Detail}
	case engine.RankMismatch:
		return []string{look.GlyphWarn + " " + id + " rank mismatch · " + ev.Detail}
	case engine.SessionLost:
		return []string{needsLine(id, ev.Detail)}
	case engine.Asked:
		if ev.Question == engine.QuestionBudget {
			return nil // budget_exceeded said it
		}
		return []string{needsLine(id, ev.Question.Kind())}
	case engine.BudgetRead:
		return nil // the card shows it
	case engine.BudgetExceeded:
		return []string{look.GlyphNeedsYou + " " + ev.Detail}
	case engine.BudgetRaised:
		return []string{ev.Detail}
	case engine.PlanClosed:
		return []string{id + " " + ev.Detail}
	case engine.Retrying:
		return []string{id + " new session (" + ev.Detail + ")"}
	case engine.VerifyStarted:
		return []string{id + " verifying (" + ev.Verify + ") · " + ev.Detail}
	case engine.VerifyPassed:
		return []string{look.GlyphDone + " " + id + " verify passed (" + ev.Verify + ")"}
	case engine.VerifyFailed:
		return []string{look.GlyphFail + " " + id + " verify failed · " + ev.Detail}
	case engine.Committed:
		return []string{look.GlyphDone + " " + id + " committed · " + ev.Detail}
	case engine.NotCommitted:
		return []string{id + " not committed · " + ev.Detail}
	case engine.Integrated:
		return []string{look.GlyphDone + " " + id + " integrated " + ev.Detail}
	case engine.IntegrationFailed:
		return hookLines(id, ev)
	case engine.Overlap:
		return []string{look.GlyphWarn + " overlap · " + ev.Detail}
	case engine.TaskDone:
		return []string{note(look.GlyphDone+" "+id+" done", ev.Detail)}
	case engine.TaskSkipped:
		return []string{note(look.GlyphSkipped+" "+id+" skipped", ev.Detail)}
	case engine.TaskReset:
		return []string{"reset " + ev.Detail}
	case engine.ResetDropped:
		return []string{ev.Detail}
	case engine.PhaseDone:
		return []string{look.GlyphDone + " phase " + ev.Phase + " complete"}
	case engine.PhaseStuck:
		lines := []string{look.GlyphWarn + " phase " + ev.Phase + " is stuck · unfinished tasks, none can start"}
		for _, w := range ev.Waiting {
			lines = append(lines, "  "+w.String())
		}
		return lines
	case engine.NotRun:
		lines := make([]string, len(ev.Waiting))
		for i, w := range ev.Waiting {
			lines[i] = "not run · " + w.String()
		}
		return lines
	case engine.PauseOn:
		return []string{"pause after task on"}
	case engine.PauseOff:
		return []string{"pause after task off"}
	case engine.Paused:
		return []string{"paused before " + id}
	case engine.ModeChanged:
		return []string{"next sessions in mode " + ev.Detail + badge(ev.Detail)}
	case engine.TaskModeChanged:
		return []string{id + "'s next session in mode " + ev.Detail + badge(ev.Detail)}
	case engine.HookStarted:
		return []string{id + " running " + ev.Detail + " hook…"}
	case engine.HookFailed:
		return hookLines(id, ev)
	case engine.Warning:
		return []string{look.GlyphWarn + " " + ev.Detail}
	case engine.RunFailed:
		return []string{look.GlyphFail + " run failed · " + ev.Detail}
	case engine.RunStopped:
		return []string{"run stopped · " + ev.Detail}
	case engine.VerifyLimit:
		return []string{look.GlyphFail + " " + id + " " + ev.Detail}
	case engine.PlanChanged:
		return []string{look.GlyphWarn + " " + ev.Detail}
	case engine.ConfigChanged, engine.ConfigRestored, engine.StaleSignal, engine.StraySignal:
		return []string{strings.TrimSpace(id + " " + ev.Detail)}
	}
	return []string{strings.TrimSpace(string(ev.Kind) + " " + id + " " + ev.Detail)}
}

// startedLine is a task's start: `M1-03 started · sonnet · mode plan`,
// with `rank → model` only when the model isn't the rank's own name, and
// `M1-06 started · user task` for a user task.
func startedLine(ev engine.Event) string {
	if ev.Model == "" {
		return ev.Task + " started · user task"
	}
	model := ev.Model
	if ev.Rank != "" && ev.Rank != ev.Model {
		model = ev.Rank + " → " + ev.Model
	}
	return ev.Task + " started · " + model + " · mode " + ev.Mode
}

// needsLine is a line that waits on the owner: `! M1-04 needs you · what`.
func needsLine(id, what string) string {
	return strings.TrimSpace(look.GlyphNeedsYou+" "+id) + " needs you · " + what
}

// hookLines are a failed task hook's reason and the last lines of its
// output (SPEC §6.7).
func hookLines(id string, ev engine.Event) []string {
	lines := []string{look.GlyphFail + " " + id + " " + ev.Detail}
	for _, l := range ev.Output {
		lines = append(lines, "  | "+l)
	}
	return lines
}

func note(s, n string) string {
	if n == "" {
		return s
	}
	return s + " · " + n
}

// skipBadge marks skip-permissions mode in words, never by color alone.
const skipBadge = "[SKIP PERMISSIONS]"

// badge is the skip-permissions badge after a mode, if the mode is yolo
// or plan:yolo.
func badge(mode string) string {
	if engine.SkipsPermissions(mode) {
		return " " + skipBadge
	}
	return ""
}

// logLook is how the log lines of an event are drawn: what needs the
// owner stands out. The words say it either way: never color alone (SPEC
// §15.4), a line drawn as Needs you, a warning or a failure starts with
// its glyph (logLines).
func logLook(k engine.EventKind) tone {
	switch k {
	case engine.NeedsYou, engine.SessionLost, engine.Asked, engine.YourTurn, engine.BudgetExceeded:
		return lookAlert // waits on the owner: plume
	case engine.TaskOverdue, engine.RankMismatch, engine.PhaseStuck, engine.PlanChanged, engine.Warning, engine.Overlap:
		return lookWarn
	case engine.HookFailed, engine.VerifyFailed, engine.VerifyLimit, engine.RunFailed, engine.IntegrationFailed:
		return lookFail
	case engine.Paused, engine.PhaseDone, engine.NotRun, engine.TaskReset:
		return lookTitle
	}
	return lookPlain
}
