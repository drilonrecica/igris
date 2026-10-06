package engine

import (
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
)

// ClaudeParams describes one Claude Code launch.
type ClaudeParams struct {
	Model     string // value for --model, e.g. "sonnet" (never a pinned full name)
	SessionID string // Claude session UUID
	Mode      string // resolved run mode (see ResolveMode)
	RulesFile string // igris rules file for --append-system-prompt-file
	ExtraArgs []string
	Resume    bool // continue session SessionID instead of starting it
}

// ClaudeArgs builds the argument list for `claude` (the command itself,
// config's claude.command, is the caller's). It contains no prompt: the rules
// travel as a file and the task prompt through the backend's prompt call,
// because herdr rejects newlines in agent arguments (SPEC §6, §11.2).
//
// Model and mode flags are always set, also on resume: --model beats the
// user's settings and applies to a resumed session's new turns (SPEC §7.4).
func ClaudeArgs(p ClaudeParams) ([]string, error) {
	switch {
	case strings.TrimSpace(p.Model) == "":
		return nil, fmt.Errorf("build claude arguments: model is empty; map the task's rank in [models]")
	case strings.TrimSpace(p.SessionID) == "":
		return nil, fmt.Errorf("build claude arguments: session ID is empty")
	case strings.TrimSpace(p.RulesFile) == "":
		return nil, fmt.Errorf("build claude arguments: rules file is empty")
	}
	flags, ok := modeFlags[p.Mode]
	if !ok {
		return nil, fmt.Errorf("build claude arguments: unknown run mode %q (want default|accept|auto|plan|yolo)", p.Mode)
	}
	for _, a := range p.ExtraArgs {
		if config.ForbiddenExtraArg(a) {
			return nil, fmt.Errorf("build claude arguments: claude.extra_args contains %q, which igris sets itself (SPEC §7.4); remove it", a)
		}
	}

	session := "--session-id"
	if p.Resume {
		session = "--resume"
	}
	args := []string{"--model", p.Model, session, p.SessionID}
	args = append(args, flags...)
	args = append(args, "--append-system-prompt-file", p.RulesFile)
	return append(args, p.ExtraArgs...), nil
}

// NewSessionID returns a random (version 4) UUID for a Claude session.
func NewSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
