package notify

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// defaultHTTP is the client a channel sends with: c (nil means a default
// one) with redirects turned off. Following one would carry the Gotify
// token, the webhook signature or a secret URL (in Referer) to whatever
// host the Location names, and its 200 would count as delivered; the 3xx
// is reported instead (statusError). The per-attempt deadline comes from
// the router's context; the client timeout is only a backstop for callers
// that use a channel on its own.
func defaultHTTP(c *http.Client) *http.Client {
	out := http.Client{Timeout: 30 * time.Second}
	if c != nil {
		out = *c
	}
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// transportError is a fixed text for a failed request. net/http's own
// text has the URL and the host, which may be a secret (a Discord or Slack
// webhook is its URL), so none of it is kept.
func transportError(err error) error {
	var dns *net.DNSError
	var rec tls.RecordHeaderError
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var alert tls.AlertError
	var ne net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return errors.New("cancelled")
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return errors.New("timed out")
	case errors.As(err, &dns):
		return errors.New("can't resolve host")
	case errors.Is(err, syscall.ECONNREFUSED):
		return errors.New("connection refused")
	case errors.As(err, &rec), errors.As(err, &verify), errors.As(err, &authority),
		errors.As(err, &host), errors.As(err, &invalid), errors.As(err, &alert):
		return errors.New("TLS failed")
	}
	return errors.New("connection failed")
}

// invalidURL is a channel's error for a URL that doesn't parse. url.Parse
// quotes parts of it, so its text is not kept.
func invalidURL(key string) error {
	return fmt.Errorf("%s is not a valid URL (it isn't shown, as it may be a secret); fix it in igris.toml or in the environment variable", key)
}

// statusError reports a non-2xx answer by its code only: the body may
// echo the request, and the reason phrase is the server's text. A redirect
// is not followed (defaultHTTP) and its Location is never shown.
func statusError(resp *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	code := resp.StatusCode
	if code/100 == 3 {
		return fmt.Errorf("server answered %d (redirect); set the final URL in igris.toml", code)
	}
	if text := http.StatusText(code); text != "" {
		return fmt.Errorf("server answered %d %s", code, text)
	}
	return fmt.Errorf("server answered %d", code)
}
