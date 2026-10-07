package tmux

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
const Name = "tmux"

// Env is the variable tmux sets in every pane it runs; igris must run
// inside tmux to use this backend.
const Env = "TMUX"

// Session refs read back from state.json are only used in this shape.
var (
	windowPattern = regexp.MustCompile(`^@[0-9]+$`)
	panePattern   = regexp.MustCompile(`^%[0-9]+$`)
)

// Startup timing (SPEC §11.5): OpenSession polls for the first hook state
// every startupPoll for up to startupWait; without one the session is taken
// to sit at a startup question (folder trust) and holds its first prompt.
const (
	startupWait = 15 * time.Second
	startupPoll = 250 * time.Millisecond
)

// Backend runs sessions in windows of the tmux server igris runs in. The
// zero value is not usable; call New.
type Backend struct {
	c      *Client
	inside bool                                             // igris runs inside tmux ($TMUX set)
	sleep  func(ctx context.Context, d time.Duration) error // tests replace it
	hooks  backend.HookStates                               // nil: agent state unknown
}

// NewFromEnv is New with "inside tmux" taken from $TMUX.
func NewFromEnv(r runner.Runner, getenv func(string) string) *Backend {
	return New(r, getenv(Env) != "")
}

// New returns a tmux backend that runs tmux through r; inside says whether
// igris runs inside a tmux client.
func New(r runner.Runner, inside bool) *Backend {
	return &Backend{c: NewClient(r), inside: inside, sleep: sleep}
}

// WithHookStates sets where sessions read their agent state: the hook
// state Claude Code records (SPEC §6.3). Without it every state is
// unknown, apart from an exited pane.
func (b *Backend) WithHookStates(h backend.HookStates) *Backend {
	b.hooks = h
	return b
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

// Name returns "tmux".
func (b *Backend) Name() string { return Name }

// Available reports why tmux can't host sessions: igris doesn't run inside
// tmux, tmux isn't installed, or its server isn't reachable.
func (b *Backend) Available(ctx context.Context) error {
	if !b.inside {
		return fmt.Errorf("igris must run inside tmux (%s is not set); start tmux and run igris in it, or run igris inside a herdr pane", Env)
	}
	_, err := b.c.Version(ctx)
	if errors.Is(err, exec.ErrNotFound) {
		return errors.New("tmux is not installed or not in PATH; install tmux 3.2 or newer")
	}
	if err != nil {
		return fmt.Errorf("the tmux server is not reachable: %w; run igris inside a running tmux session", err)
	}
	return nil
}

// OpenSession opens a detached window in the project directory running
// Claude Code with spec.Args and waits for its first hook. It returns once
// Claude Code is ready for input; without a hook after startupWait (the
// folder-trust question, or a slow start) the session holds its first
// prompt until the hooks say Claude Code is idle.
func (b *Backend) OpenSession(ctx context.Context, spec backend.SessionSpec) (backend.Session, error) {
	argv := append([]string{"claude"}, spec.Args...)
	window, pane, err := b.c.NewWindow(ctx, spec.Dir, spec.Label, argv)
	if err != nil {
		return nil, fmt.Errorf("open a tmux window for %s: %w", spec.TaskID, err)
	}
	s := &Session{
		c:     b.c,
		id:    spec.TaskID,
		sleep: b.sleep,
		hooks: b.hooks,
		ref: backend.SessionRef{
			Backend:       Name,
			TabID:         window,
			PaneID:        pane,
			ClaudeSession: spec.ClaudeSession,
		},
	}
	fail := func(err error) (backend.Session, error) {
		err = fmt.Errorf("start Claude Code for %s in tmux window %s: %w", spec.TaskID, window, err)
		// Don't leave a window behind, even when ctx is cancelled.
		if cerr := b.c.KillWindow(context.WithoutCancel(ctx), window); cerr != nil && !IsGone(cerr) {
			err = errors.Join(err, fmt.Errorf("close tmux window %s by hand: %w", window, cerr))
		}
		return nil, err
	}
	if err := b.c.RemainOnExit(ctx, window); err != nil && !IsGone(err) {
		return fail(err)
	}
	for range int(startupWait / startupPoll) {
		p, err := b.c.Pane(ctx, pane)
		switch {
		case IsGone(err):
			return nil, fmt.Errorf("start Claude Code for %s: it exited at once; run `claude` in a terminal to see why", spec.TaskID)
		case err != nil:
			return fail(err)
		case p.Dead:
			return fail(fmt.Errorf("claude exited with status %d at once; run `claude` in a terminal to see why", p.DeadStatus))
		}
		if st := s.hookState(); st != backend.Unknown {
			if st == backend.Exited {
				return fail(errors.New("claude ended its session at once"))
			}
			// SessionStart fires as the input box appears, a moment
			// before Claude Code takes input.
			if err := b.sleep(ctx, promptSettle); err != nil {
				return fail(err)
			}
			return s, nil
		}
		if err := b.sleep(ctx, startupPoll); err != nil {
			return fail(err)
		}
	}
	s.startupBlocked = true
	return s, nil
}

// Attach reattaches to a session from its stored ref (after an igris
// restart). The IDs are shape-checked first: state.json is writable by
// sessions. A missing or dead pane wraps backend.ErrSessionGone.
func (b *Backend) Attach(ctx context.Context, ref backend.SessionRef) (backend.Session, error) {
	if ref.Backend != Name {
		return nil, fmt.Errorf("attach: session ref is for backend %q, not %s", ref.Backend, Name)
	}
	if !windowPattern.MatchString(ref.TabID) || !panePattern.MatchString(ref.PaneID) ||
		(ref.ClaudeSession != "" && !backend.ValidClaudeSession(ref.ClaudeSession)) {
		return nil, fmt.Errorf("attach: session ref (window %q, pane %q) is incomplete or malformed; state.json looks damaged, delete it to start a new run", ref.TabID, ref.PaneID)
	}
	s := &Session{c: b.c, id: ref.TabID, ref: ref, sleep: b.sleep, hooks: b.hooks}
	p, err := b.c.Pane(ctx, ref.PaneID)
	switch {
	case IsGone(err):
		return nil, s.goneErr("attach", err)
	case err != nil:
		return nil, fmt.Errorf("attach tmux pane %s: %w", ref.PaneID, err)
	case p.Dead:
		return nil, s.goneErr("attach", errors.New("claude code is no longer running in the pane"))
	}
	return s, nil
}

// Notify shows a message on igris's tmux client (SPEC §10). tmux has no
// sound; the router's other channels carry the rest.
func (b *Backend) Notify(ctx context.Context, n backend.Notification) error {
	text := n.Title
	if body := strings.TrimSpace(n.Body); body != "" {
		text += ": " + body
	}
	if err := b.c.DisplayMessage(ctx, text); err != nil {
		return fmt.Errorf("tmux message: %w", err)
	}
	return nil
}
