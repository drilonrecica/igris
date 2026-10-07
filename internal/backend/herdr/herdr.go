package herdr

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
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

// A new tab's shell may still be starting up (a slow rc file, a fresh herdr
// server) when igris starts Claude Code in it; herdr then answers
// agent_pane_busy. OpenSession retries every paneReadyPoll for up to
// paneReadyWait.
const (
	paneReadyWait = 15 * time.Second
	paneReadyPoll = 250 * time.Millisecond
)

// maxAgentName is herdr's limit on agent names: [a-z][a-z0-9_-]{0,31}.
const maxAgentName = 32

// What a stored session ref may hold. The ref comes from state.json, which a
// session can edit, and its fields become herdr arguments: nothing in them
// may read as an option.
var (
	refIDPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]*$`) // tab and pane IDs, e.g. "w2B:p3"
	refAgentPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

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
	sleep     func(ctx context.Context, d time.Duration) error // waits between start attempts; tests replace it
	hooks     backend.HookStates                               // nil: no hook state
}

// WithHookStates makes sessions fall back to the hook state Claude Code
// recorded when herdr reports `unknown` (no integration, SPEC §6.3).
func (b *Backend) WithHookStates(h backend.HookStates) *Backend {
	b.hooks = h
	return b
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
	return &Backend{c: NewClient(r), workspace: workspace, sleep: sleep}
}

// sleep waits d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
		c:     b.c,
		id:    spec.TaskID,
		sleep: b.sleep,
		hooks: b.hooks,
		ref: backend.SessionRef{
			Backend:       Name,
			TabID:         tab.TabID,
			PaneID:        pane.PaneID,
			Agent:         AgentName(spec.TaskID),
			ClaudeSession: spec.ClaudeSession,
		},
	}

	err = b.startAgent(ctx, s.ref.Agent, pane.PaneID, spec.Args)
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

// startAgent starts Claude Code in pane, retrying while the pane's shell
// hasn't reached its prompt yet (agent_pane_busy, SPEC §11.2).
func (b *Backend) startAgent(ctx context.Context, name, paneID string, args []string) error {
	for wait := time.Duration(0); ; wait += paneReadyPoll {
		_, err := b.c.AgentStart(ctx, name, paneID, startTimeout, args)
		if !IsCode(err, CodeAgentPaneBusy) {
			return err
		}
		if wait >= paneReadyWait {
			return fmt.Errorf("the new tab's shell did not reach its prompt within %s: %w", paneReadyWait, err)
		}
		if serr := b.sleep(ctx, paneReadyPoll); serr != nil {
			return errors.Join(err, serr)
		}
	}
}

// Attach rebuilds a session from a stored ref after igris restarted. It
// fails with an error wrapping backend.ErrSessionGone when the pane is gone
// or Claude Code no longer runs in it. A startup prompt the session may have
// been blocked at is not remembered: State reports what `pane get` says.
func (b *Backend) Attach(ctx context.Context, ref backend.SessionRef) (backend.Session, error) {
	if ref.Backend != Name {
		return nil, fmt.Errorf("attach: session ref is for backend %q, not %s", ref.Backend, Name)
	}
	if !refIDPattern.MatchString(ref.TabID) || !refIDPattern.MatchString(ref.PaneID) || !refAgentPattern.MatchString(ref.Agent) ||
		(ref.ClaudeSession != "" && !backend.ValidClaudeSession(ref.ClaudeSession)) {
		return nil, fmt.Errorf("attach: session ref (tab %q, pane %q, agent %q) is incomplete or malformed; state.json looks damaged, delete it to start a new run", ref.TabID, ref.PaneID, ref.Agent)
	}
	s := &Session{c: b.c, id: ref.Agent, ref: ref, sleep: b.sleep, hooks: b.hooks}
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

// errNeedsHerdr is appended to availability errors: tmux is the other way
// to run igris (SPEC §11.3).
const errNeedsHerdr = "To use tmux instead, run igris inside tmux with backend = \"auto\" or \"tmux\" in igris.toml"

// Available reports why herdr can't host sessions: igris doesn't run inside
// a herdr pane, or the herdr server isn't reachable and running.
func (b *Backend) Available(ctx context.Context) error {
	if b.workspace == "" {
		return fmt.Errorf("igris must run inside a herdr pane (%s is not set); start herdr, open a pane and run igris there. %s", WorkspaceEnv, errNeedsHerdr)
	}
	st, err := b.c.ServerStatus(ctx)
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("herdr is not installed or not in PATH; install herdr and run igris inside a herdr pane. %s", errNeedsHerdr)
	}
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
		return "herdr's Claude Code integration is not installed; igris reads the agent state from Claude Code's hooks instead (`herdr integration install claude` adds herdr's own)"
	}
	return ""
}

// Notify shows a herdr toast with the sound matching n.Sound: "request"
// when igris needs the owner, "done" for completions (SPEC §10). A toast
// herdr chose not to show is not an error.
func (b *Backend) Notify(ctx context.Context, n backend.Notification) error {
	sound := SoundRequest
	switch n.Sound {
	case backend.SoundDone:
		sound = SoundDone
	case backend.SoundNone:
		sound = SoundNone
	}
	if _, err := b.c.NotificationShow(ctx, n.Title, n.Body, sound); err != nil {
		return fmt.Errorf("show herdr toast: %w", err)
	}
	return nil
}
