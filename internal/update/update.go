// Package update is `igris update` and home's Check for updates (SPEC §14,
// §15.6, decisions.md V12-P4): it asks GitHub's releases API for the
// latest release, compares it with the running igris, tells how this igris
// was installed and builds the package manager's command for it. Only the
// owner starts it; igris never checks on its own and never replaces its
// own binary.
//
// GitHub's answer is untrusted: it is read bounded, and only its tag is
// used, once it has the shape vX.Y.Z. No other text of it is shown.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// API is GitHub's API, which Latest asks by default.
	API = "https://api.github.com"
	// Module is igris's Go module, which `go install` names.
	Module = "github.com/drilonrecica/igris"
	// repo is the repository whose releases Latest reads.
	repo = "drilonrecica/igris"
	// Timeout bounds the request to GitHub.
	Timeout = 10 * time.Second
	// maxBody bounds GitHub's answer; the latest release's JSON is a few
	// KiB, its release notes included.
	maxBody = 1 << 20
)

// tagShape is the only tag Latest accepts: a release, never a pre-release
// (which the latest endpoint skips anyway) or anything else.
var tagShape = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// ReleaseURL is the release page of version v (X.Y.Z), built by igris
// rather than taken from GitHub's answer.
func ReleaseURL(v string) string {
	return "https://github.com/" + repo + "/releases/tag/v" + v
}

// Latest asks the releases API at baseURL ("" means API) for the latest
// release and returns its version, X.Y.Z. client nil means a default one,
// which takes proxies from the environment; either way the request has
// Timeout. A failed request, an answer other than 200, more than 1 MiB of
// body or a tag of another shape is an error that says what to do.
func Latest(ctx context.Context, client *http.Client, baseURL, userAgent string) (string, error) {
	if client == nil {
		client = &http.Client{}
	}
	if baseURL == "" {
		baseURL = API
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return "", fmt.Errorf("ask GitHub for the latest release: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", requestError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", statusError(resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", requestError(err)
	}
	if len(body) > maxBody {
		return "", errors.New("GitHub's answer about the latest release is over 1 MiB; try again later")
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", errors.New("GitHub's answer about the latest release isn't the JSON igris expects; try again later")
	}
	if !tagShape.MatchString(rel.Tag) {
		return "", fmt.Errorf("the latest release's tag is not vX.Y.Z, so igris doesn't use it; see https://github.com/%s/releases", repo)
	}
	return strings.TrimPrefix(rel.Tag, "v"), nil
}

// requestError says why the request failed, by kind: net/http's own text
// is not kept, so none of the answer reaches the owner's terminal.
func requestError(err error) error {
	var dns *net.DNSError
	var ne net.Error
	what := "the request failed"
	switch {
	case errors.Is(err, context.Canceled):
		return errors.New("asking GitHub for the latest release was cancelled")
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		what = fmt.Sprintf("no answer within %s", Timeout)
	case errors.As(err, &dns):
		what = "can't resolve api.github.com"
	}
	return &NetworkError{msg: "ask GitHub for the latest release: " + what + "; check your connection and try again"}
}

// NetworkError is a request to GitHub that didn't get an answer: home adds
// that the connection may be the problem.
type NetworkError struct{ msg string }

func (e *NetworkError) Error() string { return e.msg }

// statusError reports an answer other than 200 by its code only.
func statusError(code int) error {
	text := fmt.Sprintf("GitHub answered %d", code)
	if s := http.StatusText(code); s != "" {
		text += " " + s
	}
	switch code {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return errors.New(text + " to the request for the latest release (its rate limit is 60 requests an hour per address); try again later")
	case http.StatusNotFound:
		return errors.New(text + ": no release found; see https://github.com/" + repo + "/releases")
	}
	return errors.New(text + " to the request for the latest release; try again later")
}
