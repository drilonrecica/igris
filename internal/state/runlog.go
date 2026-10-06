package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// EventType classifies a run log event.
type EventType string

const (
	EventRunStarted   EventType = "run_started"
	EventRunStopped   EventType = "run_stopped"
	EventTaskStarted  EventType = "task_started"
	EventTaskDone     EventType = "task_done"
	EventTaskSkipped  EventType = "task_skipped"
	EventVerifyPassed EventType = "verify_passed"
	EventVerifyFailed EventType = "verify_failed"
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
			if n == lineCount(data) && !bytes.HasSuffix(data, []byte("\n")) {
				break
			}
			return nil, fmt.Errorf("read run log %s:%d: %w", d.eventsPath(), n, err)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read run log %s: %w", d.eventsPath(), err)
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
