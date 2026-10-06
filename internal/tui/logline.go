package tui

import (
	"fmt"
	"strings"

	"github.com/drilonrecica/igris/internal/engine"
)

// logLines renders an event for the TUI log. Unlike the --no-tui log it
// carries no typing hints: the TUI asks with buttons and dialogs.
func logLines(ev engine.Event) []string {
	id := ev.Task
	switch ev.Kind {
	case engine.RunStarted:
		return []string{"run started: " + ev.Detail}
	case engine.PhaseStarted:
		return []string{"phase " + ev.Phase + " started"}
	case engine.TaskStarted:
		if ev.Model == "" {
			return []string{id + " started (user task) · " + ev.Title}
		}
		return []string{fmt.Sprintf("%s started (%s → %s, %s) · %s", id, ev.Rank, ev.Model, ev.Mode, ev.Title)}
	case engine.TaskResumed:
		return []string{id + " resumed: " + ev.Detail}
	case engine.SessionOpened:
		return []string{id + " session open" + badge(ev.Mode)}
	case engine.YourTurn:
		return []string{id + " is your turn"}
	case engine.NeedsYou:
		return []string{id + " needs you: " + ev.Detail}
	case engine.NeedsYouClear:
		return []string{id + " is working again"}
	case engine.SessionLost:
		return []string{id + " session lost: " + ev.Detail}
	case engine.Asked:
		return []string{"? " + ev.Detail}
	case engine.Retrying:
		return []string{id + " new session (" + ev.Detail + ")"}
	case engine.VerifyStarted:
		return []string{id + " verifying: " + ev.Detail}
	case engine.VerifyPassed:
		return []string{id + " verify passed"}
	case engine.VerifyFailed:
		return []string{id + " verify failed: " + ev.Detail}
	case engine.Committed:
		return []string{id + " committed: " + ev.Detail}
	case engine.NotCommitted:
		return []string{id + " not committed: " + ev.Detail}
	case engine.TaskDone:
		return []string{note(id+" done", ev.Detail)}
	case engine.TaskSkipped:
		return []string{note(id+" skipped", ev.Detail)}
	case engine.PhaseDone:
		return []string{"phase " + ev.Phase + " complete"}
	case engine.PhaseStuck:
		lines := []string{"phase " + ev.Phase + " is stuck: unfinished tasks, none can start"}
		for _, w := range ev.Waiting {
			lines = append(lines, "  "+w.String())
		}
		return lines
	case engine.PauseOn:
		return []string{"pause after task: on"}
	case engine.PauseOff:
		return []string{"pause after task: off"}
	case engine.Paused:
		return []string{"paused before " + id}
	case engine.ModeChanged:
		return []string{"run mode for the next sessions: " + ev.Detail + badge(ev.Detail)}
	case engine.TaskModeChanged:
		return []string{"mode for " + id + "'s next session: " + ev.Detail + badge(ev.Detail)}
	case engine.Warning:
		return []string{"warning: " + ev.Detail}
	case engine.RunFailed:
		return []string{"error: " + ev.Detail}
	case engine.RunStopped:
		return []string{"run stopped: " + ev.Detail}
	case engine.VerifyLimit, engine.ConfigChanged, engine.ConfigRestored, engine.StaleSignal, engine.StraySignal:
		return []string{strings.TrimSpace(id + " " + ev.Detail)}
	}
	return []string{strings.TrimSpace(string(ev.Kind) + " " + id + " " + ev.Detail)}
}

func note(s, n string) string {
	if n == "" {
		return s
	}
	return s + " · " + n
}

// badge marks skip-permissions mode in words, never by color alone.
func badge(mode string) string {
	if mode == engine.ModeYolo {
		return " [SKIP PERMISSIONS]"
	}
	return ""
}
