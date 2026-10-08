package notify

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
)

const redacted = "[redacted]"

// marker holds a secret's place while Redact works; no error text has it.
const marker = "\x00"

// Redact replaces every non-empty secret in s, as written and in its
// URL-escaped forms (query and path escaping). The longest form goes
// first, so a secret that contains another is never left half shown; a
// marker stands in until the end, so "[redacted]" itself is never
// searched for a secret.
// Channels never put secrets in errors; this is the safety net for errors
// from net/http (which embed the request URL).
func Redact(s string, secrets []string) string {
	var forms []string
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		forms = append(forms, sec, url.QueryEscape(sec), url.PathEscape(sec))
	}
	if len(forms) == 0 {
		return s
	}
	slices.SortFunc(forms, func(a, b string) int { return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b)) })
	for _, f := range slices.Compact(forms) {
		s = strings.ReplaceAll(s, f, marker)
	}
	return strings.ReplaceAll(s, marker, redacted)
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
