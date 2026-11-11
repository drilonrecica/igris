package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

// patience bounds every wait for the hub's goroutines; a test that needs
// it has failed.
const patience = 10 * time.Second

var t0 = time.Date(2026, 11, 11, 9, 41, 7, 0, time.Local)

// fakeRun is a run the hub follows, ended or closed by the test.
type fakeRun struct {
	project string
	mu      sync.Mutex
	subs    []chan []byte
	res     engine.Result
	ended   bool
	err     error
	closed  bool
	over    bool // the streams are closed; set with them, under mu
	done    chan struct{}
	once    sync.Once
}

func newFakeRun(project string) *fakeRun {
	return &fakeRun{project: project, done: make(chan struct{})}
}

func (f *fakeRun) Snapshot() serve.Snapshot { return serve.Snapshot{Project: f.project} }
func (f *fakeRun) Request(context.Context, serve.Request) (serve.Reply, int) {
	return serve.Reply{Type: serve.TypeAck}, http.StatusOK
}
func (f *fakeRun) Project() string { return f.project }
func (f *fakeRun) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 16)
	f.mu.Lock()
	defer f.mu.Unlock()
	ch <- []byte(`{"type":"snapshot"}`)
	if f.over {
		close(ch)
		return ch, func() {}
	}
	f.subs = append(f.subs, ch)
	return ch, func() {}
}
func (f *fakeRun) Done() <-chan struct{} { return f.done }
func (f *fakeRun) Ended() (engine.Result, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.res, f.ended
}
func (f *fakeRun) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
func (f *fakeRun) Close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.finish()
}
func (f *fakeRun) send(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		ch <- []byte(line)
	}
}
func (f *fakeRun) end(res engine.Result, err error) {
	f.mu.Lock()
	f.res, f.ended, f.err = res, true, err
	f.mu.Unlock()
	f.finish()
}
func (f *fakeRun) finish() {
	f.once.Do(func() {
		f.mu.Lock()
		for _, ch := range f.subs {
			close(ch)
		}
		f.subs, f.over = nil, true
		f.mu.Unlock()
		close(f.done)
	})
}

// registry is the run registry as the test sets it, and the runs the hub
// attached.
type registry struct {
	mu       sync.Mutex
	entries  []state.RunEntry
	runs     map[string]*fakeRun // by root
	attached []string
	refuse   map[string]error
}

func (r *registry) live(context.Context) []state.RunEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]state.RunEntry(nil), r.entries...)
}

func (r *registry) attach(_ context.Context, e state.RunEntry) (Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attached = append(r.attached, e.Root)
	if err := r.refuse[e.Root]; err != nil {
		return nil, err
	}
	run := newFakeRun(e.Project)
	if r.runs == nil {
		r.runs = map[string]*fakeRun{}
	}
	r.runs[e.Root] = run
	return run, nil
}

func (r *registry) set(entries ...state.RunEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = entries
}

func (r *registry) run(root string) *fakeRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs[root]
}

func (r *registry) attaches() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.attached...)
}

func entry(project string, pid int) state.RunEntry {
	return state.RunEntry{V: 1, Root: "/p/" + project, Project: project, PID: pid, Started: t0.Add(time.Duration(pid) * time.Minute), Socket: "/s/" + project}
}

// syncBuf is a log the hub's goroutines write while the test reads it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newHub(t *testing.T, reg *registry, opts Options) (*Hub, *syncBuf) {
	t.Helper()
	log := &syncBuf{}
	opts.Live, opts.Attach, opts.Out = reg.live, reg.attach, log
	opts.Now = func() time.Time { return t0 }
	h := New(opts)
	t.Cleanup(h.Close)
	return h, log
}

// next reads the stream's next change of kind.
func next(t *testing.T, stream <-chan Change, kind ChangeKind) Change {
	t.Helper()
	timeout := time.After(patience)
	for {
		select {
		case c, ok := <-stream:
			if !ok {
				t.Fatalf("the stream closed waiting for change %d", kind)
			}
			if c.Kind == kind {
				return c
			}
		case <-timeout:
			t.Fatalf("waited for change %d", kind)
		}
	}
}

func ids(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Project)
	}
	return out
}

// With All, every live run joins, and so does a run started later; a run
// already followed is not attached again.
func TestHubAllJoins(t *testing.T) {
	reg := &registry{}
	reg.set(entry("sinjal", 1), entry("billing", 2))
	h, log := newHub(t, reg, Options{All: true})
	stream, cancel := h.Subscribe()
	defer cancel()
	ctx := context.Background()
	h.Scan(ctx)
	if got := fmt.Sprint(ids(h.Runs())); got != "[sinjal billing]" {
		t.Fatalf("runs %s", got)
	}
	next(t, stream, Joined)
	next(t, stream, Joined)
	h.Scan(ctx)
	if got := len(reg.attaches()); got != 2 {
		t.Errorf("attached %d times, want 2", got)
	}
	reg.set(entry("sinjal", 1), entry("billing", 2), entry("third", 3))
	h.Scan(ctx)
	if c := next(t, stream, Joined); c.Project != "third" || c.ID != RunID("third", "/p/third") {
		t.Errorf("joined %+v", c)
	}
	want := "09:41:07 run joined: sinjal\n09:41:07 run joined: billing\n09:41:07 run joined: third\n"
	if log.String() != want {
		t.Errorf("log:\n%s\nwant\n%s", log, want)
	}
	// A run's lines reach the hub's readers, tagged with its id.
	billing := reg.run("/p/billing")
	waitFor(t, "the hub follows billing's stream", func() bool {
		billing.mu.Lock()
		defer billing.mu.Unlock()
		return len(billing.subs) > 0
	})
	billing.send(`{"type":"event"}`)
	for {
		c := next(t, stream, Message)
		if string(c.Line) == `{"type":"event"}` {
			if c.ID != RunID("billing", "/p/billing") {
				t.Errorf("message of %s", c.ID)
			}
			break
		}
	}
}

// Without All only the chosen projects' runs join, also a later run of one.
func TestHubChosenRoots(t *testing.T) {
	reg := &registry{}
	reg.set(entry("sinjal", 1), entry("billing", 2))
	h, _ := newHub(t, reg, Options{Roots: []string{"/p/billing"}})
	h.Scan(context.Background())
	if got := fmt.Sprint(ids(h.Runs())); got != "[billing]" {
		t.Fatalf("runs %s", got)
	}
	reg.run("/p/billing").end(engine.Result{Outcome: engine.Completed}, nil)
	waitFor(t, "billing ended", func() bool { return len(h.Runs()) == 0 })
	reg.set(entry("sinjal", 1), entry("billing", 4))
	h.Scan(context.Background())
	if got := fmt.Sprint(ids(h.Runs())); got != "[billing]" {
		t.Fatalf("runs after billing's next run %s", got)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A run that ends leaves the list, says its outcome on the stream and in
// the log, stays found by its id, and isn't taken in again while its
// registry entry is still there; the project's next run joins.
func TestHubRunEnds(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  engine.Result
		err  error
		want look.Outcome
	}{
		{"completed", engine.Result{Outcome: engine.Completed}, nil, look.Completed},
		{"stuck", engine.Result{Outcome: engine.Stuck}, nil, look.Stuck},
		{"stopped", engine.Result{Outcome: engine.Stopped}, nil, look.Stopped},
		{"failed", engine.Result{}, &serve.RunError{Detail: "boom"}, look.Failed},
		{"gone", engine.Result{}, serve.ErrGone, look.InterruptedRun},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := &registry{}
			reg.set(entry("sinjal", 1))
			h, log := newHub(t, reg, Options{All: true})
			stream, cancel := h.Subscribe()
			defer cancel()
			h.Scan(context.Background())
			id := RunID("sinjal", "/p/sinjal")
			reg.run("/p/sinjal").end(tc.res, tc.err)
			if c := next(t, stream, Left); c.ID != id || c.Outcome != tc.want {
				t.Errorf("left %+v, want %s", c, tc.want)
			}
			if len(h.Runs()) != 0 {
				t.Errorf("an ended run is still listed")
			}
			if e, ok := h.Lookup(id); !ok || !e.Ended || e.Outcome != tc.want {
				t.Errorf("Lookup = %+v, %v", e, ok)
			}
			if want := "09:41:07 run ended: sinjal · " + tc.want.String() + "\n"; !strings.HasSuffix(log.String(), want) {
				t.Errorf("log %q, want it to end in %q", log, want)
			}
			h.Scan(context.Background())
			if n := len(reg.attaches()); n != 1 {
				t.Errorf("the ended run's entry was attached again (%d)", n)
			}
			reg.set(entry("sinjal", 2))
			h.Scan(context.Background())
			if e, ok := h.Lookup(id); !ok || e.Ended || len(h.Runs()) != 1 {
				t.Errorf("the project's next run: %+v, %v", e, ok)
			}
		})
	}
}

// A run that can't be followed is logged once, not on every scan; one that
// ended before the attach is not logged.
func TestHubAttachFails(t *testing.T) {
	reg := &registry{refuse: map[string]error{
		"/p/sinjal":  errors.New("serve refused: too many clients"),
		"/p/billing": fmt.Errorf("%w (nothing answers)", serve.ErrNotServed),
	}}
	reg.set(entry("sinjal", 1), entry("billing", 2))
	h, log := newHub(t, reg, Options{All: true})
	h.Scan(context.Background())
	h.Scan(context.Background())
	if want := "09:41:07 run not joined: sinjal · serve refused: too many clients\n"; log.String() != want {
		t.Errorf("log %q, want %q", log, want)
	}
	if len(h.Runs()) != 0 {
		t.Errorf("runs %v", ids(h.Runs()))
	}
}

// Closing the hub detaches from every run (Close, never a stop), logs no
// end, and closes the stream.
func TestHubClose(t *testing.T) {
	reg := &registry{}
	reg.set(entry("sinjal", 1))
	h, log := newHub(t, reg, Options{All: true})
	stream, _ := h.Subscribe()
	h.Scan(context.Background())
	h.Close()
	if r := reg.run("/p/sinjal"); !r.closed {
		t.Error("the run was not detached")
	}
	for range stream {
	}
	if strings.Contains(log.String(), "ended") {
		t.Errorf("log %q", log)
	}
	h.Scan(context.Background())
	if n := len(reg.attaches()); n != 1 {
		t.Errorf("a closed hub attached again")
	}
}

func TestRunID(t *testing.T) {
	h := state.RootHash("/p/x")[:6]
	for _, tc := range []struct{ project, want string }{
		{"sinjal", "sinjal-" + h},
		{"My Project_2", "my-project-2-" + h},
		{"--a..b--", "a-b-" + h},
		{"日本", "run-" + h},
		{"", "run-" + h},
		{strings.Repeat("a", 40), strings.Repeat("a", 32) + "-" + h},
	} {
		if got := RunID(tc.project, "/p/x"); got != tc.want {
			t.Errorf("RunID(%q) = %q, want %q", tc.project, got, tc.want)
		}
		if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(RunID(tc.project, "/p/x")) {
			t.Errorf("RunID(%q) is not path-safe", tc.project)
		}
	}
	if RunID("x", "/a") == RunID("x", "/b") {
		t.Error("two roots, one id")
	}
}

// The hub's listener pairs with the hub token and its own cookie, refuses
// a project's cookie, and lists the runs to a paired device.
func TestHubHandler(t *testing.T) {
	reg := &registry{}
	reg.set(entry("sinjal", 1))
	h, _ := newHub(t, reg, Options{All: true})
	h.Scan(context.Background())
	tok := strings.Repeat("A", 43)
	hh := h.Handler(HandlerOptions{Token: func() (string, error) { return tok, nil }})

	get := func(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		hh.ServeHTTP(w, r)
		return w
	}
	if w := get("/"); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "igris hub token") {
		t.Errorf("unpaired: %d %q", w.Code, w.Body)
	}
	w := get("/pair?token=" + tok)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("pair: %d", w.Code)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != tok || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("pairing cookie %+v", w.Result().Cookies())
	}
	w = get("/", cookie)
	id := RunID("sinjal", "/p/sinjal")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "1 live run") || !strings.Contains(w.Body.String(), `href="/p/`+id+`/"`) {
		t.Errorf("paired: %d %q", w.Code, w.Body)
	}
	// The run's page is the web UI's under its base; an id the hub
	// doesn't follow says so.
	if w := get("/p/"+id+"/", cookie); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-base="/p/`+id+`"`) {
		t.Errorf("run page: %d %q", w.Code, w.Body)
	}
	if w := get("/p/nobody-000000/", cookie); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Back to the runs") {
		t.Errorf("unknown run: %d %q", w.Code, w.Body)
	}
	// A project's cookie, new or old, holding the same value is not the
	// hub's: it counts for nothing.
	for _, name := range []string{serve.LegacyCookieName, serve.CookieFor("/p/sinjal")} {
		if w := get("/", &http.Cookie{Name: name, Value: tok}); w.Code != http.StatusUnauthorized { //nolint:gosec // a client's cookie: the attributes are the server's
			t.Errorf("cookie %s: %d", name, w.Code)
		}
	}
	// There is no protocol 1 at the hub's root.
	if w := get(serve.PathEvents, cookie); w.Code != http.StatusNotFound {
		t.Errorf("events: %d", w.Code)
	}
	// A cross-site POST is refused whatever the auth.
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	hh.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d", w.Code)
	}
}

// The pages read the hub's merged stream as joins, events by their type
// and ends with their outcome.
func TestHubPagesStream(t *testing.T) {
	reg := &registry{}
	h, _ := newHub(t, reg, Options{All: true})
	stream, cancel := source{h}.Subscribe()
	defer cancel()
	reg.set(entry("sinjal", 1))
	h.Scan(context.Background())
	id := RunID("sinjal", "/p/sinjal")
	read := func() serve.HubChange {
		t.Helper()
		select {
		case c, ok := <-stream:
			if !ok {
				t.Fatal("the stream closed")
			}
			return c
		case <-time.After(patience):
			t.Fatal("no change")
		}
		return serve.HubChange{}
	}
	if c := read(); c != (serve.HubChange{Run: id, Change: serve.HubJoined}) {
		t.Errorf("joined: %+v", c)
	}
	// The run's stream starts with its snapshot.
	if c := read(); c != (serve.HubChange{Run: id, Change: serve.HubEvent, Type: "snapshot"}) {
		t.Errorf("snapshot: %+v", c)
	}
	reg.run("/p/sinjal").send(`{"v":1,"type":"task_started","task":"A-1"}`)
	if c := read(); c != (serve.HubChange{Run: id, Change: serve.HubEvent, Type: "task_started"}) {
		t.Errorf("event: %+v", c)
	}
	reg.run("/p/sinjal").end(engine.Result{Outcome: engine.Stuck}, nil)
	if c := read(); c != (serve.HubChange{Run: id, Change: serve.HubLeft, Outcome: "stuck"}) {
		t.Errorf("left: %+v", c)
	}
	if e, ok := (source{h}).Lookup(id); !ok || !e.Ended || e.Outcome != look.Stuck {
		t.Errorf("lookup after the end: %+v %v", e, ok)
	}
	if runs := (source{h}).Runs(); len(runs) != 0 {
		t.Errorf("runs after the end: %+v", runs)
	}
}
