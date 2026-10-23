package serve

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drilonrecica/igris/internal/engine"
)

// The HTTP mapping of protocol 1 (SPEC §14, frozen §19).
const (
	PathPair    = "/pair"
	PathEvents  = "/api/v1/events"
	PathCommand = "/api/v1/command"
	// PathAction is the prefix of ntfy's answer buttons (SPEC §10).
	PathAction = "/api/v1/action/"

	// CookieName holds the owner token once a device is paired.
	CookieName = "igris_token"
	// CSRFHeader carries the CSRF token of a cookie-authenticated POST;
	// a form carries it as the field CSRFField.
	CSRFHeader = "X-Igris-CSRF"
	CSRFField  = "csrf"
)

// Limits of the HTTP listener (SPEC §14).
const (
	authWindow     = time.Minute // failed authentications are counted per minute
	authFails      = 10          // per address and window before it gets 429
	limiterMax     = 4096        // addresses tracked; the rest share one entry
	maxStreams     = 16          // event streams at once
	maxTails       = 8           // tail reads at once (the socket allows one per client)
	pingInterval   = 15 * time.Second
	headerTimeout  = 10 * time.Second
	bodyTimeout    = 30 * time.Second
	idleTimeout    = 2 * time.Minute
	maxHeaderBytes = 16 << 10
	cookieMaxAge   = 400 * 24 * time.Hour
)

// errRemoteYolo refuses a skip-permissions mode from a remote client.
const errRemoteYolo = "skip-permissions mode is never set remotely; set it in the terminal (SPEC §7.3)"

// securityHeaders go on every response (SPEC §14).
var securityHeaders = [][2]string{
	{"Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"},
	{"Referrer-Policy", "no-referrer"},
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
	{"Cross-Origin-Opener-Policy", "same-origin"},
	{"Cross-Origin-Resource-Policy", "same-origin"},
	{"Cache-Control", "no-store"},
}

// RemoteOptions configures the HTTP listener's handler.
type RemoteOptions struct {
	// Token returns the owner token (state.ReadServeToken). It is called
	// on every request, so a rotation takes effect at once; an error
	// authenticates nobody.
	Token func() (string, error)
	// TLS says the listener serves TLS: the cookie is Secure and HSTS is
	// sent.
	TLS bool
	// UI serves the paths the API doesn't, to paired clients (SPEC §15.8);
	// CSRFToken gives it the token its forms carry. Nil answers them with
	// a short text.
	UI http.Handler
	// Now is the rate limit's clock; nil is time.Now.
	Now func() time.Time
	// Ping is how often an event stream says it is alive and checks its
	// credentials; zero is 15 s.
	Ping time.Duration
}

// remote is the HTTP listener's handler: auth, CSRF, the rate limit and
// the headers in front of protocol 1 over HTTP.
type remote struct {
	srv     *Server
	opts    RemoteOptions
	limit   *limiter
	streams atomic.Int32
	tails   atomic.Int32
}

// Remote returns the HTTP listener's handler for s (SPEC §14).
func (s *Server) Remote(opts RemoteOptions) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Ping <= 0 {
		opts.Ping = pingInterval
	}
	return &remote{srv: s, opts: opts, limit: newLimiter(opts.Now)}
}

// ServeRemote serves h on l until the server closes; then it returns nil.
// Shutdown ends its event streams with the socket's clients, Close closes
// its connections.
func (s *Server) ServeRemote(l net.Listener, h http.Handler) error {
	hs := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: headerTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// TLS handshake noise from scanners; what matters is logged by
		// the handler.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = l.Close()
		return nil
	}
	s.https = append(s.https, hs)
	s.mu.Unlock()
	if err := hs.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

type csrfKey struct{}

// CSRFToken returns the CSRF token of a request the listener let through
// to the UI, for its forms; "" outside one.
func CSRFToken(ctx context.Context) string {
	s, _ := ctx.Value(csrfKey{}).(string)
	return s
}

// csrfFor derives the CSRF token from the owner token: stateless, and
// changed by a rotation.
func csrfFor(token string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte("igris csrf v1"))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// equal compares secrets in constant time.
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func (h *remote) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, kv := range securityHeaders {
		w.Header().Set(kv[0], kv[1])
	}
	if h.opts.TLS {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	// Everything but the event stream answers within the body timeout.
	if r.URL.Path != PathEvents {
		rc := http.NewResponseController(w)
		_ = rc.SetReadDeadline(time.Now().Add(bodyTimeout))
		_ = rc.SetWriteDeadline(time.Now().Add(bodyTimeout))
	}
	// Every body is bounded, whoever reads it: a form the UI parses as
	// much as a command (SPEC §14).
	r.Body = http.MaxBytesReader(w, r.Body, MaxLine)
	if strings.HasPrefix(r.URL.Path, PathAction) {
		// Its token is its authority: no cookie, so no other site can
		// borrow one, and ntfy's web app sends its own Origin.
		h.action(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !h.sameOrigin(r) {
		h.fail(w, r, http.StatusForbidden, "a request from another site is refused")
		return
	}
	if r.URL.Path == PathPair {
		h.pair(w, r)
		return
	}
	key := addrKey(r.RemoteAddr)
	if wait, limited := h.limit.blocked(key); limited {
		h.tooMany(w, r, wait)
		return
	}
	token, viaCookie, ok := h.authenticate(r, key)
	if !ok {
		h.fail(w, r, http.StatusUnauthorized, "not paired: run `igris serve token` on the machine and open its URL on this device")
		return
	}
	csrf := csrfFor(token)
	if r.Method == http.MethodPost && viaCookie && !h.csrfOK(w, r, csrf) {
		h.fail(w, r, http.StatusForbidden, "the CSRF token is missing or wrong; reload the page")
		return
	}
	switch r.URL.Path {
	case PathEvents:
		if !method(w, r, http.MethodGet) {
			return
		}
		h.events(w, r)
	case PathCommand:
		if !method(w, r, http.MethodPost) {
			return
		}
		h.command(w, r)
	default:
		if h.opts.UI == nil {
			if r.URL.Path != "/" {
				h.fail(w, r, http.StatusNotFound, "not found")
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "igris: this device is paired.\n")
			return
		}
		h.opts.UI.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfKey{}, csrf)))
	}
}

// sameOrigin refuses a request another site made: an Origin that isn't
// this listener's, or Sec-Fetch-Site: cross-site.
func (h *remote) sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if h.opts.TLS {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}

// authenticate checks the request's cookie or bearer token against the
// owner token. A wrong one counts as a failure of its address; none at
// all doesn't (an unpaired browser asking for its pages guesses nothing).
func (h *remote) authenticate(r *http.Request, key string) (token string, viaCookie, ok bool) {
	given, viaCookie := credential(r)
	if given == "" {
		return "", false, false
	}
	token, err := h.opts.Token()
	if err != nil || !equal(given, token) {
		h.limit.fail(key)
		return "", false, false
	}
	return token, viaCookie, true
}

// credential is the request's bearer token, else its cookie.
func credential(r *http.Request) (given string, viaCookie bool) {
	if a := r.Header.Get("Authorization"); a != "" {
		scheme, tok, _ := strings.Cut(a, " ")
		if !strings.EqualFold(scheme, "Bearer") {
			return "", false
		}
		return strings.TrimSpace(tok), false
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value, true
	}
	return "", false
}

// stillValid reports whether the request's credential is still the owner
// token, for a stream that outlives a rotation.
func (h *remote) stillValid(r *http.Request) bool {
	given, _ := credential(r)
	token, err := h.opts.Token()
	return err == nil && given != "" && equal(given, token)
}

// csrfOK checks a cookie-authenticated POST's CSRF token: the header, or
// a form's field.
func (h *remote) csrfOK(w http.ResponseWriter, r *http.Request, want string) bool {
	given := r.Header.Get(CSRFHeader)
	if given == "" {
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "application/x-www-form-urlencoded" {
			r.Body = http.MaxBytesReader(w, r.Body, MaxLine)
			given = r.PostFormValue(CSRFField)
		}
	}
	return given != "" && equal(given, want)
}

// pair checks the pairing URL's token, sets the cookie and sends the
// browser on to a URL without the token (SPEC §14).
func (h *remote) pair(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, http.MethodGet) {
		return
	}
	key := addrKey(r.RemoteAddr)
	if wait, limited := h.limit.blocked(key); limited {
		h.tooMany(w, r, wait)
		return
	}
	given := r.URL.Query().Get("token")
	token, err := h.opts.Token()
	if err != nil || given == "" || !equal(given, token) {
		if given != "" {
			h.limit.fail(key)
		}
		h.fail(w, r, http.StatusUnauthorized, "this pairing URL is not valid (any more): run `igris serve token` on the machine for the current one")
		return
	}
	// Secure only on TLS: a loopback or Tailscale listener is plain http,
	// where a Secure cookie would never be sent back (SPEC §14). Lax, not
	// Strict: a phone opens the pairing URL from its camera and the ntfy
	// Open button from the ntfy app, and Android Chrome withholds Strict
	// cookies on a navigation another app started, this redirect included
	// (the page then said "not paired", owner check 2026-10-22). Lax still
	// withholds the cookie on a cross-site POST and on subresource requests,
	// and every change needs the CSRF token besides.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // HttpOnly and SameSite=Lax always, Secure on TLS
		Name: CookieName, Value: token, Path: "/",
		MaxAge: int(cookieMaxAge / time.Second), HttpOnly: true, Secure: h.opts.TLS, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// events streams protocol 1 to the client as server-sent events: the
// snapshot, then every event (SPEC §14). The stream is a client of the
// server like a socket's, over a pipe, so it is bounded and dropped when
// slow the same way.
func (h *remote) events(w http.ResponseWriter, r *http.Request) {
	if h.streams.Add(1) > maxStreams {
		h.streams.Add(-1)
		h.fail(w, r, http.StatusServiceUnavailable, "too many event streams at once")
		return
	}
	defer h.streams.Add(-1)
	near, far := net.Pipe()
	defer func() { _ = far.Close() }()
	if !h.srv.attachRemote(near) {
		h.fail(w, r, http.StatusServiceUnavailable, "the run is ending")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	lines := make(chan []byte)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(far)
		sc.Buffer(make([]byte, 0, 64<<10), MaxLine+1)
		for sc.Scan() {
			b := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- b:
			case <-stop:
				return
			}
		}
	}()
	write := func(parts ...[]byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		for _, p := range parts {
			if _, err := w.Write(p); err != nil {
				return false
			}
		}
		return rc.Flush() == nil
	}
	if !write([]byte("retry: 3000\n\n")) {
		return
	}
	ping := time.NewTicker(h.opts.Ping)
	defer ping.Stop()
	for {
		select {
		case b, ok := <-lines:
			if !ok || !write([]byte("data: "), b, []byte("\n\n")) {
				return
			}
		case <-ping.C:
			if !h.stillValid(r) || !write([]byte(": ping\n\n")) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// command takes one protocol-1 command or tail request and answers its
// reply (SPEC §14).
func (h *remote) command(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		h.fail(w, r, http.StatusUnsupportedMediaType, "a command is sent as application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxLine))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			h.fail(w, r, http.StatusRequestEntityTooLarge, "a command is at most 1 MiB")
			return
		}
		h.fail(w, r, http.StatusBadRequest, "the command could not be read")
		return
	}
	req, err := decodeRequest(body)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if req.Type == TypeTail {
		// A tail is a backend read; a token holder gets a bounded number
		// at once, as a socket client gets one.
		if h.tails.Add(1) > maxTails {
			h.tails.Add(-1)
			h.fail(w, r, http.StatusServiceUnavailable, "too many tail reads at once; try again")
			return
		}
		defer h.tails.Add(-1)
	}
	rep, status := h.srv.remoteRequest(r.Context(), req)
	writeReply(w, status, rep)
}

// remoteRequest answers a remote client's request as the socket would,
// except that there is no hello and a skip-permissions mode is refused
// whatever it carries (SPEC §14).
func (s *Server) remoteRequest(ctx context.Context, r Request) (Reply, int) {
	if err := checkID(r.ID); err != nil {
		return Reply{Type: TypeError, Error: err.Error()}, http.StatusBadRequest
	}
	switch r.Type {
	case TypeHello:
		return Reply{Type: TypeError, ID: r.ID, Error: "there is no hello over HTTP: the event stream starts with the snapshot"}, http.StatusBadRequest
	case TypeTail:
		if rep, bad := checkTail(r); bad {
			return rep, http.StatusBadRequest
		}
		rep := s.readTail(ctx, r)
		if rep.Type == TypeError {
			return rep, http.StatusBadRequest
		}
		return rep, http.StatusOK
	}
	cmd, err := r.command()
	if err != nil {
		return Reply{Type: TypeError, ID: r.ID, Error: err.Error()}, http.StatusBadRequest
	}
	if cmd.Kind == engine.CmdMode || cmd.Kind == engine.CmdTaskMode {
		// The engine's own reading of the mode: once the run's phrase was
		// typed it takes a skip-permissions mode even without yes.
		if engine.SkipsPermissions(strings.ToLower(strings.TrimSpace(cmd.Text))) {
			return Reply{Type: TypeError, ID: r.ID, Error: errRemoteYolo}, http.StatusForbidden
		}
		cmd.Yes = false
	}
	s.answering(cmd)
	if s.opts.Sender != nil {
		s.opts.Sender.Send(cmd)
	}
	return Reply{Type: TypeAck, ID: r.ID}, http.StatusOK
}

// attachRemote attaches conn as a client that only listens: its snapshot
// is queued at once and the events follow, until conn closes. It reports
// false when the server is closed.
func (s *Server) attachRemote(conn net.Conn) bool {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = conn.Close()
		return false
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		s.serveClient(conn, func(c *client) {
			s.snapshot(c)
			// Until the stream's side closes, or the client is closed.
			_, _ = io.Copy(io.Discard, c.conn)
		})
	}()
	return true
}

// method answers 405 unless r uses want.
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method == want {
		return true
	}
	w.Header().Set("Allow", want)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// fail answers an error: a protocol-1 error under /api/, else text.
func (h *remote) fail(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeReply(w, status, Reply{Type: TypeError, Error: msg})
		return
	}
	// A page the browser opened gets the UI's refusal page (the brand and
	// the next step, SPEC §15.8); its script, stylesheet and icon a short
	// text.
	if p, ok := h.opts.UI.(refuser); ok && !strings.HasPrefix(r.URL.Path, pathStatic) && r.URL.Path != pathFavicon {
		p.refuse(w, status, msg)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "igris: "+msg+"\n")
}

// refuser draws a refusal as a page: the web UI's handler is one.
type refuser interface {
	refuse(w http.ResponseWriter, status int, msg string)
}

func (h *remote) tooMany(w http.ResponseWriter, r *http.Request, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
	h.fail(w, r, http.StatusTooManyRequests, "too many failed attempts from this address; wait a minute")
}

// writeReply writes rep as JSON with status.
func writeReply(w http.ResponseWriter, status int, rep Reply) {
	rep.V = Protocol
	b, err := encode(rep)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"v":1,"type":"error","error":"reply not encoded"}`+"\n")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// addrKey is the rate limit's key of a remote address: an IPv4 address,
// or an IPv6 address's /64, which one host usually holds whole.
func addrKey(remoteAddr string) string {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	a := ap.Addr().Unmap()
	if a.Is6() {
		p, _ := a.WithZone("").Prefix(64)
		return p.String()
	}
	return a.String()
}

// limiter counts failed authentications per address in fixed windows
// (SPEC §14). The table is bounded: once full, new addresses share one
// entry, so a flood of addresses fails closed rather than growing it.
type limiter struct {
	mu   sync.Mutex
	now  func() time.Time
	seen map[string]*window
}

type window struct {
	start time.Time
	fails int
}

// overflowKey is the entry addresses share once the table is full.
const overflowKey = "*"

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, seen: map[string]*window{}}
}

// blocked reports whether key has failed too often in its window, and how
// long until the window ends.
func (l *limiter) blocked(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, k := range []string{key, overflowKey} {
		w := l.seen[k]
		if w == nil || now.Sub(w.start) >= authWindow {
			continue
		}
		if w.fails >= authFails {
			return w.start.Add(authWindow).Sub(now), true
		}
		if k == key {
			break // tracked on its own: the shared entry isn't its
		}
	}
	return 0, false
}

// fail counts a failed authentication of key.
func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w := l.seen[key]
	if w == nil && len(l.seen) >= limiterMax {
		for k, old := range l.seen {
			if now.Sub(old.start) >= authWindow {
				delete(l.seen, k)
			}
		}
		if len(l.seen) >= limiterMax {
			key = overflowKey
			w = l.seen[key]
		}
	}
	if w == nil || now.Sub(w.start) >= authWindow {
		w = &window{start: now}
		l.seen[key] = w
	}
	w.fails++
}
