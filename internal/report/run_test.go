package report

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

var t0 = time.Date(2026, 10, 7, 9, 30, 0, 0, time.UTC)

func goodRun() *state.Run {
	return &state.Run{
		Version: 1, StartedAt: t0, Phases: []string{"V02-C", "V02-D"}, Through: "V02-D",
		Current: &state.Current{TaskID: "V02-03", Mode: "auto", StartedAt: t0.Add(time.Minute),
			Session: &backend.SessionRef{Backend: "herdr", TabID: "t1", PaneID: "p2"}},
	}
}

func TestNewRunInfoNone(t *testing.T) {
	if got := NewRunInfo(RunInput{RunErr: state.ErrNoRun}); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

func TestNewRunInfo(t *testing.T) {
	tests := []struct {
		name string
		in   RunInput
		want RunInfo
	}{
		{"running here", RunInput{Run: goodRun(), Lock: state.LockState{Held: true, Alive: true, Info: state.LockInfo{PID: 42}},
			Signals: []state.Signal{{ID: "V02-03", Action: "done"}}},
			RunInfo{Phases: []string{"V02-C", "V02-D"}, Through: "V02-D", StartedAt: "2026-10-07T09:30:00Z", Task: "V02-03", Mode: "auto",
				Since: "2026-10-07T09:31:00Z", Session: "herdr t1 p2", Lock: LockHere, LockDetail: "pid 42", Signals: []string{"V02-03 done"}}},
		{"elsewhere", RunInput{Run: goodRun(), Lock: state.LockState{Held: true, Remote: true, Info: state.LockInfo{PID: 7, Host: "box"}}},
			RunInfo{Phases: []string{"V02-C", "V02-D"}, Through: "V02-D", StartedAt: "2026-10-07T09:30:00Z", Task: "V02-03", Mode: "auto",
				Since: "2026-10-07T09:31:00Z", Session: "herdr t1 p2", Lock: LockElsewhere, LockDetail: "pid 7 on box", Signals: []string{}}},
		{"stale between tasks", RunInput{Run: &state.Run{Version: 1, StartedAt: t0, Phases: []string{"A"}},
			Lock: state.LockState{Held: true, Stale: true, Reason: "process no longer running", Info: state.LockInfo{PID: 9}}, SignalsBad: 1},
			RunInfo{Phases: []string{"A"}, StartedAt: "2026-10-07T09:30:00Z", Lock: LockStale, LockDetail: "pid 9: process no longer running", Signals: []string{"1 unreadable"}}},
		{"lock only", RunInput{RunErr: state.ErrNoRun, Lock: state.LockState{Held: true, Unreadable: true, Reason: "bad json"}},
			RunInfo{Lock: LockUnreadable, LockDetail: "bad json", Signals: []string{}}},
		{"state without lock", RunInput{Run: &state.Run{Version: 1, StartedAt: t0, Phases: []string{"A"}}},
			RunInfo{Phases: []string{"A"}, StartedAt: "2026-10-07T09:30:00Z", Lock: LockNone, Signals: []string{}}},
		// A user task has no session, so the engine stores no mode (found at gate V02-G).
		{"user task without mode", RunInput{Run: &state.Run{Version: 1, StartedAt: t0, Phases: []string{"A"},
			Current: &state.Current{TaskID: "A-04", StartedAt: t0.Add(time.Minute)}}},
			RunInfo{Phases: []string{"A"}, StartedAt: "2026-10-07T09:30:00Z", Task: "A-04", Since: "2026-10-07T09:31:00Z",
				Lock: LockNone, Signals: []string{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewRunInfo(tt.in)
			if got == nil || !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestNewRunInfoUnreadableState(t *testing.T) {
	badMode, badTask, badPhase, badThrough := goodRun(), goodRun(), goodRun(), goodRun()
	badMode.Current.Mode = "turbo"
	badTask.Current.TaskID = "../etc"
	badPhase.Phases = []string{"ok", "bad id"}
	badThrough.Through = "x\x1b[31m"
	tests := []struct {
		name string
		in   RunInput
		want string
	}{
		{"mode", RunInput{Run: badMode}, "unknown mode"},
		{"task", RunInput{Run: badTask}, "malformed task ID"},
		{"phase", RunInput{Run: badPhase}, "malformed phase ID"},
		{"through", RunInput{Run: badThrough}, "malformed phase ID"},
		{"parse error", RunInput{RunErr: errors.New("read run state: boom\x1b[2J")}, "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewRunInfo(tt.in)
			if got == nil || !strings.Contains(got.Unreadable, tt.want) || strings.ContainsRune(got.Unreadable, 0x1b) {
				t.Fatalf("got %+v, want Unreadable containing %q", got, tt.want)
			}
			if got.Task != "" || got.Mode != "" || len(got.Phases) != 0 {
				t.Errorf("unreadable state leaked fields: %+v", got)
			}
		})
	}
}
