package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// EventType classifies a run log event.
type EventType string

const (
	EventRunStarted   EventType = "run_started"
	EventRunStopped   EventType = "run_stopped"
	EventTaskStarted  EventType = "task_started"
	EventTaskResumed  EventType = "task_resumed"
	EventTaskDone     EventType = "task_done"
	EventTaskSkipped  EventType = "task_skipped"
	EventTaskOverdue  EventType = "task_overdue"
	EventTaskReset    EventType = "task_reset"
	EventVerifyPassed EventType = "verify_passed"
	EventVerifyFailed EventType = "verify_failed"
	EventCommitted    EventType = "committed"
	EventNotification EventType = "notification"
	EventError        EventType = "error"
)

// Event is one line of .igris/runs.jsonl (SPEC §13).
type Event struct {
	At     time.Time `json:"at"`
	Type   EventType `json:"type"`
	Task   string    `json:"task,omitempty"`
	Rank   string    `json:"rank,omitempty"`
	Model  string    `json:"model,omitempty"`
	Detail string    `json:"detail,omitempty"` // never secrets (AGENTS §6)
}

// Append adds e to the run log. A zero At is set to the current time.
func (d *Dir) Append(e Event) error {
	if e.At.IsZero() {
		e.At = d.now()
	}
	e.At = e.At.UTC()
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("append to run log: %w", err)
	}
	f, err := os.OpenFile(d.eventsPath(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("append to run log: %w", err)
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

// Events reads the run log in order. A missing log has no events. A
// truncated last line (e.g. after a crash) is ignored.
func (d *Dir) Events() ([]Event, error) {
	data, err := os.ReadFile(d.eventsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run log: %w", err)
	}
	return parseEvents(data, d.eventsPath(), false)
}

// ErrNoLog is returned by PeekEvents when there is no run log.
var ErrNoLog = errors.New("no run log")

// PeekEvents reads root/.igris/runs.jsonl without creating or changing
// anything. It returns ErrNoLog if there is none. Unlike Events, a
// malformed last line is skipped even when newline-terminated.
func PeekEvents(root string) ([]Event, error) {
	path := filepath.Join(root, DirName, "runs.jsonl")
	data, err := os.ReadFile(path) //nolint:gosec // igris's own run log
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoLog
	}
	if err != nil {
		return nil, fmt.Errorf("read run log: %w", err)
	}
	return parseEvents(data, path, true)
}

// parseEvents decodes run log lines. A malformed last line is skipped if
// it is unterminated or lenientLast is set; any other one is an error.
func parseEvents(data []byte, path string, lenientLast bool) ([]Event, error) {
	var events []Event
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			if n == lineCount(data) && (lenientLast || !bytes.HasSuffix(data, []byte("\n"))) {
				break
			}
			return nil, fmt.Errorf("read run log %s:%d: %w", path, n, err)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read run log %s: %w", path, err)
	}
	return events, nil
}

func lineCount(data []byte) int {
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}
