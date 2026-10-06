package notify

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// defaultHTTP is used when a channel is given no client. The per-attempt
// deadline comes from the router's context; the client timeout is only a
// backstop for callers that use a channel on its own.
func defaultHTTP(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// transportError drops the request URL that net/http puts into its errors:
// for Discord the URL is the secret.
func transportError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// statusError reports a non-2xx answer by its status only; the body may echo
// the request.
func statusError(resp *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("server answered %s", resp.Status)
}
