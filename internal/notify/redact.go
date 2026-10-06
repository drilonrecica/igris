package notify

import "strings"

const redacted = "[redacted]"

// Redact replaces every non-empty secret in s. A secret that appears
// URL-escaped is not caught here; channels never put secrets in errors, this
// is the safety net for errors from net/http (which embed the request URL).
func Redact(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, redacted)
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
