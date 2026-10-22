package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// goodToken and badToken have the token's shape.
var (
	goodToken = strings.Repeat("g", 43)
	badToken  = strings.Repeat("b", 43)
)

// clock is a settable time for the rate limit.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// tokenBox is the owner token a test can rotate.
type tokenBox struct {
	mu  sync.Mutex
	tok string
}

func (b *tokenBox) get() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tok, nil
}

func (b *tokenBox) set(tok string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tok = tok
}

// web is an HTTP listener under test.
type web struct {
	t     *testing.T
	srv   *Server
	send  *sender
	clock *clock
	token *tokenBox
	ts    *httptest.Server
}

func newWeb(t *testing.T, tune func(*RemoteOptions)) *web {
	t.Helper()
	return newWebWith(t, nil, tune)
}

// newWebWith is newWeb with the server's options tuned too.
func newWebWith(t *testing.T, server func(*Options), tune func(*RemoteOptions)) *web {
	t.Helper()
	w := &web{t: t, send: &sender{}, clock: &clock{t: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}, token: &tokenBox{tok: goodToken}}
	so := Options{Version: "1.6.0", Project: "demo", Backend: "fake", Sender: w.send,
		Tail: func(_ context.Context, task string, n int) ([]string, error) {
			return []string{"one " + task, "two\x1b[31m"}, nil
		}}
	if server != nil {
		server(&so)
	}
	w.srv = New(so)
	opts := RemoteOptions{Token: w.token.get, Now: w.clock.now}
	if tune != nil {
		tune(&opts)
	}
	w.ts = httptest.NewServer(w.srv.Remote(opts))
	t.Cleanup(func() {
		w.ts.Close()
		_ = w.srv.Close()
	})
	return w
}

// do sends a request; auth is "bearer", "cookie" or "".
func (w *web) do(method, path, auth, body string, hdr map[string]string) *http.Response {
	w.t.Helper()
	req, err := http.NewRequest(method, w.ts.URL+path, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	switch auth {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+goodToken)
	case "cookie":
		req.AddCookie(&http.Cookie{Name: CookieName, Value: goodToken}) //nolint:gosec // a client's cookie: the attributes are the server's
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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

var noRedirect = &http.Client{
	Timeout:       patience,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func readReply(t *testing.T, resp *http.Response) Reply {
	t.Helper()
	var r Reply
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatalf("reply: %v", err)
	}
	return r
}

func TestHTTPPairing(t *testing.T) {
	for _, tlsOn := range []bool{false, true} {
		w := newWeb(t, func(o *RemoteOptions) { o.TLS = tlsOn })
		resp := w.do("GET", PathPair+"?token="+goodToken, "", "", nil)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
			t.Fatalf("pair: %d to %q, want 303 to /", resp.StatusCode, resp.Header.Get("Location"))
		}
		cookies := resp.Cookies()
		if len(cookies) != 1 {
			t.Fatalf("pair set %d cookies", len(cookies))
		}
		c := cookies[0]
		if c.Name != CookieName || c.Value != goodToken || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure != tlsOn || c.MaxAge < 300*24*3600 {
			t.Errorf("tls %v: cookie %+v", tlsOn, c)
		}
		if hsts := resp.Header.Get("Strict-Transport-Security"); (hsts != "") != tlsOn {
			t.Errorf("tls %v: HSTS %q", tlsOn, hsts)
		}
		for _, bad := range []string{PathPair + "?token=" + badToken, PathPair, PathPair + "?token="} {
			resp := w.do("GET", bad, "", "", nil)
			if resp.StatusCode != http.StatusUnauthorized || len(resp.Cookies()) != 0 {
				t.Errorf("%s: %d with %d cookies, want 401 and none", bad, resp.StatusCode, len(resp.Cookies()))
			}
		}
		if resp := w.do("POST", PathPair+"?token="+goodToken, "", "", nil); resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST pair: %d, want 405", resp.StatusCode)
		}
	}
}

func TestHTTPAuth(t *testing.T) {
	w := newWeb(t, nil)
	for _, tc := range []struct {
		name, path string
		hdr        map[string]string
		auth       string
		want       int
	}{
		{"nothing, page", "/", nil, "", http.StatusUnauthorized},
		{"nothing, stream", PathEvents, nil, "", http.StatusUnauthorized},
		{"bearer", "/", nil, "bearer", http.StatusOK},
		{"cookie", "/", nil, "cookie", http.StatusOK},
		{"wrong bearer", "/", map[string]string{"Authorization": "Bearer " + badToken}, "", http.StatusUnauthorized},
		{"lower-case scheme", "/", map[string]string{"Authorization": "bearer " + goodToken}, "", http.StatusOK},
		{"basic", "/", map[string]string{"Authorization": "Basic " + goodToken}, "", http.StatusUnauthorized},
		{"wrong cookie", "/", map[string]string{"Cookie": CookieName + "=" + badToken}, "", http.StatusUnauthorized},
		{"token in the query", "/?token=" + goodToken, nil, "", http.StatusUnauthorized},
		{"unknown path", "/nope", nil, "bearer", http.StatusNotFound},
	} {
		resp := w.do("GET", tc.path, tc.auth, "", tc.hdr)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
	resp := w.do("GET", PathEvents, "", "", nil)
	if r := readReply(t, resp); r.Type != TypeError || !strings.Contains(r.Error, "igris serve token") {
		t.Errorf("unpaired API answer %+v, want a protocol error saying how to pair", r)
	}
	// A token that can't be read authenticates nobody.
	w2 := newWeb(t, func(o *RemoteOptions) {
		o.Token = func() (string, error) { return "", io.ErrUnexpectedEOF }
	})
	if resp := w2.do("GET", "/", "bearer", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unreadable token: %d, want 401", resp.StatusCode)
	}
}

func TestHTTPRateLimit(t *testing.T) {
	w := newWeb(t, nil)
	wrong := map[string]string{"Authorization": "Bearer " + badToken}
	// Missing credentials guess nothing: an unpaired browser isn't locked out.
	for range 2 * authFails {
		w.do("GET", "/", "", "", nil)
	}
	if resp := w.do("GET", "/", "bearer", "", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("after unauthenticated requests: %d, want 200", resp.StatusCode)
	}
	for i := range authFails {
		path := "/"
		if i%2 == 1 {
			path = PathPair + "?token=" + badToken // a wrong pairing token counts too
		}
		if resp := w.do("GET", path, "", "", wrong); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d, want 401", i+1, resp.StatusCode)
		}
	}
	for _, path := range []string{"/", PathPair + "?token=" + goodToken, PathEvents} {
		resp := w.do("GET", path, "bearer", "", nil)
		if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "60" {
			t.Errorf("%s while limited: %d, Retry-After %q; want 429, 60", path, resp.StatusCode, resp.Header.Get("Retry-After"))
		}
	}
	w.clock.add(authWindow)
	if resp := w.do("GET", "/", "bearer", "", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("after the window: %d, want 200", resp.StatusCode)
	}
}

func TestAddrKey(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.1:5000":              "192.0.2.1",
		"[::ffff:192.0.2.1]:5000":     "192.0.2.1",
		"[2001:db8:1:2:3:4:5:6]:5000": "2001:db8:1:2::/64",
		"[2001:db8:1:2:ff::1]:1":      "2001:db8:1:2::/64",
		"[fe80::1%eth0]:1":            "fe80::/64",
		"garbage":                     "garbage",
	} {
		if got := addrKey(in); got != want {
			t.Errorf("addrKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// A flood of addresses can't grow the table: past limiterMax they share
// one entry, which fails closed.
func TestLimiterBounded(t *testing.T) {
	c := &clock{t: time.Unix(0, 0)}
	l := newLimiter(c.now)
	for i := range limiterMax {
		l.fail("a" + strings.Repeat("x", i%7) + string(rune('0'+i%10)) + time.Duration(i).String())
	}
	for i := range authFails {
		l.fail("new" + time.Duration(i).String())
	}
	if len(l.seen) > limiterMax+1 {
		t.Fatalf("table grew to %d", len(l.seen))
	}
	if _, limited := l.blocked("another"); !limited {
		t.Error("an untracked address isn't limited while the shared entry is")
	}
	c.add(authWindow)
	l.fail("fresh") // expired entries make room
	if _, ok := l.seen["fresh"]; !ok || len(l.seen) > 2 {
		t.Errorf("expired entries not pruned: %d left", len(l.seen))
	}
}

func TestHTTPCSRFAndOrigin(t *testing.T) {
	var gotCSRF []string
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCSRF = append(gotCSRF, CSRFToken(r.Context()))
		_, _ = io.WriteString(w, "ui")
	})
	w := newWeb(t, func(o *RemoteOptions) { o.UI = ui })
	pause := `{"v":1,"type":"pause","id":"1"}`
	csrf := csrfFor(goodToken)
	for _, tc := range []struct {
		name string
		auth string
		hdr  map[string]string
		want int
	}{
		{"cookie without csrf", "cookie", nil, http.StatusForbidden},
		{"cookie, wrong csrf", "cookie", map[string]string{CSRFHeader: csrfFor(badToken)}, http.StatusForbidden},
		{"cookie with csrf", "cookie", map[string]string{CSRFHeader: csrf}, http.StatusOK},
		{"bearer needs none", "bearer", nil, http.StatusOK},
		{"foreign origin", "bearer", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"own origin", "cookie", map[string]string{CSRFHeader: csrf, "Origin": w.ts.URL}, http.StatusOK},
		{"cross-site fetch", "cookie", map[string]string{CSRFHeader: csrf, "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-origin fetch", "cookie", map[string]string{CSRFHeader: csrf, "Sec-Fetch-Site": "same-origin"}, http.StatusOK},
	} {
		if resp := w.do("POST", PathCommand, tc.auth, pause, tc.hdr); resp.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
	if n := len(w.send.got()); n != 4 {
		t.Errorf("%d commands reached the run, want the 4 allowed", n)
	}
	// A form carries the token as a field; the UI gets it for its pages.
	form := url.Values{CSRFField: {csrf}}.Encode()
	for field, want := range map[string]int{form: http.StatusOK, "csrf=nope": http.StatusForbidden} {
		req, _ := http.NewRequest("POST", w.ts.URL+"/answer", strings.NewReader(field))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: CookieName, Value: goodToken}) //nolint:gosec // a client's cookie: the attributes are the server's
		r2, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = r2.Body.Close()
		if r2.StatusCode != want {
			t.Errorf("form %q: %d, want %d", field, r2.StatusCode, want)
		}
	}
	w.do("GET", "/", "cookie", "", nil)
	if len(gotCSRF) == 0 || gotCSRF[len(gotCSRF)-1] != csrf {
		t.Errorf("the UI got CSRF tokens %q, want %q", gotCSRF, csrf)
	}
	if csrf == csrfFor(badToken) || csrf == goodToken {
		t.Error("the CSRF token must change with the owner token and differ from it")
	}
}

func TestHTTPSecurityHeaders(t *testing.T) {
	w := newWeb(t, nil)
	wrong := map[string]string{"Authorization": "Bearer " + badToken}
	resps := []*http.Response{
		w.do("GET", "/", "", "", nil),
		w.do("GET", "/", "bearer", "", nil),
		w.do("GET", "/nope", "bearer", "", nil),
		w.do("GET", PathPair+"?token="+goodToken, "", "", nil),
		w.do("POST", PathCommand, "bearer", `{"v":1,"type":"pause","id":"1"}`, nil),
		w.do("GET", PathCommand, "bearer", "", nil),
	}
	for range authFails {
		w.do("GET", "/", "", "", wrong)
	}
	resps = append(resps, w.do("GET", "/", "bearer", "", nil)) // 429
	for i, resp := range resps {
		for _, kv := range securityHeaders {
			if got := resp.Header.Get(kv[0]); got != kv[1] {
				t.Errorf("response %d (%d): %s = %q, want %q", i, resp.StatusCode, kv[0], got, kv[1])
			}
		}
	}
	csp := resps[0].Header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors 'none'", "script-src 'self'", "default-src 'none'"} {
		if !strings.Contains(csp, want) || strings.Contains(csp, "unsafe-inline") {
			t.Errorf("CSP %q: want %s, no unsafe-inline", csp, want)
		}
	}
}

func TestHTTPCommands(t *testing.T) {
	w := newWeb(t, nil)
	for _, tc := range []struct {
		name, body string
		want       int
		reply      string // the reply's type, or a part of its error
	}{
		{"pause", `{"v":1,"type":"pause","id":"p1"}`, http.StatusOK, TypeAck},
		{"mode auto, yes dropped", `{"v":1,"type":"mode","id":"m1","text":"auto","yes":true}`, http.StatusOK, TypeAck},
		{"skip", `{"v":1,"type":"skip","id":"s1","task":"M1-02","text":"not needed"}`, http.StatusOK, TypeAck},
		{"mode yolo", `{"v":1,"type":"mode","id":"y1","text":"yolo","yes":true}`, http.StatusForbidden, "never set remotely"},
		{"mode YOLO spaced", `{"v":1,"type":"mode","id":"y2","text":"  YOLO\n"}`, http.StatusForbidden, "never set remotely"},
		{"mode plan:yolo", `{"v":1,"type":"mode","id":"y3","text":"plan:yolo"}`, http.StatusForbidden, "never set remotely"},
		{"task_mode yolo", `{"v":1,"type":"task_mode","id":"y4","task":"M1-02","text":"yolo","yes":true}`, http.StatusForbidden, "never set remotely"},
		{"task_mode plan:yolo", `{"v":1,"type":"task_mode","id":"y5","task":"M1-02","text":"Plan:Yolo"}`, http.StatusForbidden, "never set remotely"},
		{"hello", `{"v":1,"type":"hello","id":"h","yolo":true}`, http.StatusBadRequest, "no hello over HTTP"},
		{"no id", `{"v":1,"type":"pause"}`, http.StatusBadRequest, "needs an id"},
		{"unknown type", `{"v":1,"type":"launch","id":"u"}`, http.StatusBadRequest, "unknown message type"},
		{"other version", `{"v":2,"type":"pause","id":"v"}`, http.StatusBadRequest, "not supported"},
		{"not json", `pause`, http.StatusBadRequest, "not a protocol message"},
		{"deny without feedback", `{"v":1,"type":"plan","id":"d","plan":"deny"}`, http.StatusBadRequest, "feedback"},
		{"tail", `{"v":1,"type":"tail","id":"t","n":5,"task":"M1-02"}`, http.StatusOK, TypeTail},
		{"tail n 0", `{"v":1,"type":"tail","id":"t0","n":0}`, http.StatusBadRequest, "n is 1 to"},
	} {
		resp := w.do("POST", PathCommand, "bearer", tc.body, nil)
		r := readReply(t, resp)
		if resp.StatusCode != tc.want || (r.Type != tc.reply && !strings.Contains(r.Error, tc.reply)) {
			t.Errorf("%s: %d %+v, want %d %s", tc.name, resp.StatusCode, r, tc.want, tc.reply)
		}
		if resp.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s: content type %q", tc.name, resp.Header.Get("Content-Type"))
		}
		if tc.name == "tail" && (len(r.Lines) != 2 || r.Lines[0] != "one M1-02" || strings.Contains(r.Lines[1], "\x1b")) {
			t.Errorf("tail lines %q, want two, cleaned", r.Lines)
		}
	}
	got := w.send.got()
	if len(got) != 3 || got[0].Kind != engine.CmdPause || got[1].Kind != engine.CmdMode || got[1].Yes || got[2].Kind != engine.CmdSkip || got[2].Task != "M1-02" {
		t.Errorf("the run got %+v; want pause, mode auto without yes, skip", got)
	}
	for _, c := range got {
		if engine.SkipsPermissions(strings.ToLower(strings.TrimSpace(c.Text))) {
			t.Errorf("a skip-permissions mode reached the run: %+v", c)
		}
	}
	if resp := w.do("POST", PathCommand, "bearer", "", map[string]string{"Content-Type": "text/plain"}); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain: %d, want 415", resp.StatusCode)
	}
	big := `{"v":1,"type":"done","id":"b","text":"` + strings.Repeat("x", MaxLine) + `"}`
	if resp := w.do("POST", PathCommand, "bearer", big, nil); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over 1 MiB: %d, want 413", resp.StatusCode)
	}
	if resp := w.do("GET", PathCommand, "bearer", "", nil); resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "POST" {
		t.Errorf("GET command: %d, want 405 with Allow: POST", resp.StatusCode)
	}
	if resp := w.do("POST", PathEvents, "bearer", `{}`, nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST events: %d, want 405", resp.StatusCode)
	}
}

// A run whose owner typed the phrase at start takes a skip-permissions
// mode without yes (the engine's yoloConfirmed): the listener refuses it
// before it gets there.
func TestHTTPNeverYoloOnAConfirmedRun(t *testing.T) {
	eng := &yoloEngine{}
	w := newWebWith(t, func(o *Options) { o.Sender = eng }, nil)
	for _, body := range []string{
		`{"v":1,"type":"mode","id":"1","text":"yolo"}`,
		`{"v":1,"type":"task_mode","id":"2","task":"A-1","text":"plan:yolo"}`,
	} {
		if resp := w.do("POST", PathCommand, "bearer", body, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", body, resp.StatusCode)
		}
	}
	if n := eng.n.Load(); n != 0 {
		t.Errorf("%d mode commands reached a yolo-confirmed run", n)
	}
}

type yoloEngine struct{ n atomic.Int32 }

func (e *yoloEngine) Send(engine.Command) { e.n.Add(1) }

// sse reads an event stream.
type sse struct {
	t    *testing.T
	resp *http.Response
	r    *bufio.Reader
}

func (w *web) stream(ctx context.Context) *sse {
	w.t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", w.ts.URL+PathEvents, nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		w.t.Fatalf("events: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return &sse{t: w.t, resp: resp, r: bufio.NewReader(resp.Body)}
}

// next returns the stream's next frame: "data:<json>" or a comment; ""
// once the stream ended.
func (s *sse) next() string {
	s.t.Helper()
	got := make(chan string, 1)
	go func() {
		var frame []string
		for {
			line, err := s.r.ReadString('\n')
			if err != nil {
				got <- ""
				return
			}
			line = strings.TrimSuffix(line, "\n")
			if line == "" {
				got <- strings.Join(frame, "\n")
				return
			}
			frame = append(frame, line)
		}
	}()
	select {
	case f := <-got:
		return f
	case <-time.After(patience):
		s.t.Fatal("no frame from the event stream")
		return ""
	}
}

func (s *sse) message() map[string]any {
	s.t.Helper()
	for {
		f := s.next()
		if f == "" {
			s.t.Fatal("the stream ended")
		}
		if strings.HasPrefix(f, ":") || strings.HasPrefix(f, "retry:") {
			continue
		}
		data, ok := strings.CutPrefix(f, "data: ")
		if !ok || strings.Contains(data, "\n") {
			s.t.Fatalf("frame %q is not one data line", f)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			s.t.Fatalf("frame %q: %v", f, err)
		}
		return m
	}
}

func TestHTTPEventsSnapshotThenEvents(t *testing.T) {
	var hellos atomic.Int32
	w := newWebWith(t, func(o *Options) { o.Hello = func(Request) { hellos.Add(1) } }, nil)
	w.srv.Publish(engine.Event{Kind: engine.RunStarted, Run: "20261022-090000-abcd", At: time.Now(), Phase: "M1"})
	s := w.stream(t.Context())
	if f := s.next(); f != "retry: 3000" {
		t.Errorf("first frame %q, want the retry", f)
	}
	snap := s.message()
	if snap["type"] != TypeSnapshot || snap["run"] != "20261022-090000-abcd" || snap["project"] != "demo" {
		t.Fatalf("first message %v, want the snapshot", snap)
	}
	// The snapshot is queued once the stream is attached: events follow.
	w.srv.Publish(engine.Event{Kind: engine.TaskStarted, At: time.Now(), Task: "M1-01", Title: "line\nbreak"})
	ev := s.message()
	if ev["type"] != string(engine.TaskStarted) || ev["task"] != "M1-01" || ev["v"] != float64(1) {
		t.Errorf("event %v", ev)
	}
	if n := hellos.Load(); n != 0 {
		t.Errorf("an HTTP stream said hello %d times; it is never the starter", n)
	}
}

func TestHTTPEventsEndOnRotation(t *testing.T) {
	w := newWeb(t, func(o *RemoteOptions) { o.Ping = 5 * time.Millisecond })
	s := w.stream(t.Context())
	s.message() // the snapshot
	if f := s.next(); f != ": ping" {
		t.Fatalf("frame %q, want a ping", f)
	}
	w.token.set(badToken)
	for {
		f := s.next()
		if f == "" {
			break
		}
		if f != ": ping" {
			t.Fatalf("frame %q after the rotation", f)
		}
	}
	if resp := w.do("GET", "/", "bearer", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the old token after rotation: %d, want 401", resp.StatusCode)
	}
}

func TestHTTPEventStreamsBounded(t *testing.T) {
	w := newWeb(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range maxStreams {
		w.stream(ctx).message()
	}
	if resp := w.do("GET", PathEvents, "bearer", "", nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("stream %d: %d, want 503", maxStreams+1, resp.StatusCode)
	}
}

// A slow event stream is dropped like a slow socket client, never waited
// for.
func TestHTTPSlowStreamDropped(t *testing.T) {
	w := newWebWith(t, func(o *Options) { o.QueueLen = 4 }, nil)
	s := w.stream(t.Context())
	s.message()
	for range 4096 { // never blocks: the stream isn't read meanwhile
		w.srv.Publish(engine.Event{Kind: engine.Warning, At: time.Now(), Detail: strings.Repeat("w", 1024)})
	}
	for s.next() != "" { // what got through, then the end
	}
}

// Shutdown ends the event streams and the listener; ServeRemote returns.
func TestHTTPServeRemoteShutdown(t *testing.T) {
	srv := New(Options{Version: "1.6.0"})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() {
		served <- srv.ServeRemote(l, srv.Remote(RemoteOptions{Token: func() (string, error) { return goodToken, nil }}))
	}()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", "http://"+l.Addr().String()+PathEvents, nil)
	req.Header.Set("Authorization", "Bearer "+goodToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	s := &sse{t: t, resp: resp, r: bufio.NewReader(resp.Body)}
	s.message()
	ctx, cancel := context.WithTimeout(t.Context(), patience)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for s.next() != "" {
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("ServeRemote: %v", err)
		}
	case <-time.After(patience):
		t.Fatal("ServeRemote did not return")
	}
	if _, err := net.DialTimeout("tcp", l.Addr().String(), patience); err == nil {
		t.Error("the listener still accepts after Shutdown")
	}
}

// Tail reads over HTTP are bounded: the socket allows one per client, and
// a token holder must not get a backend read per request.
func TestHTTPTailsBounded(t *testing.T) {
	started := make(chan struct{}, maxTails)
	release := make(chan struct{})
	w := newWebWith(t, func(o *Options) {
		o.Tail = func(ctx context.Context, _ string, _ int) ([]string, error) {
			started <- struct{}{}
			select {
			case <-release:
				return []string{"line"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}, nil)
	body := `{"v":1,"type":"tail","id":"t","n":5}`
	tail := func() (int, error) {
		req, err := http.NewRequest(http.MethodPost, w.ts.URL+PathCommand, strings.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+goodToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := noRedirect.Do(req)
		if err != nil {
			return 0, err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	codes := make(chan int, maxTails)
	var wg sync.WaitGroup
	for range maxTails {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, err := tail()
			if err != nil {
				t.Error(err)
			}
			codes <- code
		}()
	}
	for range maxTails {
		<-started // every allowed read is in the backend
	}
	if code, err := tail(); err != nil || code != http.StatusServiceUnavailable {
		t.Errorf("tail %d: %d %v, want 503", maxTails+1, code, err)
	}
	close(release)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("an allowed tail got %d", code)
		}
	}
	if code, err := tail(); err != nil || code != http.StatusOK {
		t.Errorf("after the reads ended: %d %v, want 200", code, err)
	}
}

// An unknown message type is named in the error cut and cleaned: over the
// socket a 1 MiB type made the reply too long to send at all.
func TestHTTPUnknownTypeIsCut(t *testing.T) {
	w := newWeb(t, nil)
	long := strings.Repeat("a\x1b[2J", 4000)
	body, err := json.Marshal(map[string]any{"v": 1, "type": long, "id": "u"})
	if err != nil {
		t.Fatal(err)
	}
	resp := w.do("POST", PathCommand, "bearer", string(body), nil)
	r := readReply(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(r.Error, "unknown message type") {
		t.Fatalf("%d %+v", resp.StatusCode, r)
	}
	if len(r.Error) > 2*maxID || strings.Contains(r.Error, "\x1b") {
		t.Errorf("the error echoes the type uncut or uncleaned: %q", r.Error)
	}
	if n := len(w.send.got()); n != 0 {
		t.Errorf("%d commands reached the run", n)
	}
}
