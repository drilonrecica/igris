// Package side opens, lists, focuses and closes side sessions (SPEC §6.10,
// since 1.2): Claude Code sessions the owner opens from the TUI that are
// not tasks. igris watches them for no signal, counts no attempt and
// writes no Status; it only records each one under .igris/side/ so a
// restarted igris finds it again, bound to its own pane through the
// backend's AttachSide.
package side

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/hook"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
	"github.com/drilonrecica/igris/internal/usage"
)

// MaxOpen is how many side sessions may be open at a time.
const MaxOpen = 8

// MaxPrompt bounds a first prompt, in bytes (SPEC §6.10).
const MaxPrompt = 8 << 10

// Env is the variable every side session's environment holds, set to its
// ID: `igris done`, `skip` and `reset` refuse while it is set (SPEC §6.2).
const Env = "IGRIS_SIDE"

// gitTimeout bounds each git call (worktree add, status, remove).
const gitTimeout = 2 * time.Minute

// CleanPrompt is a first prompt as igris types it: escape sequences and
// control characters other than newlines and tabs removed, cut to
// MaxPrompt bytes on a character boundary.
func CleanPrompt(s string) string {
	s = textsafe.Clean(s)
	if len(s) <= MaxPrompt {
		return s
	}
	s = s[:MaxPrompt]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// Options configures a Manager.
type Options struct {
	Root    string          // the project root
	Backend backend.Backend // the backend sessions open in
	Runner  runner.Runner   // runs git for a side session's own worktree
	// GitEnv is git's environment; nil inherits igris's.
	GitEnv []string
	// IgrisPath is the igris binary the hooks run; "" means the running
	// one (engine.IgrisPath).
	IgrisPath string
	Now       func() time.Time       // nil: time.Now
	NewID     func() (string, error) // nil: state.NewSideID
	NewUUID   func() (string, error) // nil: engine.NewSessionID
	// Log appends a line to the run log (side_opened, side_closed and
	// side_usage, SPEC §6.10, §13); nil logs nothing. It sets no run: the
	// caller's Log adds the run the writing igris has, if any.
	Log func(state.Event)
	// Usage reads an ended session's transcript and logs its side_usage
	// line ([usage] enabled, SPEC §6.9).
	Usage bool
}

// Manager keeps the side sessions of one project. It holds the sessions it
// opened or reattached; .igris/side/ is the truth, so several Managers
// (a run and a read-only home, each its own igris) can share a project.
// It is safe for concurrent use.
type Manager struct {
	o        Options
	mu       sync.Mutex
	sessions map[string]backend.Session // by side ID
}

// New returns a Manager for o.Root.
func New(o Options) *Manager {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NewID == nil {
		o.NewID = state.NewSideID
	}
	if o.NewUUID == nil {
		o.NewUUID = engine.NewSessionID
	}
	return &Manager{o: o, sessions: map[string]backend.Session{}}
}

// Spec is a side session to open, as the dialog chose it (SPEC §15.7).
type Spec struct {
	Preset string // state.SidePresetBlank, SidePresetTask or SidePresetIgris
	Task   string // the task, with SidePresetTask
	Rank   string // the [models] rank
	Model  string // the rank's model
	Mode   string // one of state.SideModes
	// ExtraArgs are claude.extra_args, checked as for a task (SPEC §7.4).
	ExtraArgs []string
	// Worktree gives the session its own worktree (SPEC §6.10).
	Worktree bool
	// PlanPath is the plan's absolute path: a session in its own worktree
	// may read it (SPEC §6.8).
	PlanPath string
	// RunTask is the task a run's session works on in the project root
	// right now, "" when no run does: a session in the shared tree is told
	// to ask before editing files.
	RunTask string
	// Prompt is the first prompt, typed once the session is ready;
	// CleanPrompt applies. Empty types nothing.
	Prompt string
}

// Entry is an open side session.
type Entry struct {
	Record  state.SideRecord
	Session backend.Session
}

// Ended is a side session this igris found gone, or closed.
type Ended struct {
	Record state.SideRecord
	// Closed: the owner closed it (Close); otherwise it was found gone.
	Closed bool
	// Foreign: its record named a pane igris did not start for it, which
	// was left alone (SPEC §6.10).
	Foreign bool
	// Worktree is its own worktree when it is kept: found gone, or it has
	// uncommitted changes. nil when it had none or it was removed.
	Worktree *state.Worktree
	// WorktreeRemoved: Close removed its clean worktree; the branch stays.
	WorktreeRemoved bool
}

// Label is a side session's pane label: "side · <subject> · <rank>", the
// subject being the task for SidePresetTask and "igris" for
// SidePresetIgris; a blank session has none.
func Label(preset, task, rank string) string {
	switch preset {
	case state.SidePresetTask:
		return "side · " + task + " · " + rank
	case state.SidePresetIgris:
		return "side · igris · " + rank
	}
	return "side · " + rank
}

func checkSpec(s Spec) error {
	switch {
	case s.Preset != state.SidePresetBlank && s.Preset != state.SidePresetTask && s.Preset != state.SidePresetIgris:
		return fmt.Errorf("unknown preset %q", s.Preset)
	case (s.Preset == state.SidePresetTask) != (s.Task != ""):
		return errors.New("a task is named with the about-this-task preset, and only then")
	case s.Task != "" && !plan.ValidID(s.Task):
		return fmt.Errorf("invalid task ID %q", s.Task)
	case !slices.Contains(state.SideModes, s.Mode):
		return fmt.Errorf("mode %q: side sessions run in %s, never skipping permissions", s.Mode, strings.Join(state.SideModes, ", "))
	case strings.TrimSpace(s.Rank) == "" || strings.TrimSpace(s.Model) == "":
		return errors.New("no rank or model; map the rank in [models]")
	case !state.ValidSideRank(s.Rank) || !state.ValidSideModel(s.Model):
		return fmt.Errorf("rank %q or its model %q is not a plain name", s.Rank, s.Model)
	case s.RunTask != "" && !plan.ValidID(s.RunTask):
		return fmt.Errorf("invalid task ID %q", s.RunTask)
	case s.Worktree && !filepath.IsAbs(s.PlanPath):
		return fmt.Errorf("plan path %q is not absolute", s.PlanPath)
	}
	return nil
}

// Open opens a side session for s through the backend's OpenSession
// (SPEC §6.10): in the project root, or in a new worktree of its own; with
// its rules and hooks files and IGRIS_SIDE in its environment; then it
// records the session and types the first prompt. When the prompt fails
// the session stays open and recorded, and the error says so.
func (m *Manager) Open(ctx context.Context, s Spec) (Entry, error) {
	if err := checkSpec(s); err != nil {
		return Entry{}, fmt.Errorf("open a side session: %w", err)
	}
	recs, bad, err := state.ReadSideRecords(m.o.Root)
	if err != nil {
		return Entry{}, fmt.Errorf("open a side session: %w", err)
	}
	if len(recs)+len(bad) >= MaxOpen {
		return Entry{}, fmt.Errorf("open a side session: %d are open, the most igris keeps; close one first", len(recs)+len(bad))
	}
	id, err := m.freeID(recs, bad)
	if err != nil {
		return Entry{}, fmt.Errorf("open a side session: %w", err)
	}
	uuid, err := m.o.NewUUID()
	if err != nil {
		return Entry{}, fmt.Errorf("open a side session: %w", err)
	}

	rec := state.SideRecord{
		V: state.SideVersion, ID: id, Backend: m.o.Backend.Name(), ClaudeSession: uuid,
		Model: s.Model, Rank: s.Rank, Mode: s.Mode, Preset: s.Preset, Task: s.Task, Dir: ".",
	}
	dir := m.o.Root
	cleanup := func() { _ = state.RemoveSideFiles(m.o.Root, id) }
	fail := func(err error) (Entry, error) {
		cleanup()
		return Entry{}, fmt.Errorf("open side session %s: %w", id, err)
	}
	if s.Worktree {
		wt := state.SideWorktree(id)
		if err := state.EnsureWorktreesDir(m.o.Root); err != nil {
			return fail(err)
		}
		if _, err := m.git(ctx, m.o.Root, "worktree", "add", "-b", wt.Branch, wt.Path, "HEAD"); err != nil {
			return Entry{}, fmt.Errorf("open side session %s: make its worktree %s: %w (it needs a git repository with at least one commit)", id, wt.Path, err)
		}
		rec.Worktree, rec.Dir, dir = wt, wt.Path, filepath.Join(m.o.Root, wt.Path)
		cleanup = func() {
			_ = state.RemoveSideFiles(m.o.Root, id)
			ctx := context.WithoutCancel(ctx)
			if _, err := m.git(ctx, m.o.Root, "worktree", "remove", "--force", wt.Path); err == nil {
				_, _ = m.git(ctx, m.o.Root, "branch", "-D", wt.Branch)
			}
		}
	}

	runTask := s.RunTask
	if s.Worktree {
		runTask = "" // it shares no files with the run (SPEC §6.10)
	}
	rules, err := state.WriteSideRules(m.o.Root, id, prompt.SideRules(runTask))
	if err != nil {
		return fail(err)
	}
	settings, err := m.writeHooks(id, s)
	if err != nil {
		return fail(err)
	}
	args, err := engine.ClaudeArgs(engine.ClaudeParams{
		Model:        s.Model,
		SessionID:    uuid,
		Mode:         s.Mode,
		RulesFile:    rules,
		SettingsFile: settings,
		ExtraArgs:    s.ExtraArgs,
	})
	if err != nil {
		return fail(err)
	}
	if err := state.ClearAgentState(m.o.Root, uuid); err != nil {
		return fail(err)
	}
	rec.At = m.o.Now().UTC()
	sess, err := m.o.Backend.OpenSession(ctx, backend.SessionSpec{
		Side:          id,
		Dir:           dir,
		Label:         Label(s.Preset, s.Task, s.Rank),
		Args:          args,
		Env:           []string{Env + "=" + id},
		ClaudeSession: uuid,
	})
	if err != nil {
		return fail(fmt.Errorf("%w; check that `claude` starts in %s (installed, on PATH, logged in)", err, m.o.Backend.Name()))
	}
	rec.Ref = sess.Ref()
	if err := state.WriteSideRecord(m.o.Root, rec); err != nil {
		// Unrecorded, a restarted igris would never find it again.
		_ = sess.Close(context.WithoutCancel(ctx))
		return fail(err)
	}
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	m.log(state.Event{Type: state.EventSideOpened, Side: id, Rank: rec.Rank, Model: rec.Model})
	e := Entry{Record: rec, Session: sess}
	if text := CleanPrompt(s.Prompt); strings.TrimSpace(text) != "" {
		if err := sess.Prompt(ctx, text); err != nil {
			return e, fmt.Errorf("side session %s is open, but its first prompt didn't reach it: %w; type it in its pane", id, err)
		}
	}
	return e, nil
}

// freeID returns a new side ID that names no recorded session.
func (m *Manager) freeID(recs []state.SideRecord, bad []state.SideProblem) (string, error) {
	for range 16 {
		id, err := m.o.NewID()
		if err != nil {
			return "", err
		}
		if !backend.ValidSideID(id) {
			return "", fmt.Errorf("invalid side session ID %q", id)
		}
		taken := slices.ContainsFunc(recs, func(r state.SideRecord) bool { return r.ID == id }) ||
			slices.ContainsFunc(bad, func(p state.SideProblem) bool { return p.ID == id })
		if _, err := os.Lstat(state.SideRulesPath(m.o.Root, id)); !taken && err != nil {
			return id, nil
		}
	}
	return "", errors.New("no free side session ID")
}

// writeHooks writes the session's hooks file: the state hooks of SPEC §6.3
// only, no plan approval and no status line, and in its own worktree a rule
// to read the plan (SPEC §6.8). Without a usable igris binary it writes
// none ("", like engine.WriteHooks): hook state is an aid.
func (m *Manager) writeHooks(id string, s Spec) (string, error) {
	exe := m.o.IgrisPath
	if exe == "" {
		if exe = engine.IgrisPath(); exe == "" {
			return "", nil
		}
	}
	var allow []string
	// Claude Code reads a rule's path as a pattern: a plan path with
	// pattern characters gets no rule, the session asks instead.
	if s.Worktree && !strings.ContainsAny(s.PlanPath, `*?[]{}!\`) {
		allow = []string{"Read(/" + s.PlanPath + ")"}
	}
	data, err := hook.Settings(exe, m.o.Root, allow, nil, "")
	if err != nil {
		return "", err
	}
	return state.WriteSideHooks(m.o.Root, id, data)
}

// Sync is what Refresh found.
type Sync struct {
	Open     []Entry  // reattached or still running, oldest first
	Ended    []Ended  // found gone by this igris since the last Refresh
	Warnings []string // records left alone or forgotten, and why
}

// Refresh reads .igris/side/ and reattaches every recorded side session
// it doesn't hold yet with AttachSide, bound to its own pane (SPEC §6.10):
// home and the run view call it when they open, and home on its refresh.
// A session that is gone, or whose record names a pane igris did not start
// for it, ends: this igris claims its record, removes it and its files and
// reports it in Ended, keeping a worktree it had. A record for another
// backend, or one AttachSide can't check now, is left for later.
func (m *Manager) Refresh(ctx context.Context) (Sync, error) {
	var out Sync
	recs, bad, err := state.ReadSideRecords(m.o.Root)
	if err != nil {
		return out, err
	}
	for _, p := range bad {
		if errors.Is(p.Err, state.ErrSideNewer) {
			out.Warnings = append(out.Warnings, fmt.Sprintf("side %s was recorded by a newer igris; this one leaves it alone", p.ID))
			continue
		}
		if _, claimed, _ := state.ClaimSideRecord(m.o.Root, p.ID); claimed {
			_ = state.RemoveSideFiles(m.o.Root, p.ID)
			m.forget(p.ID)
			out.Warnings = append(out.Warnings, fmt.Sprintf("side %s: its record is damaged (%v); igris forgot it, close its pane by hand if it is still open", p.ID, p.Err))
		}
	}
	live := map[string]bool{}
	for _, r := range recs {
		live[r.ID] = true
		if r.Backend != m.o.Backend.Name() {
			out.Warnings = append(out.Warnings, fmt.Sprintf("side %s runs in %s, and igris uses %s now; open igris in %s to see it", r.ID, r.Backend, m.o.Backend.Name(), r.Backend))
			continue
		}
		sess, err := m.session(ctx, r)
		switch {
		case errors.Is(err, backend.ErrSessionGone):
			foreign := errors.Is(err, backend.ErrSessionForeign)
			if foreign {
				out.Warnings = append(out.Warnings, fmt.Sprintf("side %s: its record names a pane igris did not start for it; igris forgot it and left the pane alone", r.ID))
			}
			if e, ok := m.end(r.ID, foreign); ok {
				out.Ended = append(out.Ended, e)
			}
		case err != nil:
			out.Warnings = append(out.Warnings, fmt.Sprintf("side %s: %v", r.ID, err))
		default:
			out.Open = append(out.Open, Entry{Record: r, Session: sess})
		}
	}
	// Ended by another igris meanwhile.
	m.mu.Lock()
	for id := range m.sessions {
		if !live[id] {
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	return out, nil
}

// session returns r's session: the one this Manager holds while its pane
// lives, else AttachSide's. A gone session is an error wrapping
// backend.ErrSessionGone.
func (m *Manager) session(ctx context.Context, r state.SideRecord) (backend.Session, error) {
	m.mu.Lock()
	sess := m.sessions[r.ID]
	m.mu.Unlock()
	if sess != nil {
		st, err := sess.State(ctx)
		if err != nil {
			return nil, err
		}
		if st != backend.Exited {
			return sess, nil
		}
		return nil, fmt.Errorf("side %s: %w", r.ID, backend.ErrSessionGone)
	}
	sess, err := m.o.Backend.AttachSide(ctx, r.Ref, r.ID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.sessions[r.ID] = sess
	m.mu.Unlock()
	return sess, nil
}

// end claims side session id's record and removes it and its files. It
// reports false when another igris claimed it first: that one reports it.
func (m *Manager) end(id string, foreign bool) (Ended, bool) {
	m.forget(id)
	r, claimed, err := state.ClaimSideRecord(m.o.Root, id)
	_ = state.RemoveSideFiles(m.o.Root, id)
	if !claimed || err != nil {
		return Ended{}, false
	}
	e := Ended{Record: r, Foreign: foreign}
	m.logEnded(r)
	if r.Worktree != nil && isDir(filepath.Join(m.o.Root, r.Worktree.Path)) {
		e.Worktree = r.Worktree
	}
	return e, true
}

// log appends ev to the run log, if the Manager has one.
func (m *Manager) log(ev state.Event) {
	if m.o.Log != nil {
		m.o.Log(ev)
	}
}

// logEnded logs that r ended and, with [usage] on, what its transcript
// recorded (SPEC §6.10): once, by the igris that claimed the record. The
// transcript is read as a task's is (SPEC §6.9); a side session is never
// an attempt and never audited.
func (m *Manager) logEnded(r state.SideRecord) {
	if m.o.Log == nil {
		return
	}
	m.log(state.Event{Type: state.EventSideClosed, Side: r.ID, Rank: r.Rank, Model: r.Model})
	if !m.o.Usage {
		return
	}
	ev := state.Event{Type: state.EventSideUsage, Side: r.ID, Rank: r.Rank, Model: r.Model}
	rd := m.readUsage(r)
	if rd == nil || !rd.Available() {
		ev.Detail = "the transcript could not be read"
		if rd == nil {
			ev.Detail = "no transcript reported by the session's hooks"
		}
		m.log(ev)
		return
	}
	got := rd.Usage()
	ev.Usage, ev.Models, ev.SubModels = &got.Main, got.Models, got.SubModels
	if !got.Sub.IsZero() {
		ev.Sub = &got.Sub
	}
	m.log(ev)
}

// readUsage reads everything r's transcript recorded since it opened; nil
// when its hooks reported none. Only the path they reported is read, as for
// a task (SPEC §6.9).
func (m *Manager) readUsage(r state.SideRecord) *usage.Reader {
	rec, ok := state.PeekAgentState(m.o.Root, r.ClaudeSession)
	if !ok || rec.Transcript == "" {
		return nil
	}
	rd := usage.NewReader(rec.Transcript, r.At)
	_ = rd.Finish(m.o.Now()) // a failure leaves it unavailable
	return rd
}

func (m *Manager) forget(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// record returns side session id's record.
func (m *Manager) record(id string) (state.SideRecord, error) {
	if !backend.ValidSideID(id) {
		return state.SideRecord{}, fmt.Errorf("invalid side session ID %q", id)
	}
	recs, _, err := state.ReadSideRecords(m.o.Root)
	if err != nil {
		return state.SideRecord{}, err
	}
	for _, r := range recs {
		if r.ID == id {
			return r, nil
		}
	}
	return state.SideRecord{}, fmt.Errorf("side %s is not open", id)
}

// Focus brings side session id's pane to the front. A gone session wraps
// backend.ErrSessionGone; the next Refresh ends it.
func (m *Manager) Focus(ctx context.Context, id string) error {
	r, err := m.record(id)
	if err != nil {
		return fmt.Errorf("focus side %s: %w", id, err)
	}
	sess, err := m.session(ctx, r)
	if err != nil {
		return fmt.Errorf("focus side %s: %w", id, err)
	}
	return sess.Focus(ctx)
}

// Close closes side session id (SPEC §6.10): its pane, unless its record
// names a pane igris did not start for it; then its record and files. Its
// own worktree is removed when `git status --porcelain` is empty (the
// branch stays) and kept otherwise. Closing a session whose pane is gone
// already only forgets it. ok is false when another igris ended it first.
func (m *Manager) Close(ctx context.Context, id string) (e Ended, ok bool, err error) {
	r, err := m.record(id)
	if err != nil {
		return Ended{}, false, fmt.Errorf("close side %s: %w", id, err)
	}
	foreign := false
	if r.Backend != m.o.Backend.Name() {
		return Ended{}, false, fmt.Errorf("close side %s: it runs in %s, and igris uses %s now; close it from igris in %s", id, r.Backend, m.o.Backend.Name(), r.Backend)
	}
	sess, err := m.session(ctx, r)
	switch {
	case errors.Is(err, backend.ErrSessionGone):
		foreign = errors.Is(err, backend.ErrSessionForeign)
	case err != nil:
		return Ended{}, false, fmt.Errorf("close side %s: %w", id, err)
	default:
		if err := sess.Close(ctx); err != nil {
			return Ended{}, false, fmt.Errorf("close side %s: %w", id, err)
		}
	}
	e, ok = m.end(id, foreign)
	if !ok {
		return Ended{}, false, nil
	}
	e.Closed = true
	if wt := e.Worktree; wt != nil {
		out, err := m.git(ctx, filepath.Join(m.o.Root, wt.Path), "status", "--porcelain")
		if err == nil && strings.TrimSpace(out) == "" {
			if _, err := m.git(ctx, m.o.Root, "worktree", "remove", wt.Path); err == nil {
				e.Worktree, e.WorktreeRemoved = nil, true
			}
		}
	}
	return e, true, nil
}

// git runs git in dir and returns its output; a non-zero exit is an error.
func (m *Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := m.o.Runner.Run(ctx, runner.Cmd{Name: "git", Args: args, Dir: dir, Env: m.o.GitEnv, Timeout: gitTimeout})
	if err == nil {
		err = res.Err()
	}
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}

// isDir reports whether path is a directory itself, not a symlink to one
// a session put in its place.
func isDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}
