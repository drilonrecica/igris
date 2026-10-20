package side

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/prompt"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

var at = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

// world is a project with a fake backend and a scripted git, in which
// side IDs and Claude sessions are handed out in order.
type world struct {
	t    *testing.T
	root string
	be   *fake.Backend
	git  *runner.Fake
	n    int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, root: t.TempDir(), be: fake.New(), git: &runner.Fake{}}
	// worktree add makes the directory, as git would.
	w.git.Func(func(c runner.Cmd) (runner.Result, error) {
		if len(c.Args) > 5 && c.Args[0] == "worktree" && c.Args[1] == "add" {
			if err := os.MkdirAll(filepath.Join(c.Dir, c.Args[4]), 0o750); err != nil {
				return runner.Result{}, err
			}
		}
		return runner.Result{}, nil
	})
	if err := os.MkdirAll(filepath.Join(w.root, state.DirName), 0o700); err != nil {
		t.Fatal(err)
	}
	return w
}

// manager returns a new Manager: a new igris on the same project.
func (w *world) manager() *Manager {
	return New(Options{
		Root: w.root, Backend: w.be, Runner: w.git, IgrisPath: "/usr/bin/igris",
		Now: func() time.Time { return at },
		NewID: func() (string, error) {
			w.n++
			return fmt.Sprintf("a%05x", w.n), nil
		},
		NewUUID: func() (string, error) {
			return fmt.Sprintf("7c1d2e3f-4a5b-4c6d-8e7f-%012x", w.n), nil
		},
	})
}

func blank() Spec {
	return Spec{Preset: state.SidePresetBlank, Rank: "sonnet", Model: "sonnet", Mode: "default"}
}

func (w *world) open(m *Manager, s Spec) Entry {
	w.t.Helper()
	e, err := m.Open(context.Background(), s)
	if err != nil {
		w.t.Fatalf("Open: %v", err)
	}
	return e
}

func (w *world) gitCalls() [][]string {
	var out [][]string
	for _, c := range w.git.Calls() {
		out = append(out, c.Args)
	}
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A side session opens through OpenSession with its side ID and no task,
// IGRIS_SIDE, its own rules and hooks files, and is recorded; nothing of a
// task's is touched (SPEC §6.10).
func TestOpen(t *testing.T) {
	w := newWorld(t)
	w.be.SetAutoSignal(func(context.Context, string) error {
		t.Error("a side session's prompt triggered the task auto-signal")
		return nil
	})
	s := blank()
	s.Preset, s.Task, s.Rank, s.Model, s.Mode = state.SidePresetTask, "M1-03", "opus", "claude-opus-5-5", "accept"
	s.ExtraArgs = []string{"--verbose"}
	s.Prompt = "About M1-03\x1b[31m\nQuestion: why?"
	e := w.open(w.manager(), s)

	id, uuid := "a00001", "7c1d2e3f-4a5b-4c6d-8e7f-000000000001"
	rules, hooks := filepath.Join(w.root, ".igris/side", id+".rules.md"), filepath.Join(w.root, ".igris/side", id+".settings.json")
	opened := w.be.Opened()
	if len(opened) != 1 {
		t.Fatalf("opened %d sessions", len(opened))
	}
	spec := opened[0]
	wantArgs := []string{"--model", "claude-opus-5-5", "--session-id", uuid, "--permission-mode", "acceptEdits",
		"--append-system-prompt-file", rules, "--settings", hooks, "--verbose"}
	if spec.Side != id || spec.TaskID != "" || spec.Dir != w.root || spec.Label != "side · M1-03 · opus" ||
		spec.ClaudeSession != uuid || !slices.Equal(spec.Env, []string{"IGRIS_SIDE=" + id}) || !slices.Equal(spec.Args, wantArgs) {
		t.Errorf("spec %+v\nwant args %q", spec, wantArgs)
	}
	if got := w.be.Prompts(fake.SideKey(id)); !slices.Equal(got, []string{"About M1-03\nQuestion: why?"}) {
		t.Errorf("prompts %q", got)
	}
	want := state.SideRecord{V: 1, ID: id, At: at, Backend: "fake", Ref: e.Session.Ref(), ClaudeSession: uuid,
		Model: "claude-opus-5-5", Rank: "opus", Mode: "accept", Preset: "task", Task: "M1-03", Dir: "."}
	if e.Record != want {
		t.Errorf("entry record %+v, want %+v", e.Record, want)
	}
	recs, bad, err := state.ReadSideRecords(w.root)
	if err != nil || len(bad) != 0 || len(recs) != 1 || recs[0] != want {
		t.Errorf("recorded %+v, %v, %v", recs, bad, err)
	}
	if got := read(t, rules); got != prompt.SideRules("") {
		t.Errorf("rules:\n%s", got)
	}
	settings := read(t, hooks)
	for _, bad := range []string{"plan-approve", "statusLine", "permissions"} {
		if strings.Contains(settings, bad) {
			t.Errorf("hooks file has %s:\n%s", bad, settings)
		}
	}
	if !strings.Contains(settings, "'/usr/bin/igris' hook --root '"+w.root+"' Stop") {
		t.Errorf("hooks file lacks the state hooks:\n%s", settings)
	}
	// Not a task: no state.json, no signal, no task rules or hooks.
	for _, p := range []string{"state.json", "signals", "prompts", "hooks", "runs.jsonl"} {
		if _, err := os.Stat(filepath.Join(w.root, state.DirName, p)); !os.IsNotExist(err) {
			t.Errorf(".igris/%s exists after opening a side session", p)
		}
	}
	if len(w.git.Calls()) != 0 {
		t.Errorf("git ran: %v", w.gitCalls())
	}
}

// During a run in the shared tree the rules name the running task; a
// session in its own worktree shares no files with it and may read the
// plan (SPEC §6.10).
func TestOpenSharedTreeAndWorktree(t *testing.T) {
	w := newWorld(t)
	m := w.manager()
	s := blank()
	s.RunTask = "M1-04"
	shared := w.open(m, s)
	if got := read(t, state.SideRulesPath(w.root, shared.Record.ID)); got != prompt.SideRules("M1-04") {
		t.Errorf("shared-tree rules:\n%s", got)
	}

	s.Worktree, s.PlanPath = true, "/work/demo/imp-docs/tasks.md"
	own := w.open(m, s)
	id := own.Record.ID
	wt := state.SideWorktree(id)
	if *own.Record.Worktree != *wt || own.Record.Dir != ".igris/worktrees/side-"+id {
		t.Errorf("record %+v", own.Record)
	}
	if got := w.gitCalls(); !slices.EqualFunc(got, [][]string{{"worktree", "add", "-b", "igris/side-" + id, ".igris/worktrees/side-" + id, "HEAD"}}, slices.Equal) {
		t.Errorf("git %q", got)
	}
	if c := w.git.Calls()[0]; c.Dir != w.root {
		t.Errorf("worktree add ran in %s", c.Dir)
	}
	if spec := w.be.Opened()[1]; spec.Dir != filepath.Join(w.root, ".igris/worktrees/side-"+id) {
		t.Errorf("session dir %s", spec.Dir)
	}
	if got := read(t, state.SideRulesPath(w.root, id)); got != prompt.SideRules("") {
		t.Errorf("own-worktree rules name the run's task:\n%s", got)
	}
	if got := read(t, state.SideHooksPath(w.root, id)); !strings.Contains(got, `"Read(//work/demo/imp-docs/tasks.md)"`) {
		t.Errorf("own-worktree hooks lack the plan's Read rule:\n%s", got)
	}

	// A plan path with pattern characters gets no rule.
	s.PlanPath = "/work/[x]/tasks.md"
	odd := w.open(m, s)
	if got := read(t, state.SideHooksPath(w.root, odd.Record.ID)); strings.Contains(got, "permissions") {
		t.Errorf("hooks have a rule for a pattern path:\n%s", got)
	}
}

func TestOpenRefuses(t *testing.T) {
	tests := map[string]func(s *Spec){
		"yolo":             func(s *Spec) { s.Mode = "yolo" },
		"plan:yolo":        func(s *Spec) { s.Mode = "plan:yolo" },
		"plan:auto":        func(s *Spec) { s.Mode = "plan:auto" },
		"task without id":  func(s *Spec) { s.Preset = state.SidePresetTask },
		"id without task":  func(s *Spec) { s.Task = "M1-01" },
		"bad task":         func(s *Spec) { s.Preset, s.Task = state.SidePresetTask, "-rf" },
		"preset":           func(s *Spec) { s.Preset = "x" },
		"no model":         func(s *Spec) { s.Model = "" },
		"bad run task":     func(s *Spec) { s.RunTask = "a b" },
		"relative plan":    func(s *Spec) { s.Worktree, s.PlanPath = true, "tasks.md" },
		"forbidden extra":  func(s *Spec) { s.ExtraArgs = []string{"--dangerously-skip-permissions"} },
		"model as a flag":  func(s *Spec) { s.Model = "--help" },
		"extra permission": func(s *Spec) { s.ExtraArgs = []string{"--permission-mode", "bypassPermissions"} },
	}
	for name, mutate := range tests {
		w := newWorld(t)
		s := blank()
		mutate(&s)
		if _, err := w.manager().Open(context.Background(), s); err == nil {
			t.Errorf("%s: opened", name)
		}
		if len(w.be.Opened()) != 0 {
			t.Errorf("%s: a session opened", name)
		}
		if entries, _ := os.ReadDir(state.SideDir(w.root)); len(entries) != 0 {
			t.Errorf("%s: left %v", name, entries)
		}
	}
}

// A failed open leaves nothing behind: no files, and its fresh worktree
// and branch removed.
func TestOpenFailsClean(t *testing.T) {
	w := newWorld(t)
	w.be.FailOpen(errors.New("no claude"))
	s := blank()
	s.Worktree, s.PlanPath = true, "/p/tasks.md"
	if _, err := w.manager().Open(context.Background(), s); err == nil || !strings.Contains(err.Error(), "no claude") {
		t.Fatalf("Open = %v", err)
	}
	if entries, _ := os.ReadDir(state.SideDir(w.root)); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
	want := [][]string{
		{"worktree", "add", "-b", "igris/side-a00001", ".igris/worktrees/side-a00001", "HEAD"},
		{"worktree", "remove", "--force", ".igris/worktrees/side-a00001"},
		{"branch", "-D", "igris/side-a00001"},
	}
	if got := w.gitCalls(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("git %q", got)
	}
}

func TestOpenAtMostMaxOpen(t *testing.T) {
	w := newWorld(t)
	m := w.manager()
	for range MaxOpen {
		w.open(m, blank())
	}
	if _, err := m.Open(context.Background(), blank()); err == nil || !strings.Contains(err.Error(), "close one first") {
		t.Errorf("Open number %d = %v", MaxOpen+1, err)
	}
	if n := len(w.be.Opened()); n != MaxOpen {
		t.Errorf("%d sessions opened", n)
	}
}

// A restarted igris finds its side sessions again, bound to their own
// panes; quitting never closed them (SPEC §6.10).
func TestRefreshReattaches(t *testing.T) {
	w := newWorld(t)
	first := w.open(w.manager(), blank())
	second := w.open(w.manager(), blank())

	m := w.manager() // igris restarted
	sync, err := m.Refresh(context.Background())
	if err != nil || len(sync.Ended) != 0 || len(sync.Warnings) != 0 || len(sync.Open) != 2 {
		t.Fatalf("Refresh = %+v, %v", sync, err)
	}
	if sync.Open[0].Record != first.Record || sync.Open[1].Record != second.Record || sync.Open[0].Session.Ref() != first.Session.Ref() {
		t.Errorf("open %+v", sync.Open)
	}
	if err := m.Focus(context.Background(), first.Record.ID); err != nil {
		t.Fatal(err)
	}
	if got := w.be.Focused(); len(got) != 1 || got[0] != first.Session.Ref() {
		t.Errorf("focused %v", got)
	}
	if len(w.be.Closed()) != 0 {
		t.Errorf("closed %v", w.be.Closed())
	}
	if err := m.Focus(context.Background(), "ffffff"); err == nil {
		t.Error("focused a side session that isn't open")
	}
}

// A gone side session ends once: of two igris processes that find it
// gone, the one that claims its record reports it, with its worktree kept
// (SPEC §6.10).
func TestRefreshEndsGoneSessions(t *testing.T) {
	w := newWorld(t)
	s := blank()
	s.Worktree, s.PlanPath = true, "/p/tasks.md"
	e := w.open(w.manager(), s)
	run, home := w.manager(), w.manager()
	if sync, _ := run.Refresh(context.Background()); len(sync.Open) != 1 {
		t.Fatalf("not reattached: %+v", sync)
	}
	w.be.Kill(e.Session.Ref())

	a, err := run.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := home.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Ended) != 1 || len(b.Ended) != 0 || len(a.Open)+len(b.Open) != 0 {
		t.Fatalf("run %+v, home %+v; want one Ended", a, b)
	}
	end := a.Ended[0]
	if !reflect.DeepEqual(end.Record, e.Record) || end.Closed || end.Foreign || end.Worktree == nil || *end.Worktree != *state.SideWorktree(e.Record.ID) {
		t.Errorf("ended %+v", end)
	}
	if entries, _ := os.ReadDir(state.SideDir(w.root)); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
	if again, _ := run.Refresh(context.Background()); len(again.Ended)+len(again.Open) != 0 {
		t.Errorf("ended twice: %+v", again)
	}
}

// A record that names a pane igris did not start for its side session (a
// session rewrote it) ends without that pane being touched (SPEC §6.10).
func TestRefreshForeignPane(t *testing.T) {
	w := newWorld(t)
	task, err := w.be.OpenSession(context.Background(), backend.SessionSpec{TaskID: "M1-03"})
	if err != nil {
		t.Fatal(err)
	}
	e := w.open(w.manager(), blank())
	r := e.Record
	r.Ref.PaneID = task.Ref().PaneID
	if err := state.WriteSideRecord(w.root, r); err != nil {
		t.Fatal(err)
	}
	sync, err := w.manager().Refresh(context.Background())
	if err != nil || len(sync.Ended) != 1 || !sync.Ended[0].Foreign || len(sync.Warnings) != 1 || !strings.Contains(sync.Warnings[0], "did not start") {
		t.Fatalf("Refresh = %+v, %v", sync, err)
	}
	if st, _ := task.State(context.Background()); st == backend.Exited || len(w.be.Closed()) != 0 || len(w.be.Focused()) != 0 {
		t.Errorf("the task's pane was touched: %s, closed %v", st, w.be.Closed())
	}
}

// Records igris can't use: a damaged one is forgotten with a warning, a
// newer igris's left alone, another backend's left for igris there.
func TestRefreshProblemRecords(t *testing.T) {
	w := newWorld(t)
	e := w.open(w.manager(), blank())
	other := e.Record
	other.ID, other.Backend, other.Ref.Backend = "b00001", "tmux", "tmux"
	if err := state.WriteSideRecord(w.root, other); err != nil {
		t.Fatal(err)
	}
	dir := state.SideDir(w.root)
	if err := os.WriteFile(filepath.Join(dir, "c00001.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "d00001.json"), []byte(`{"v":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sync, err := w.manager().Refresh(context.Background())
	if err != nil || len(sync.Open) != 1 || len(sync.Ended) != 0 || len(sync.Warnings) != 3 {
		t.Fatalf("Refresh = %+v, %v", sync, err)
	}
	for _, want := range []string{"side d00001 was recorded by a newer igris", "side c00001: its record is damaged", "side b00001 runs in tmux, and igris uses fake now"} {
		if !slices.ContainsFunc(sync.Warnings, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("warnings %q lack %q", sync.Warnings, want)
		}
	}
	for name, want := range map[string]bool{"b00001.json": true, "c00001.json": false, "d00001.json": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s: exists %v, want %v", name, err == nil, want)
		}
	}
	if _, _, err := w.manager().Close(context.Background(), "b00001"); err == nil {
		t.Error("closed another backend's side session")
	}
}

// Closing closes the pane and forgets the session; a clean worktree is
// removed (the branch stays), a dirty one kept and named (SPEC §6.10).
func TestClose(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	s := blank()
	s.Worktree, s.PlanPath = true, "/p/tasks.md"
	m := w.manager()
	clean, dirty, plain := w.open(m, s), w.open(m, s), w.open(m, blank())
	w.git.On([]string{"git", "status", "--porcelain"}, runner.Result{}, nil)
	w.git.On([]string{"git", "worktree", "remove"}, runner.Result{}, nil)
	w.git.On([]string{"git", "status", "--porcelain"}, runner.Result{Stdout: []byte(" M main.go\n")}, nil)

	restarted := w.manager()
	e, ok, err := restarted.Close(ctx, clean.Record.ID)
	if err != nil || !ok || !e.Closed || e.Worktree != nil || !e.WorktreeRemoved || !reflect.DeepEqual(e.Record, clean.Record) {
		t.Errorf("Close(clean) = %+v, %v, %v", e, ok, err)
	}
	e, ok, err = restarted.Close(ctx, dirty.Record.ID)
	if err != nil || !ok || e.WorktreeRemoved || e.Worktree == nil || *e.Worktree != *state.SideWorktree(dirty.Record.ID) {
		t.Errorf("Close(dirty) = %+v, %v, %v", e, ok, err)
	}
	e, ok, err = m.Close(ctx, plain.Record.ID)
	if err != nil || !ok || e.Worktree != nil || e.WorktreeRemoved {
		t.Errorf("Close(plain) = %+v, %v, %v", e, ok, err)
	}
	if got := w.be.Closed(); len(got) != 3 || got[0] != clean.Session.Ref() || got[2] != plain.Session.Ref() {
		t.Errorf("closed %v", got)
	}
	cid := clean.Record.ID
	wantGit := [][]string{
		{"status", "--porcelain"},
		{"worktree", "remove", ".igris/worktrees/side-" + cid},
		{"status", "--porcelain"},
	}
	if got := w.gitCalls()[2:]; !slices.EqualFunc(got, wantGit, slices.Equal) {
		t.Errorf("git %q", got)
	}
	if c := w.git.Calls()[2]; c.Dir != filepath.Join(w.root, ".igris/worktrees/side-"+cid) {
		t.Errorf("status ran in %s", c.Dir)
	}
	if entries, _ := os.ReadDir(state.SideDir(w.root)); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
	if _, _, err := m.Close(ctx, plain.Record.ID); err == nil {
		t.Error("closed a closed side session")
	}
	if sync, _ := m.Refresh(ctx); len(sync.Open)+len(sync.Ended) != 0 {
		t.Errorf("after closing: %+v", sync)
	}
}

// Closing a side session whose pane is gone already only forgets it.
func TestCloseGone(t *testing.T) {
	w := newWorld(t)
	e := w.open(w.manager(), blank())
	w.be.Kill(e.Session.Ref())
	end, ok, err := w.manager().Close(context.Background(), e.Record.ID)
	if err != nil || !ok || !end.Closed {
		t.Errorf("Close = %+v, %v, %v", end, ok, err)
	}
	if len(w.be.Closed()) != 0 {
		t.Errorf("closed %v", w.be.Closed())
	}
}

func TestCleanPrompt(t *testing.T) {
	long := strings.Repeat("é", MaxPrompt) // 2 bytes each
	tests := map[string]string{
		"plain":                          "plain",
		"a\x1b[31mb\x07c\nd":             "abc\nd",
		long:                             strings.Repeat("é", MaxPrompt/2),
		strings.Repeat("x", MaxPrompt+5): strings.Repeat("x", MaxPrompt),
	}
	for in, want := range tests {
		if got := CleanPrompt(in); got != want {
			t.Errorf("CleanPrompt(%.20q…) = %.20q… (%d bytes), want %d bytes", in, got, len(got), len(want))
		}
	}
}

func TestLabel(t *testing.T) {
	for _, tt := range []struct{ preset, task, want string }{
		{state.SidePresetBlank, "", "side · opus"},
		{state.SidePresetTask, "M1-03", "side · M1-03 · opus"},
		{state.SidePresetIgris, "", "side · igris · opus"},
	} {
		if got := Label(tt.preset, tt.task, "opus"); got != tt.want {
			t.Errorf("Label(%s) = %q, want %q", tt.preset, got, tt.want)
		}
	}
}
