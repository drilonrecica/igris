package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/look"
	"github.com/drilonrecica/igris/internal/plan"
)

// hubCookie is the hub's cookie in these tests (internal/hub's CookieName).
const hubCookie = "igris_hub"

// hubServed is a run of the hub under test: an in-process Server seen as
// a hub's run, with the commands it was sent and a stream the test feeds.
type hubServed struct {
	*Server
	send  *sender
	mu    sync.Mutex
	lines []chan []byte
}

func (s *hubServed) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 16)
	ch <- []byte(`{"v":1,"type":"snapshot"}`)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, ch)
	return ch, func() {}
}

// fakeHub is the hub as its pages read it.
type fakeHub struct {
	mu   sync.Mutex
	runs []HubRun
	gone map[string]HubRun
	subs []chan HubChange
	// subscribed is told of every reader of the merged stream.
	subscribed chan struct{}
}

func (f *fakeHub) Runs() []HubRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]HubRun(nil), f.runs...)
}

func (f *fakeHub) Lookup(id string) (HubRun, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.runs {
		if r.ID == id {
			return r, true
		}
	}
	r, ok := f.gone[id]
	return r, ok
}

func (f *fakeHub) Subscribe() (<-chan HubChange, func()) {
	ch := make(chan HubChange, 16)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, ch)
	select {
	case f.subscribed <- struct{}{}:
	default:
	}
	return ch, func() {}
}

func (f *fakeHub) publish(c HubChange) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		ch <- c
	}
}

// end moves run id from the live runs to the ended ones.
func (f *fakeHub) end(id string, o look.Outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, r := range f.runs {
		if r.ID == id {
			r.Ended, r.Outcome = true, o
			if f.gone == nil {
				f.gone = map[string]HubRun{}
			}
			f.gone[id] = r
			f.runs = append(f.runs[:i], f.runs[i+1:]...)
			return
		}
	}
}

// hubWeb is the hub's listener under test.
type hubWeb struct {
	t   *testing.T
	hub *fakeHub
	ts  *httptest.Server
	ran map[string]*hubServed // by id
}

// hubNow is the hub tests' clock: 4m12s into the runs.
func hubNow() time.Time { return uiT0.Add(4*time.Minute + 12*time.Second) }

func newHubWeb(t *testing.T) *hubWeb {
	t.Helper()
	w := &hubWeb{t: t, hub: &fakeHub{subscribed: make(chan struct{}, 4)}, ran: map[string]*hubServed{}}
	w.ts = httptest.NewServer(Guard(GuardOptions{
		Token: func() (string, error) { return goodToken, nil }, Cookie: hubCookie, Pair: "igris hub token",
		Next: NewHubUI(w.hub, HubUIOptions{Now: hubNow}),
	}))
	t.Cleanup(w.ts.Close)
	return w
}

// add starts a run of project with the plan, publishes evs into it and
// puts it in the hub as id.
func (w *hubWeb) add(id, project string, evs ...engine.Event) *hubServed {
	w.t.Helper()
	path := filepath.Join(w.t.TempDir(), "tasks.md")
	if err := os.WriteFile(path, []byte(uiPlan), 0o600); err != nil {
		w.t.Fatal(err)
	}
	s := &hubServed{send: &sender{}}
	s.Server = New(Options{Version: "1.11.0", Project: project, Backend: "fake", Mode: "accept", Sender: s.send,
		Plan: func() (*plan.Plan, error) { return plan.Load(path, plan.Options{}) }})
	w.t.Cleanup(func() { _ = s.Close() })
	for _, ev := range evs {
		s.Publish(ev)
	}
	w.hub.mu.Lock()
	w.hub.runs = append(w.hub.runs, HubRun{ID: id, Project: project, Run: s})
	w.hub.mu.Unlock()
	w.ran[id] = s
	return s
}

func (w *hubWeb) do(method, path, ctype, body string, hdr map[string]string) *http.Response {
	w.t.Helper()
	req, err := http.NewRequest(method, w.ts.URL+path, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: hubCookie, Value: goodToken}) //nolint:gosec // a client's cookie
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (w *hubWeb) get(path string) (int, string, http.Header) {
	w.t.Helper()
	resp := w.do(http.MethodGet, path, "", "", nil)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		w.t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

func (w *hubWeb) postForm(path string, f url.Values) (int, string, http.Header) {
	w.t.Helper()
	f.Set(CSRFField, csrfFor(goodToken))
	resp := w.do(http.MethodPost, path, "application/x-www-form-urlencoded", f.Encode(), nil)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		w.t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

func (w *hubWeb) command(path, body string) (int, Reply) {
	w.t.Helper()
	resp := w.do(http.MethodPost, path, "application/json", body, map[string]string{CSRFHeader: csrfFor(goodToken)})
	var rep Reply
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		w.t.Fatalf("POST %s: %v", path, err)
	}
	return resp.StatusCode, rep
}

// The runs of the hub tests, each from the shared plan's phase P1.
var (
	hubStart = []engine.Event{{At: uiT0, Kind: engine.RunStarted, Run: "r1", Phase: "P1", Detail: "phase P1"}}
	// hubWorking: P1-02 runs.
	hubWorking = append(slicesOf(hubStart),
		engine.Event{At: uiT0.Add(time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-02", Title: "Parser", Rank: "opus", Model: "claude-opus-5-5", Mode: "plan"})
	// hubCommit: P1-03 asks to commit.
	hubCommit = append(slicesOf(hubStart),
		engine.Event{At: uiT0.Add(time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Rank: "sonnet", Model: "claude-sonnet-5-5"},
		engine.Event{At: uiT0.Add(3 * time.Minute), Kind: engine.Asked, Phase: "P1", Task: "P1-03", Question: engine.QuestionCommit, Detail: `commit the changes of P1-03 as "P1-03: Writer"?`})
	// hubTwoAsks: P1-02 presents a plan, P1-03 is blocked in its pane.
	hubTwoAsks = append(slicesOf(hubStart),
		engine.Event{At: uiT0.Add(time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-02", Title: "Parser", Rank: "opus", Model: "claude-opus-5-5", Mode: "plan"},
		engine.Event{At: uiT0.Add(2 * time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Rank: "sonnet", Model: "claude-sonnet-5-5"},
		engine.Event{At: uiT0.Add(time.Minute), Kind: engine.Asked, Phase: "P1", Task: "P1-02", Question: engine.QuestionPlanApproval, Then: "accept", Plan: "# Plan\n\n1. parse\n2. test it"},
		engine.Event{At: uiT0.Add(2 * time.Minute), Kind: engine.NeedsYou, Task: "P1-03", Detail: "waiting for a permission or an answer"})
	// hubPaused: the pause took effect before any task.
	hubPaused = append(slicesOf(hubStart), engine.Event{At: uiT0.Add(time.Second), Kind: engine.PauseOn})
)

func slicesOf(evs []engine.Event) []engine.Event { return append([]engine.Event(nil), evs...) }

// renderHubPages renders every golden page of the hub.
func renderHubPages(t *testing.T) map[string]string {
	t.Helper()
	pages := map[string]string{}
	page := func(name string, w *hubWeb, path string, want int) {
		t.Helper()
		status, body, hdr := w.get(path)
		if status != want || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
			t.Fatalf("%s: GET %s = %d %s, want %d", name, path, status, hdr.Get("Content-Type"), want)
		}
		pages[name] = body
	}

	w := newHubWeb(t)
	page("hub-none.html", w, "/", http.StatusOK)

	w = newHubWeb(t)
	w.add("sinjal-0a1b2c", "sinjal", hubWorking...)
	page("hub-one.html", w, "/", http.StatusOK)

	w = newHubWeb(t)
	w.add("alpha-111111", "alpha", hubWorking...)
	w.add("beta-222222", "beta", hubPaused...)
	w.add("gamma-333333", "gamma", hubStart...)
	page("hub-three.html", w, "/", http.StatusOK)

	// The run that needs you leads, though it started last.
	w = newHubWeb(t)
	w.add("alpha-111111", "alpha", hubWorking...)
	w.add("beta-222222", "beta", hubCommit...)
	page("hub-needs.html", w, "/", http.StatusOK)

	// A run that ended is drawn for a page that saw it end (?ended=), last,
	// with its outcome; not for a page drawn afresh.
	w = newHubWeb(t)
	w.add("alpha-111111", "alpha", hubWorking...)
	w.add("beta-222222", "beta", append(slicesOf(hubWorking), engine.Event{At: uiT0.Add(4 * time.Minute), Kind: engine.TaskDone, Task: "P1-02"})...)
	w.add("gamma-333333", "gamma", hubWorking...)
	w.hub.end("beta-222222", look.Completed)
	page("hub-ended.html", w, PathLive+"?ended=beta-222222,nobody-000000", http.StatusOK)

	// One sheet for every run's questions: 1 of 3 across two projects.
	w = newHubWeb(t)
	w.add("alpha-111111", "alpha", hubWorking...)
	w.add("beta-222222", "beta", hubTwoAsks...)
	w.add("gamma-333333", "gamma", hubCommit...)
	page("hub-sheet.html", w, "/", http.StatusOK)
	page("hub-sheet-live.html", w, PathLive, http.StatusOK)
	// The wide layout's pane: the run asked for (?run=), beside the list.
	page("hub-pick.html", w, "/?run=alpha-111111", http.StatusOK)
	page("hub-pick-live.html", w, PathLive+"?run=gamma-333333", http.StatusOK)
	page("hub-run.html", w, "/p/gamma-333333/", http.StatusOK)
	page("hub-unknown.html", w, "/p/nobody-000000/", http.StatusNotFound)
	w.hub.end("alpha-111111", look.Stopped)
	page("hub-run-ended.html", w, "/p/alpha-111111/", http.StatusGone)
	return pages
}

// The hub's pages as rendered, against their golden files. The light and
// dark themes are the stylesheet's (prefers-color-scheme): the markup is
// the same.
func TestHubPages(t *testing.T) {
	for name, body := range renderHubPages(t) {
		golden(t, name, body)
	}
}

// The hub's pages keep the CSP (no inline script, handler or style),
// escape what runs tell, offer no skip-permissions mode, and every form
// of the sheet posts to its own run; Stop, Skip and Done ask a second
// confirmation there too.
func TestHubPagesSafe(t *testing.T) {
	pages := renderHubPages(t)
	for name, body := range pages {
		for _, tag := range scriptTag.FindAllString(body, -1) {
			if !regexp.MustCompile(`^<script src="(/p/[a-z0-9-]+)?/static/app\.js" defer>$`).MatchString(tag) {
				t.Errorf("%s: script tag %s", name, tag)
			}
		}
		for _, bad := range []string{"style=", "javascript:", "<style", "http://", "https://"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s holds %q", name, bad)
			}
		}
		if m := inlineEvent.FindString(body); m != "" {
			t.Errorf("%s: event handler attribute %q", name, m)
		}
		for _, mode := range []string{"yolo", "plan:yolo"} {
			if strings.Contains(body, ">"+mode+"</option>") {
				t.Errorf("%s offers %s", name, mode)
			}
		}
	}
	sheet := pages["hub-sheet.html"]
	sections := regexp.MustCompile(`(?s)<section class="question[^"]*"[^>]*data-base="(/p/[a-z0-9-]+)">.*?</section>`).FindAllStringSubmatch(sheet, -1)
	if len(sections) != 3 {
		t.Fatalf("%d entries on the sheet, want 3", len(sections))
	}
	typeOf := regexp.MustCompile(`name="type" value="([a-z_]+)"`)
	for _, s := range sections {
		forms := regexp.MustCompile(`(?s)<form .*?</form>`).FindAllString(s[0], -1)
		if len(forms) == 0 {
			t.Fatalf("an entry without answers: %s", s[0])
		}
		for _, f := range forms {
			if !strings.Contains(f, `action="`+s[1]+`/act"`) {
				t.Errorf("a form of %s posts elsewhere: %s", s[1], f)
			}
			typ := typeOf.FindStringSubmatch(f)[1]
			_, wants := confirmOf(typ, "")
			if has := strings.Contains(f, `data-confirm="1"`); has != wants {
				t.Errorf("%s form of %s: confirmation = %v, want %v", typ, s[1], has, wants)
			}
		}
	}
	for _, want := range []string{"1 of 3 ›", "2 of 3 ›", "3 of 3 ›", `<span class="project">beta</span>`, `<span class="project">gamma</span>`} {
		if !strings.Contains(sheet, want) {
			t.Errorf("the sheet has no %q", want)
		}
	}
	if strings.Count(sheet, `class="question sheet"`) != 1 {
		t.Error("not exactly one entry drawn as the sheet")
	}
}

// The list orders the runs by urgency and keeps an ended run only for a
// page that saw it end.
func TestHubOrder(t *testing.T) {
	w := newHubWeb(t)
	w.add("alpha-111111", "alpha", hubWorking...)
	w.add("beta-222222", "beta", hubWorking...)
	w.add("gamma-333333", "gamma", hubCommit...)
	w.hub.end("alpha-111111", look.Failed)
	row := regexp.MustCompile(`id="run-([a-z0-9-]+)"`)
	order := func(path string) []string {
		_, body, _ := w.get(path)
		var out []string
		for _, m := range row.FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return out
	}
	if got := strings.Join(order("/"), " "); got != "gamma-333333 beta-222222" {
		t.Errorf("fresh page: %s", got)
	}
	if got := strings.Join(order("/live?ended=alpha-111111"), " "); got != "gamma-333333 beta-222222 alpha-111111" {
		t.Errorf("after the end: %s", got)
	}
	_, body, _ := w.get("/live?ended=alpha-111111")
	if !strings.Contains(body, "✗ failed") || !strings.Contains(body, "2 live runs · 1 needs you") {
		t.Errorf("the ended row or the summary: %s", body)
	}
}

// Every action goes to its run as its protocol-1 command, through the
// API and the forms alike; a skip-permissions mode is refused, and Stop
// asks first.
func TestHubActions(t *testing.T) {
	w := newHubWeb(t)
	a := w.add("alpha-111111", "alpha", hubWorking...)
	b := w.add("beta-222222", "beta", hubCommit...)

	status, rep := w.command("/p/beta-222222"+PathCommand, `{"v":1,"type":"answer","id":"w1","task":"P1-03","yes":true}`)
	if status != http.StatusOK || rep.Type != TypeAck {
		t.Fatalf("answer: %d %+v", status, rep)
	}
	if got := b.send.got(); len(got) != 1 || got[0].Kind != engine.CmdAnswer || got[0].Task != "P1-03" {
		t.Errorf("beta was sent %+v", got)
	}
	if got := a.send.got(); len(got) != 0 {
		t.Errorf("alpha was sent %+v", got)
	}
	for _, body := range []string{`{"v":1,"type":"mode","id":"w2","text":"yolo","yes":true}`, `{"v":1,"type":"task_mode","id":"w3","task":"P1-04","text":"plan:yolo","yes":true}`} {
		if status, rep := w.command("/p/alpha-111111"+PathCommand, body); status != http.StatusForbidden || rep.Type != TypeError {
			t.Errorf("%s: %d %+v", body, status, rep)
		}
	}
	if status, _, _ := w.postForm("/p/alpha-111111"+PathAct, url.Values{"type": {CmdMode}, "text": {"yolo"}}); status != http.StatusForbidden {
		t.Errorf("form yolo: %d", status)
	}
	if got := a.send.got(); len(got) != 0 {
		t.Errorf("alpha was sent %+v", got)
	}
	// Without the CSRF token nothing is sent.
	resp := w.do(http.MethodPost, "/p/alpha-111111"+PathCommand, "application/json", `{"v":1,"type":"pause","id":"w4"}`, nil)
	if resp.StatusCode != http.StatusForbidden || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("no CSRF: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// Stop asks first, posting to its run; confirmed, it goes there and
	// the browser back to the run's page.
	status, body, _ := w.postForm("/p/alpha-111111"+PathAct, url.Values{"type": {CmdStop}})
	if status != http.StatusOK || !strings.Contains(body, `action="/p/alpha-111111/act"`) || !strings.Contains(body, "Stop igris?") {
		t.Errorf("stop: %d %s", status, body)
	}
	status, _, hdr := w.postForm("/p/alpha-111111"+PathAct, url.Values{"type": {CmdStop}, confirmedField: {"1"}})
	if status != http.StatusSeeOther || hdr.Get("Location") != "/p/alpha-111111/" {
		t.Errorf("stop confirmed: %d to %q", status, hdr.Get("Location"))
	}
	if got := a.send.got(); len(got) != 1 || got[0].Kind != engine.CmdStop {
		t.Errorf("alpha was sent %+v", got)
	}
	// An ended run takes nothing.
	w.hub.end("alpha-111111", look.Stopped)
	if status, rep := w.command("/p/alpha-111111"+PathCommand, `{"v":1,"type":"pause","id":"w5"}`); status != http.StatusGone || rep.Type != TypeError {
		t.Errorf("ended run: %d %+v", status, rep)
	}
}

// A run's pages under /p/<id>/: the base on every path, ?task= opening
// that question, /p/<id> sent on to /p/<id>/, and unknown or ended ids
// saying so with the way back to the runs.
func TestHubRunPages(t *testing.T) {
	w := newHubWeb(t)
	w.add("beta-222222", "beta", hubTwoAsks...)
	_, body, _ := w.get("/p/beta-222222/?task=P1-03")
	if !regexp.MustCompile(`class="question sheet"[^>]*data-task="P1-03"`).MatchString(body) {
		t.Errorf("?task=P1-03 doesn't open its entry")
	}
	for _, m := range uiPath.FindAllStringSubmatch(body, -1) {
		if !strings.HasPrefix(m[1], "/p/beta-222222") {
			t.Errorf("path %q not under the run's base", m[1])
		}
	}
	for _, p := range []string{PathLive, PathPlan, PathPlanLive} {
		if status, body, _ := w.get("/p/beta-222222" + p); status != http.StatusOK || !strings.Contains(body, "beta") {
			t.Errorf("GET %s: %d", p, status)
		}
	}
	status, _, hdr := w.get("/p/beta-222222?task=P1-02")
	if status != http.StatusSeeOther || hdr.Get("Location") != "/p/beta-222222/?task=P1-02" {
		t.Errorf("/p/<id>: %d to %q", status, hdr.Get("Location"))
	}
	for _, p := range []string{"/p/nobody-000000/", "/p/Bad_ID/", "/p/x/../../etc"} {
		status, body, _ := w.get(p)
		if status != http.StatusNotFound || !strings.Contains(body, `href="/"`) || !strings.Contains(body, "Back to the runs") {
			t.Errorf("GET %s: %d %s", p, status, body)
		}
	}
	if status, _, _ := w.get("/nowhere"); status != http.StatusNotFound {
		t.Errorf("GET /nowhere: %d", status)
	}
	if status, body, _ := w.get("/static/app.js"); status != http.StatusOK || !strings.Contains(body, "dataset.hub") {
		t.Errorf("the script: %d", status)
	}
}

// sseData reads the next data line of an event stream.
func sseData(t *testing.T, sc *bufio.Scanner) string {
	t.Helper()
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			return d
		}
	}
	t.Fatalf("the stream ended: %v", sc.Err())
	return ""
}

// The list follows the hub's merged stream, a run's page its run's
// protocol-1 stream; neither has the write deadline of a page.
func TestHubStreams(t *testing.T) {
	w := newHubWeb(t)
	w.add("alpha-111111", "alpha", hubWorking...)
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	open := func(path string) *bufio.Scanner {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: hubCookie, Value: goodToken}) //nolint:gosec // a client's cookie
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("GET %s: %d %s", path, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		return bufio.NewScanner(resp.Body)
	}
	run := open("/p/alpha-111111" + PathEvents)
	if d := sseData(t, run); !strings.Contains(d, `"snapshot"`) {
		t.Errorf("the run's stream starts with %s", d)
	}
	hub := open(PathHubEvents)
	select {
	case <-w.hub.subscribed:
	case <-ctx.Done():
		t.Fatal("the hub's stream never subscribed")
	}
	w.hub.publish(HubChange{Run: "alpha-111111", Change: HubLeft, Outcome: "completed"})
	var c HubChange
	if err := json.Unmarshal([]byte(sseData(t, hub)), &c); err != nil || c != (HubChange{Run: "alpha-111111", Change: HubLeft, Outcome: "completed"}) {
		t.Errorf("hub change %+v %v", c, err)
	}
}

// panePart is the pane of a hub page: the chosen run's page beside the
// list (the live part's last section), and the run it is of.
func panePart(t *testing.T, body string) (run, pane string) {
	t.Helper()
	i := strings.Index(body, `<section class="pane"`)
	if i < 0 {
		return "", ""
	}
	pane = body[i:]
	if j := strings.Index(pane, "</main>"); j >= 0 {
		pane = pane[:j]
	}
	m := regexp.MustCompile(`^<section class="pane" aria-label="Run [^"]+" data-run="([a-z0-9-]+)" data-base="/p/([a-z0-9-]+)">`).FindStringSubmatch(pane)
	if m == nil || m[1] != m[2] {
		t.Fatalf("the pane's head: %.200s", pane)
	}
	return m[1], pane
}

// The wide layout's pane (SPEC §15.8): the run ?run= asks for while it
// goes on, else the first that needs the owner, else the first; marked
// in the list; drawn with the run's base and without its own sheet, its
// cards linking to their entries of the list's sheet.
func TestHubPane(t *testing.T) {
	w := newHubWeb(t)
	if _, body, _ := w.get("/"); strings.Contains(body, `class="pane"`) || strings.Contains(body, "aria-current") {
		t.Errorf("a pane with no run")
	}
	w.add("alpha-111111", "alpha", hubWorking...)
	w.add("beta-222222", "beta", hubTwoAsks...)
	w.add("gamma-333333", "gamma", hubCommit...)
	w.add("delta-444444", "delta", hubWorking...)
	for _, c := range []struct{ path, want string }{
		{"/", "beta-222222"},
		{PathLive, "beta-222222"},
		{"/?run=alpha-111111", "alpha-111111"},
		{PathLive + "?run=gamma-333333", "gamma-333333"},
		{"/?run=nobody-000000", "beta-222222"},
		{"/?run=%3Cb%3E", "beta-222222"},
	} {
		_, body, _ := w.get(c.path)
		run, _ := panePart(t, body)
		if run != c.want {
			t.Errorf("GET %s: the pane is %q, want %q", c.path, run, c.want)
		}
		if cur := regexp.MustCompile(`data-run="([a-z0-9-]+)" aria-current="true"`).FindAllStringSubmatch(body, -1); len(cur) != 1 || cur[0][1] != c.want {
			t.Errorf("GET %s: marked in the list %v, want %s", c.path, cur, c.want)
		}
	}

	_, body, _ := w.get("/?run=beta-222222")
	_, pane := panePart(t, body)
	if strings.Contains(pane, `class="question`) || strings.Contains(pane, "wordmark") {
		t.Errorf("the pane draws its own sheet or brand:\n%s", pane)
	}
	for _, m := range uiPath.FindAllStringSubmatch(pane, -1) {
		if !strings.HasPrefix(m[1], "/p/beta-222222") {
			t.Errorf("path %q of the pane not under its run's base", m[1])
		}
	}
	if !strings.Contains(pane, `<nav class="bar"`) || !strings.Contains(pane, `value="stop"`) || !strings.Contains(pane, `data-confirm="1"`) {
		t.Errorf("the pane has no Plan, Pause and Stop, or Stop doesn't confirm")
	}
	// Each card of beta that asks links to its own entry of the list's
	// sheet, numbered across every run.
	for _, task := range []string{"P1-02", "P1-03"} {
		m := regexp.MustCompile(`(?s)id="card-` + task + `".*?class="to-sheet" href="#(q-\d+)"`).FindStringSubmatch(pane)
		if m == nil {
			t.Errorf("card %s doesn't link to the sheet", task)
			continue
		}
		if !regexp.MustCompile(`id="` + m[1] + `" data-key="beta-222222 [^"]*" data-task="` + task + `"`).MatchString(body) {
			t.Errorf("card %s links to %s, not its entry", task, m[1])
		}
	}

	// A run that ended is not shown beside the list.
	w.hub.end("beta-222222", look.Completed)
	_, body, _ = w.get("/?run=beta-222222&ended=beta-222222")
	if run, _ := panePart(t, body); run != "gamma-333333" {
		t.Errorf("the pane after beta ended is %q, want gamma-333333", run)
	}
}
