package serve

import (
	"context"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
	"github.com/drilonrecica/igris/internal/textsafe"
)

// A run seen over its socket (decisions.md V111-P1): a Mirror attaches to
// a serve as a protocol-1 client, keeps the snapshot it got first and folds
// every event into it with the serve's own tracker, so the web UI (UISource)
// draws it as it draws the serve's in-process run. Its commands are
// protocol-1 requests; its event stream fans out to any number of
// readers (Subscribe).

// MirrorOptions configures a Mirror.
type MirrorOptions struct {
	// Name is this igris in the hello, e.g. "igris 1.11.0".
	Name string
	// Dial connects to the run's socket (Dial with the project's root); it
	// returns an error wrapping ErrNotServed when nothing answers there.
	Dial func(ctx context.Context) (net.Conn, error)
	// Plan loads the plan for a snapshot's phases, as the serve's own
	// Options.Plan; nil keeps the phases of the last snapshot, each task's
	// status following the events' changes.
	Plan func() (*plan.Plan, error)
	// Retry is the wait between reconnect attempts; zero means 1 s.
	Retry time.Duration
	// StreamLen bounds what may wait for one reader of the stream before
	// it is dropped as too slow; zero means 4096 messages.
	StreamLen int
	// Logf reports what the client itself tells (a command not sent); nil
	// drops it.
	Logf func(format string, args ...any)
}

// Mirror is one served run seen over its socket. Create it with
// NewMirror; it implements UISource.
type Mirror struct {
	opts   MirrorOptions
	client *Client

	mu     sync.Mutex
	track  *tracker
	head   Snapshot // the last snapshot: who serves, the plan's phases
	online bool
	subs   map[chan []byte]struct{}
	done   bool // the client stopped: the streams are closed
	res    engine.Result
	err    error
	ended  bool // End was told
	over   chan struct{}
}

// NewMirror attaches to a served run and returns once it has the run's
// snapshot. The error wraps ErrNotServed when no run answers. The mirror
// follows the run, reconnecting when the connection drops, until the run
// ends, nothing answers on its socket any more or Close is called.
func NewMirror(ctx context.Context, opts MirrorOptions) (*Mirror, error) {
	if opts.StreamLen <= 0 {
		opts.StreamLen = defaultQueueLen
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	m := &Mirror{opts: opts, track: newTracker(""), subs: map[chan []byte]struct{}{}, over: make(chan struct{})}
	c, err := Attach(ctx, ClientOptions{Name: opts.Name, Dial: opts.Dial, Sink: mirrorSink{m}, Retry: opts.Retry})
	if err != nil {
		return nil, err
	}
	m.client = c
	go func() {
		<-c.Done()
		m.mu.Lock()
		m.done, m.online = true, false
		for ch := range m.subs {
			close(ch)
			delete(m.subs, ch)
		}
		m.mu.Unlock()
		close(m.over)
	}()
	return m, nil
}

// Close detaches from the run; the streams end.
func (m *Mirror) Close() {
	m.client.Close()
	<-m.over
}

// Done is closed once the mirror has stopped following the run: it ended,
// nothing answers on its socket any more, or Close was called.
func (m *Mirror) Done() <-chan struct{} { return m.over }

// Ended is how the run ended for the mirror, as a Client's Sink is told;
// ok is false while it hasn't. A run that ended with an error, or whose
// serve went away without its end, has Err.
func (m *Mirror) Ended() (res engine.Result, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.res, m.ended
}

// Err is the error the mirror ended with: a RunError, ErrGone, or the
// serve's refusal; nil while it follows the run and after a run that
// ended without one.
func (m *Mirror) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// Online says the mirror is connected; false while it reconnects and once
// it has stopped.
func (m *Mirror) Online() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.online
}

// Project is the project's name, as the run's snapshot says it.
func (m *Mirror) Project() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.head.Project
}

// Snapshot is the run as a client of its serve starting now would see it:
// the snapshot the mirror got last with every event since folded in.
func (m *Mirror) Snapshot() Snapshot {
	phases, planErr, read := m.readPlan()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(phases, planErr, read)
}

// readPlan reads the plan through opts.Plan, outside the lock as the
// serve does; read is false without one.
func (m *Mirror) readPlan() (phases []Phase, planErr string, read bool) {
	if m.opts.Plan == nil {
		return nil, "", false
	}
	p, err := m.opts.Plan()
	if err != nil {
		return nil, cut(textsafe.Line(err.Error()), 4<<10), true
	}
	if p != nil {
		phases = phasesOf(p)
	}
	return phases, "", true
}

// snapshotLocked is the snapshot with phases when read, else with the
// last snapshot's; m.mu is held.
func (m *Mirror) snapshotLocked(phases []Phase, planErr string, read bool) Snapshot {
	if !read {
		phases, planErr = clonePhases(m.head.Phases), m.head.PlanError
	}
	snap := m.track.snapshot(phases)
	snap.Server, snap.Project, snap.Backend = m.head.Server, m.head.Project, m.head.Backend
	if h := m.head.SessionHost; h != nil {
		hc := *h
		snap.SessionHost = &hc
	}
	snap.Phases, snap.PlanError = phases, planErr
	return snap
}

// clonePhases copies phases, tasks and all.
func clonePhases(phases []Phase) []Phase {
	if phases == nil {
		return nil
	}
	out := make([]Phase, len(phases))
	for i, ph := range phases {
		ph.Tasks = slices.Clone(ph.Tasks)
		for j := range ph.Tasks {
			ph.Tasks[j].Together = slices.Clone(ph.Tasks[j].Together)
		}
		out[i] = ph
	}
	return out
}

// Subscribe follows the run's protocol-1 stream, each message one encoded
// line as the serve sends it: the snapshot first, then every event, and a
// fresh snapshot after the mirror reconnected. The channel is closed when
// the mirror stops, when cancel is called, or when the reader falls
// MirrorOptions.StreamLen messages behind (it subscribes again for a fresh
// snapshot, as a dropped socket client reconnects). The web UI's event
// stream (SSE) reads it.
func (m *Mirror) Subscribe() (stream <-chan []byte, cancel func()) {
	phases, planErr, read := m.readPlan()
	ch := make(chan []byte, m.opts.StreamLen)
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := encodeSnapshot(m.snapshotLocked(phases, planErr, read))
	if err == nil {
		ch <- b
	}
	if m.done || err != nil {
		close(ch)
		return ch, func() {}
	}
	m.subs[ch] = struct{}{}
	return ch, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, ok := m.subs[ch]; ok {
			delete(m.subs, ch)
			close(ch)
		}
	}
}

// fanOut hands b to every reader, dropping the ones too slow to take it;
// m.mu is held.
func (m *Mirror) fanOut(b []byte) {
	for ch := range m.subs {
		select {
		case ch <- b:
		default:
			close(ch)
			delete(m.subs, ch)
		}
	}
}

// Request sends a web UI's request to the run as a protocol-1 request and
// answers its reply, as the serve's own Server.Request does: no hello, and
// a skip-permissions mode is refused here and never sent (SPEC §7.3, §14).
// A request the run couldn't be asked (the connection is lost) is answered
// 503.
func (m *Mirror) Request(ctx context.Context, r Request) (Reply, int) {
	if err := checkID(r.ID); err != nil {
		return Reply{Type: TypeError, Error: err.Error()}, http.StatusBadRequest
	}
	id := r.ID
	switch r.Type {
	case TypeHello:
		return Reply{Type: TypeError, ID: id, Error: "there is no hello over HTTP: the event stream starts with the snapshot"}, http.StatusBadRequest
	case TypeTail:
		if rep, bad := checkTail(r); bad {
			return rep, http.StatusBadRequest
		}
		r = Request{Type: TypeTail, Task: r.Task, N: r.N}
	default:
		cmd, err := r.command()
		if err != nil {
			return Reply{Type: TypeError, ID: id, Error: err.Error()}, http.StatusBadRequest
		}
		if cmd.Kind == engine.CmdMode || cmd.Kind == engine.CmdTaskMode {
			if engine.SkipsPermissions(strings.ToLower(strings.TrimSpace(cmd.Text))) {
				return Reply{Type: TypeError, ID: id, Error: errRemoteYolo}, http.StatusForbidden
			}
			r.Yes = false
		}
		r.Text = cmd.Text // what was checked is what is sent
		r.N, r.Client, r.Yolo = 0, "", false
	}
	rep, err := m.client.Do(ctx, r)
	if err != nil {
		return Reply{Type: TypeError, ID: id, Error: "the run could not be asked: " + err.Error()}, http.StatusServiceUnavailable
	}
	rep.ID = id
	if rep.Type == TypeError {
		return rep, http.StatusBadRequest
	}
	return rep, http.StatusOK
}

// mirrorSink is the Mirror as its client's Sink.
type mirrorSink struct{ m *Mirror }

// Snapshot starts the mirror over from s: on attaching and after each
// reconnect.
func (k mirrorSink) Snapshot(s Snapshot) {
	m := k.m
	m.mu.Lock()
	defer m.mu.Unlock()
	m.track, m.online = trackerFrom(s), true
	m.head = Snapshot{Server: s.Server, Project: s.Project, Backend: s.Backend, SessionHost: s.SessionHost,
		Phases: clonePhases(s.Phases), PlanError: s.PlanError}
	if b, err := encodeSnapshot(s); err == nil {
		m.fanOut(b)
	}
}

// PushWire folds one event of the run in, as the serve folded it, and
// hands it to the readers.
func (k mirrorSink) PushWire(raw engine.Event, ev Event) {
	m := k.m
	m.mu.Lock()
	defer m.mu.Unlock()
	m.track.apply(raw, ev)
	for _, ch := range ev.Changes {
		setStatus(m.head.Phases, ch.ID, ch.To)
	}
	if b, err := encodeEvent(ev); err == nil {
		m.fanOut(b)
	}
}

// setStatus sets task id's status in phases.
func setStatus(phases []Phase, id, status string) {
	for i := range phases {
		for j := range phases[i].Tasks {
			if phases[i].Tasks[j].ID == id {
				phases[i].Tasks[j].Status = status
			}
		}
	}
}

// Push is what the client itself tells: a command the run refused or the
// client couldn't send. It is not the run's, so it stays out of the
// snapshot.
func (k mirrorSink) Push(ev engine.Event) {
	k.m.opts.Logf("%s: %s", ev.Kind, ev.Detail)
}

// Lost says the connection dropped; a fresh snapshot follows.
func (k mirrorSink) Lost() {
	k.m.mu.Lock()
	defer k.m.mu.Unlock()
	k.m.online = false
}

// End keeps how the run ended for the mirror.
func (k mirrorSink) End(res engine.Result, err error) {
	k.m.mu.Lock()
	defer k.m.mu.Unlock()
	k.m.res, k.m.err, k.m.ended = res, err, true
}
