package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drilonrecica/igris/internal/look"
)

// The hub's pages (SPEC §14, §15.8, decisions.md V111-P1): the runs the
// hub follows at /, most urgent first, with one sheet for the questions
// of every run, and each run's own pages under /p/<id>/, the web UI's
// pages with that base path over the run's mirror. Every action is the
// run's protocol-1 command, sent to it through its mirror.

// The hub's paths, besides "/" and PathLive.
const (
	// PathHubEvents is the hub's merged stream: a HubChange per message.
	// Not protocol 1, so not under /api/v1/.
	PathHubEvents = "/events"
	// PathHubRun is the prefix of a run's pages: /p/<id>/.
	PathHubRun = "/p/"
	// endedField lists, on / and its live part, the runs the page saw end
	// (their ids, comma-separated), so a refresh keeps them with their
	// outcome until the page is reloaded.
	endedField = "ended"
	// maxEnded bounds the ids endedField is read for.
	maxEnded = 32
)

// HubRunSource is one run the hub follows: what its pages read and act
// through, and its protocol-1 stream for their script (a Mirror).
type HubRunSource interface {
	UISource
	// Subscribe follows the run's protocol-1 stream, the snapshot first
	// (Mirror.Subscribe).
	Subscribe() (stream <-chan []byte, cancel func())
}

var _ HubRunSource = (*Mirror)(nil)

// HubRun is a run of the hub as its pages show it.
type HubRun struct {
	// ID names the run in the hub's URLs (`<name>-<h6>`).
	ID      string
	Project string
	Run     HubRunSource
	// Ended says the hub no longer follows the run; Outcome is how it
	// ended.
	Ended   bool
	Outcome look.Outcome
}

// HubChange is one message of the hub's merged stream, as its script
// reads it: a run that joined, an event of a run (its protocol-1 type
// only: the page fetches what it shows), or a run that left with its
// outcome.
type HubChange struct {
	Run     string `json:"run"`
	Change  string `json:"change"`
	Type    string `json:"type,omitempty"`
	Outcome string `json:"outcome,omitempty"`
}

// The kinds of HubChange.
const (
	HubJoined = "joined"
	HubEvent  = "event"
	HubLeft   = "left"
)

// HubSource is what the hub's pages read the hub through (internal/hub).
type HubSource interface {
	// Runs are the runs the hub follows, oldest first; ended ones left
	// out.
	Runs() []HubRun
	// Lookup is the run with id, also one that ended.
	Lookup(id string) (HubRun, bool)
	// Subscribe follows the hub's merged stream; the channel closes when
	// the hub ends or the reader falls behind.
	Subscribe() (stream <-chan HubChange, cancel func())
}

// HubUIOptions configures the hub's pages.
type HubUIOptions struct {
	// Now is the clock for how long runs, tasks and questions have run;
	// nil is time.Now.
	Now func() time.Time
	// Logf reports a page that could not be rendered; nil drops it.
	Logf func(format string, args ...any)
}

// hubUI serves the hub's pages.
type hubUI struct {
	src     HubSource
	now     func() time.Time
	logf    func(format string, args ...any)
	u       *ui // the templates at base "", for the list and the messages
	static  http.Handler
	streams atomic.Int32
	tails   atomic.Int32

	mu    sync.Mutex
	pages map[string]hubPages // by run id
}

// hubPages are one run's pages, for the run they were made for: a
// project's next run has the same id and gets its own.
type hubPages struct {
	run HubRunSource
	ui  http.Handler
}

// NewHubUI returns the hub's pages over src. The caller authenticates
// every request and checks the CSRF token of a cookie's POST (Guard).
func NewHubUI(src HubSource, opts HubUIOptions) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err) // the embedded directory is there by construction
	}
	static := http.StripPrefix(pathStatic, http.FileServerFS(sub))
	return &hubUI{src: src, now: opts.Now, logf: opts.Logf, static: static, pages: map[string]hubPages{},
		u: &ui{src: noRun{}, now: opts.Now, logf: opts.Logf, pages: parseUI(""), static: static}}
}

// noRun is the hub's own pages' UISource: they belong to no run.
type noRun struct{}

func (noRun) Snapshot() Snapshot { return Snapshot{} }
func (noRun) Request(context.Context, Request) (Reply, int) {
	return Reply{Type: TypeError, Error: "the hub has no run of its own"}, http.StatusNotFound
}
func (noRun) Project() string { return hubName }

// hubName is the hub's name in the brand header: `igris · hub`.
const hubName = "hub"

// backToRuns is the way back from a run's message page to the list.
const backToRuns = "Back to the runs"

func (h *hubUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch p := r.URL.Path; {
	case p == "/" || p == PathLive:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			method(w, r, http.MethodGet)
			return
		}
		name := map[string]string{"/": "hub", PathLive: "hub-live"}[p]
		h.u.render(w, http.StatusOK, name, h.view(r))
	case p == PathHubEvents:
		if method(w, r, http.MethodGet) {
			h.events(w, r)
		}
	case strings.HasPrefix(p, PathHubRun):
		h.run(w, r)
	case p == pathFavicon || strings.HasPrefix(p, pathStatic):
		h.u.ServeHTTP(w, r)
	default:
		h.message(w, http.StatusNotFound, "Not found", "There is nothing at this address.")
	}
}

// refuse draws a request the listener turned away as the web UI does.
func (h *hubUI) refuse(w http.ResponseWriter, status int, msg string) { h.u.refuse(w, status, msg) }

// message shows a page of the hub with a title, a text and the way back
// to the runs.
func (h *hubUI) message(w http.ResponseWriter, status int, title, text string) {
	h.u.render(w, status, "message", messageView{Project: hubName, Title: title, Text: text, Back: backToRuns})
}

// run serves /p/<id>/...: the run's pages with that base path, its
// protocol-1 stream and its commands; an unknown or ended run says so.
func (h *hubUI) run(w http.ResponseWriter, r *http.Request) {
	id, rest, slash := strings.Cut(strings.TrimPrefix(r.URL.Path, PathHubRun), "/")
	if !validRunID(id) {
		h.message(w, http.StatusNotFound, "No such run", "The hub follows no run of that name.")
		return
	}
	base := PathHubRun + id
	if !slash {
		// /p/<id> is the run's page at /p/<id>/, its query kept (ntfy's
		// Open, ?task=<ID>).
		to := base + "/"
		if q := r.URL.RawQuery; q != "" {
			to += "?" + q
		}
		http.Redirect(w, r, to, http.StatusSeeOther) //nolint:gosec // a path of this host: /p/ and a checked id, the query after it
		return
	}
	run, ok := h.src.Lookup(id)
	if api := strings.HasPrefix("/"+rest, "/api/"); api && (!ok || run.Ended) {
		// The script's stream or command: protocol 1's error.
		if !ok {
			writeReply(w, http.StatusNotFound, Reply{Type: TypeError, Error: "the hub follows no run " + id})
			return
		}
		writeReply(w, http.StatusGone, Reply{Type: TypeError, Error: "the run ended: " + run.Outcome.String()})
		return
	}
	switch {
	case !ok:
		h.message(w, http.StatusNotFound, "No such run", "The hub follows no run "+id+": it may have ended before the hub saw it, or the hub doesn't take its project.")
		return
	case run.Ended:
		h.message(w, http.StatusGone, "Run ended", run.Project+" ended: "+run.Outcome.Glyph()+" "+run.Outcome.String()+". Its pages closed with it.")
		return
	}
	switch "/" + rest {
	case PathEvents:
		if method(w, r, http.MethodGet) {
			h.runEvents(w, r, run.Run)
		}
		return
	case PathCommand:
		if method(w, r, http.MethodPost) {
			serveCommand(w, r, &h.tails, run.Run.Request)
		}
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path, r2.URL.RawPath = "/"+rest, ""
	h.pagesOf(id, run.Run).ServeHTTP(w, r2)
}

// pagesOf is the web UI of run under /p/<id>, made once per run.
func (h *hubUI) pagesOf(id string, run HubRunSource) http.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p, ok := h.pages[id]; ok && p.run == run {
		return p.ui
	}
	ui := NewUI(run, UIOptions{Now: h.now, Base: PathHubRun + id, Logf: h.logf})
	h.pages[id] = hubPages{run: run, ui: ui}
	return ui
}

// validRunID says id could name a run of the hub: lower case letters,
// digits and dashes, as internal/hub's RunID makes them.
func validRunID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// runEvents streams a run's protocol 1 to its page's script, as the
// listener of its own serve does.
func (h *hubUI) runEvents(w http.ResponseWriter, r *http.Request, run HubRunSource) {
	if h.streams.Add(1) > maxStreams {
		h.streams.Add(-1)
		writeReply(w, http.StatusServiceUnavailable, Reply{Type: TypeError, Error: "too many event streams at once"})
		return
	}
	defer h.streams.Add(-1)
	lines, cancel := run.Subscribe()
	defer cancel()
	check := streamCheckOf(r.Context())
	writeSSE(w, r, lines, check.ping, check.valid)
}

// events streams the hub's merged stream to the list's script.
func (h *hubUI) events(w http.ResponseWriter, r *http.Request) {
	if h.streams.Add(1) > maxStreams {
		h.streams.Add(-1)
		h.message(w, http.StatusServiceUnavailable, "Not now", "Too many event streams at once.")
		return
	}
	defer h.streams.Add(-1)
	changes, cancel := h.src.Subscribe()
	defer cancel()
	lines := make(chan []byte)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(lines)
		for c := range changes {
			b, err := json.Marshal(c)
			if err != nil {
				continue
			}
			select {
			case lines <- b:
			case <-stop:
				return
			}
		}
	}()
	check := streamCheckOf(r.Context())
	writeSSE(w, r, lines, check.ping, check.valid)
}

// hubView is the hub's list: a row per run, most urgent first, and the
// sheet with every run's questions.
type hubView struct {
	CSRF string
	// Live counts the runs that go on, Needs those of them that need the
	// owner.
	Live, Needs int
	Rows        []hubRow
	Sheet       []questionView
}

// Summary is the line under the title: `3 live runs · 1 needs you`.
func (v hubView) Summary() string {
	if v.Live == 0 {
		return "no live run"
	}
	s := look.Count(v.Live, "live run", "live runs")
	if v.Needs > 0 {
		s += fmt.Sprintf(" · %d %s", v.Needs, look.Plural(v.Needs, "needs you", "need you"))
	}
	return s
}

// hubRow is a run of the list: its project, status word and line, phase
// bar and elapsed time (SPEC §15.4).
type hubRow struct {
	ID, Project string
	Status      status
	// Glyph goes before the word (look.Status.Glyph): ! or ●.
	Glyph       string
	Phase       *Phase
	Done, Total int
	Segments    []segment
	// Elapsed is how long the run has run, from Started; none once it
	// ended.
	Elapsed string
	Started time.Time
	Ended   bool
}

// Rank orders the rows: what needs the owner, then what works, then
// what ended.
func (r hubRow) rank() int {
	switch {
	case r.Ended:
		return 2
	case r.Status.Tone == "needs":
		return 0
	}
	return 1
}

// view arranges the hub's runs for the list.
func (h *hubUI) view(r *http.Request) hubView {
	csrf := CSRFToken(r.Context())
	v := hubView{CSRF: csrf}
	// A run's view reads the request for its ?task=; the list has none.
	plain := r.Clone(r.Context())
	plain.URL = &url.URL{Path: "/"}
	type drawn struct {
		row   hubRow
		sheet []questionView
	}
	var all []drawn
	seen := map[string]bool{}
	for _, run := range h.src.Runs() {
		if seen[run.ID] {
			continue
		}
		seen[run.ID] = true
		row, sheet := h.rowOf(run, plain)
		all = append(all, drawn{row, sheet})
	}
	for _, id := range endedIDs(r) {
		if seen[id] {
			continue
		}
		seen[id] = true
		if run, ok := h.src.Lookup(id); ok && run.Ended {
			row, _ := h.rowOf(run, plain)
			all = append(all, drawn{row: row})
		}
	}
	slices.SortStableFunc(all, func(a, b drawn) int { return a.row.rank() - b.row.rank() })
	for _, d := range all {
		v.Rows = append(v.Rows, d.row)
		if !d.row.Ended {
			v.Live++
			if d.row.rank() == 0 {
				v.Needs++
			}
		}
		v.Sheet = append(v.Sheet, d.sheet...)
	}
	for i := range v.Sheet {
		q := &v.Sheet[i]
		q.Sheet = i == 0
		q.N, q.Of, q.NextN = i+1, len(v.Sheet), (i+1)%len(v.Sheet)+1
	}
	return v
}

// rowOf is run's row and its entries of the sheet, each named by its
// project and sent to it.
func (h *hubUI) rowOf(run HubRun, r *http.Request) (hubRow, []questionView) {
	u := &ui{src: run.Run, now: h.now}
	pv := u.view(r)
	row := hubRow{ID: run.ID, Project: run.Project, Status: pv.Status, Phase: pv.Phase, Done: pv.Done, Total: pv.Total,
		Segments: pv.Segments, Elapsed: pv.Elapsed, Started: pv.Snap.Started}
	if row.Project == "" {
		row.Project = "run"
	}
	if run.Ended || pv.Snap.Ended {
		row.Ended, row.Elapsed = true, ""
		if run.Ended {
			o := run.Outcome
			row.Status = status{Word: look.Done.String(), Line: o.Glyph() + " " + o.String(), Tone: outcomeTone(o)}
		}
		return row, nil
	}
	// The line's clock is the oldest question's; the run's own is
	// Elapsed, not said twice.
	if row.Status.Since.Equal(pv.Snap.Started) {
		row.Status.Ran = ""
	}
	for _, s := range []look.Status{look.Working, look.NeedsYou} {
		if row.Status.Word == s.String() {
			row.Glyph = s.Glyph()
		}
	}
	base := PathHubRun + run.ID
	sheet := pv.Sheet
	for i := range sheet {
		q := &sheet[i]
		q.Project, q.Base = row.Project, base
		q.Key = run.ID + " " + q.Key
		for j := range q.Answers {
			q.Answers[j].Base = base
		}
	}
	return row, sheet
}

// endedIDs are the runs the page saw end, from its endedField.
func endedIDs(r *http.Request) []string {
	var out []string
	for _, id := range strings.Split(r.URL.Query().Get(endedField), ",") {
		if validRunID(id) && len(out) < maxEnded {
			out = append(out, id)
		}
	}
	return out
}
