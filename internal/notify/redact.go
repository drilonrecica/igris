package notify

import (
	"net/url"
	"strings"
)

const redacted = "[redacted]"

// Redact replaces every non-empty secret in s, as written and in its
// URL-escaped forms (query and path escaping). Channels never put secrets in
// errors; this is the safety net for errors from net/http (which embed the
// request URL).
func Redact(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, redacted)
		for _, esc := range []string{url.QueryEscape(sec), url.PathEscape(sec)} {
			if esc != sec {
				s = strings.ReplaceAll(s, esc, redacted)
			}
		}
	}
	return s
}

type redactedError struct{ msg string }

func (e redactedError) Error() string { return e.msg }

// redactError returns an error with the same text as err, minus secrets. The
// original is dropped on purpose: unwrapping it would give the secret back.
func redactError(err error, secrets []string) error {
	if err == nil {
		return nil
	}
	return redactedError{Redact(err.Error(), secrets)}
}
