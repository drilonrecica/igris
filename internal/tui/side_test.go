package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/report"
	"github.com/drilonrecica/igris/internal/side"
	"github.com/drilonrecica/igris/internal/state"
)

// fakeSide is a SideSessions for tests: List returns rows, Open records
// its request and adds a row. Safe from the program's goroutines.
type fakeSide struct {
	mu       sync.Mutex
	setup    SideSetup
	setupErr error
	prefill  string
	rows     []SideRow
	lines    []string // List's lines, every time
	openErr  error
	openID   string // "" is "c0ffee"
	opened   []SideRequest
	prefills []SidePrefill
	focused  []string
	closed   []string
	lists    int
}

func newFakeSide() *fakeSide {
	return &fakeSide{setup: SideSetup{
		Ranks: []SideRank{{"sonnet", "sonnet"}, {"opus", "claude-opus-5-5"}},
		Mode:  "accept",
	}, prefill: "about M0-03\n\nQuestion: "}
}

func (f *fakeSide) Setup(context.Context) (SideSetup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setup, f.setupErr
}

func (f *fakeSide) List(context.Context) (SideList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	return SideList{Rows: append([]SideRow(nil), f.rows...), Lines: f.lines}, nil
}

func (f *fakeSide) Prefill(_ context.Context, req SidePrefill) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prefills = append(f.prefills, req)
	return f.prefill, nil
}

func (f *fakeSide) Open(_ context.Context, req SideRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, req)
	if f.openErr != nil {
		return "", f.openErr
	}
	id := f.openID
	if id == "" {
		id = "c0ffee"
	}
	f.rows = append(f.rows, SideRow{ID: id, State: "idle", At: t0, Rank: req.Rank, Mode: req.Mode})
	return id, nil
}

func (f *fakeSide) Focus(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focused = append(f.focused, id)
	return nil
}

func (f *fakeSide) Close(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, id)
	for i, r := range f.rows {
		if r.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			break
		}
	}
	return "side " + id + " closed", nil
}

// sideHarness is a run view with side sessions; pump feeds the side
// calls' results back, as the program would.
func sideHarness(t *testing.T, fs *fakeSide) *harness {
	t.Helper()
	hs := newHarness(t, 120, 40)
	hs.m.opts.Side = fs
	hs.m.side = newSideUI(context.Background(), fs, func() time.Time { return hs.now }, nil)
	pump(hs.m, hs.m.side.list())
	return hs
}

// pump runs cmd and hands each side result back to m until none is left;
// a batch's commands are pumped in turn, other messages dropped.
func pump(m tea.Model, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if o, ok := msg.(ownedMsg); ok {
			msg = o.msg
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				pump(m, c)
			}
			return
		}
		if _, ok := msg.(sideMsg); !ok {
			return
		}
		_, cmd = m.Update(msg)
	}
}

// press presses k on the run view and pumps what it starts.
func (hs *harness) press(ks ...string) {
	for _, k := range ks {
		_, cmd := hs.m.Update(keyMsg(k))
		pump(hs.m, cmd)
	}
}

func (hs *harness) dialogTitle() string {
	if hs.m.dialog == nil {
		return ""
	}
	return hs.m.dialog.title
}

func optionLabels(d *dialog) []string {
	var out []string
	for _, o := range d.options {
		out = append(out, o.label)
	}
	return out
}

// During a run in the shared tree: About this task names the current task,
// the mode preselects default whatever [side] mode says, never yolo, Where
// warns about the task, and the shared tree tells the session about it.
func TestSideDialogDuringRun(t *testing.T) {
	fs := newFakeSide()
	hs := sideHarness(t, fs)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0"}, started("M0-03"), opened("M0-03"))

	hs.press("S")
	if hs.dialogTitle() != "Side session" {
		t.Fatalf("S with none open: %q", hs.dialogTitle())
	}
	if got := optionLabels(hs.m.dialog); len(got) != 3 || !strings.Contains(got[1], "About this task — M0-03") {
		t.Fatalf("presets: %q", got)
	}
	hs.press("2") // About this task
	if d := hs.m.dialog; hs.dialogTitle() != "First prompt" || d.input.value != fs.prefill || !d.inField {
		t.Fatalf("after the preset: %q %+v", hs.dialogTitle(), d.input)
	}
	if p := fs.prefills; len(p) != 1 || p[0].Preset != state.SidePresetTask || p[0].Task != "M0-03" || !p[0].Running {
		t.Errorf("prefill request: %+v", p)
	}
	if !strings.Contains(hs.m.View(), "Question:") {
		t.Errorf("the prompt's text isn't shown:\n%s", hs.m.View())
	}
	hs.press("w", "h", "y", "?", "enter")
	if hs.dialogTitle() != "Model" || optionLabels(hs.m.dialog)[1] != "opus → claude-opus-5-5" {
		t.Fatalf("model step: %q %q", hs.dialogTitle(), optionLabels(hs.m.dialog))
	}
	hs.press("enter")
	modes := optionLabels(hs.m.dialog)
	if hs.dialogTitle() != "Mode" || !strings.HasPrefix(modes[0], "manual") || len(modes) != 5 {
		t.Fatalf("mode step: %q %q", hs.dialogTitle(), modes)
	}
	for _, l := range modes {
		if strings.Contains(l, "yolo") || strings.Contains(l, "plan:") || strings.Contains(l, "SKIP") {
			t.Errorf("a side session is offered %q", l)
		}
	}
	hs.press("enter")
	if hs.dialogTitle() != "Where" || !strings.Contains(hs.m.dialog.detail, "M0-03 is working in this tree; a plan edit holds the run") {
		t.Fatalf("where step: %q %q", hs.dialogTitle(), hs.m.dialog.detail)
	}
	hs.press("enter") // Shared tree
	if hs.dialogTitle() != "Open a side session?" || !strings.Contains(hs.m.dialog.detail, "M0-03 is working in this tree") {
		t.Fatalf("summary: %q %q", hs.dialogTitle(), hs.m.dialog.detail)
	}
	hs.press("enter")
	if hs.m.dialog != nil {
		t.Fatalf("still open: %q", hs.dialogTitle())
	}
	want := SideRequest{Preset: state.SidePresetTask, Task: "M0-03", Rank: "sonnet", Mode: "manual", Prompt: fs.prefill + "why?", RunTask: "M0-03"}
	if len(fs.opened) != 1 || fs.opened[0] != want {
		t.Fatalf("opened %+v, want %+v", fs.opened, want)
	}
	view := hs.m.View()
	if !strings.Contains(view, "side c0ffee opened") || !strings.Contains(view, "· side 1") {
		t.Errorf("no log line or header count:\n%s", view)
	}
}

// The dialog names where the session opens by the run's backend: a view
// pushed by home has the config's `auto` and an attached one none, so the
// served run's snapshot says (the V17 gate saw "in a herdr tab" on tmux).
func TestSideDialogNamesTheRunsBackend(t *testing.T) {
	for _, tt := range []struct{ opts, served, want string }{
		{"auto", "tmux", "in a tmux window"},
		{"", "tmux", "in a tmux window"},
		{"auto", "herdr", "in a herdr tab"},
		{"tmux", "", "in a tmux window"},
	} {
		hs := sideHarness(t, newFakeSide())
		hs.m.opts.Backend, hs.m.backend = tt.opts, tt.served
		hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0"}, started("M0-03"), opened("M0-03"))
		hs.press("S")
		if hs.dialogTitle() != "Side session" || !strings.Contains(strings.Join(strings.Fields(hs.m.dialog.detail), " "), tt.want) {
			t.Errorf("backend %q, served %q: %q", tt.opts, tt.served, hs.m.dialog.detail)
		}
	}
}

// Own worktree tells the session about no task; in a worktree run only the
// plan warning is given and the mode isn't forced to manual.
func TestSideDialogWorktrees(t *testing.T) {
	fs := newFakeSide()
	hs := sideHarness(t, fs)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0"}, started("M0-03"), opened("M0-03"))
	hs.press("S", "1", "enter", "enter", "enter", "down", "enter", "enter")
	if len(fs.opened) != 1 || !fs.opened[0].Worktree || fs.opened[0].RunTask != "" || fs.opened[0].Mode != "manual" {
		t.Fatalf("own worktree: %+v", fs.opened)
	}

	fs2 := newFakeSide()
	hs2 := sideHarness(t, fs2)
	hs2.events(engine.Event{Kind: engine.RunStarted, Phase: "M0"}, engine.Event{Kind: engine.WorktreeReady}, started("M0-03"), opened("M0-03"))
	hs2.press("S", "1", "enter", "enter")
	if modes := optionLabels(hs2.m.dialog); !strings.HasPrefix(modes[0], "accept") {
		t.Errorf("a worktree run's modes: %q", modes)
	}
	hs2.press("enter")
	if d := hs2.m.dialog.detail; d != "The run works in its own worktree; a plan edit holds the run." {
		t.Errorf("worktree run warning: %q", d)
	}
	hs2.press("enter", "enter")
	if len(fs2.opened) != 1 || fs2.opened[0].RunTask != "" || fs2.opened[0].Worktree {
		t.Errorf("worktree run, shared tree: %+v", fs2.opened)
	}
}

// Without a run there is no Where step, the mode is [side] mode's, and a
// blank session has no first prompt.
func TestSideDialogNoRun(t *testing.T) {
	fs := newFakeSide()
	hs := sideHarness(t, fs)
	hs.events(engine.Event{Kind: engine.RunStopped, Detail: "done"})
	hs.press("S", "1")
	if hs.dialogTitle() != "First prompt" || hs.m.dialog.input.value != "" || len(fs.prefills) != 0 {
		t.Fatalf("blank: %q %+v %+v", hs.dialogTitle(), hs.m.dialog.input, fs.prefills)
	}
	if !strings.Contains(hs.m.View(), "Empty: the session opens at its input box") {
		t.Errorf("the empty prompt isn't explained:\n%s", hs.m.View())
	}
	hs.press("enter", "down", "enter", "enter")
	if hs.dialogTitle() != "Open a side session?" {
		t.Fatalf("no run: %q", hs.dialogTitle())
	}
	hs.press("enter")
	want := SideRequest{Preset: state.SidePresetBlank, Rank: "opus", Mode: "accept"}
	if len(fs.opened) != 1 || fs.opened[0] != want {
		t.Fatalf("opened %+v, want %+v", fs.opened, want)
	}
}

// The first prompt is cleaned of escape sequences and refused past 8 KiB.
func TestSidePromptCleanedAndBounded(t *testing.T) {
	fs := newFakeSide()
	fs.prefill = strings.Repeat("x", side.MaxPrompt-2)
	hs := sideHarness(t, fs)
	hs.press("S", "2")
	hs.press("\x1b[31m", "abc", "enter")
	if hs.dialogTitle() != "First prompt" || !strings.Contains(hs.m.dialog.input.hint, "at most 8 KiB") {
		t.Fatalf("over 8 KiB: %q %q", hs.dialogTitle(), hs.m.dialog.input.hint)
	}
	hs.press("backspace", "backspace", "backspace", "enter")
	if hs.dialogTitle() != "Model" {
		t.Fatalf("shortened: %q %q", hs.dialogTitle(), hs.m.dialog.input.hint)
	}
	hs.press("enter", "enter", "enter", "enter") // model, mode, where, open
	if len(fs.opened) != 1 || strings.ContainsRune(fs.opened[0].Prompt, 0x1b) || len(fs.opened[0].Prompt) > side.MaxPrompt {
		t.Fatalf("prompt not cleaned: %q", fs.opened[0].Prompt[len(fs.opened[0].Prompt)-10:])
	}
}

// With sessions open, S opens the page: o focuses one, x asks before
// closing it, Keep it goes back, Close session closes it.
func TestSidePage(t *testing.T) {
	fs := newFakeSide()
	fs.rows = []SideRow{
		{ID: "a3f2c1", State: "working", At: t0.Add(-12 * time.Minute), Subject: "M0-03", Rank: "sonnet", Mode: "default", Worktree: true},
		{ID: "b4e3d2", State: "idle", At: t0.Add(-2 * time.Hour), Rank: "opus", Mode: "plan"},
	}
	hs := sideHarness(t, fs)
	if !strings.Contains(hs.m.View(), "· side 2") {
		t.Errorf("header without the count:\n%s", hs.m.View())
	}
	hs.press("S")
	labels := optionLabels(hs.m.dialog)
	if hs.dialogTitle() != "Side sessions · 2 open" || labels[0] != "side a3f2c1 · working · 12m · M0-03 · sonnet · default · worktree" || labels[1] != "side b4e3d2 · idle · 2h · opus · plan" {
		t.Fatalf("page: %q %q", hs.dialogTitle(), labels)
	}
	hs.press("down", "o")
	if hs.m.dialog != nil || len(fs.focused) != 1 || fs.focused[0] != "b4e3d2" {
		t.Fatalf("o: %q focused %v", hs.dialogTitle(), fs.focused)
	}
	hs.press("S", "x")
	if hs.dialogTitle() != "Close side a3f2c1?" || optionLabels(hs.m.dialog)[0] != "Keep it" {
		t.Fatalf("x: %q %q", hs.dialogTitle(), optionLabels(hs.m.dialog))
	}
	hs.press("enter") // Keep it
	if hs.dialogTitle() != "Side sessions · 2 open" || len(fs.closed) != 0 {
		t.Fatalf("keep it: %q closed %v", hs.dialogTitle(), fs.closed)
	}
	hs.press("x", "2")
	if len(fs.closed) != 1 || fs.closed[0] != "a3f2c1" {
		t.Fatalf("closed %v", fs.closed)
	}
	if hs.dialogTitle() != "Side sessions · 1 open" || hs.m.log[len(hs.m.log)-1].text != "side a3f2c1 closed" {
		t.Errorf("after close: %q, log %+v", hs.dialogTitle(), hs.m.log)
	}
	hs.press("enter") // the remaining row: focus
	if len(fs.focused) != 2 || fs.focused[1] != "b4e3d2" {
		t.Errorf("enter on a row: %v", fs.focused)
	}
	hs.press("S", "2", "1") // New… then Blank
	if hs.dialogTitle() != "First prompt" {
		t.Errorf("New…: %q", hs.dialogTitle())
	}
}

// An engine question takes the dialog slot; the side flow's late answer
// doesn't take it back.
func TestSideDialogGivesWayToQuestions(t *testing.T) {
	fs := newFakeSide()
	hs := sideHarness(t, fs)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M0"}, started("M0-03"), opened("M0-03"))
	hs.press("S")
	_, cmd := hs.m.Update(keyMsg("1"))
	hs.events(engine.Event{Kind: engine.Asked, Task: "M0-03", Question: engine.QuestionCommit})
	pump(hs.m, cmd)
	if hs.dialogTitle() != "Commit M0-03?" {
		t.Fatalf("the question lost its dialog to %q", hs.dialogTitle())
	}
	// A side dialog is not the run's: the run's end leaves it open.
	hs.m.dialog = nil
	hs.press("S")
	hs.events(engine.Event{Kind: engine.RunStopped, Detail: "stopped"})
	if hs.dialogTitle() != "Side session" {
		t.Errorf("the run's end closed the side dialog")
	}
}

// Setup's error ends in a dialog that says why; lines from List are given
// once.
func TestSideErrorsAndLines(t *testing.T) {
	fs := newFakeSide()
	fs.setupErr = errors.New("igris.toml is invalid; fix it first")
	fs.lines = []string{"side a3f2c1 ended; its worktree .igris/worktrees/side-a3f2c1 is kept"}
	hs := sideHarness(t, fs)
	hs.press("S", "1")
	if hs.dialogTitle() != "The side session did not open" || !strings.Contains(hs.m.dialog.detail, "igris.toml is invalid") {
		t.Fatalf("setup error: %q %q", hs.dialogTitle(), hs.m.dialog.detail)
	}
	n := 0
	for _, l := range hs.m.log {
		if strings.Contains(l.text, "side a3f2c1 ended") {
			n++
		}
	}
	if n != 1 || fs.lists < 2 {
		t.Errorf("the ended line was logged %d times over %d lists", n, fs.lists)
	}
}

// Home: S and the button need a reachable backend or an open session; the
// right column says how many are open and opens the page; a read-only
// home's dialog asks Where, from the run Setup reports.
func TestSideHome(t *testing.T) {
	fs := newFakeSide()
	svc := &fakeServices{side: fs}
	m := newHome(context.Background(), svc, &theme{})
	m.now = func() time.Time { return homeNow }
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	snap := homeSnap("ready")
	_, cmd := m.Update(snapshotMsg{snap, nil})
	pump(m, cmd) // home's refresh lists the side sessions
	m.Update(backendMsg{errors.New("not inside herdr")})
	if m.offers(actSide) {
		t.Error("Side… offered without a backend or a session")
	}
	m.Update(backendMsg{nil})
	if !m.offers(actSide) {
		t.Fatal("Side… not offered")
	}
	fs.setup.Run = &SideRun{Task: "M2-04"}
	_, cmd = m.Update(keyMsg("S"))
	pump(m, cmd)
	if m.dialog == nil || m.dialog.title != "Side session" {
		t.Fatalf("S: %+v", m.dialog)
	}
	for _, k := range []string{"1", "enter", "enter", "enter"} {
		_, cmd := m.Update(keyMsg(k))
		pump(m, cmd)
	}
	if m.dialog == nil || m.dialog.title != "Where" || !strings.Contains(m.dialog.detail, "M2-04 is working in this tree") {
		t.Fatalf("read-only home: %+v", m.dialog)
	}
	for _, k := range []string{"enter", "enter"} {
		_, cmd := m.Update(keyMsg(k))
		pump(m, cmd)
	}
	if len(fs.opened) != 1 || fs.opened[0].RunTask != "M2-04" || m.status != "side c0ffee opened" {
		t.Fatalf("opened %+v, status %q", fs.opened, m.status)
	}
	view := m.View()
	if !strings.Contains(view, "SIDE 1 open") {
		t.Fatalf("no SIDE line:\n%s", view)
	}
	m.Update(backendMsg{errors.New("gone")})
	if !m.offers(actSide) {
		t.Error("an open session isn't offered without the backend")
	}
	for i, l := range m.lines {
		if l.act == actSide {
			m.focus, m.lineSel = homeLines, i
		}
	}
	_, cmd = m.Update(keyMsg("enter"))
	pump(m, cmd)
	if m.dialog == nil || m.dialog.title != "Side sessions · 1 open" {
		t.Fatalf("the SIDE line: %+v", m.dialog)
	}
}

// The run view started from home gets home's side sessions.
func TestSideRunViewFromHome(t *testing.T) {
	fs := newFakeSide()
	m := newHome(context.Background(), &fakeServices{side: fs}, &theme{})
	m.snap = &report.Snapshot{Project: "sinjal"}
	v := m.runView(report.RunRequest{}, &liveRun{h: &runHandle{feed: NewFeed()}, sender: nil}).(*model)
	if v.opts.Side != SideSessions(fs) || v.side == nil {
		t.Errorf("run view side: %+v", v.opts.Side)
	}
}

func TestSideWhereParallelRun(t *testing.T) {
	f := &sideFlow{run: &SideRun{Task: "A-02", Parallel: true}}
	if got, want := f.treeWarning(), "The run merges into this tree; a plan edit holds the run."; got != want {
		t.Errorf("parallel: %q, want %q", got, want)
	}
	if !f.sharedRun() {
		t.Error("manual must still come first in a parallel run")
	}
	f.run.Parallel = false
	if got := f.treeWarning(); got != "A-02 is working in this tree; a plan edit holds the run." {
		t.Errorf("single-task: %q", got)
	}
}
