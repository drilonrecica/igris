package config

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"text/template"
	tparse "text/template/parse"
	"time"
	"unicode/utf8"
)

// QuietWindow is [notify] quiet: a daily window in wall-clock time,
// [Start, End) in minutes after midnight, crossing midnight when Start >
// End (SPEC §10).
type QuietWindow struct {
	Start, End int
}

// ParseQuiet reads "HH:MM-HH:MM" (24-hour). ok is false for "", which
// means no quiet hours.
func ParseQuiet(s string) (w QuietWindow, ok bool, err error) {
	if s == "" {
		return QuietWindow{}, false, nil
	}
	bad := fmt.Errorf("notify.quiet = %q is invalid; use \"HH:MM-HH:MM\" in 24-hour local time, e.g. \"22:00-07:00\", or \"\" for none", s)
	from, to, found := strings.Cut(s, "-")
	if !found {
		return QuietWindow{}, false, bad
	}
	if w.Start, ok = clockMinutes(from); !ok {
		return QuietWindow{}, false, bad
	}
	if w.End, ok = clockMinutes(to); !ok {
		return QuietWindow{}, false, bad
	}
	if w.Start == w.End {
		return QuietWindow{}, false, fmt.Errorf("notify.quiet = %q starts and ends at the same time; give a window such as \"22:00-07:00\", or \"\" for none", s)
	}
	return w, true, nil
}

// clockMinutes reads "HH:MM" as minutes after midnight.
func clockMinutes(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	for _, i := range []int{0, 1, 3, 4} {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	h, m := int(s[0]-'0')*10+int(s[1]-'0'), int(s[3]-'0')*10+int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// Contains reports whether t's wall-clock time, in t's own location, is in
// the window. Being evaluated each time, the window moves with DST.
func (w QuietWindow) Contains(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	if w.Start < w.End {
		return m >= w.Start && m < w.End
	}
	return m >= w.Start || m < w.End
}

// String is the window as a digest names it, "22:00–07:00".
func (w QuietWindow) String() string {
	return fmt.Sprintf("%02d:%02d–%02d:%02d", w.Start/60, w.Start%60, w.End/60, w.End%60)
}

// defaultBreakThrough are the events quiet hours never hold by default:
// the urgent ones.
func defaultBreakThrough() []string { return []string{"needs_input", "session_lost", "task_overdue"} }

// BreakThrough is [notify] break_through: the events quiet hours never
// hold. An empty list holds everything.
type BreakThrough []string

// IsZero reports the default list (or none given), so a config that leaves
// it alone keeps the hash it had before the key existed.
func (b BreakThrough) IsZero() bool { return b == nil || slices.Equal(b, defaultBreakThrough()) }

// TaskDoneDigest is [notify] task_done_digest: 0 sends one task_done
// message per task, N ≥ 2 one per N tasks, DigestPhase one per phase. In
// igris.toml it is an integer or "phase".
type TaskDoneDigest int

// DigestPhase is task_done_digest = "phase".
const DigestPhase TaskDoneDigest = -1

const digestPhaseName = "phase"

func badDigest(v any) error {
	return fmt.Errorf("notify.task_done_digest = %v is invalid; use 0 (one message per task), a number of tasks of at least 2, or %q", v, digestPhaseName)
}

// UnmarshalTOML implements toml.Unmarshaler: an integer or "phase"; 1
// means 0.
func (d *TaskDoneDigest) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case int64:
		if x < 0 || x > 1_000_000 {
			return badDigest(x)
		}
		if x == 1 {
			x = 0
		}
		*d = TaskDoneDigest(x)
		return nil
	case string:
		if x != digestPhaseName {
			return badDigest(strconv.Quote(x))
		}
		*d = DigestPhase
		return nil
	}
	return badDigest(v)
}

// MarshalTOML implements toml.Marshaler.
func (d TaskDoneDigest) MarshalTOML() ([]byte, error) {
	return []byte(d.String()), nil
}

// String is the value as igris.toml writes it: 0, 5 or "phase".
func (d TaskDoneDigest) String() string {
	if d == DigestPhase {
		return strconv.Quote(digestPhaseName)
	}
	return strconv.Itoa(int(d))
}

// Every reports how many task_done messages make one: 0 when they are not
// counted (off, or one per phase).
func (d TaskDoneDigest) Every() int {
	if d < 2 {
		return 0
	}
	return int(d)
}

// On reports whether task_done messages are grouped at all.
func (d TaskDoneDigest) On() bool { return d == DigestPhase || d >= 2 }

// MessageVars are the variables a notification template sees (SPEC §10):
// exactly these, never file contents or command output.
type MessageVars struct {
	Event   string
	Project string
	Phase   string
	TaskID  string
	Title   string // markdown stripped
	What    string
	RunID   string
	At      time.Time // local time
}

// MaxTemplate is the longest notification template, in characters.
const MaxTemplate = 1000

// maxTemplateOutput bounds what one template execution may write, so a
// runaway template can't eat memory; channels cut far shorter.
const maxTemplateOutput = 64 << 10

// sampleVars is what a template is tried against when the config loads.
var sampleVars = MessageVars{
	Event: "task_done", Project: "demo", Phase: "M1", TaskID: "M1-03", Title: "Config loader",
	What: "done", RunID: "20261008-091500-3fa2", At: time.Date(2026, 10, 8, 9, 15, 0, 0, time.Local),
}

// ParseMessageTemplate parses a notification template and runs it once on
// a sample message, so an unknown variable fails here, not at 3 a.m. name
// is the config key ("notify.slack.template").
func ParseMessageTemplate(name, text string) (*template.Template, error) {
	if n := utf8.RuneCountInString(text); n > MaxTemplate {
		return nil, fmt.Errorf("%s is %d characters long; keep it to %d", name, n, MaxTemplate)
	}
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err == nil {
		if action := loopOrCall(t); action != "" {
			return nil, fmt.Errorf("%s uses {{%s}}; a notification template may use only variables, if/else and with (no range, define, template or block)", name, action)
		}
		_, err = ExecuteMessageTemplate(t, sampleVars)
	}
	if err != nil {
		hint := "fix the template (Go text/template syntax)"
		if strings.Contains(err.Error(), "can't evaluate field") {
			hint = "use Event, Project, Phase, TaskID, Title, What, RunID or At"
		}
		return nil, fmt.Errorf("%s: %w; %s", name, err, hint)
	}
	return t, nil
}

// loopOrCall names the first action of t that loops or calls a template
// ("range", "define", "template"; a block is a define and a template), ""
// when there is none. A template runs at every message, so it must stay a
// few substitutions: a loop could spin the CPU for as long as it likes,
// and nothing in a message is a list.
func loopOrCall(t *template.Template) string {
	if len(t.Templates()) > 1 {
		return "define"
	}
	var walk func(n tparse.Node) string
	walkList := func(l *tparse.ListNode) string {
		if l == nil {
			return ""
		}
		for _, n := range l.Nodes {
			if a := walk(n); a != "" {
				return a
			}
		}
		return ""
	}
	walk = func(n tparse.Node) string {
		switch n := n.(type) {
		case *tparse.RangeNode:
			return "range"
		case *tparse.TemplateNode:
			return "template"
		case *tparse.IfNode:
			return cmp.Or(walkList(n.List), walkList(n.ElseList))
		case *tparse.WithNode:
			return cmp.Or(walkList(n.List), walkList(n.ElseList))
		case *tparse.ListNode:
			return walkList(n)
		}
		return ""
	}
	if t.Tree == nil {
		return ""
	}
	return walkList(t.Root)
}

// ExecuteMessageTemplate runs t on v. Output past 64 KiB is an error.
func ExecuteMessageTemplate(t *template.Template, v MessageVars) (string, error) {
	w := &cappedWriter{max: maxTemplateOutput}
	if err := t.Execute(w, v); err != nil {
		return "", err
	}
	return w.b.String(), nil
}

var errTemplateTooLong = errors.New("the template writes more than 64 KiB")

type cappedWriter struct {
	b   strings.Builder
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.b.Len()+len(p) > w.max {
		return 0, errTemplateTooLong
	}
	return w.b.Write(p)
}

// validateNotify checks the [notify] settings v0.5 added: quiet hours,
// break_through, task_done_digest and the channel templates (SPEC §12).
func (c *Config) validateNotify(add func(string, ...any)) {
	n := c.Notify
	if _, _, err := ParseQuiet(n.Quiet); err != nil {
		add("%v", err)
	}
	for _, ev := range n.BreakThrough {
		if !contains(validEvents, ev) {
			add("notify.break_through contains unknown event %q; use any of: %s", ev, strings.Join(validEvents, ", "))
		}
	}
	if n.TaskDoneDigest < DigestPhase {
		add("%v", badDigest(int(n.TaskDoneDigest)))
	}
	for _, tt := range n.templates() {
		if tt.text == "" {
			continue
		}
		if _, err := ParseMessageTemplate(tt.key, tt.text); err != nil {
			add("%v", err)
		}
	}
}

// templates lists the channel templates by config key.
func (n Notify) templates() []struct{ key, text string } {
	return []struct{ key, text string }{
		{"notify.ntfy.template", n.Ntfy.Template},
		{"notify.discord.template", n.Discord.Template},
		{"notify.webhook.template", n.Webhook.Template},
		{"notify.slack.template", n.Slack.Template},
		{"notify.gotify.template", n.Gotify.Template},
	}
}

// QuietHours returns the quiet window; ok is false when there is none (or
// the setting is invalid, which Validate reports).
func (n Notify) QuietHours() (QuietWindow, bool) {
	w, ok, err := ParseQuiet(n.Quiet)
	return w, ok && err == nil
}

// BreakThroughEvents is break_through, the default list when unset.
func (n Notify) BreakThroughEvents() []string {
	if n.BreakThrough == nil {
		return defaultBreakThrough()
	}
	return n.BreakThrough
}
