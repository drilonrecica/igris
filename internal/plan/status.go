package plan

import (
	"strings"
	"unicode"
)

// Status is a task status (SPEC §3.3).
type Status int

// Task statuses. StatusUnknown marks a cell that matched no keyword.
const (
	StatusUnknown Status = iota
	Ready
	Blocked
	InProgress
	Done
	Skipped
)

// statusKeywords lists keywords longest first so "in progress" is tried
// before any shorter keyword.
var statusKeywords = []struct {
	word string
	s    Status
}{
	{"in progress", InProgress},
	{"blocked", Blocked},
	{"skipped", Skipped},
	{"ready", Ready},
	{"done", Done},
}

// String returns the keyword as written in a plan.
func (s Status) String() string {
	for _, k := range statusKeywords {
		if k.s == s {
			return k.word
		}
	}
	return "unknown"
}

// Satisfied reports whether the status counts as a met dependency.
func (s Status) Satisfied() bool { return s == Done || s == Skipped }

// ParseStatus parses a Status cell: a keyword, case-insensitive, optionally
// in backticks, optionally followed by free text (the suffix), which is kept
// and ignored for logic. ok is false for an unknown status.
func ParseStatus(cell string) (s Status, suffix string, ok bool) {
	v := strings.TrimSpace(cell)
	if rest, found := strings.CutPrefix(v, "`"); found {
		// "`done` (note)" or "`done (note)`": drop the first pair of backticks.
		v = strings.Replace(rest, "`", "", 1)
	}
	lower := strings.ToLower(v)
	for _, k := range statusKeywords {
		if !strings.HasPrefix(lower, k.word) {
			continue
		}
		rest := v[len(k.word):]
		if rest != "" && isWordByte(rest[0]) {
			return StatusUnknown, "", false // "readyish", "done2"
		}
		return k.s, strings.TrimSpace(rest), true
	}
	return StatusUnknown, "", false
}

// statusSynonyms maps words other plans use for a status to the keyword
// igris expects. They only feed the hint in the validation error: an unknown
// status stays an error, igris never reads one as another (SPEC §3.3).
var statusSynonyms = map[string]Status{
	"dropped": Skipped, "cancelled": Skipped, "canceled": Skipped, "wontfix": Skipped,
	"abandoned": Skipped, "obsolete": Skipped, "n/a": Skipped,
	"completed": Done, "complete": Done, "finished": Done, "closed": Done,
	"wip": InProgress, "doing": InProgress, "started": InProgress, "in-progress": InProgress, "in_progress": InProgress,
}

// statusSynonym returns the keyword a known synonym in cell stands for: the
// cell's first word (or "won't do"), case-insensitive, backticks, '*' and
// leading symbols such as "✅" ignored.
func statusSynonym(cell string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(strings.NewReplacer("`", "", "*", "").Replace(cell)))
	v = strings.TrimLeftFunc(v, func(r rune) bool { return !unicode.IsLetter(r) })
	if s, _, ok := ParseStatus(v); ok {
		return s.String(), true // "✅ Done", "**done**"
	}
	if s, ok := statusSynonyms[v]; ok {
		return s.String(), true
	}
	if word, _, _ := strings.Cut(v, " "); word != v {
		if s, ok := statusSynonyms[strings.TrimRight(word, ":,;")]; ok {
			return s.String(), true
		}
	}
	if strings.HasPrefix(v, "won't do") {
		return Skipped.String(), true
	}
	return "", false
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// Owner says who works on a task (SPEC §3.2).
type Owner string

// Owners. The zero value marks an unknown owner.
const (
	OwnerAgent     Owner = "agent"
	OwnerUser      Owner = "user"
	OwnerAgentUser Owner = "agent + user"
)

// ParseOwner parses an Owner cell, case-insensitive and ignoring spacing
// around "+". An empty cell means agent. ok is false for an unknown owner.
func ParseOwner(cell string) (Owner, bool) {
	switch strings.Join(strings.Fields(strings.ToLower(strings.Trim(cell, " \t`"))), "") {
	case "", "agent":
		return OwnerAgent, true
	case "user":
		return OwnerUser, true
	case "agent+user":
		return OwnerAgentUser, true
	}
	return "", false
}

// IsAgent reports whether igris launches a session for the task.
func (o Owner) IsAgent() bool { return o == OwnerAgent || o == OwnerAgentUser }

// isNone reports whether a Deps, Model or Mode cell means "nothing": empty,
// "—", "-" or "none" (case-insensitive).
func isNone(s string) bool {
	s = strings.Trim(s, " \t`")
	return s == "" || s == "—" || s == "-" || strings.EqualFold(s, "none")
}
