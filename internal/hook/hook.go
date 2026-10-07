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
	"PermissionRequest", "Notification", "Stop", "SessionEnd",
}

// Payload holds the fields of a hook event igris reads; Claude Code sends
// more, which are ignored.
type Payload struct {
	SessionID        string `json:"session_id"`
	Event            string `json:"hook_event_name"`
	Source           string `json:"source"`            // SessionStart
	ToolName         string `json:"tool_name"`         // PreToolUse, PostToolUse, PermissionRequest
	NotificationType string `json:"notification_type"` // Notification
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
		if p.NotificationType == "idle_prompt" {
			return backend.Idle, true
		}
		return backend.Blocked, true
	case "Stop":
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
	return state.WriteAgentState(root, p.SessionID, state.AgentState{State: st, Event: p.Event, At: now().UTC()})
}

// Settings returns the hooks-only Claude Code settings file igris passes a
// session with --settings: every event in Events runs
// '<exe>' hook --root '<root>' <Event>. exe and root come from igris and the
// owner, never from the plan; they are single-quoted for the shell Claude
// Code runs hook commands with.
func Settings(exe, root string) ([]byte, error) {
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
	data, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
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
