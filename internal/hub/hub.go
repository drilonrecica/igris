// Package hub is `igris hub` (SPEC §14, decisions.md V111-P1): several
// live runs behind one HTTP listener and one pairing. The hub is its own
// process and a client of each run: it finds the runs in the run registry
// (state.LiveRuns), follows each chosen one through a serve.Mirror over its
// socket, and with All takes in the runs started later. It manages runs and
// never starts or stops one: ending the hub only detaches its mirrors.
package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
	"github.com/drilonrecica/igris/internal/textsafe"
)

const (
	// Interval is how often the registry is read again for runs that
	// started or answer again: polled, no fsnotify (V111-P1).
	Interval = 2 * time.Second
	// CookieName is the cookie a browser is paired with the hub by; a
	// project's listener has its own (serve.CookieFor).
	CookieName = "igris_hub"
	// PairCommand prints the hub's pairing URL.
	PairCommand = "igris hub token"
	// streamLen bounds what may wait for one reader of the hub's stream
	// before it is dropped as too slow.
	streamLen = 4096
	// resubscribeWait is how long a run's stream that was dropped waits
	// before it follows the run again, so a run that just ended isn't
	// followed in a loop.
	resubscribeWait = 100 * time.Millisecond
)

// Run is one run the hub follows: a serve.Mirror.
type Run interface {
	serve.UISource
	// Subscribe follows the run's protocol-1 stream (serve.Mirror).
	Subscribe() (stream <-chan []byte, cancel func())
	// Done is closed once the run is no longer followed.
	Done() <-chan struct{}
	// Ended and Err are how the run ended (serve.Mirror).
	Ended() (engine.Result, bool)
	Err() error
	// Close detaches from the run; the run goes on.
	Close()
}

var _ Run = (*serve.Mirror)(nil)

// Options configures a Hub.
type Options struct {
	// All takes every live run and every run started later; otherwise
	// Roots names the projects whose runs the hub takes.
	All   bool
	Roots []string
	// Live returns the registry's live runs; nil is state.LiveRuns.
	Live func(ctx context.Context) []state.RunEntry
	// Attach follows a run; nil attaches a serve.Mirror to its socket as
	// Name ("igris 1.11.0").
	Attach func(ctx context.Context, e state.RunEntry) (Run, error)
	Name   string
	// Out takes the hub's log lines (`09:41:07 run joined: sinjal`); nil
	// drops them.
	Out io.Writer
	// Now stamps the log lines; nil is time.Now.
	Now func() time.Time
}

// Entry is one run of the hub.
type Entry struct {
	// ID names the run in the hub's URLs: `<name>-<h6>` (RunID).
	ID      string
	Root    string
	Project string
	Started time.Time
	Run     Run
	// Ended says the hub no longer follows the run, and Outcome how it
	// ended.
	Ended   bool
	Outcome look.Outcome
}

// ChangeKind is what a Change says.
type ChangeKind int

const (
	// Joined is a run the hub took in.
	Joined ChangeKind = iota
	// Message is one protocol-1 line of a run, as its serve sent it.
	Message
	// Left is a run that ended, with its outcome.
	Left
)

// Change is one message of the hub's merged stream.
type Change struct {
	Kind    ChangeKind
	ID      string
	Project string
	// Line is a Message's protocol-1 line.
	Line []byte
	// Outcome is a Left run's.
	Outcome look.Outcome
}

// Hub follows the chosen live runs. Create it with New, then call Scan
// (once, and each Interval, as Run does) and Close.
type Hub struct {
	opts  Options
	roots map[string]bool

	mu     sync.Mutex
	runs   map[string]*Entry // by root; an ended run stays until its project's next run joins
	keys   map[string]string // root → the registry entry its run (or failed attach) came from
	subs   map[chan Change]struct{}
	closed bool
	wg     sync.WaitGroup
}

// New returns a hub following nothing yet.
func New(opts Options) *Hub {
	if opts.Live == nil {
		opts.Live = func(ctx context.Context) []state.RunEntry {
			runs, _ := state.LiveRuns(ctx)
			return runs
		}
	}
	if opts.Attach == nil {
		name := opts.Name
		opts.Attach = func(ctx context.Context, e state.RunEntry) (Run, error) {
			return serve.NewMirror(ctx, serve.MirrorOptions{
				Name: name,
				Dial: func(ctx context.Context) (net.Conn, error) { return serve.DialSocket(ctx, e.Socket) },
			})
		}
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	roots := map[string]bool{}
	for _, r := range opts.Roots {
		roots[r] = true
	}
	return &Hub{opts: opts, roots: roots, runs: map[string]*Entry{}, keys: map[string]string{}, subs: map[chan Change]struct{}{}}
}

// Run scans the registry now and every Interval until ctx ends, then
// closes the hub.
func (h *Hub) Run(ctx context.Context) {
	defer h.Close()
	h.Scan(ctx)
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Scan(ctx)
		}
	}
}

// wants says whether the hub takes the runs of the project at root.
func (h *Hub) wants(root string) bool { return h.opts.All || h.roots[root] }

// entryKey tells registry entries apart: a project's next serve has
// another pid or start, so its run joins while the one that just ended
// (its entry is still there while it shuts down) isn't taken in again.
func entryKey(e state.RunEntry) string {
	return fmt.Sprintf("%d %s", e.PID, e.Started.UTC().Format(time.RFC3339Nano))
}

// Scan reads the registry once and follows every live run the hub takes
// and doesn't follow yet. A run that can't be followed is logged once.
func (h *Hub) Scan(ctx context.Context) {
	for _, e := range h.opts.Live(ctx) {
		if ctx.Err() != nil {
			return
		}
		if !h.wants(e.Root) {
			continue
		}
		key := entryKey(e)
		h.mu.Lock()
		cur, closed := h.runs[e.Root], h.closed
		seen := h.keys[e.Root] == key
		h.mu.Unlock()
		if closed {
			return
		}
		if (cur != nil && !cur.Ended) || seen {
			continue
		}
		run, err := h.opts.Attach(ctx, e)
		project := textsafe.Line(e.Project)
		if err != nil {
			h.mu.Lock()
			h.keys[e.Root] = key
			h.mu.Unlock()
			if errors.Is(err, serve.ErrNotServed) {
				continue // it ended between the registry and the attach
			}
			h.logf("run not joined: %s · %s", project, textsafe.Line(err.Error()))
			continue
		}
		if p := textsafe.Line(run.Project()); p != "" {
			project = p
		}
		ent := &Entry{ID: RunID(project, e.Root), Root: e.Root, Project: project, Started: e.Started, Run: run}
		h.mu.Lock()
		h.keys[e.Root] = key
		if h.closed {
			h.mu.Unlock()
			run.Close()
			return
		}
		h.runs[e.Root] = ent
		h.wg.Add(2)
		h.mu.Unlock()
		// The line first, so whoever reads the change finds it logged.
		h.logf("run joined: %s", project)
		h.mu.Lock()
		h.fanOut(Change{Kind: Joined, ID: ent.ID, Project: project})
		h.mu.Unlock()
		go h.follow(ent)
		go h.watch(ent)
	}
}

// follow hands the run's stream on to the hub's readers, following it
// again after it was dropped as too slow, until the run is no longer
// followed.
func (h *Hub) follow(e *Entry) {
	defer h.wg.Done()
	for {
		stream, cancel := e.Run.Subscribe()
		for line := range stream {
			h.mu.Lock()
			h.fanOut(Change{Kind: Message, ID: e.ID, Project: e.Project, Line: line})
			h.mu.Unlock()
		}
		cancel()
		select {
		case <-e.Run.Done():
			return
		case <-time.After(resubscribeWait):
		}
	}
}

// watch notes the run's end: its outcome, a log line and a Left change.
// A run the hub detached from as it closed didn't end.
func (h *Hub) watch(e *Entry) {
	defer h.wg.Done()
	<-e.Run.Done()
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return
	}
	outcome := outcomeOf(e.Run)
	// The line first, so whoever reads the change finds it logged.
	h.logf("run ended: %s · %s", e.Project, outcome)
	h.mu.Lock()
	defer h.mu.Unlock()
	e.Ended, e.Outcome = true, outcome
	h.fanOut(Change{Kind: Left, ID: e.ID, Project: e.Project, Outcome: outcome})
}

// outcomeOf is how a run the hub followed ended: its own outcome, a
// failure, or interrupted when its serve went away without its end.
func outcomeOf(r Run) look.Outcome {
	if err := r.Err(); err != nil {
		var re *serve.RunError
		if errors.As(err, &re) {
			return look.Failed
		}
		return look.InterruptedRun
	}
	res, ok := r.Ended()
	if !ok {
		return look.InterruptedRun
	}
	switch res.Outcome {
	case engine.Completed:
		return look.Completed
	case engine.Stuck:
		return look.Stuck
	}
	return look.Stopped
}

// Runs are the runs the hub follows, oldest first. A run that ended is
// left out: a page drawn now doesn't show it, while one drawn before saw
// it end on the stream (a Left change) and keeps its outcome until it is
// reloaded.
func (h *Hub) Runs() []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Entry
	for _, e := range h.runs {
		if !e.Ended {
			out = append(out, *e)
		}
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if c := a.Started.Compare(b.Started); c != 0 {
			return c
		}
		return strings.Compare(a.Root, b.Root)
	})
	return out
}

// Lookup is the run with id, also one that ended (until its project's
// next run joins).
func (h *Hub) Lookup(id string) (Entry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.runs {
		if e.ID == id {
			return *e, true
		}
	}
	return Entry{}, false
}

// Subscribe follows the hub's merged stream: every run's protocol-1 lines
// tagged with its ID, and the runs that join and end. The channel is
// closed when the hub closes, when cancel is called, or when the reader
// falls too far behind (it subscribes again, and reads Runs afresh).
func (h *Hub) Subscribe() (stream <-chan Change, cancel func()) {
	ch := make(chan Change, streamLen)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// fanOut hands c to every reader, dropping the ones too slow to take it;
// h.mu is held.
func (h *Hub) fanOut(c Change) {
	for ch := range h.subs {
		select {
		case ch <- c:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// Close detaches from every run (they go on) and ends the stream.
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	var runs []Run
	for _, e := range h.runs {
		if !e.Ended {
			runs = append(runs, e.Run)
		}
	}
	h.mu.Unlock()
	for _, r := range runs {
		r.Close()
	}
	h.wg.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

// logf writes one log line stamped with the local time (SPEC §14).
func (h *Hub) logf(format string, args ...any) {
	fmt.Fprintf(h.opts.Out, "%s %s\n", look.ClockSeconds(h.opts.Now()), fmt.Sprintf(format, args...))
}

// RunID names a run in the hub's URLs: the project's name made safe for a
// path (lower case letters, digits and dashes; "run" when nothing is left),
// a dash and the first 6 hex characters of the sha256 of its root, so two
// projects of one name stay apart (decisions.md V111-P1).
func RunID(project, root string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(project) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	name := b.String()
	if name == "" {
		name = "run"
	}
	return name + "-" + state.RootHash(root)[:6]
}
