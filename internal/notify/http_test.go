package notify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

// every channel that talks HTTP, built for one base URL.
func httpChannels(base string) []Channel {
	return []Channel{
		&Ntfy{Server: base, Topic: "igris", Token: "NTFYSECRET"},
		&Discord{WebhookURL: base + "/api/webhooks/1/HOOKSECRET"},
		&Slack{WebhookURL: base + "/services/T0/B0/HOOKSECRET"},
		&Webhook{URL: base + "/in/HOOKSECRET", Secret: "SIGSECRET"},
		&Gotify{Server: base, Token: "GOTIFYSECRET"},
	}
}

// A redirect is never followed: it would carry the token header, the
// signature or a secret URL (in Referer) to another host, and a 200 there
// would count as delivered. The Location is never shown.
func TestRedirectIsNotFollowed(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	t.Cleanup(target.Close)
	for _, code := range []int{301, 302, 307, 308} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/LOCATIONSECRET", code)
		}))
		for _, ch := range httpChannels(srv.URL) {
			err := ch.Send(context.Background(), Message{Event: NeedsInput, Project: "p"})
			if err == nil || !strings.Contains(err.Error(), "(redirect); set the final URL in igris.toml") {
				t.Errorf("%s %d: err = %v, want a redirect error", ch.Name(), code, err)
				continue
			}
			if strings.Contains(err.Error(), "LOCATIONSECRET") || strings.Contains(err.Error(), target.URL) {
				t.Errorf("%s %d: error shows the Location: %v", ch.Name(), code, err)
			}
		}
		// A client a caller passes in gets the same rule.
		d := &Discord{WebhookURL: srv.URL + "/x", HTTP: srv.Client()}
		if err := d.Send(context.Background(), Message{Event: NeedsInput}); err == nil {
			t.Errorf("%d: injected client followed the redirect", code)
		}
		srv.Close()
	}
	if n := followed.Load(); n != 0 {
		t.Errorf("redirect target got %d requests, want 0", n)
	}
}

// A transport failure is told by a fixed text: net/http's own text has the
// URL and the host, which may be a secret.
func TestTransportErrorTexts(t *testing.T) {
	wrap := func(err error) error {
		return &url.Error{Op: "Post", URL: "https://SECRETHOST/HOOKSECRET", Err: err}
	}
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"dns", wrap(&net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "SECRETHOST"}}), "can't resolve host"},
		{"refused", wrap(&net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{}, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}), "connection refused"},
		{"tls header", wrap(tls.RecordHeaderError{Msg: "SECRETHOST first record does not look like a TLS handshake"}), "TLS failed"},
		{"tls verify", wrap(&tls.CertificateVerificationError{Err: x509.HostnameError{Host: "SECRETHOST", Certificate: &x509.Certificate{}}}), "TLS failed"},
		{"unknown authority", wrap(x509.UnknownAuthorityError{}), "TLS failed"},
		{"deadline", wrap(context.DeadlineExceeded), "timed out"},
		{"net timeout", wrap(&net.OpError{Op: "dial", Err: timeoutErr{}}), "timed out"},
		{"other", wrap(errors.New("SECRETHOST: unexpected EOF")), "connection failed"},
		{"eof", wrap(io.ErrUnexpectedEOF), "connection failed"},
	} {
		got := transportError(tt.err)
		if got == nil || got.Error() != tt.want {
			t.Errorf("%s: %v, want %q", tt.name, got, tt.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout SECRETHOST" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// Real failures against local servers: refused and TLS.
func TestTransportErrorsFromRealServers(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	closed := srv.URL
	srv.Close()
	tlsSrv := httptest.NewUnstartedServer(http.NotFoundHandler())
	tlsSrv.Config.ErrorLog = log.New(io.Discard, "", 0) // the failed handshakes
	tlsSrv.StartTLS()                                   // a certificate igris doesn't trust
	t.Cleanup(tlsSrv.Close)
	for _, tt := range []struct{ base, want string }{{closed, "connection refused"}, {tlsSrv.URL, "TLS failed"}} {
		for _, ch := range httpChannels(tt.base) {
			err := ch.Send(context.Background(), Message{Event: NeedsInput})
			if err == nil || err.Error() != ch.Name()+": "+tt.want {
				t.Errorf("%s at %s: %v, want %q", ch.Name(), tt.base, err, ch.Name()+": "+tt.want)
			}
		}
	}
}

// A URL that doesn't parse gets a fixed text: url.Parse quotes parts of
// it.
func TestInvalidURLErrorsAreFixed(t *testing.T) {
	for _, ch := range []Channel{
		&Discord{WebhookURL: "https://h/%zzHOOKSECRET"},
		&Slack{WebhookURL: "https://h/%zzHOOKSECRET"},
		&Webhook{URL: "https://h/%zzHOOKSECRET"},
		&Gotify{Server: "https://h/%zzHOOKSECRET", Token: "t"},
		&Ntfy{Server: "https://h/%zzHOOKSECRET", Topic: "t"},
	} {
		err := ch.Send(context.Background(), Message{Event: NeedsInput})
		if err == nil || strings.Contains(err.Error(), "zz") || strings.Contains(err.Error(), "HOOKSECRET") || !strings.Contains(err.Error(), "not a valid URL") {
			t.Errorf("%s: %v", ch.Name(), err)
		}
	}
}

// A secret that contains another is replaced first, so no part of it is
// left in the text.
func TestRedactLongestFirst(t *testing.T) {
	for _, secrets := range [][]string{{"abc", "abcdef"}, {"abcdef", "abc"}} {
		if got := Redact("x abcdef y abc", secrets); got != "x [redacted] y [redacted]" {
			t.Errorf("%v: %q", secrets, got)
		}
	}
	// An escaped form longer than another secret goes first too.
	if got := Redact("k=a+b%2Fc", []string{"a", "a b/c"}); got != "k=[redacted]" {
		t.Errorf("escaped: %q", got)
	}
}

// Overlapping secrets: the longer one is hidden whole.
func TestRedactOverlap(t *testing.T) {
	if got := Redact("xabcdef", []string{"xab", "abcdef"}); strings.Contains(got, "cdef") {
		t.Errorf("%q shows part of the longer secret", got)
	}
}
