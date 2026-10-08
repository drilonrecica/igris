package state

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
)

// LogVersion is the run log schema igris writes (SPEC §13, docs/runlog.md).
// A line without "v" is version 0 (igris v0.2–v0.4).
const LogVersion = 1

// EventType classifies a run log event.
type EventType string

const (
	EventRunStarted    EventType = "run_started"
	EventRunStopped    EventType = "run_stopped"
	EventTaskStarted   EventType = "task_started"
	EventTaskResumed   EventType = "task_resumed"
	EventTaskRetried   EventType = "task_retried"
	EventTaskDone      EventType = "task_done"
	EventTaskSkipped   EventType = "task_skipped"
	EventTaskOverdue   EventType = "task_overdue"
	EventTaskReset     EventType = "task_reset"
	EventVerifyPassed  EventType = "verify_passed"
	EventVerifyFailed  EventType = "verify_failed"
	EventCommitted     EventType = "committed"
	EventNeedsYou      EventType = "needs_you"
	EventNeedsYouClear EventType = "needs_you_clear"
	EventNotification  EventType = "notification"
	EventError         EventType = "error"
)

// Why a needs_you event says igris waits on the owner (SPEC §13). Readers
// see an unknown reason as ReasonOther.
const (
	ReasonIdle          = "idle"            // settled for needs_input_after without a signal
	ReasonBlocked       = "blocked"         // waiting for a permission or an answer
	ReasonSkipRequest   = "skip_request"    // the session asks to skip its task
	ReasonSessionLost   = "session_lost"    // the session is gone
	ReasonTaskOverdue   = "task_overdue"    // the attempt runs past the task's Timeout
	ReasonVerifyLimit   = "verify_limit"    // verify_max_attempts failures in a row
	ReasonVerifyNotSent = "verify_not_sent" // a verify failure could not be sent to the session
	ReasonHookFailed    = "hook_failed"     // the before_task hook failed
	ReasonCommit        = "commit"          // the commit question under commit = "ask"
	ReasonResetRequest  = "reset_request"   // a reset request waits for confirmation
	ReasonPlanChanged   = "plan_changed"    // the plan changed outside igris (SPEC §5.4)
	ReasonOther         = "other"
)

var reasons = map[string]bool{
	ReasonIdle: true, ReasonBlocked: true, ReasonSkipRequest: true, ReasonSessionLost: true,
	ReasonTaskOverdue: true, ReasonVerifyLimit: true, ReasonVerifyNotSent: true, ReasonHookFailed: true,
	ReasonCommit: true, ReasonResetRequest: true, ReasonPlanChanged: true,
}

// Event is one line of .igris/runs.jsonl (SPEC §13, docs/runlog.md). Fields
// are only ever added within a version, never removed or renamed.
type Event struct {
	V    int       `json:"v"` // LogVersion when written; 0 for a v0 line
	At   time.Time `json:"at"`
	Type EventType `json:"type"`
	// Run is the run ID (NewRunID) on every line a run writes.
	Run   string `json:"run,omitempty"`
	Task  string `json:"task,omitempty"`
	Rank  string `json:"rank,omitempty"`
	Model string `json:"model,omitempty"`
	// Attempt is the agent task's attempt within the run, from 1.
	Attempt int `json:"attempt,omitempty"`
	// Session is the Claude session UUID, on task_started, task_resumed and
	// task_retried.
	Session string `json:"session,omitempty"`
	// Phase, Title and Owner describe the task on task_started and
	// task_resumed.
	Phase   string `json:"phase,omitempty"`
	Title   string `json:"title,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Profile string `json:"profile,omitempty"` // verify profile, on verify_*
	Commit  string `json:"commit,omitempty"`  // commit SHA, on committed
	// DurationMS is the task's time in this run on task_done/task_skipped,
	// the verify command's on verify_*, and the run's on run_stopped.
	DurationMS int64  `json:"duration_ms,omitempty"`
	Reason     string `json:"reason,omitempty"` // on needs_you and needs_you_clear
	Detail     string `json:"detail,omitempty"` // never secrets (AGENTS §6)
}

var runIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// ValidRunID reports whether id has the shape of a run ID (NewRunID).
func ValidRunID(id string) bool { return runIDPattern.MatchString(id) }

// NewRunID returns the ID of a run starting at start: its time in UTC and
// 4 random hex characters, e.g. 20261008-091500-3fa2 (SPEC §13). It has
// dashes, so it never looks like a `report` index.
func NewRunID(start time.Time) (string, error) {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate run ID: %w", err)
	}
	return start.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]), nil
}

// Append adds e to the run log as a LogVersion line. A zero At is set to
// the current time. A log whose last line was cut off (a crash, a full
// disk) gets the newline first, so the new line is never glued to it.
func (d *Dir) Append(e Event) error {
	if e.At.IsZero() {
		e.At = d.now()
	}
	e.V, e.At = LogVersion, e.At.UTC()
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("append to run log: %w", err)
	}
	f, err := os.OpenFile(d.eventsPath(), os.O_RDWR|os.O_CREATE|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("append to run log: %w", err)
	}
	if cut, err := endsMidLine(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("append to run log %s: %w", d.eventsPath(), err)
	} else if cut {
		line = append([]byte{'\n'}, line...)
	}
	// One write per line, so concurrent appends don't interleave.
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("append to run log %s: %w", d.eventsPath(), err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("append to run log %s: %w", d.eventsPath(), err)
	}
	return nil
}

// endsMidLine reports whether f is not empty and doesn't end in a newline.
func endsMidLine(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false, err
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], st.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
}

// Events reads the run log in order. A missing log has no events. A
// truncated last line (e.g. after a crash) is ignored; any other line that
// doesn't decode is an error. Readers that only show the log use PeekLog,
// which skips such lines.
func (d *Dir) Events() ([]Event, error) {
	data, err := os.ReadFile(d.eventsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run log: %w", err)
	}
	log, err := parseEvents(data, d.eventsPath(), true)
	return log.Events, err
}

// ErrNoLog is returned by PeekLog and PeekEvents when there is no run log.
var ErrNoLog = errors.New("no run log")

// Log is the run log as PeekLog reads it.
type Log struct {
	Events []Event
	// Unreadable counts the lines that were skipped: not JSON, not an
	// event, or longer than MaxLogLine.
	Unreadable int
}

// MaxLogLine is the longest run log line read; longer ones are skipped.
const MaxLogLine = 1 << 20

// PeekLog reads root/.igris/runs.jsonl without creating or changing
// anything. It returns ErrNoLog if there is none. A line that can't be read
// is skipped and counted, never an error, so one bad line can't hide the
// rest of the history (SPEC §13); a cut-off last line is skipped without
// being counted (igris may be writing it).
func PeekLog(root string) (Log, error) {
	path := filepath.Join(root, DirName, "runs.jsonl")
	data, err := os.ReadFile(path) //nolint:gosec // igris's own run log
	if errors.Is(err, fs.ErrNotExist) {
		return Log{}, ErrNoLog
	}
	if err != nil {
		return Log{}, fmt.Errorf("read run log: %w", err)
	}
	return parseEvents(data, path, false)
}

// PeekEvents is PeekLog's events.
func PeekEvents(root string) ([]Event, error) {
	log, err := PeekLog(root)
	return log.Events, err
}

// parseEvents decodes run log lines. An unterminated last line that
// doesn't decode is dropped. With strict, any other line that doesn't
// decode is an error; without it, it is skipped and counted. A line from a
// newer igris (v > LogVersion) whose fields have other types than this
// version's keeps the fields that decode.
func parseEvents(data []byte, path string, strict bool) (Log, error) {
	var log Log
	for n := 1; len(data) > 0; n++ {
		line, rest, terminated := bytes.Cut(data, []byte("\n"))
		data = rest
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(line) > MaxLogLine {
			if strict {
				return log, fmt.Errorf("read run log %s:%d: line longer than %d bytes", path, n, MaxLogLine)
			}
			log.Unreadable++
			continue
		}
		e, err := decodeEvent(line)
		switch {
		case err == nil:
			log.Events = append(log.Events, e.checked())
		case !terminated:
			// Cut off, e.g. by a crash: not an event yet.
		case strict:
			return log, fmt.Errorf("read run log %s:%d: %w", path, n, err)
		default:
			log.Unreadable++
		}
	}
	return log, nil
}

// decodeEvent decodes one line. A field of the wrong type fails a line of
// this version; a newer version's line keeps the fields that decode.
func decodeEvent(line []byte) (Event, error) {
	var e Event
	err := json.Unmarshal(line, &e)
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && e.V > LogVersion {
		return e, nil
	}
	return e, err
}

// checked drops the values that fail their shape check: a bad run ID or
// session UUID counts as absent (it may end up in a command), an unknown
// needs-you reason is ReasonOther. The log is igris's own, but anything
// in the project can write it.
func (e Event) checked() Event {
	if e.Run != "" && !ValidRunID(e.Run) {
		e.Run = ""
	}
	if e.Session != "" && !backend.ValidClaudeSession(e.Session) {
		e.Session = ""
	}
	if e.Reason != "" && !reasons[e.Reason] {
		e.Reason = ReasonOther
	}
	if e.Attempt < 0 {
		e.Attempt = 0
	}
	// A duration no run can have (negative, or over maxDurationMS) is
	// unknown, so sums over the log can't overflow.
	if e.DurationMS < 0 || e.DurationMS > maxDurationMS {
		e.DurationMS = 0
	}
	return e
}

// maxDurationMS is the longest duration_ms read: 30 days.
const maxDurationMS = int64(30 * 24 * time.Hour / time.Millisecond)
