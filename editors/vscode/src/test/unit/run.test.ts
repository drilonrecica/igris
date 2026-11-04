import * as assert from "node:assert/strict";
import { apply } from "../../run";
import { ev, recorded } from "../support/transcripts";

// The fold follows internal/serve's tracker: the same events give the same
// run a fresh snapshot would.
describe("the run's fold", () => {
  it("follows the plan's Status cells and drops a finished task's card", () => {
    const { snapshot: run, events } = recorded("dropped.jsonl");
    for (const e of events) apply(run, e);
    assert.deepEqual(
      run.phases[0]!.tasks.map((t) => [t.id, t.status]),
      [
        ["S1-01", "done"],
        ["S1-02", "ready"],
      ],
    );
    assert.deepEqual(run.active, []);
  });

  it("starts a card in plan order and a question settles on the next step", () => {
    const { snapshot: run } = recorded("parallel.jsonl");
    apply(run, ev({ type: "task_started", task: "P1-03", title: "Join", rank: "sonnet", model: "claude-sonnet-5-5", mode: "plan", changes: [{ id: "P1-03", to: "in progress" }] }));
    assert.deepEqual(
      run.active.map((c) => [c.task, c.state]),
      [
        ["P1-01", "question"],
        ["P1-02", "working"],
        ["P1-03", "working"],
      ],
    );
    assert.equal(run.phases[0]!.tasks[2]!.status, "in progress");

    apply(run, ev({ type: "committed", task: "P1-01", detail: "P1-01: Left" }));
    assert.deepEqual(run.questions, []);
    apply(run, ev({ type: "asked", task: "P1-02", question: "confirm_skip", detail: "Skip P1-02?" }));
    assert.equal(run.questions.length, 1);
    assert.equal(run.active[1]!.state, "question");
    apply(run, ev({ type: "task_skipped", task: "P1-02", changes: [{ id: "P1-02", to: "skipped" }] }));
    assert.deepEqual(run.questions, []);
    assert.deepEqual(
      run.active.map((c) => c.task),
      ["P1-01", "P1-03"],
    );
  });

  it("keeps the run's questions off the cards", () => {
    const { snapshot: run } = recorded("parallel.jsonl");
    assert.ok(run.budgetQuestion);
    apply(run, ev({ type: "budget_raised", detail: "raised to 3M" }));
    assert.equal(run.budgetQuestion, undefined);
    apply(run, ev({ type: "asked", question: "commit_batch", detail: "Keep 3 commits?" }));
    assert.equal(run.batchQuestion?.detail, "Keep 3 commits?");
    assert.equal(run.questions.length, 1, "the task's question stays");
    apply(run, ev({ type: "commits_undone", detail: "3 commits undone" }));
    assert.equal(run.batchQuestion, undefined);
  });

  it("gives a sequential run's task-less event to its one card", () => {
    const { snapshot: run } = recorded("working.jsonl");
    apply(run, ev({ type: "needs_you", detail: "blocked on a permission prompt" }));
    assert.equal(run.active[0]!.state, "needs_you");
    apply(run, ev({ type: "needs_you_clear" }));
    assert.equal(run.active[0]!.state, "working");
  });

  it("counts a wait that becomes a need from its start", () => {
    const { snapshot: run } = recorded("working.jsonl");
    apply(run, ev({ type: "agent_waiting", task: "S1-01", at: 1000 }));
    apply(run, ev({ type: "needs_you", task: "S1-01", at: 5000, detail: "idle" }));
    assert.equal(run.active[0]!.state, "needs_you");
    assert.equal(run.active[0]!.since, 1000);
  });

  it("holds and pauses, and the end clears every question", () => {
    const { snapshot: run } = recorded("parallel.jsonl");
    apply(run, ev({ type: "limit_hold", task: "P1-03", detail: "5h at 96%", limit: { window: "five_hour", resetsAt: 9e12, percent: 96, task: "", detail: "" } }));
    assert.equal(run.limited?.task, "P1-03");
    apply(run, ev({ type: "limit_resume" }));
    assert.equal(run.limited, undefined);
    apply(run, ev({ type: "pause_on" }));
    apply(run, ev({ type: "paused" }));
    assert.ok(run.paused && run.holding);
    apply(run, ev({ type: "pause_off" }));
    assert.ok(!run.paused && !run.holding);
    apply(run, ev({ type: "mode_changed", detail: "plan" }));
    assert.equal(run.mode, "plan");
    apply(run, ev({ type: "run_stopped", detail: "stopped" }));
    assert.ok(run.ended);
    assert.equal(run.end, "stopped");
    assert.deepEqual(run.questions, []);
    assert.equal(run.budgetQuestion, undefined);
  });

  it("ignores event types it doesn't know", () => {
    const { snapshot: run } = recorded("working.jsonl");
    const before = JSON.stringify(run);
    apply(run, ev({ type: "something_from_igris_3" }));
    assert.equal(JSON.stringify(run), before);
  });
});
