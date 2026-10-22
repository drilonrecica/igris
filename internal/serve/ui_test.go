package serve

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/plan"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// uiT0 is the web UI tests' clock.
var uiT0 = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

// uiPlan is a synthetic plan: a phase with a done task, two
// agent tasks running, an agent task to do and a user task.
const uiPlan = "## P1 — Parser <i>and</i> writer\n\n" +
	"| ID | Task | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|---|\n" +
	"| P1-01 | Go module | — | done | sonnet | agent | — |\n" +
	"| P1-02 | <b>Parser</b> | P1-01 | in progress | opus | agent | plan |\n" +
	"| P1-03 | Writer | P1-01 | in progress | sonnet | agent | — |\n" +
	"| P1-04 | Docs | P1-02 | blocked | sonnet | agent | — |\n" +
	"| P1-05 | Owner check | P1-03 | blocked | — | user | — |\n\n" +
	"## P2 — Later\n\n" +
	"| ID | Task | Deps | Status | Model | Owner | Mode |\n|---|---|---|---|---|---|---|\n" +
	"| P2-01 | Release | P1-05 | blocked | — | user | — |\n"

// uiWeb is the HTTP listener with the web UI under test.
func uiWeb(t *testing.T, mode string, withPlan bool) *web {
	t.Helper()
	w := &web{t: t, send: &sender{}, clock: &clock{t: uiT0}, token: &tokenBox{tok: goodToken}}
	so := Options{Version: "1.6.0", Project: "demo", Backend: "fake", Mode: mode, Sender: w.send}
	if withPlan {
		path := filepath.Join(t.TempDir(), "tasks.md")
		if err := os.WriteFile(path, []byte(uiPlan), 0o600); err != nil {
			t.Fatal(err)
		}
		so.Plan = func() (*plan.Plan, error) { return plan.Load(path, plan.Options{}) }
	}
	w.srv = New(so)
	now := func() time.Time { return uiT0.Add(4*time.Minute + 12*time.Second) }
	w.ts = httptest.NewServer(w.srv.Remote(RemoteOptions{Token: w.token.get, Now: w.clock.now, UI: w.srv.UI(UIOptions{Now: now})}))
	t.Cleanup(func() {
		w.ts.Close()
		_ = w.srv.Close()
	})
	return w
}

// running publishes a parallel run with a plan to approve, a commit
// question, a task mode and a reset request.
func running(s *Server) {
	at := func(d time.Duration) time.Time { return uiT0.Add(d) }
	slot := func(n int) *int { return &n }
	for _, ev := range []engine.Event{
		{At: at(0), Kind: engine.RunStarted, Run: "r1", Phase: "P1", Detail: "phase P1"},
		{At: at(time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-02", Title: "<b>Parser</b>", Rank: "opus", Model: "claude-opus-5-5", Mode: "plan", Slot: slot(0)},
		{At: at(2 * time.Second), Kind: engine.TaskStarted, Phase: "P1", Task: "P1-03", Title: "Writer", Rank: "sonnet", Model: "claude-sonnet-5-5", Mode: "accept", Slot: slot(1)},
		{At: at(time.Minute), Kind: engine.Asked, Phase: "P1", Task: "P1-02", Question: engine.QuestionPlanApproval, Then: "accept",
			Plan: "# Plan\n\n1. parse <script>alert(1)</script>\n2. test it"},
		{At: at(2 * time.Minute), Kind: engine.Asked, Phase: "P1", Task: "P1-03", Question: engine.QuestionCommit, Detail: `commit the changes of P1-03 as "P1-03: Writer"?`},
		{At: at(3 * time.Minute), Kind: engine.TaskModeChanged, Phase: "P1", Task: "P1-04", Detail: "auto"},
		{At: at(3 * time.Minute), Kind: engine.Warning, Phase: "P1", Detail: "ignored mode: unknown run mode \"fast\""},
		{At: at(4 * time.Minute), Kind: engine.Asked, Phase: "P1", Task: "P1-04", Question: engine.QuestionConfirmReset, Detail: "reset P1-04 to blocked? the session's reason: wrong deps"},
	} {
		s.Publish(ev)
	}
}

// get fetches path with the cookie and returns the status and the body.
func (w *web) get(path, auth string) (int, string, http.Header) {
	w.t.Helper()
	resp := w.do(http.MethodGet, path, auth, "", nil)
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		w.t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

// postForm posts a form with the cookie and the CSRF token, unless the
// form sets its own.
func (w *web) postForm(path string, f url.Values) (int, string, http.Header) {
	w.t.Helper()
	if _, ok := f[CSRFField]; !ok {
		f.Set(CSRFField, csrfFor(goodToken))
	}
	req, err := http.NewRequest(http.MethodPost, w.ts.URL+path, strings.NewReader(f.Encode()))
	if err != nil {
		w.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: CookieName, Value: goodToken}) //nolint:gosec // a client's cookie
	resp, err := noRedirect.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		w.t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "web", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil { //nolint:gosec // a golden file
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a golden file of this test
	if err != nil {
		t.Fatalf("%v (run go test ./internal/serve -run TestUIPages -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file; run with -update and review the diff\n--- got ---\n%s", name, got)
	}
}

// uiPages renders every golden page.
func uiPages(t *testing.T) map[string]string {
	t.Helper()
	pages := map[string]string{}
	page := func(name string, w *web, path string) {
		status, body, hdr := w.get(path, "cookie")
		if status != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
			t.Fatalf("%s: GET %s = %d %s", name, path, status, hdr.Get("Content-Type"))
		}
		pages[name] = body
	}

	w := uiWeb(t, "accept", true)
	running(w.srv)
	page("running.html", w, "/")
	page("running-live.html", w, PathLive)
	status, body, _ := w.postForm(PathAct, url.Values{"type": {CmdSkip}, "task": {"P1-03"}, "text": {"not <needed>"}})
	if status != http.StatusOK {
		t.Fatalf("skip without confirmation = %d", status)
	}
	pages["confirm-skip.html"] = body
	status, body, _ = w.postForm(PathAct, url.Values{"type": {CmdMode}, "text": {"yolo"}})
	if status != http.StatusForbidden {
		t.Fatalf("mode yolo = %d", status)
	}
	pages["refused-yolo.html"] = body

	w = uiWeb(t, "yolo", true)
	for _, ev := range []engine.Event{
		{At: uiT0, Kind: engine.RunStarted, Run: "r2", Phase: "P1"},
		{At: uiT0, Kind: engine.PauseOn},
		{At: uiT0.Add(time.Minute), Kind: engine.TaskStarted, Task: "P1-05", Title: "Owner check"},
		{At: uiT0.Add(time.Minute), Kind: engine.YourTurn, Task: "P1-05", Detail: "check it by hand"},
	} {
		w.srv.Publish(ev)
	}
	page("yolo-user.html", w, "/")

	w = uiWeb(t, "plan", true)
	for _, ev := range []engine.Event{
		{At: uiT0, Kind: engine.RunStarted, Run: "r3", Phase: "P1"},
		{At: uiT0, Kind: engine.TaskStarted, Task: "P1-02", Title: "Parser", Rank: "opus", Model: "claude-opus-5-5", Mode: "plan"},
		{At: uiT0.Add(time.Minute), Kind: engine.SessionLost, Task: "P1-02", Detail: "the pane is gone"},
		{At: uiT0.Add(time.Minute), Kind: engine.Asked, Task: "P1-02", Question: engine.QuestionSessionLost, Detail: "the session of P1-02 is gone: start fresh or continue, mark the task done, skip it, or stop"},
	} {
		w.srv.Publish(ev)
	}
	page("session-lost.html", w, "/")

	w = uiWeb(t, "plan", true)
	for _, ev := range []engine.Event{
		{At: uiT0, Kind: engine.RunStarted, Run: "r4", Phase: "P1"},
		{At: uiT0, Kind: engine.TaskStarted, Task: "P1-02", Title: "Parser", Rank: "opus", Model: "claude-opus-5-5", Mode: "plan"},
		{At: uiT0.Add(time.Minute), Kind: engine.TaskDone, Task: "P1-02"},
		{At: uiT0.Add(time.Minute), Kind: engine.RunStopped, Detail: "stopped"},
	} {
		w.srv.Publish(ev)
	}
	page("ended.html", w, "/")

	w = uiWeb(t, "", false)
	page("empty.html", w, "/")
	return pages
}

// The pages as rendered, against their golden files.
func TestUIPages(t *testing.T) {
	for name, body := range uiPages(t) {
		golden(t, name, body)
	}
}

var (
	scriptTag   = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	inlineEvent = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
)

// No page has an inline script, an event handler attribute or a style
// attribute: the CSP allows neither (SPEC §14, §15.8). And none offers a
// skip-permissions mode.
func TestUIPagesKeepTheCSPAndNoYolo(t *testing.T) {
	for name, body := range uiPages(t) {
		for _, tag := range scriptTag.FindAllString(body, -1) {
			if tag != `<script src="/static/app.js" defer>` {
				t.Errorf("%s: script tag %s", name, tag)
			}
		}
		if strings.Count(body, "<script") != strings.Count(body, `<script src="/static/app.js" defer></script>`) {
			t.Errorf("%s: a script with content", name)
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
			if strings.Contains(body, "<option>"+mode+"</option>") || strings.Contains(body, "<option selected>"+mode+"</option>") {
				t.Errorf("%s offers %s", name, mode)
			}
		}
		if strings.Contains(body, "<script>alert") || strings.Contains(body, "<b>Parser") || strings.Contains(body, "<i>and") {
			t.Errorf("%s: plan or event text not escaped", name)
		}
	}
	w := uiWeb(t, "accept", true)
	_, _, hdr := w.get("/", "cookie")
	if csp := hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP = %q", csp)
	}
}

// Stop, skip and done ask a second confirmation in the page's forms;
// nothing else does.
func TestUISecondConfirmations(t *testing.T) {
	body := uiPages(t)["running.html"]
	forms := regexp.MustCompile(`(?s)<form .*?</form>`).FindAllString(body, -1)
	if len(forms) == 0 {
		t.Fatal("no forms")
	}
	typeOf := regexp.MustCompile(`name="type" value="([a-z_]+)"`)
	for _, f := range forms {
		m := typeOf.FindStringSubmatch(f)
		if m == nil {
			t.Fatalf("form without a type: %s", f)
		}
		_, wants := confirmOf(m[1], "")
		if has := strings.Contains(f, `data-confirm="1"`) && strings.Contains(f, `name="confirmed" value="1"`); has != wants {
			t.Errorf("%s form: confirmation = %v, want %v", m[1], has, wants)
		}
	}
}

// The forms post protocol-1 commands without JavaScript: the second
// confirmation first where one is asked, then the command, and back to
// the page. What the API refuses, the form is refused too.
func TestUIFormActions(t *testing.T) {
	tests := []struct {
		name   string
		form   url.Values
		status int
		want   *engine.Command
		body   string // the page says this
	}{
		{"pause", url.Values{"type": {CmdPause}}, http.StatusSeeOther, &engine.Command{Kind: engine.CmdPause}, ""},
		{"stop asks first", url.Values{"type": {CmdStop}}, http.StatusOK, nil, "Stop igris?"},
		{"stop confirmed", url.Values{"type": {CmdStop}, confirmedField: {"1"}}, http.StatusSeeOther, &engine.Command{Kind: engine.CmdStop}, ""},
		{"skip asks first", url.Values{"type": {CmdSkip}, "task": {"P1-03"}, "text": {"later"}}, http.StatusOK, nil, `name="text" value="later"`},
		{"skip confirmed", url.Values{"type": {CmdSkip}, "task": {"P1-03"}, "text": {"later"}, confirmedField: {"1"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdSkip, Task: "P1-03", Text: "later"}, ""},
		{"done asks first", url.Values{"type": {CmdDone}, "task": {"P1-02"}}, http.StatusOK, nil, "Mark P1-02 done?"},
		{"done confirmed", url.Values{"type": {CmdDone}, "task": {"P1-02"}, "text": {"by hand"}, confirmedField: {"1"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdDone, Task: "P1-02", Text: "by hand"}, ""},
		{"commit", url.Values{"type": {CmdAnswer}, "task": {"P1-03"}, "yes": {"1"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdAnswer, Task: "P1-03", Yes: true}, ""},
		{"reset kept", url.Values{"type": {CmdAnswer}, "task": {"P1-04"}, "question": {"confirm_reset"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdAnswer, Task: "P1-04", Question: engine.QuestionConfirmReset}, ""},
		{"retry continuing", url.Values{"type": {CmdRetry}, "task": {"P1-02"}, "continue": {"1"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdRetry, Task: "P1-02", Continue: true}, ""},
		{"plan approved", url.Values{"type": {CmdPlan}, "task": {"P1-02"}, "plan": {"allow"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdPlan, Task: "P1-02", Plan: "allow"}, ""},
		{"plan rejected", url.Values{"type": {CmdPlan}, "task": {"P1-02"}, "plan": {"deny"}, "text": {"split it"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdPlan, Task: "P1-02", Plan: "deny", Text: "split it"}, ""},
		{"plan rejected without feedback", url.Values{"type": {CmdPlan}, "task": {"P1-02"}, "plan": {"deny"}}, http.StatusBadRequest, nil, "feedback"},
		{"mode", url.Values{"type": {CmdMode}, "text": {"auto"}}, http.StatusSeeOther, &engine.Command{Kind: engine.CmdMode, Text: "auto"}, ""},
		{"task mode", url.Values{"type": {CmdTaskMode}, "task": {"P1-04"}, "text": {"manual"}}, http.StatusSeeOther,
			&engine.Command{Kind: engine.CmdTaskMode, Task: "P1-04", Text: "manual"}, ""},
		{"mode yolo", url.Values{"type": {CmdMode}, "text": {"yolo"}}, http.StatusForbidden, nil, "never set remotely"},
		{"mode plan:yolo with yes", url.Values{"type": {CmdMode}, "text": {" Plan:Yolo "}, "yes": {"1"}}, http.StatusForbidden, nil, "never set remotely"},
		{"task mode yolo", url.Values{"type": {CmdTaskMode}, "task": {"P1-04"}, "text": {"yolo"}}, http.StatusForbidden, nil, "never set remotely"},
		{"tail", url.Values{"type": {TypeTail}, "task": {"P1-02"}}, http.StatusBadRequest, nil, "not a command"},
		{"hello", url.Values{"type": {TypeHello}}, http.StatusBadRequest, nil, "not a command"},
		{"bad task", url.Values{"type": {CmdDone}, "task": {"../x"}, confirmedField: {"1"}}, http.StatusBadRequest, nil, "not a task ID"},
		{"no CSRF", url.Values{"type": {CmdPause}, CSRFField: {""}}, http.StatusForbidden, nil, "CSRF"},
		{"wrong CSRF", url.Values{"type": {CmdPause}, CSRFField: {"x"}}, http.StatusForbidden, nil, "CSRF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := uiWeb(t, "accept", true)
			running(w.srv)
			status, body, hdr := w.postForm(PathAct, tt.form)
			if status != tt.status {
				t.Fatalf("status = %d, want %d: %s", status, tt.status, body)
			}
			if status == http.StatusSeeOther && hdr.Get("Location") != "/" {
				t.Errorf("Location = %q", hdr.Get("Location"))
			}
			if !strings.Contains(body, tt.body) {
				t.Errorf("the page doesn't say %q:\n%s", tt.body, body)
			}
			got := w.send.got()
			switch {
			case tt.want == nil && len(got) > 0:
				t.Errorf("sent %+v", got)
			case tt.want != nil && (len(got) != 1 || got[0] != *tt.want):
				t.Errorf("sent %+v, want %+v", got, *tt.want)
			}
		})
	}
}

// The confirmation page posts the same command again, confirmed.
func TestUIConfirmationPostsAgain(t *testing.T) {
	w := uiWeb(t, "accept", true)
	running(w.srv)
	_, body, _ := w.postForm(PathAct, url.Values{"type": {CmdSkip}, "task": {"P1-03"}, "text": {"later"}})
	field := regexp.MustCompile(`<input type="hidden" name="([a-z]+)" value="([^"]*)">`)
	f := url.Values{}
	for _, m := range field.FindAllStringSubmatch(body, -1) {
		f.Set(m[1], m[2])
	}
	f.Set(confirmedField, "1")
	if f.Get(CSRFField) != csrfFor(goodToken) {
		t.Fatalf("the confirmation page's CSRF token = %q", f.Get(CSRFField))
	}
	if status, _, _ := w.postForm(PathAct, f); status != http.StatusSeeOther {
		t.Fatalf("confirmed = %d", status)
	}
	if got := w.send.got(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdSkip, Task: "P1-03", Text: "later"}) {
		t.Errorf("sent %+v", got)
	}
}

// The UI's routes: the page, its live part, the script and the stylesheet
// to paired devices only; the templates stay inside.
func TestUIRoutes(t *testing.T) {
	w := uiWeb(t, "accept", true)
	running(w.srv)
	tests := []struct {
		method, path, auth string
		status             int
		ctype              string
	}{
		{"GET", "/", "cookie", 200, "text/html"},
		{"GET", "/", "bearer", 200, "text/html"},
		{"GET", "/", "", 401, "text/plain"},
		{"GET", PathLive, "cookie", 200, "text/html"},
		{"GET", PathLive, "", 401, "text/plain"},
		{"GET", "/static/app.js", "cookie", 200, "text/javascript"},
		{"GET", "/static/app.css", "cookie", 200, "text/css"},
		{"GET", "/static/app.js", "", 401, "text/plain"},
		{"GET", "/static/page.html", "cookie", 404, "text/html"},
		{"GET", "/static/", "cookie", 404, "text/html"},
		{"GET", "/nope", "cookie", 404, "text/html"},
		{"GET", PathAct, "cookie", 405, "text/plain"},
		{"DELETE", "/", "bearer", 405, "text/plain"},
	}
	for _, tt := range tests {
		resp := w.do(tt.method, tt.path, tt.auth, "", nil)
		if resp.StatusCode != tt.status || !strings.HasPrefix(resp.Header.Get("Content-Type"), tt.ctype) {
			t.Errorf("%s %s (%s) = %d %s, want %d %s", tt.method, tt.path, tt.auth, resp.StatusCode, resp.Header.Get("Content-Type"), tt.status, tt.ctype)
		}
	}
	_, live, _ := w.get(PathLive, "cookie")
	_, page, _ := w.get("/", "cookie")
	if strings.Contains(live, "<html") || !strings.Contains(page, live) {
		t.Error("the live part is not the page's")
	}
}

// The script talks to the paths the listener serves.
func TestUIScriptPaths(t *testing.T) {
	js, err := uiFiles.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{PathCommand, PathEvents, PathLive, CSRFHeader, `"confirmed"`, `meta[name="igris-csrf"]`} {
		if !strings.Contains(string(js), p) {
			t.Errorf("app.js doesn't use %s", p)
		}
	}
}

// serve ends with the run (SPEC §14), so after run_stopped the page can't
// fetch the ended run's page: the script shows the end itself, removing
// what that page leaves out (found at the V16 gate: the page kept a live
// run's buttons and said "Failed to fetch").
func TestUIScriptEndsWithTheRun(t *testing.T) {
	b, err := uiFiles.ReadFile("ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if !regexp.MustCompile(`m\.type === "run_stopped"\) \{\s*ended\(m\.detail\);\s*return;`).MatchString(js) {
		t.Error("app.js doesn't end the page on run_stopped without fetching it")
	}
	remove := regexp.MustCompile(`var gone = live.querySelectorAll\("(form\.cmd[^"]*)"\)`).FindStringSubmatch(js)
	if remove == nil {
		t.Fatal("app.js's ended removes nothing")
	}
	pages := uiPages(t)
	ended := pages["ended.html"]
	if !strings.Contains(ended, "ended: stopped") {
		t.Error("the ended page doesn't say so")
	}
	for _, c := range []struct{ sel, mark string }{
		{"form.cmd", `class="cmd`},
		{"section.question", `<section class="question"`},
		{".actions", `<div class="actions">`},
		{"details", "<details>"},
		{"section[aria-label='Run mode']", `<section aria-label="Run mode">`},
	} {
		if !strings.Contains(remove[1], c.sel) {
			t.Errorf("app.js's ended keeps %s", c.sel)
		}
		if strings.Contains(ended, c.mark) {
			t.Errorf("the ended page has %s", c.mark)
		}
		shown := false
		for name, body := range pages {
			shown = shown || (name != "ended.html" && strings.Contains(body, c.mark))
		}
		if !shown {
			t.Errorf("no live page has %s: the test no longer checks it", c.mark)
		}
	}
}

func TestRan(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Second: "0s", 59 * time.Second: "59s", 4*time.Minute + 12*time.Second: "4m12s",
		time.Hour + 5*time.Minute + 59*time.Second: "1h05m",
	} {
		if got := ran(d); got != want {
			t.Errorf("ran(%v) = %q, want %q", d, got, want)
		}
	}
}

// A form is shape-checked before the confirmation page names its task:
// nothing of the form is shown back unchecked.
func TestUIFormCheckedBeforeConfirm(t *testing.T) {
	w := uiWeb(t, "accept", true)
	running(w.srv)
	junk := "<b>" + strings.Repeat("x", 300)
	status, body, _ := w.postForm(PathAct, url.Values{"type": {CmdSkip}, "task": {junk}, "text": {"later"}})
	if status != http.StatusBadRequest || !strings.Contains(body, "not a task ID") {
		t.Fatalf("status = %d, want 400 naming the task ID: %s", status, body)
	}
	if strings.Contains(body, "<form") || strings.Contains(body, strings.Repeat("x", 100)) {
		t.Errorf("the page shows the form or the whole junk:\n%s", body)
	}
	if got := w.send.got(); len(got) != 0 {
		t.Errorf("sent %+v", got)
	}
}

// Every body is bounded to 1 MiB, a form as much as a command: the CSRF
// header path never parsed the form before the UI did, where Go's own
// limit is 10 MiB.
func TestUIFormBodyBounded(t *testing.T) {
	w := uiWeb(t, "accept", true)
	running(w.srv)
	big := "type=pause&note=" + strings.Repeat("x", MaxLine)
	req, err := http.NewRequest(http.MethodPost, w.ts.URL+PathAct, strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(CSRFHeader, csrfFor(goodToken))
	req.AddCookie(&http.Cookie{Name: CookieName, Value: goodToken}) //nolint:gosec // a client's cookie
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "could not be read") {
		t.Errorf("a 1 MiB form: %d %s, want 400", resp.StatusCode, body)
	}
	if got := w.send.got(); len(got) != 0 {
		t.Errorf("sent %+v", got)
	}
}
