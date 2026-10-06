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

// WorkspaceEnv is the variable herdr sets in every pane it opens.
const WorkspaceEnv = "HERDR_WORKSPACE_ID"

// NewFromEnv is New with the workspace taken from HERDR_WORKSPACE_ID. When
// igris runs outside a herdr pane the workspace is empty and Available
// says so.
func NewFromEnv(r runner.Runner, getenv func(string) string) *Backend {
	return New(r, getenv(WorkspaceEnv))
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

// Attach rebuilds a session from a stored ref after igris restarted. It
// fails with an error wrapping backend.ErrSessionGone when the pane is gone
// or Claude Code no longer runs in it. A startup prompt the session may have
// been blocked at is not remembered: State reports what `pane get` says.
func (b *Backend) Attach(ctx context.Context, ref backend.SessionRef) (backend.Session, error) {
	if ref.Backend != Name {
		return nil, fmt.Errorf("attach: session ref is for backend %q, not %s", ref.Backend, Name)
	}
	if ref.TabID == "" || ref.PaneID == "" || ref.Agent == "" {
		return nil, fmt.Errorf("attach: session ref %+v is incomplete; state.json looks damaged, delete it to start a new run", ref)
	}
	s := &Session{c: b.c, id: ref.Agent, ref: ref}
	p, err := b.c.PaneGet(ctx, ref.PaneID)
	switch {
	case gone(err):
		return nil, s.goneErr("attach", err)
	case err != nil:
		return nil, fmt.Errorf("attach %s: %w", ref.Agent, err)
	case p.Agent == "":
		return nil, s.goneErr("attach", errors.New("claude code is no longer running in the pane"))
	}
	return s, nil
}

// errNeedsHerdr is appended to availability errors so the owner knows v1
// has no other backend (SPEC §11.3).
const errNeedsHerdr = "igris v1 requires herdr (tmux support is planned)"

// Available reports why herdr can't host sessions: igris doesn't run inside
// a herdr pane, or the herdr server isn't reachable and running.
func (b *Backend) Available(ctx context.Context) error {
	if b.workspace == "" {
		return fmt.Errorf("igris must run inside a herdr pane (%s is not set); start herdr, open a pane and run igris there. %s", WorkspaceEnv, errNeedsHerdr)
	}
	st, err := b.c.ServerStatus(ctx)
	if err != nil {
		return fmt.Errorf("the herdr server is not reachable: %w; start herdr first. %s", err, errNeedsHerdr)
	}
	if st.Status != "running" {
		return fmt.Errorf("the herdr server is %q, not running; start herdr first. %s", st.Status, errNeedsHerdr)
	}
	return nil
}

// IntegrationHint returns advice when herdr's Claude Code integration isn't
// installed, so agent states would be inaccurate, and "" when it is
// installed or can't be determined.
func (b *Backend) IntegrationHint(ctx context.Context) string {
	st, err := b.c.IntegrationStatus(ctx)
	if err != nil {
		return ""
	}
	if s, ok := st["claude"]; ok && strings.HasPrefix(s, "not installed") {
		return "herdr's Claude Code integration is not installed, so agent states may be inaccurate; run `herdr integration install claude`"
	}
	return ""
}

// Notify is replaced by the real toast in M3-05.
func (b *Backend) Notify(context.Context, backend.Notification) error { return nil }
