package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drilonrecica/igris/internal/backend"
	"github.com/drilonrecica/igris/internal/engine"
	"github.com/drilonrecica/igris/internal/serve"
)

// Several active tasks (SPEC §15.1): a card per task in plan order, one
// selected, the Needs-you queue in plan order, the switcher when narrow.

// multiPlan is a parallel phase with three agent tasks and a later one.
var multiPlan = `<!-- igris:format 3 -->

## M1 — Parallel work
<!-- igris:parallel -->

| ID | Task | Deps | Status | Model | Owner |
|---|---|---|---|---|---|
| M1-03 | **Parser** — read the input | — | in progress | sonnet | agent |
| M1-04 | **Writer** — write the output | — | in progress | sonnet | agent |
| M1-05 | **Docs** — say how it works | — | in progress | opus | agent |
| M1-06 | **Release** — ship it | M1-03 | blocked | sonnet | agent |
`

// startedIn is task id started in slot slot, with its session open on
// pane id.
func startedIn(id string, slot int) []engine.Event {
	s := slot
	return []engine.Event{
		{Kind: engine.TaskStarted, Phase: "M1", Task: id, Title: "title of " + id, Rank: "sonnet", Model: "sonnet", Mode: "plan", Slot: &s},
		{Kind: engine.SessionOpened, Task: id, Mode: "plan", Session: &backend.SessionRef{Backend: "herdr", PaneID: "p-" + id}, Slot: &s},
	}
}

// multiHarness runs M1-05, then M1-03, then M1-04: their cards end up in
// plan order, the first started selected.
func multiHarness(t *testing.T, w, h int) *harness {
	hs := newHarness(t, w, h)
	hs.m.loc = time.UTC
	hs.withPlan(multiPlan)
	hs.events(engine.Event{Kind: engine.RunStarted, Phase: "M1", Detail: "phase M1"})
	hs.events(startedIn("M1-05", 0)...)
	hs.events(startedIn("M1-03", 1)...)
	hs.events(startedIn("M1-04", 2)...)
	return hs
}

func cardIDs(m *model) []string {
	var ids []string
	for _, c := range m.cards {
		ids = append(ids, c.id)
	}
	return ids
}

func TestCardsInPlanOrderNewOneNeverSelected(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	if got := cardIDs(hs.m); !slices.Equal(got, []string{"M1-03", "M1-04", "M1-05"}) {
		t.Errorf("cards %v, want plan order", got)
	}
	if hs.m.cur.id != "M1-05" {
		t.Errorf("selected %s; a new card must never be selected by itself", hs.m.cur.id)
	}
	v := hs.m.View()
	for _, want := range []string{"3 active", "› M1-05 · slot 0", "M1-03 · slot 1", "M1-04 · slot 2", "┏", "┃"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "┏") != 1 {
		t.Errorf("want exactly one heavier border:\n%s", v)
	}
	checkFits(t, v, 120, 40)
}

func TestSelectedTaskEndsNextCardInPlanOrder(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.keys("[") // M1-05 → M1-04
	if hs.m.cur.id != "M1-04" {
		t.Fatalf("[ selected %s, want M1-04", hs.m.cur.id)
	}
	hs.events(engine.Event{Kind: engine.TaskDone, Phase: "M1", Task: "M1-04"})
	if hs.m.cur == nil || hs.m.cur.id != "M1-05" {
		t.Errorf("after the selected task ended: %v, want the next card M1-05", hs.m.cur)
	}
	hs.events(engine.Event{Kind: engine.TaskDone, Phase: "M1", Task: "M1-05"})
	if hs.m.cur == nil || hs.m.cur.id != "M1-03" {
		t.Errorf("after the last card ended: %v, want the first, M1-03", hs.m.cur)
	}
	if v := hs.m.View(); strings.Contains(v, "active") || strings.Contains(v, "┏") {
		t.Errorf("one active task is drawn as before, without the cards:\n%s", v)
	}
}

func TestBracketsWrapAndActOnTheSelectedCard(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.keys("]") // M1-05 → M1-03, wrapping
	if hs.m.cur.id != "M1-03" {
		t.Fatalf("] selected %s, want M1-03", hs.m.cur.id)
	}
	hs.keys("d")
	got := hs.s.take()
	if len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdDone, Task: "M1-03"}) {
		t.Errorf("d sent %+v, want done for the selected card's task", got)
	}
	hs.keys("o")
	if len(hs.focus) != 1 || hs.focus[0].PaneID != "p-M1-03" {
		t.Errorf("o focused %+v, want the selected card's pane", hs.focus)
	}
	hs.keys("r")
	if hs.m.dialog == nil || hs.m.dialog.task != "M1-03" {
		t.Fatalf("r opened %+v, want the retry dialog of M1-03", hs.m.dialog)
	}
}

func TestClickOnACardSelectsIt(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.click(func(tg target) bool { return tg.act == actCardPick && tg.option == 0 })
	if hs.m.cur.id != "M1-03" {
		t.Errorf("click selected %s, want M1-03", hs.m.cur.id)
	}
	if len(hs.s.take()) != 0 {
		t.Error("selecting a card sent a command")
	}
}

func TestNeedsYouIsOneQueueInPlanOrder(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	ask := func(id string, q engine.Question) engine.Event {
		return engine.Event{Kind: engine.Asked, Phase: "M1", Task: id, Question: q, Detail: id + " asks"}
	}
	// M1-05 asks first: its dialog opens at once.
	hs.events(ask("M1-05", engine.QuestionCommit))
	if d := hs.m.dialog; d == nil || d.task != "M1-05" {
		t.Fatalf("dialog %+v, want M1-05's question", d)
	}
	// M1-03 asks while it is open: it waits its turn, M1-05's stays.
	hs.events(ask("M1-03", engine.QuestionConfirmSkip))
	if d := hs.m.dialog; d == nil || d.task != "M1-05" {
		t.Fatalf("dialog %+v, want M1-05's still", d)
	}
	// The banner names the first in plan order and counts the rest.
	if v := hs.m.View(); !strings.Contains(v, "! M1-03 needs you · skip? · 2 tasks need you") {
		t.Errorf("banner lacks the queue in plan order:\n%s", v)
	}
	// Esc leaves both pending; Answer… opens the first in plan order.
	hs.keys("esc")
	hs.click(isAct(actAnswer))
	if d := hs.m.dialog; d == nil || d.task != "M1-03" {
		t.Fatalf("Answer… opened %+v, want M1-03's, first in plan order", d)
	}
	// Answering it brings the next.
	hs.keys("enter")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdAnswer, Task: "M1-03"}) {
		t.Errorf("sent %+v, want M1-03's answer", got)
	}
	if d := hs.m.dialog; d == nil || d.task != "M1-05" {
		t.Fatalf("after answering: %+v, want M1-05's question next", d)
	}
	hs.keys("enter")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdAnswer, Yes: true, Task: "M1-05"}) {
		t.Errorf("sent %+v, want M1-05's commit", got)
	}
	if hs.m.dialog != nil || hs.m.question() != nil {
		t.Errorf("queue not empty: dialog %+v, question %+v", hs.m.dialog, hs.m.question())
	}
}

func TestCardAnswerOpensItsOwnQuestion(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.events(
		engine.Event{Kind: engine.SessionLost, Phase: "M1", Task: "M1-03"},
		engine.Event{Kind: engine.Asked, Phase: "M1", Task: "M1-03", Question: engine.QuestionSessionLost},
	)
	hs.keys("esc")
	// M1-03 took the selection when it began to need you (SPEC §15.1).
	if hs.m.cur.id != "M1-03" {
		t.Fatalf("selected %s, want M1-03, which needs you", hs.m.cur.id)
	}
	hs.events(engine.Event{Kind: engine.Asked, Phase: "M1", Task: "M1-05", Question: engine.QuestionCommit})
	hs.keys("esc")
	// The selection never jumps away from a card that needs you; the
	// owner picks M1-05: its [Answer…] opens its own question, though
	// M1-03's is first in line.
	if hs.m.cur.id != "M1-03" {
		t.Fatalf("selected %s, want M1-03 kept", hs.m.cur.id)
	}
	hs.click(func(tg target) bool { return tg.act == actCardPick && tg.option == 2 })
	hs.click(isAct(actAnswerCard))
	if d := hs.m.dialog; d == nil || d.task != "M1-05" {
		t.Fatalf("card's Answer… opened %+v, want M1-05's", d)
	}
	hs.keys("esc")
	// Skip… from M1-03's session-lost question skips M1-03.
	hs.click(isAct(actAnswer))
	hs.click(isOption(3))
	hs.keys("w", "h", "y", "enter", "1")
	if got := hs.s.take(); len(got) != 1 || got[0] != (engine.Command{Kind: engine.CmdSkip, Text: "why", Task: "M1-03"}) {
		t.Errorf("sent %+v, want M1-03 skipped", got)
	}
	if d := hs.m.dialog; d == nil || d.task != "M1-05" {
		t.Errorf("after the skip: %+v, want M1-05's question next", d)
	}
}

func TestQuestionSettledOnlyForItsTask(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.events(engine.Event{Kind: engine.Asked, Phase: "M1", Task: "M1-04", Question: engine.QuestionCommit})
	hs.events(engine.Event{Kind: engine.TaskDone, Phase: "M1", Task: "M1-03"})
	if d := hs.m.dialog; d == nil || d.task != "M1-04" {
		t.Errorf("another task's end closed M1-04's question: %+v", d)
	}
	hs.events(engine.Event{Kind: engine.Committed, Phase: "M1", Task: "M1-04"})
	if hs.m.dialog != nil || hs.m.question() != nil {
		t.Errorf("M1-04's commit did not settle its question")
	}
}

func TestTaskListMarksEveryWaitingTask(t *testing.T) {
	hs := multiHarness(t, 120, 40)
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-03", Detail: "idle"})
	r := hs.m.rows()
	if g := r.glyph(r.task("M1-03")); g != "!" {
		t.Errorf("M1-03 glyph %q, want ! though it isn't selected", g)
	}
	if g := r.glyph(r.task("M1-04")); g != "●" {
		t.Errorf("M1-04 glyph %q, want ●", g)
	}
}

func TestNarrowSwitcher(t *testing.T) {
	hs := multiHarness(t, 50, 20)
	hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-04", Detail: "idle"})
	v := hs.m.View()
	// M1-04 needs you: it is selected and first, whatever the plan order.
	if !strings.Contains(v, "‹ ! M1-04 · 1 of 3 › ● M1-03 ● M1-05") {
		t.Errorf("no switcher line:\n%s", v)
	}
	if strings.Contains(v, "M1-03 · slot") {
		t.Errorf("narrow shows the selected card only:\n%s", v)
	}
	checkFits(t, v, 50, 20)
	hs.click(func(tg target) bool { return tg.act == actCardPick && tg.option == 0 })
	if hs.m.cur.id != "M1-03" {
		t.Errorf("tap selected %s, want M1-03", hs.m.cur.id)
	}
	if v := hs.m.View(); !strings.Contains(v, "‹ ● M1-03 · 2 of 3 › ! M1-04 ● M1-05") {
		t.Errorf("switcher after the tap:\n%s", v)
	}
	// › and ‹ follow the switcher's order.
	hs.click(isAct(actCardNext))
	if hs.m.cur.id != "M1-05" {
		t.Errorf("› selected %s, want M1-05", hs.m.cur.id)
	}
	hs.click(isAct(actCardPrev))
	if hs.m.cur.id != "M1-03" {
		t.Errorf("‹ selected %s, want M1-03", hs.m.cur.id)
	}
	hs.click(isAct(actCardPrev))
	if hs.m.cur.id != "M1-04" {
		t.Errorf("‹ selected %s, want M1-04", hs.m.cur.id)
	}
}

func TestMultiLayouts(t *testing.T) {
	for _, sz := range [][2]int{{120, 40}, {100, 24}, {80, 24}, {50, 20}} {
		hs := multiHarness(t, sz[0], sz[1])
		hs.events(engine.Event{Kind: engine.NeedsYou, Phase: "M1", Task: "M1-04", Detail: "the agent is idle"})
		v := hs.m.View()
		checkFits(t, v, sz[0], sz[1])
		golden(t, fmt.Sprintf("multi_%dx%d", sz[0], sz[1]), v)
	}
}

// Every size draws within the screen, however many cards.
func TestMultiFitsEverySize(t *testing.T) {
	for w := 20; w <= 140; w += 7 {
		for _, h := range []int{8, 13, 20, 40} {
			hs := multiHarness(t, w, h)
			checkFits(t, hs.m.View(), w, h)
		}
	}
}

func TestEachCardHasItsTail(t *testing.T) {
	hs := multiHarness(t, 120, 60) // room for every card's tail
	src := &taskTails{lines: map[string][]string{"M1-03": {"⏺ parsing"}, "M1-05": {"⏺ writing docs"}}}
	hs.m.opts.Tail = src.tail
	hs.m.Update(run(hs.m.fetchTail()))
	v := hs.m.View()
	for _, want := range []string{"parsing", "writing docs"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks the tail %q:\n%s", want, v)
		}
	}
	if got := src.asked(); !slices.Equal(got, []string{"M1-03", "M1-04", "M1-05"}) {
		t.Errorf("tails read for %v, want every card in plan order", got)
	}
}

// taskTails answers a tail per task.
type taskTails struct {
	mu    sync.Mutex
	lines map[string][]string
	tasks []string
}

func (s *taskTails) tail(_ context.Context, task string, _ int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks = append(s.tasks, task)
	return s.lines[task], nil
}

func (s *taskTails) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tasks)
}

func TestRestoreSeveralActive(t *testing.T) {
	hs := newHarness(t, 120, 40)
	hs.withPlan(multiPlan)
	one, two := 1, 0
	q := serve.Event{V: 1, Type: string(engine.Asked), Task: "M1-05", Question: string(engine.QuestionCommit), At: t0}
	hs.m.restore(serve.Snapshot{
		Phase: "M1",
		Active: []serve.Current{
			{Task: "M1-03", Slot: &one, State: serve.StateWorking, Model: "sonnet"},
			{Task: "M1-05", Slot: &two, State: serve.StateQuestion, Model: "opus"},
		},
		Current:   &serve.Current{Task: "M1-03"},
		Question:  &q,
		Questions: []serve.Event{q},
	})
	if got := cardIDs(hs.m); !slices.Equal(got, []string{"M1-03", "M1-05"}) {
		t.Errorf("cards %v", got)
	}
	if hs.m.cur.id != "M1-03" || *hs.m.card("M1-05").slot != 0 {
		t.Errorf("selected %s, slot %v", hs.m.cur.id, hs.m.card("M1-05").slot)
	}
	if d := hs.m.dialog; d == nil || d.task != "M1-05" {
		t.Errorf("dialog %+v, want M1-05's pending question", d)
	}
	// A reconnect keeps the owner's selection.
	hs.keys("esc", "]")
	hs.m.restore(serve.Snapshot{Phase: "M1", Active: []serve.Current{{Task: "M1-03"}, {Task: "M1-05"}}})
	if hs.m.cur.id != "M1-05" {
		t.Errorf("after a reconnect selected %s, want M1-05 still", hs.m.cur.id)
	}
}
