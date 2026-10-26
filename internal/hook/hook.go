// Package hook turns Claude Code hook events into agent state (SPEC §6.3,
// V03-P1). igris passes each session a hooks-only settings file (Settings)
// whose hooks run the hidden `igris hook`; Run maps the event on stdin to a
// state and records it in .igris/agent-state/<session uuid>.json, where the
// backends read it.
package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	planpkg "github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/state"
)

const (
	// maxInput bounds the event JSON read from stdin.
	maxInput = 64 << 10
	// Deadline is how long `igris hook` may take before it gives up; a hook
	// must never hold Claude Code up.
	Deadline = 2 * time.Second
	// commandTimeout is the timeout Claude Code gives each hook command.
	commandTimeout = 5
)

// Events are the Claude Code hook events igris installs, in the order they
// appear in the settings file.
var Events = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"PermissionRequest", "Notification", "Stop", "StopFailure", "SessionEnd",
}

// Claude Code's quota notification types (SPEC §6.3, since 1.8): it
// continues after the reset, waits for Enter, or gave up continuing.
const (
	QuotaResumeFired    = "quota_auto_resume_fired"
	QuotaResumeStale    = "quota_auto_resume_stale"
	QuotaResumeDisabled = "quota_auto_resume_disabled"
)

// RateLimitError is StopFailure's error for a turn the API rejected for a
// usage limit.
const RateLimitError = "rate_limit"

// Payload holds the fields of a hook event igris reads; Claude Code sends
// more, which are ignored.
type Payload struct {
	SessionID        string `json:"session_id"`
	Event            string `json:"hook_event_name"`
	Source           string `json:"source"`            // SessionStart
	ToolName         string `json:"tool_name"`         // PreToolUse, PostToolUse, PermissionRequest
	NotificationType string `json:"notification_type"` // Notification
	Error            string `json:"error"`             // StopFailure: rate_limit, or another error Claude Code shows
	// TranscriptPath is the session's transcript, in every event (SPEC
	// §6.9); kept only when state.ValidTranscript accepts it.
	TranscriptPath string `json:"transcript_path"`
	// Stop: work Claude Code resumes the session for by itself (SPEC §6.3).
	// Kept raw and read leniently, so an unexpected shape never loses the
	// Stop itself; only the statuses and the count are looked at.
	BackgroundTasks json.RawMessage `json:"background_tasks"`
	SessionCrons    json.RawMessage `json:"session_crons"`
}

// finished are background task statuses that won't resume the session.
// Claude Code drops a finished task from the list (2.1.299 sends only
// "running" ones); these are a guard should it keep some.
var finished = map[string]bool{
	"completed": true, "failed": true, "killed": true, "stopped": true,
	"cancelled": true, "canceled": true, "done": true, "error": true,
}

// pending reports whether a Stop leaves work that will resume the session:
// a background task (shell, monitor, subagent, …) that hasn't finished, or
// a scheduled wakeup or cron.
// An entry igris can't read counts as pending: Claude Code listed it.
func (p Payload) pending() bool {
	var tasks []json.RawMessage
	if json.Unmarshal(p.BackgroundTasks, &tasks) == nil {
		for _, raw := range tasks {
			var t struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(raw, &t) != nil || !finished[t.Status] {
				return true
			}
		}
	}
	var crons []json.RawMessage
	return json.Unmarshal(p.SessionCrons, &crons) == nil && len(crons) > 0
}

// Map returns the agent state an event means, and false for events that
// say nothing about it (SPEC §6.3).
func Map(p Payload) (backend.AgentState, bool) {
	switch p.Event {
	case "SessionStart":
		if p.Source == "compact" { // fires mid-turn
			return "", false
		}
		return backend.Idle, true
	case "UserPromptSubmit", "PostToolUse":
		return backend.Working, true
	case "PreToolUse":
		if p.ToolName == "AskUserQuestion" || p.ToolName == "ExitPlanMode" {
			return backend.Blocked, true
		}
		return backend.Working, true
	case "PermissionRequest":
		return backend.Blocked, true
	case "Notification":
		switch p.NotificationType {
		case "idle_prompt":
			return backend.Idle, true
		case QuotaResumeFired:
			return backend.Working, true
		case QuotaResumeStale, QuotaResumeDisabled:
			return backend.Limited, true
		}
		return backend.Blocked, true
	case "Stop":
		if p.pending() {
			return backend.Waiting, true
		}
		return backend.Idle, true
	case "StopFailure":
		// The turn ended on an error Claude Code shows; a usage limit is
		// the one it waits out by itself (SPEC §6.3).
		if p.Error == RateLimitError {
			return backend.Limited, true
		}
		return backend.Idle, true
	case "SessionEnd":
		return backend.Exited, true
	}
	return "", false
}

// Run reads one event from in and records its state for the project at
// root. It never takes longer than Deadline; the caller ignores its error
// apart from tests, since a hook must not disturb Claude Code.
func Run(ctx context.Context, root string, in io.Reader, now func() time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- record(root, in, now) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("hook: %w", ctx.Err())
	}
}

func record(root string, in io.Reader, now func() time.Time) error {
	data, err := io.ReadAll(io.LimitReader(in, maxInput+1))
	if err != nil {
		return fmt.Errorf("hook: read event: %w", err)
	}
	if len(data) > maxInput {
		return errors.New("hook: event larger than 64 KiB")
	}
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("hook: decode event: %w", err)
	}
	st, ok := Map(p)
	if !ok {
		return nil
	}
	if p.Event == "Notification" && st == backend.Idle {
		// Claude Code sends idle_prompt a minute after a Stop even while a
		// background task runs; the session still waits on that work.
		if cur, ok, _ := state.ReadAgentState(root, p.SessionID); ok && cur.State == backend.Waiting {
			return nil
		}
	}
	rec := state.AgentState{State: st, Event: p.Event, At: now().UTC()}
	if st == backend.Limited && p.Event == "Notification" {
		rec.Event = p.NotificationType // which quota notification (SPEC §6.3)
	}
	if st == backend.Blocked {
		rec.Cause, rec.Tool = Cause(p)
	}
	if state.ValidTranscript(p.TranscriptPath, p.SessionID) {
		rec.Transcript = p.TranscriptPath
	}
	return state.WriteAgentState(root, p.SessionID, rec)
}

// Cause says why an event that blocks the session blocks it (SPEC §6.3):
// a question (AskUserQuestion), a plan (ExitPlanMode), a permission with
// the tool's name, never its input, or another notification.
func Cause(p Payload) (cause, tool string) {
	switch {
	case p.ToolName == "AskUserQuestion" && (p.Event == "PreToolUse" || p.Event == "PermissionRequest"):
		return state.CauseQuestion, ""
	case p.ToolName == "ExitPlanMode" && (p.Event == "PreToolUse" || p.Event == "PermissionRequest"):
		return state.CausePlan, ""
	case p.Event == "PermissionRequest":
		if state.ValidTool(p.ToolName) {
			return state.CausePermission, p.ToolName
		}
		return state.CausePermission, ""
	}
	return state.CauseNotification, ""
}

// Settings returns the Claude Code settings file igris passes a session
// with --settings: every event in Events runs
// '<exe>' hook --root '<root>' <Event>, and autoContinueAtUsageLimit is on. exe and root come from igris and the
// owner, never from the plan; they are single-quoted for the shell Claude
// Code runs hook commands with. allow, when not empty, adds permission
// allow rules: a session in a run's worktree gets the `igris done` rules
// there, since the worktree lacks the project's untracked
// .claude/settings.local.json (SPEC §6.8). plan, when not nil, adds the
// plan approval entry: a second PermissionRequest matcher for ExitPlanMode
// that runs plan-approve with the mode and the wait as arguments, so a
// session can't change either once it runs (SPEC §6.3). statusTask, when
// not empty, is the task ID of a session whose status line is igris's, to
// record the subscription quota (StatusLineCommand, [usage] statusline).
func Settings(exe, root string, allow []string, plan *PlanApproval, statusTask string) ([]byte, error) {
	type command struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type matcher struct {
		Matcher string    `json:"matcher,omitempty"`
		Hooks   []command `json:"hooks"`
	}
	hooks := make(map[string][]matcher, len(Events))
	for _, ev := range Events {
		m := matcher{Hooks: []command{{
			Type:    "command",
			Command: shellQuote(exe) + " hook --root " + shellQuote(root) + " " + ev,
			Timeout: commandTimeout,
		}}}
		switch ev {
		case "PreToolUse", "PostToolUse", "PermissionRequest":
			m.Matcher = "*"
		}
		hooks[ev] = []matcher{m}
	}
	if plan != nil {
		if !SetModes[plan.Mode] || plan.Wait <= 0 {
			return nil, fmt.Errorf("hooks settings: plan approval needs a mode and a wait, not %q and %s", plan.Mode, plan.Wait)
		}
		wait := plan.Wait.Round(time.Second)
		hooks["PermissionRequest"] = append(hooks["PermissionRequest"], matcher{Matcher: "ExitPlanMode", Hooks: []command{{
			Type: "command",
			Command: shellQuote(exe) + " hook --root " + shellQuote(root) + " " + PlanCommand +
				" --mode " + shellQuote(plan.Mode) + " --wait " + shellQuote(wait.String()),
			Timeout: int((wait + planGrace) / time.Second),
		}}})
	}
	// Every task session waits at a usage limit and continues by itself at
	// the reset, whatever the owner's own setting says (SPEC §6.3, V18-P1).
	settings := map[string]any{"hooks": hooks, "autoContinueAtUsageLimit": true}
	if statusTask != "" {
		if !planpkg.ValidID(statusTask) {
			return nil, fmt.Errorf("hooks settings: invalid task ID %q for the status line", statusTask)
		}
		settings["statusLine"] = map[string]string{
			"type": "command",
			"command": shellQuote(exe) + " hook --root " + shellQuote(root) + " " + StatusLineCommand +
				" " + shellQuote(statusTask),
		}
	}
	if len(allow) > 0 {
		settings["permissions"] = map[string]any{"allow": allow}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("hooks settings: %w", err)
	}
	return append(data, '\n'), nil
}

// shellQuote quotes s for a POSIX shell: inside single quotes nothing is
// special except the quote itself.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
