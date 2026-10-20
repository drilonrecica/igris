package usage

import "strings"

// aliases are the [models] values the rank audit knows the ids of: each
// matches claude-<alias>-… (SPEC §6.9).
var aliases = []string{"fable", "opus", "sonnet", "haiku"}

// Checkable reports whether the rank audit can check sessions run on want,
// a resolved [models] value: one of the aliases or a full id (claude-…).
// Anything else (default, opusplan, a custom name) is unchecked.
func Checkable(want string) bool {
	want = bare(want)
	return isAlias(want) || strings.HasPrefix(want, "claude-")
}

// Matches reports whether the model id a transcript records (id) is what
// want resolves to: an alias matches any claude-<alias>-… id, a full id
// matches itself with or without a -YYYYMMDD suffix, and a bracketed suffix
// such as [1m] is ignored on both. An unchecked want matches anything.
func Matches(want, id string) bool {
	want, id = bare(want), bare(id)
	switch {
	case isAlias(want):
		return strings.HasPrefix(id, "claude-"+want+"-")
	case strings.HasPrefix(want, "claude-"):
		return undated(want) == undated(id)
	}
	return true
}

// Mismatch returns the first of models that doesn't match want, if any.
func Mismatch(want string, models []string) (string, bool) {
	for _, m := range models {
		if !Matches(want, m) {
			return m, true
		}
	}
	return "", false
}

func isAlias(s string) bool {
	for _, a := range aliases {
		if s == a {
			return true
		}
	}
	return false
}

// bare is s in lower case without a bracketed suffix (opus[1m] → opus).
func bare(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, '['); i >= 0 && strings.HasSuffix(s, "]") {
		s = s[:i]
	}
	return s
}

// undated is a full id without a -YYYYMMDD suffix.
func undated(id string) string {
	i := strings.LastIndexByte(id, '-')
	if i < 0 || len(id)-i-1 != 8 || strings.Trim(id[i+1:], "0123456789") != "" {
		return id
	}
	return id[:i]
}
