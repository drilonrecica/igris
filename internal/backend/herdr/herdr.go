package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/runner"
)

// Name is the backend name reported by Name and stored in session refs.
const Name = "herdr"

// startTimeout is how long `agent start` waits for Claude Code to be ready
// for input (SPEC §11.2).
const startTimeout = 120 * time.Second

// maxAgentName is herdr's limit on agent names: [a-z][a-z0-9_-]{0,31}.
const maxAgentName = 32

// AgentName derives the herdr agent name for a task: "igris-<id>"
// lowercased, with every character herdr doesn't allow replaced by "-" and
// cut to herdr's 32-character limit (SPEC §11.2).
func AgentName(taskID string) string {
	var b strings.Builder
	for _, r := range "igris-" + strings.ToLower(taskID) {
		if b.Len() == maxAgentName {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Backend runs sessions in herdr tabs of one workspace. The zero value is
// not usable; call New.
type Backend struct {
	c         *Client
	workspace string
}

// New returns a herdr backend that opens tabs in workspace (igris's own
// HERDR_WORKSPACE_ID) and runs herdr through r.
func New(r runner.Runner, workspace string) *Backend {
	return &Backend{c: NewClient(r), workspace: workspace}
}

// Name returns "herdr".
func (b *Backend) Name() string { return Name }

// OpenSession opens a tab in the project directory and starts Claude Code in
// it with spec.Args. It returns once Claude Code is ready for input, or is
// blocked at a startup prompt such as folder trust; the session then holds
// the first prompt until the owner is past it.
func (b *Backend) OpenSession(ctx context.Context, spec backend.SessionSpec) (backend.Session, error) {
	tab, pane, err := b.c.TabCreate(ctx, TabCreateOpts{
		Workspace: b.workspace,
		Cwd:       spec.Dir,
		Label:     spec.Label,
		Env:       spec.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("open a herdr tab for %s: %w", spec.TaskID, err)
	}
	s := &Session{
		c:  b.c,
		id: spec.TaskID,
		ref: backend.SessionRef{
			Backend: Name,
			TabID:   tab.TabID,
			PaneID:  pane.PaneID,
			Agent:   AgentName(spec.TaskID),
		},
	}

	_, err = b.c.AgentStart(ctx, s.ref.Agent, pane.PaneID, startTimeout, spec.Args)
	switch {
	case IsCode(err, CodeAgentNotReady):
		// Blocked during startup; the agent name stays usable (P0-03).
		s.startupBlocked = true
	case err != nil:
		err = fmt.Errorf("start Claude Code for %s in herdr tab %s: %w", spec.TaskID, tab.TabID, err)
		// Don't leave an empty tab behind, even when ctx is cancelled.
		if cerr := b.c.TabClose(context.WithoutCancel(ctx), tab.TabID); cerr != nil && !IsCode(cerr, CodeTabNotFound) {
			err = errors.Join(err, fmt.Errorf("close herdr tab %s by hand: %w", tab.TabID, cerr))
		}
		return nil, err
	}
	return s, nil
}
