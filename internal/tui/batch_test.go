package tui

// Fewer interruptions in the run view (SPEC §6.5, §10, §15.5, V18-P3):
// Commit all on the commit question, the batch question with Keep and a
// confirmed Undo, and a blocked session's Needs-you line saying why.

import (
	"strings"
	"testing"

	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/serve"
	"github.com/drilonrecica/igris/internal/state"
)

func batchAsked(undo bool) engine.Event {
	return engine.Event{Kind: engine.Asked, Question: engine.QuestionCommitBatch, Undo: undo,
		Detail: "Keep the 2 commits of this run?", Output: []string{"M0-01: Go module", "M0-02: Entrypoint"}}
}

// Commit all is the commit question's third answer: the commit, with
// text all, for the question's task.
func TestCommitAllAnswer(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.events(started("M0-03"), opened("M0-03"),
		engine.Event{Kind: engine.Asked, Task: "M0-03", Question: engine.QuestionCommit, Detail: `commit the changes of M0-03 as "M0-03: Makefile"?`})
	if !strings.Contains(hs.m.View(), "Commit all (no more commit questions this run)") {
		t.Fatalf("no Commit all:\n%s", hs.m.View())
	}
	hs.key("3")
	want := engine.Command{Kind: engine.CmdAnswer, Yes: true, Text: engine.CommitAllText, Task: "M0-03"}
	if got := hs.s.take(); len(got) != 1 || got[0] != want {
		t.Errorf("sent %+v, want %+v", got, want)
	}
	hs.events(engine.Event{Kind: engine.CommitPolicy, Detail: "Commit all: every task is committed without asking for the rest of this run"})
	if !strings.Contains(hs.m.View(), "Commit all: every task is committed without asking") {
		t.Errorf("no feed line:\n%s", hs.m.View())
	}
}

// The batch question lists the run's commits; Keep is the default, Undo
// takes a second confirmation whose default is Back.
func TestBatchQuestion(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.events(started("M0-03"), opened("M0-03"), batchAsked(true))
	d := hs.m.dialog
	if d == nil || d.question != engine.QuestionCommitBatch || !hs.m.needsOwner() {
		t.Fatalf("dialog = %+v", d)
	}
	view := hs.m.View()
	for _, want := range []string{"Keep the 2 commits of this run?", "M0-02: Entrypoint", "1. Keep", "2. Undo… (git reset --soft; the changes stay)"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	// Esc keeps asking.
	hs.key("esc")
	if q := hs.m.question(); q == nil || q.Question != engine.QuestionCommitBatch {
		t.Fatalf("question = %+v", q)
	}
	// Undo: confirmed, never with one key; Back returns to the question.
	hs.m.dialog = questionDialog(*hs.m.question())
	hs.key("2")
	if d := hs.m.dialog; d == nil || d.title != "Undo the run's commits?" || d.selected != 1 {
		t.Fatalf("Undo opened %+v", hs.m.dialog)
	}
	if len(hs.s.take()) != 0 {
		t.Fatal("Undo went out without the confirmation")
	}
	hs.key("enter") // Back
	if d := hs.m.dialog; d == nil || d.question != engine.QuestionCommitBatch {
		t.Fatalf("Back opened %+v", hs.m.dialog)
	}
	hs.key("2")
	hs.key("1")
	want := engine.Command{Kind: engine.CmdAnswer, Question: engine.QuestionCommitBatch}
	if got := hs.s.take(); len(got) != 1 || got[0] != want {
		t.Fatalf("sent %+v, want %+v", got, want)
	}
	hs.events(engine.Event{Kind: engine.CommitsUndone, Detail: "2 commits undone; their changes are in the tree"})
	if hs.m.batchAsk != nil || hs.m.dialog != nil || !strings.Contains(hs.m.View(), "2 commits undone; their changes are in the tree") {
		t.Errorf("after Undo: ask %+v, dialog %+v", hs.m.batchAsk, hs.m.dialog)
	}
}

// Keep sends yes; without Undo offered there is Keep alone.
func TestBatchKeepOnly(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.events(started("M0-03"), opened("M0-03"), batchAsked(false))
	if len(hs.m.dialog.options) != 1 {
		t.Fatalf("options %+v, want Keep alone", hs.m.dialog.options)
	}
	hs.key("enter")
	want := engine.Command{Kind: engine.CmdAnswer, Question: engine.QuestionCommitBatch, Yes: true}
	if got := hs.s.take(); len(got) != 1 || got[0] != want {
		t.Fatalf("sent %+v, want %+v", got, want)
	}
	if hs.m.batchAsk != nil || hs.m.dialog != nil {
		t.Errorf("after Keep: ask %+v, dialog %+v", hs.m.batchAsk, hs.m.dialog)
	}
}

// An attached view restores the batch question from the snapshot.
func TestBatchRestored(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.m.restore(serve.Snapshot{Run: "r", Phase: "M0",
		BatchQuestion: &serve.Event{Type: string(engine.Asked), Question: string(engine.QuestionCommitBatch), Undo: true, Detail: "Keep the 2 commits of this run?"}})
	if q := hs.m.question(); hs.m.batchAsk == nil || q == nil || q.Question != engine.QuestionCommitBatch || !q.Undo {
		t.Fatalf("ask %+v", hs.m.batchAsk)
	}
}

// A blocked session's Needs-you line says why, from the hooks' cause.
func TestNeedsYouSaysWhy(t *testing.T) {
	for _, tt := range []struct {
		cause, tool, want string
	}{
		{state.CauseQuestion, "", "M0-03 needs you · asks you a question in its pane"},
		{state.CausePermission, "Bash", "M0-03 needs you · waits for a permission (Bash)"},
		{state.CausePlan, "", "M0-03 needs you · plan to approve"},
		{"", "", "M0-03 needs you · waits for you in its pane"},
	} {
		hs := newHarness(t, 120, 40)
		hs.events(started("M0-03"), opened("M0-03"),
			engine.Event{Kind: engine.NeedsYou, Task: "M0-03", Reason: state.ReasonBlocked, Cause: tt.cause, Tool: tt.tool,
				Detail: "the agent is blocked and has not run `igris done` for 30s"})
		view := hs.m.View()
		if strings.Count(view, tt.want) < 2 { // the banner and the log
			t.Errorf("%s: view lacks %q twice:\n%s", tt.cause, tt.want, view)
		}
	}
}
