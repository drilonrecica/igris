package tmux

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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

// Backend runs sessions in windows of the tmux server igris runs in, or,
// headless, in a detached session of its own. The zero value is not
// usable; call New or NewHeadless.
type Backend struct {
	c      *Client
	inside bool // igris runs inside tmux ($TMUX set)
	// headless is the detached session the windows open in when igris
	// runs outside tmux (igris serve, SPEC §11.5), and dir its directory;
	// "" inside tmux.
	headless, dir string
	sleep         func(ctx context.Context, d time.Duration) error // tests replace it
	hooks         backend.HookStates                               // nil: agent state unknown
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

// NewHeadless returns a tmux backend for an igris serve outside tmux (SPEC
// §11.5): its windows open in the detached session SessionName(root) on
// the default server, created in root when it doesn't exist.
func NewHeadless(r runner.Runner, root string) *Backend {
	b := New(r, false)
	b.headless, b.dir = SessionName(root), root
	return b
}

// sessionLen is the longest project part of a headless session's name.
const sessionLen = 32

// SessionName is the headless session of the project at root:
// igris-<project>, the root's base name with every character outside
// [A-Za-z0-9_-] replaced by '-', at most 32 characters (SPEC §11.5).
func SessionName(root string) string {
	var b []byte
	for _, c := range filepath.Base(root) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			c = '-'
		}
		b = append(b, byte(c))
	}
	return "igris-" + string(b[:min(len(b), sessionLen)])
}

// Headless returns the detached session the windows open in, "" when igris
// runs inside tmux.
func (b *Backend) Headless() string { return b.headless }

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
// Headless, only tmux itself is needed: the session's server starts with
// the first window.
func (b *Backend) Available(ctx context.Context) error {
	if b.headless != "" {
		_, err := b.c.ProgramVersion(ctx)
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("tmux is not installed or not in PATH; install tmux 3.2 or newer")
		}
		if err != nil {
			return fmt.Errorf("tmux can't be run: %w", err)
		}
		return nil
	}
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
	if spec.Side != "" && !backend.ValidSideID(spec.Side) {
		return nil, fmt.Errorf("open a tmux window: invalid side session ID %q", spec.Side)
	}
	argv := append([]string{"claude"}, spec.Args...)
	target := ""
	if b.headless != "" {
		// Every time: the owner may have closed the session during the run.
		if err := b.c.EnsureSession(ctx, b.headless, b.dir); err != nil {
			return nil, fmt.Errorf("open the tmux session %s for %s: %w", b.headless, spec.Subject(), err)
		}
		target = b.headless
	}
	window, pane, err := b.c.NewWindow(ctx, target, spec.Dir, spec.Label, spec.Env, argv)
	if err != nil {
		return nil, fmt.Errorf("open a tmux window for %s: %w", spec.Subject(), err)
	}
	s := &Session{
		c:     b.c,
		id:    spec.Subject(),
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
		err = fmt.Errorf("start Claude Code for %s in tmux window %s: %w", spec.Subject(), window, err)
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
			return nil, fmt.Errorf("start Claude Code for %s: it exited at once; run `claude` in a terminal to see why", spec.Subject())
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
// sessions. A missing or dead pane wraps backend.ErrSessionGone. So does,
// with backend.ErrSessionForeign, a pane whose start command is not igris's
// claude for taskID and the ref's Claude session (StartedFor): a session
// could otherwise name the owner's shell as its pane and have igris type
// there (V10-05).
func (b *Backend) Attach(ctx context.Context, ref backend.SessionRef, taskID string) (backend.Session, error) {
	return b.attach(ctx, ref, backend.RulesFile(taskID), taskID, "state.json", "delete the file to start a new run")
}

// AttachSide is Attach for side session sideID: the pane's start command
// must name its rules file, .igris/side/<id>.rules.md (SPEC §6.10).
func (b *Backend) AttachSide(ctx context.Context, ref backend.SessionRef, sideID string) (backend.Session, error) {
	if !backend.ValidSideID(sideID) {
		return nil, fmt.Errorf("attach: invalid side session ID %q", sideID)
	}
	return b.attach(ctx, ref, backend.SideRulesFile(sideID), "side "+sideID, "its record in .igris/side/", "igris forgets it")
}

// attach is Attach for the session started with the rules file rules. For
// messages, what names the session, source the file the ref came from and
// fix what to do about a bad one.
func (b *Backend) attach(ctx context.Context, ref backend.SessionRef, rules, what, source, fix string) (backend.Session, error) {
	if ref.Backend != Name {
		return nil, fmt.Errorf("attach: session ref is for backend %q, not %s", ref.Backend, Name)
	}
	if !windowPattern.MatchString(ref.TabID) || !panePattern.MatchString(ref.PaneID) ||
		(ref.ClaudeSession != "" && !backend.ValidClaudeSession(ref.ClaudeSession)) {
		return nil, fmt.Errorf("attach: session ref (window %q, pane %q) is incomplete or malformed; %s looks damaged, %s", ref.TabID, ref.PaneID, source, fix)
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
	if ref.ClaudeSession == "" {
		return nil, s.goneErr("attach", errors.New("the session ref has no Claude session to check the pane against"))
	}
	start, err := b.c.StartCommand(ctx, ref.PaneID)
	switch {
	case IsGone(err):
		return nil, s.goneErr("attach", err)
	case err != nil:
		return nil, fmt.Errorf("attach tmux pane %s: %w", ref.PaneID, err)
	case !startedWith(start, ref.ClaudeSession, rules):
		return nil, fmt.Errorf("attach tmux pane %s for %s: %w: %w; %s names a pane igris did not start for it, %s", ref.PaneID, what, backend.ErrSessionGone, backend.ErrSessionForeign, source, fix)
	}
	return s, nil
}

// StartedFor reports whether start, a pane's start command as tmux shows
// it, is claude started by igris for taskID with the Claude session uuid:
// `claude … --session-id <uuid> … --append-system-prompt-file <…/.igris/
// prompts/<task>.rules.md>`. tmux joins the arguments with spaces and
// quotes one that holds a space.
func StartedFor(start, uuid, taskID string) bool {
	return startedWith(start, uuid, backend.RulesFile(taskID))
}

// StartedForSide is StartedFor for side session id, whose rules file is
// .igris/side/<id>.rules.md.
func StartedForSide(start, uuid, id string) bool {
	return startedWith(start, uuid, backend.SideRulesFile(id))
}

// startedWith reports whether start is claude with the Claude session uuid
// and the rules file rules (relative to the project root).
func startedWith(start, uuid, rules string) bool {
	f := strings.Fields(start)
	if len(f) == 0 || f[0] != "claude" {
		return false
	}
	session := false
	for i := 1; i+1 < len(f); i++ {
		if f[i] == "--session-id" && f[i+1] == uuid {
			session = true
		}
	}
	return session && (strings.Contains(start, "/"+rules+" ") || strings.HasSuffix(start, "/"+rules) || strings.Contains(start, "/"+rules+"\""))
}

// Notify shows a message on igris's tmux client (SPEC §10). tmux has no
// sound; the router's other channels carry the rest. Headless there is no
// client to show it on, so it shows nothing.
func (b *Backend) Notify(ctx context.Context, n backend.Notification) error {
	if b.headless != "" {
		return nil
	}
	text := n.Title
	if body := strings.TrimSpace(n.Body); body != "" {
		text += ": " + body
	}
	if err := b.c.DisplayMessage(ctx, text); err != nil {
		return fmt.Errorf("tmux message: %w", err)
	}
	return nil
}
