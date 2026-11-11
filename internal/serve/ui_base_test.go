package serve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubSource is a UISource that isn't a *Server: the snapshot of one, a
// name of its own, and the requests it was sent.
type stubSource struct {
	snap Snapshot
	mu   sync.Mutex
	sent []Request
}

func (s *stubSource) Snapshot() Snapshot { return s.snap }
func (s *stubSource) Project() string    { return "stub" }
func (s *stubSource) Request(_ context.Context, r Request) (Reply, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, r)
	return Reply{Type: TypeAck, ID: r.ID}, http.StatusOK
}

// uiPath is a path of the pages' own in an attribute: a link, a script, a
// form's action, the live part's address, the base itself.
var uiPath = regexp.MustCompile(`(?:href|src|action|data-live|data-base)="(/[^"]*)"`)

// TestUIBase: under a base path every path the pages use starts with it,
// the page carries it for the script, and a form's command goes to the
// source and back to the base's page.
func TestUIBase(t *testing.T) {
	const base = "/p/demo-abc123"
	srv := New(Options{Project: "demo"})
	t.Cleanup(func() { _ = srv.Close() })
	running(srv)
	src := &stubSource{snap: srv.Snapshot()}
	now := func() time.Time { return uiT0.Add(5 * time.Minute) }
	h := http.StripPrefix(base, NewUI(src, UIOptions{Now: now, Base: base}))
	do := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if form != nil {
			r = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r = r.WithContext(context.WithValue(r.Context(), csrfKey{}, "tok"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	// based checks every path of body starts with base and returns how
	// many there were.
	based := func(name, body string) int {
		t.Helper()
		m := uiPath.FindAllStringSubmatch(body, -1)
		for _, p := range m {
			if p[1] != base && !strings.HasPrefix(p[1], base+"/") {
				t.Errorf("%s: path %q is not under %s", name, p[1], base)
			}
		}
		return len(m)
	}

	for _, tc := range []struct{ path, live string }{{base + "/", base + "/live"}, {base + "/plan", base + "/plan/live"}} {
		w := do(http.MethodGet, tc.path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", tc.path, w.Code, w.Body)
		}
		body := w.Body.String()
		if !strings.Contains(body, `data-base="`+base+`"`) || !strings.Contains(body, `data-live="`+tc.live+`"`) {
			t.Errorf("GET %s: no data-base or data-live %q", tc.path, tc.live)
		}
		if !strings.Contains(body, `<script src="`+base+`/static/app.js"`) {
			t.Errorf("GET %s: the script is not under the base", tc.path)
		}
		if n := based(tc.path, body); n < 5 {
			t.Errorf("GET %s: %d paths, want the page's links, forms and assets", tc.path, n)
		}
	}
	for _, p := range []string{base + "/live", base + "/plan/live"} {
		if w := do(http.MethodGet, p, nil); w.Code != http.StatusOK || based(p, w.Body.String()) == 0 {
			t.Errorf("GET %s: %d, or no paths", p, w.Code)
		}
	}
	if w := do(http.MethodGet, base+"/static/app.js", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "dataset.base") {
		t.Errorf("GET the script: %d", w.Code)
	}
	w := do(http.MethodGet, base+"/nowhere", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "stub") {
		t.Errorf("not found: %d, want the source's project", w.Code)
	}
	based("not found", w.Body.String())

	// Stop asks its second confirmation, under the base, without sending.
	w = do(http.MethodPost, base+"/act", url.Values{"type": {CmdStop}})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="`+base+`/act"`) {
		t.Errorf("stop: %d, want the confirmation posting to the base", w.Code)
	}
	based("confirm", w.Body.String())
	// Pause goes to the source, and the browser back to the base's page.
	w = do(http.MethodPost, base+"/act", url.Values{"type": {CmdPause}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/" {
		t.Errorf("pause: %d to %q, want 303 to %s/", w.Code, w.Header().Get("Location"), base)
	}
	if len(src.sent) != 1 || src.sent[0].Type != CmdPause {
		t.Errorf("sent %+v, want the one pause", src.sent)
	}
}

// TestUIServeHasNoBase: serve's own pages carry no data-base, and every
// path is the root's.
func TestUIServeHasNoBase(t *testing.T) {
	w := uiWeb(t, "accept", true)
	running(w.srv)
	for _, p := range []string{"/", PathPlan} {
		_, body, _ := w.get(p, "cookie")
		if strings.Contains(body, "data-base") {
			t.Errorf("GET %s: data-base on serve's page", p)
		}
		if !strings.Contains(body, `<script src="/static/app.js"`) {
			t.Errorf("GET %s: the script not at /static/", p)
		}
	}
}
