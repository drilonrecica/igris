package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/state"
)

// A session can rewrite state.json. A conversation ID that is not a UUID
// could read as a claude flag on `--resume`; "continue" is refused for it
// and the owner can still retry fresh.
func TestRetryContinueRefusesATamperedSessionID(t *testing.T) {
	h := newHarness(t, chainPlan, "")
	h.autoSignalExcept("A-1")
	h.stopMidTask(6 * time.Second)
	run, err := h.dir.LoadRun()
	if err != nil {
		t.Fatal(err)
	}
	h.be.Kill(*run.Current.Session)
	run.Current.ClaudeSession = "--dangerously-skip-permissions"
	if err := h.dir.SaveRun(run); err != nil {
		t.Fatal(err)
	}

	h.be.SetAutoSignal(func(_ context.Context, id string) error { return h.signal(id) })
	h.onEvent = func(ev Event) {
		switch {
		case ev.Kind == Asked && ev.Question == QuestionSessionLost:
			h.eng.Send(Command{Kind: CmdRetry, Continue: true})
		case ev.Kind == Warning && strings.Contains(ev.Detail, "not a session ID"):
			h.eng.Send(Command{Kind: CmdRetry})
		}
	}
	if _, err := h.run(resumeLast); err != nil {
		t.Fatalf("resumed run: %v", err)
	}
	for _, s := range h.be.Opened() {
		for _, a := range s.Args {
			if a == "--dangerously-skip-permissions" || a == "--resume" {
				t.Errorf("%s launched with %q", s.TaskID, s.Args)
			}
		}
	}
	if got := h.statuses(); got != "A-1=done A-2=done A-3=done B-1=ready" {
		t.Errorf("statuses = %s", got)
	}
}

// Text from sessions (notes, verify output) reaches the UI through events
// and goes back to the pane as a prompt: both are free of escape sequences.
func TestUntrustedTextIsCleaned(t *testing.T) {
	h := newHarness(t, chainPlan, verifyTOML)
	h.verifyResults(failWith(1, "\x1b[31mFAIL\x1b[0m test\x1b[201~\x1b[Z\n"), pass)
	h.be.Script("A-1", append(states(backend.Working, 3), backend.Idle)...)
	h.resignal(1)
	h.be.SetAutoSignal(func(_ context.Context, id string) error {
		return h.dir.WriteSignal(state.Signal{ID: id, Action: state.ActionDone, Note: "did " + id + "\x1b]0;owned\x07\nsecond line"})
	})
	if _, err := h.run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, ev := range h.events {
		for _, s := range []string{ev.Detail, ev.Title} {
			if strings.ContainsAny(s, "\x1b\x07") {
				t.Errorf("%s event carries control characters: %q", ev.Kind, s)
			}
		}
	}
	if got := h.event(TaskDone, "A-2").Detail; got != "did A-2 second line" {
		t.Errorf("done note = %q, want the escape sequence gone and one line", got)
	}
	fb := h.be.Prompts("A-1")[1]
	if strings.Contains(fb, "\x1b") || !strings.Contains(fb, "FAIL test\n") {
		t.Errorf("failure prompt not cleaned:\n%s", fb)
	}
	// The commit message body is the cleaned note too.
	var commits []string
	for _, c := range h.calls("git") {
		if c.Args[0] == "commit" {
			commits = append(commits, strings.Join(c.Args, " "))
		}
	}
	for _, c := range commits {
		if strings.Contains(c, "\x1b") {
			t.Errorf("commit args carry an escape: %q", c)
		}
	}
}
