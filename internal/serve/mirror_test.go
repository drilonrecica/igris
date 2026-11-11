package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend/fake"
	"github.com/drilonrecica/igris/internal/config"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/runner"
	"github.com/drilonrecica/igris/internal/state"
)

var _ UISource = (*Mirror)(nil)

// snapJSON is s as the wire has it, for comparing two snapshots.
func snapJSON(t *testing.T, s Snapshot) string {
	t.Helper()
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A tracker seeded from a snapshot continues it: its snapshot is the
// snapshot, and the events after it fold in as into the original.
func TestTrackerFromSnapshot(t *testing.T) {
	evs := wireEvents()
	evs = evs[:len(evs)-1] // without the end, so the cards and questions stay
	at := evs[0].At
	slot := 1
	evs = append(evs[:2], append([]engine.Event{
		{At: at, Kind: engine.TaskStarted, Phase: "M0", Task: "M0-02", Title: "B", Rank: "opus", Model: "opus", Slot: &slot},
		{At: at, Kind: engine.ResolverOpened, Phase: "M0", Task: "M0-02", Reason: "conflict", Detail: "resolving a conflict"},
		{At: at, Kind: engine.TaskModeChanged, Task: "M0-03", Detail: "plan"},
		{At: at, Kind: engine.PauseOn},
		{At: at, Kind: engine.Asked, Task: "M0-02", Question: engine.QuestionConfirmReset, Detail: "reset?"},
		{At: at, Kind: engine.BudgetRead, Budgets: []engine.BudgetUse{{Budget: "run", Limit: "10M", Used: "1M", Percent: 10}}},
	}, evs[2:]...)...)
	run := "r1"
	orig := newTracker("plan")
	for _, ev := range evs {
		orig.apply(ev, fromEngine(ev, run))
	}
	phases := []Phase{{ID: "M0", Tasks: []Task{{ID: "M0-02"}, {ID: "M0-01"}}}}
	want := orig.snapshot(phases)
	seeded := trackerFrom(want)
	if got := seeded.snapshot(phases); snapJSON(t, got) != snapJSON(t, want) {
		t.Fatalf("seeded snapshot:\n%s\nwant\n%s", snapJSON(t, got), snapJSON(t, want))
	}
	for _, ev := range []engine.Event{
		{At: at.Add(time.Minute), Kind: engine.ResolverDone, Phase: "M0", Task: "M0-02"},
		{At: at.Add(time.Minute), Kind: engine.NeedsYouClear, Phase: "M0", Task: "M0-02"},
		{At: at.Add(2 * time.Minute), Kind: engine.TaskDone, Phase: "M0", Task: "M0-01"},
		{At: at.Add(2 * time.Minute), Kind: engine.ResetDropped, Task: "M0-02"},
		{At: at.Add(3 * time.Minute), Kind: engine.RunStopped, Phase: "M0", Detail: "stopped"},
	} {
		orig.apply(ev, fromEngine(ev, run))
		seeded.apply(ev, fromEngine(ev, run))
		if got, want := seeded.snapshot(phases), orig.snapshot(phases); snapJSON(t, got) != snapJSON(t, want) {
			t.Fatalf("after %s:\n%s\nwant\n%s", ev.Kind, snapJSON(t, got), snapJSON(t, want))
		}
	}
	// Nothing is shared: changing the seeded tracker leaves the snapshot.
	before := snapJSON(t, want)
	seeded.overrides["M0-09"] = "plan"
	if len(seeded.cards) > 0 {
		seeded.cards[0].State = "lost"
	}
	if snapJSON(t, want) != before {
		t.Error("the seeded tracker shares memory with the snapshot")
	}
}

// drops dials the project's socket and keeps the client ends, so a test
// can drop the mirror's connection as a network or a serve might.
type drops struct {
	root string

	mu    sync.Mutex
	conns []net.Conn
	dials int
}

func (d *drops) dial(ctx context.Context) (net.Conn, error) {
	c, err := Dial(ctx, d.root)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	d.conns = append(d.conns, c)
	return c, nil
}

func (d *drops) drop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.conns {
		_ = c.Close()
	}
	d.conns = nil
}

func (d *drops) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// waitUntil polls ok until it holds or patience runs out.
func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// collect reads a mirror's stream into lines until it closes.
type collect struct {
	mu    sync.Mutex
	types []string
	done  chan struct{}
}

func collectStream(ch <-chan []byte) *collect {
	c := &collect{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		for b := range ch {
			var head struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(b, &head)
			c.mu.Lock()
			c.types = append(c.types, head.Type)
			c.mu.Unlock()
		}
	}()
	return c
}

func (c *collect) count(typ string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, x := range c.types {
		if x == typ {
			n++
		}
	}
	return n
}

// A served run on the fake backend and a fake clock, seen by two mirrors
// over its socket (SPEC §13, decisions.md V111-P1): one reads the plan as
// the serve does, one keeps the snapshot's phases. Their connections drop
// mid-run and they reconnect with a fresh snapshot; the run is stopped
// through a mirror; at the end each mirror's snapshot is the serve's.
func TestMirrorFollowsAServedRun(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"tasks.md": e2ePlan, state.ConfigFile: e2eTOML} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join(root, state.ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	clock := engine.NewFakeClock(t0)
	dir, err := state.Open(root, state.Options{Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	be := fake.New()
	git := &runner.Fake{}
	git.Func(func(c runner.Cmd) (runner.Result, error) {
		switch c.Args[0] {
		case "rev-parse":
			return runner.Result{Stdout: []byte("3fa29c1e0b6d4a8f9c2e7b1d5a0f6e3c8b9d2a4f\n")}, nil
		case "symbolic-ref":
			return runner.Result{Stdout: []byte("main\n")}, nil
		case "rev-list":
			return runner.Result{Stdout: []byte("0\n")}, nil
		}
		return runner.Result{}, nil
	})
	loadPlan := func() (*plan.Plan, error) {
		return plan.Load(filepath.Join(root, "tasks.md"), plan.Options{Root: root})
	}
	var eng *engine.Engine
	sent := make(chan engine.Command, 8)
	var hellos []Request
	var hmu sync.Mutex
	srv := New(Options{
		Version: "1.11.0", Project: "synthetic", Backend: be.Name(), Mode: "plan",
		Sender: sendFunc(func(c engine.Command) { eng.Send(c); sent <- c }),
		Tail:   func(ctx context.Context, task string, n int) ([]string, error) { return eng.TailTask(ctx, task, n) },
		Plan:   loadPlan,
		Hello: func(r Request) {
			hmu.Lock()
			hellos = append(hellos, r)
			hmu.Unlock()
		},
	})
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	eng, err = engine.New(engine.Options{
		Config: cfg, Backend: be, State: dir, Clock: clock, Runner: git, Phase: "A",
		Events: srv.Publish,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*patience)
	defer cancel()
	d1, d2 := &drops{root: root}, &drops{root: root}
	var withPlan, without *Mirror
	var stream *collect
	open := func(d *drops, p func() (*plan.Plan, error)) *Mirror {
		m, err := NewMirror(ctx, MirrorOptions{Name: "igris 1.11.0", Dial: d.dial, Plan: p, Retry: time.Millisecond})
		if err != nil {
			t.Fatalf("NewMirror = %v", err)
		}
		t.Cleanup(m.Close)
		return m
	}
	// The fake clock runs these on the engine's goroutine: the run stands
	// still meanwhile.
	clock.At(time.Minute, func() {
		withPlan, without = open(d1, loadPlan), open(d2, nil)
		ch, _ := withPlan.Subscribe()
		stream = collectStream(ch)
		if got, want := snapJSON(t, withPlan.Snapshot()), snapJSON(t, srv.Snapshot()); got != want {
			t.Errorf("the mirror's first snapshot:\n%s\nwant\n%s", got, want)
		}
		if p := without.Project(); p != "synthetic" {
			t.Errorf("Project = %q", p)
		}
	})
	clock.At(2*time.Minute, func() {
		if err := dir.WriteSignal(state.Signal{ID: "A-1", Action: state.ActionDone, Note: "did A-1"}); err != nil {
			t.Error(err)
		}
	})
	clock.At(3*time.Minute, func() {
		// A-1 is done and A-4 has started; both connections drop.
		d1.drop()
		d2.drop()
		waitUntil(t, "both mirrors to reconnect", func() bool {
			return d1.count() == 2 && d2.count() == 2 && withPlan.Online() && without.Online()
		})
		waitUntil(t, "the fresh snapshot on the stream", func() bool { return stream.count(TypeSnapshot) == 2 })
	})
	clock.At(4*time.Minute, func() {
		// A task-mode change while the mirrors were away from the first
		// snapshot, and the stop, both through a mirror.
		if rep, status := without.Request(ctx, Request{Type: CmdTaskMode, ID: "m1", Task: "A-3", Text: "accept"}); status != http.StatusOK || rep.Type != TypeAck || rep.ID != "m1" {
			t.Errorf("task_mode = %d %+v", status, rep)
		}
		if rep, status := withPlan.Request(ctx, Request{Type: CmdStop, ID: "s1"}); status != http.StatusOK || rep.Type != TypeAck || rep.ID != "s1" {
			t.Errorf("stop = %d %+v", status, rep)
		}
	})
	clock.At(24*time.Hour, func() { t.Error("the run did not stop within a day of fake time") })

	res, err := eng.Run(context.Background())
	if err != nil || res.Outcome != engine.Stopped {
		t.Fatalf("Run = %s, %v", res.Outcome, err)
	}
	if withPlan == nil {
		t.Fatal("the mirrors never attached")
	}
	for _, m := range []*Mirror{withPlan, without} {
		waitUntil(t, "the mirror to see the end", func() bool { return m.Snapshot().Ended })
	}
	want := srv.Snapshot()
	if !want.Ended || want.End != "stopped" {
		t.Fatalf("the serve's snapshot didn't end: %+v", want)
	}
	if got := withPlan.Snapshot(); snapJSON(t, got) != snapJSON(t, want) {
		t.Errorf("the mirror's snapshot:\n%s\nwant the serve's\n%s", snapJSON(t, got), snapJSON(t, want))
	}
	// Without a plan reader the phases are the last snapshot's with the
	// statuses the events changed; all else is the serve's.
	got := without.Snapshot()
	statuses := func(s Snapshot) string {
		var out []string
		for _, ph := range s.Phases {
			for _, tk := range ph.Tasks {
				out = append(out, tk.ID+"="+tk.Status)
			}
		}
		return strings.Join(out, " ")
	}
	if g, w := statuses(got), statuses(want); g != w || g == "" {
		t.Errorf("statuses without a plan reader: %s, want %s", g, w)
	}
	got.Phases, want.Phases = nil, nil
	if snapJSON(t, got) != snapJSON(t, want) {
		t.Errorf("the mirror's snapshot without phases:\n%s\nwant\n%s", snapJSON(t, got), snapJSON(t, want))
	}
	if !strings.Contains(snapJSON(t, got), `"A-3": "accept"`) {
		t.Errorf("the task mode sent through the mirror is not in its snapshot: %v", got.Overrides)
	}
	if n := stream.count(string(engine.RunStopped)); n != 1 {
		t.Errorf("run_stopped on the stream %d times", n)
	}

	// The serve ends: the mirrors stop, and so does the stream.
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Mirror{withPlan, without} {
		select {
		case <-m.Done():
		case <-time.After(patience):
			t.Fatal("a mirror did not stop with its serve")
		}
		if res, ok := m.Ended(); !ok || res.Outcome != engine.Stopped {
			t.Errorf("Ended = %+v, %v", res, ok)
		}
		if err := m.Err(); err != nil {
			t.Errorf("Err = %v", err)
		}
	}
	select {
	case <-stream.done:
	case <-time.After(patience):
		t.Fatal("the stream did not close")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v", err)
		}
	case <-time.After(patience):
		t.Error("Serve did not return")
	}
	close(sent)
	for c := range sent {
		if c.Kind == engine.CmdMode || (c.Kind == engine.CmdTaskMode && engine.SkipsPermissions(c.Text)) {
			t.Errorf("the engine got %+v", c)
		}
	}
	hmu.Lock()
	defer hmu.Unlock()
	if len(hellos) != 4 {
		t.Errorf("%d hellos, want 4 (two mirrors, each twice)", len(hellos))
	}
	for _, h := range hellos {
		if h.Yolo || h.Client != "igris 1.11.0" {
			t.Errorf("hello %+v", h)
		}
	}
}

// serveSocket serves s on a unix socket in a fresh project directory and
// returns the project's root.
func serveSocket(t *testing.T, s *Server) string {
	t.Helper()
	root := t.TempDir()
	dir, err := state.Open(root, state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	l, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(l)
	}()
	t.Cleanup(func() {
		_ = s.Close()
		<-done
	})
	return root
}

// mirrorOf opens a mirror of the run served at root.
func mirrorOf(t *testing.T, root string, streamLen int) *Mirror {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	t.Cleanup(cancel)
	m, err := NewMirror(ctx, MirrorOptions{
		Name: "igris 1.11.0", Retry: time.Millisecond, StreamLen: streamLen,
		Dial: func(ctx context.Context) (net.Conn, error) { return Dial(ctx, root) },
	})
	if err != nil {
		t.Fatalf("NewMirror = %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

// A skip-permissions mode is refused by the mirror and never reaches the
// run, however it is spelled; other modes go without yes, as the serve's
// own web UI sends them.
func TestMirrorNeverSendsYolo(t *testing.T) {
	snd := &sender{}
	var raw [][]byte
	var rmu sync.Mutex
	s := New(Options{Version: "1.11.0", Sender: snd, Hello: func(r Request) {
		b, _ := json.Marshal(r)
		rmu.Lock()
		raw = append(raw, b)
		rmu.Unlock()
	}})
	m := mirrorOf(t, serveSocket(t, s), 0)
	ctx := context.Background()
	for _, r := range []Request{
		{Type: CmdMode, ID: "1", Text: "yolo"},
		{Type: CmdMode, ID: "2", Text: " YOLO ", Yes: true},
		{Type: CmdTaskMode, ID: "3", Task: "M1-01", Text: "plan:yolo", Yes: true},
		{Type: CmdMode, ID: "4", Text: "yo\x1b[0mlo", Yes: true},
	} {
		rep, status := m.Request(ctx, r)
		if status != http.StatusForbidden || rep.Type != TypeError || rep.ID != r.ID || rep.Error != errRemoteYolo {
			t.Errorf("%q: %d %+v, want 403 %s", r.Text, status, rep, errRemoteYolo)
		}
	}
	rep, status := m.Request(ctx, Request{Type: CmdMode, ID: "5", Text: "plan", Yes: true})
	if status != http.StatusOK || rep.Type != TypeAck || rep.ID != "5" {
		t.Fatalf("plan: %d %+v", status, rep)
	}
	got := snd.got()
	if len(got) != 1 || got[0].Kind != engine.CmdMode || got[0].Text != "plan" || got[0].Yes {
		t.Errorf("the run got %+v, want only plan without yes", got)
	}
	rmu.Lock()
	defer rmu.Unlock()
	if len(raw) != 1 || bytes.Contains(raw[0], []byte("yolo")) {
		t.Errorf("hellos: %s", raw)
	}
}

// The mirror answers as the serve's own Request does: no hello, the
// request's shape checked, the run's refusal a 400, and a 503 once the
// run can't be asked.
func TestMirrorRequests(t *testing.T) {
	snd := &sender{}
	s := New(Options{Version: "1.11.0", Sender: snd,
		Tail: func(_ context.Context, task string, n int) ([]string, error) {
			return []string{"tail of " + task, strings.Repeat("x", n)}, nil
		}})
	m := mirrorOf(t, serveSocket(t, s), 0)
	ctx := context.Background()
	for _, tc := range []struct {
		r      Request
		status int
		typ    string
	}{
		{Request{Type: TypeHello, ID: "h"}, http.StatusBadRequest, TypeError},
		{Request{Type: CmdPause}, http.StatusBadRequest, TypeError}, // no id
		{Request{Type: "launch", ID: "u"}, http.StatusBadRequest, TypeError},
		{Request{Type: TypeTail, ID: "t0", N: 0}, http.StatusBadRequest, TypeError},
		{Request{Type: CmdPause, ID: "p"}, http.StatusOK, TypeAck},
		{Request{Type: CmdDone, ID: "d", Task: "M1-01", Text: "note"}, http.StatusOK, TypeAck},
	} {
		rep, status := m.Request(ctx, tc.r)
		if status != tc.status || rep.Type != tc.typ || rep.ID != tc.r.ID {
			t.Errorf("%s %q: %d %+v, want %d %s", tc.r.Type, tc.r.ID, status, rep, tc.status, tc.typ)
		}
	}
	rep, status := m.Request(ctx, Request{Type: TypeTail, ID: "t1", Task: "M1-01", N: 3})
	if status != http.StatusOK || rep.Type != TypeTail || rep.ID != "t1" || strings.Join(rep.Lines, "|") != "tail of M1-01|xxx" {
		t.Errorf("tail: %d %+v", status, rep)
	}
	got := snd.got()
	if len(got) != 2 || got[0].Kind != engine.CmdPause || got[1].Kind != engine.CmdDone || got[1].Text != "note" || got[1].Task != "M1-01" {
		t.Errorf("the run got %+v", got)
	}

	// The serve ends without the run's end: the mirror stops, gone.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.Done():
	case <-time.After(patience):
		t.Fatal("the mirror did not stop")
	}
	if err := m.Err(); err == nil {
		t.Error("Err = nil, want the run gone")
	}
	if _, ok := m.Ended(); !ok {
		t.Error("Ended not told")
	}
	if m.Online() {
		t.Error("still online")
	}
	if rep, status := m.Request(ctx, Request{Type: CmdPause, ID: "late"}); status != http.StatusServiceUnavailable || rep.Type != TypeError || rep.ID != "late" {
		t.Errorf("after the end: %d %+v", status, rep)
	}
	ch, _ := m.Subscribe()
	var lines int
	for range ch {
		lines++
	}
	if lines != 1 {
		t.Errorf("a stream opened after the end had %d lines, want the snapshot only", lines)
	}
}

// A reader of the stream that falls behind is dropped; the others go on,
// and a reader that cancels is gone.
func TestMirrorStreamSlowReader(t *testing.T) {
	s := New(Options{Version: "1.11.0"})
	m := mirrorOf(t, serveSocket(t, s), 2)
	slow, _ := m.Subscribe()
	fast, _ := m.Subscribe()
	gone, cancel := m.Subscribe()
	cancel()
	cancel() // twice is harmless
	if b := <-fast; !bytes.Contains(b, []byte(`"type":"snapshot"`)) {
		t.Fatalf("first message %s", b)
	}
	var mu sync.Mutex
	var fastTypes []string
	go func() {
		for b := range fast {
			var h struct{ Type string }
			_ = json.Unmarshal(b, &h)
			mu.Lock()
			fastTypes = append(fastTypes, h.Type)
			mu.Unlock()
		}
	}()
	evs := []engine.Event{
		{At: t0, Kind: engine.RunStarted, Run: "r1", Phase: "M1"},
		{At: t0, Kind: engine.PauseOn},
		{At: t0, Kind: engine.PauseOff},
	}
	for i, ev := range evs {
		// One at a time, so only the slow reader falls behind.
		s.Publish(ev)
		waitUntil(t, "the fast reader's event", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(fastTypes) == i+1
		})
	}
	var n int
	for range slow {
		n++
	}
	if n != 2 {
		t.Errorf("the slow reader got %d messages before it was dropped, want 2", n)
	}
	if b, ok := <-gone; !ok || !bytes.Contains(b, []byte(`"type":"snapshot"`)) {
		t.Errorf("the cancelled reader: %s %v", b, ok)
	}
	if _, ok := <-gone; ok {
		t.Error("the cancelled reader's stream is still open")
	}
	if snap := m.Snapshot(); snap.Run != "r1" || snap.Paused {
		t.Errorf("snapshot: run %q, paused %v; want r1, resumed", snap.Run, snap.Paused)
	}
}
