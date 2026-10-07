// Package backend defines the seam between the igris engine and the terminal
// multiplexer that hosts Claude Code sessions (SPEC §11.1). The engine only
// talks to Backend and Session; herdr and the in-process fake implement them.
package backend

import (
	"context"
	"errors"
)

// ErrSessionGone is wrapped by Session and Backend calls when the session's
// pane no longer exists or Claude Code exited. The engine treats it as
// "session lost" (SPEC §6.3).
var ErrSessionGone = errors.New("session is gone")

// Backend opens and reattaches sessions and shows notifications.
type Backend interface {
	Name() string
	// Available reports why the backend can't be used, e.g. the herdr server
	// is unreachable or igris doesn't run inside a herdr pane.
	Available(ctx context.Context) error
	// OpenSession opens a pane and starts Claude Code in it. It returns once
	// the session is ready for input (or blocked at a startup prompt).
	OpenSession(ctx context.Context, spec SessionSpec) (Session, error)
	// Attach reattaches to a session after an igris restart. A session that
	// no longer exists yields an error wrapping ErrSessionGone.
	Attach(ctx context.Context, ref SessionRef) (Session, error)
	Notify(ctx context.Context, n Notification) error
}

// Session is one Claude Code session in its own pane.
type Session interface {
	// Ref identifies the session so it can be stored in state.json.
	Ref() SessionRef
	// Prompt submits text (multi-line is fine) to the agent. A gone session
	// yields an error wrapping ErrSessionGone.
	Prompt(ctx context.Context, text string) error
	// State reports the agent state. A gone session is (Exited, nil), not an
	// error, so watching can treat it like any other observation.
	State(ctx context.Context) (AgentState, error)
	// Focus brings the session's pane to the front. A gone session yields an
	// error wrapping ErrSessionGone.
	Focus(ctx context.Context) error
	// Close closes the pane. Closing a session that is already gone is not
	// an error.
	Close(ctx context.Context) error
}

// PromptHolder is implemented by sessions that hold the first prompt back
// while the agent sits at a startup prompt (e.g. Claude Code's folder-trust
// question) and deliver it once the agent is ready. The held prompt lives in
// the session; a reattached session doesn't know it, so the engine records
// it and hands it back with HoldPrompt.
type PromptHolder interface {
	// PromptPending reports a prompt that is held and not delivered yet.
	PromptPending() bool
	// HoldPrompt holds text until the agent is ready, as Prompt does at a
	// startup prompt.
	HoldPrompt(text string)
}

// AgentState is the agent state reported by the backend. The values match
// herdr's agent_status, plus Exited for a session that is gone.
type AgentState string

const (
	Working AgentState = "working"
	Idle    AgentState = "idle"
	Blocked AgentState = "blocked"
	Done    AgentState = "done"
	Unknown AgentState = "unknown"
	Exited  AgentState = "exited"
)

// Settled reports whether the agent has stopped working and waits for
// input: idle, done or blocked.
func (s AgentState) Settled() bool {
	return s == Idle || s == Done || s == Blocked
}

// SessionSpec describes a session to open.
type SessionSpec struct {
	TaskID string   // the plan task; backends derive session names from it
	Dir    string   // working directory (the project root)
	Label  string   // pane label, "<ID> · <rank>"
	Args   []string // Claude Code argv without the program name
	Env    []string // environment additions, KEY=VALUE
}

// SessionRef is the serializable identity of a session. Backends fill the
// fields they need and leave the others empty.
type SessionRef struct {
	Backend string `json:"backend"`
	TabID   string `json:"tab_id,omitempty"`
	PaneID  string `json:"pane_id,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

// Sound selects the notification sound.
type Sound string

const (
	SoundRequest Sound = "request" // igris needs the owner
	SoundDone    Sound = "done"    // something finished
	SoundNone    Sound = "none"
)

// Notification is a toast shown by the backend.
type Notification struct {
	Title string
	Body  string
	Sound Sound
}
