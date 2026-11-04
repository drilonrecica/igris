import * as assert from "node:assert/strict";
import {
  asksOf,
  cardActions,
  commandOf,
  guard,
  modeChoice,
  MODES,
  needsWords,
  notice,
  OFFERED_MODES,
  questionOf,
  runActions,
  sheetOf,
  SKIP_PERMISSIONS_REFUSAL,
  taskModable,
  taskModeChoice,
  type Choice,
  type Confirm,
  type Prompts,
} from "../../answers";
import type { Card } from "../../protocol";
import { ev, recorded } from "../support/transcripts";

function card(fields: Partial<Card> & { task: string; state: string }): Card {
  return { title: "", rank: "", model: "", mode: "", user: false, since: 0, detail: "", reason: "", cause: "", tool: "", resolver: "", session: undefined, ...fields };
}

const labels = (cs: Choice[]): string[] => cs.map((c) => c.label);

/** Prompts that answer from a script and record what they were asked. */
class Scripted implements Prompts {
  asked: string[] = [];
  constructor(private readonly answers: { input?: string; pick?: string; confirm?: boolean }) {}
  async input(i: { prompt: string }): Promise<string | undefined> {
    this.asked.push(`input ${i.prompt}`);
    return this.answers.input;
  }
  async pick(p: { prompt: string; items: string[] }): Promise<string | undefined> {
    this.asked.push(`pick ${p.prompt}: ${p.items.join(",")}`);
    return this.answers.pick;
  }
  async confirm(c: Confirm): Promise<boolean> {
    this.asked.push(`confirm ${c.title}`);
    return this.answers.confirm ?? false;
  }
}

describe("answering", () => {
  describe("each question gets the web UI's answers", () => {
    const cases: { question: string; extra?: object; title: string; answers: string[] }[] = [
      { question: "commit", title: "Commit A-01?", answers: ["Commit", "Leave uncommitted", "Commit all (no more commit questions this run)"] },
      { question: "confirm_skip", title: "A-01 asks to skip", answers: ["Keep working", "Skip the task"] },
      { question: "session_lost", title: "A-01: session lost", answers: ["Start fresh", "Continue conversation", "Mark done", "Skip", "Stop igris"] },
      { question: "hook_failed", title: "A-01: before_task hook failed", answers: ["Retry", "Mark done", "Skip", "Stop igris"] },
      { question: "integration", title: "A-01 didn't integrate", answers: ["Rerun fresh", "Mark done (integrated by hand)", "Skip", "Stop igris"] },
      {
        question: "integration",
        extra: { resolve: true },
        title: "A-01 didn't integrate",
        answers: ["Resolve (a resolver session)", "Rerun fresh", "Mark done (integrated by hand)", "Skip", "Stop igris"],
      },
      { question: "files_in_the_way", title: "A-01 didn't integrate", answers: ["Try again", "Mark done (integrated by hand)", "Skip", "Stop igris"] },
      { question: "confirm_reset", title: "Reset A-01?", answers: ["Keep it as it is", "Reset A-01"] },
      { question: "plan_approval", extra: { plan: "1. do it", then: "accept" }, title: "A-01 presents a plan", answers: ["Approve: the session switches to accept", "Reject with feedback", "Read the plan"] },
      { question: "budget", extra: { budget: { budget: "phase:V18", limit: "20M" } }, title: "phase V18 budget 20M reached", answers: ["Raise", "Continue (it no longer holds this run)", "Stop igris"] },
      { question: "commit_batch", extra: { detail: "Keep the 3 commits of this run? a, b, c", undo: true }, title: "Keep the 3 commits of this run?", answers: ["Keep", "Undo (git reset --soft; the changes stay)"] },
      { question: "commit_batch", extra: { detail: "Keep the 3 commits of this run? Undo isn't offered: main moved." }, title: "Keep the 3 commits of this run?", answers: ["Keep"] },
      { question: "something_new", title: "Question", answers: ["Yes", "No"] },
    ];
    for (const c of cases) {
      it(`${c.question}${c.extra ? ` ${JSON.stringify(c.extra)}` : ""}`, () => {
        const a = questionOf(ev({ type: "asked", task: "A-01", question: c.question, ...c.extra }));
        assert.equal(a.title, c.title);
        assert.deepEqual(labels(a.choices), c.answers);
      });
    }

    it("sends what the protocol says for each answer", () => {
      const commit = questionOf(ev({ type: "asked", task: "A-01", question: "commit" }));
      assert.deepEqual(
        commit.choices.map((c) => c.cmd),
        [
          { type: "answer", task: "A-01", yes: true },
          { type: "answer", task: "A-01", yes: false },
          { type: "answer", task: "A-01", yes: true, text: "all" },
        ],
      );
      const reset = questionOf(ev({ type: "asked", task: "A-01", question: "confirm_reset" }));
      assert.deepEqual(reset.choices[1]!.cmd, { type: "answer", task: "A-01", yes: true, question: "confirm_reset" });
      const batch = questionOf(ev({ type: "asked", question: "commit_batch", undo: true }));
      assert.deepEqual(batch.choices[1]!.cmd, { type: "answer", question: "commit_batch", yes: false });
      const plan = questionOf(ev({ type: "asked", task: "A-01", question: "plan_approval", plan: "x" }));
      assert.deepEqual(plan.choices[0]!.cmd, { type: "plan", task: "A-01", plan: "allow" });
      assert.deepEqual(plan.choices[1]!.cmd, { type: "plan", task: "A-01", plan: "deny" });
      assert.equal(plan.choices[2]!.show, "x");
      const lost = questionOf(ev({ type: "asked", task: "A-01", question: "session_lost" }));
      assert.deepEqual(lost.choices[1]!.cmd, { type: "retry", task: "A-01", continue: true });
    });

    it("says where a plan too long for the snapshot is", () => {
      const a = questionOf(ev({ type: "asked", task: "A-01", question: "plan_approval" }));
      assert.deepEqual(labels(a.choices), ["Approve: the session switches to the mode after approval", "Reject with feedback"]);
      assert.match(a.text, /too long/);
    });
  });

  describe("the web UI's confirmations: skip, stop, done and Undo ask twice", () => {
    it("asks for them and nothing else", () => {
      const a = questionOf(ev({ type: "asked", task: "A-01", question: "session_lost" }));
      const confirms = a.choices.map((c) => c.confirm?.title ?? "");
      assert.deepEqual(confirms, ["", "", "Mark A-01 done?", "Skip A-01?", "Stop igris?"]);
      const batch = questionOf(ev({ type: "asked", question: "commit_batch", undo: true }));
      assert.equal(batch.choices[0]!.confirm, undefined);
      assert.equal(batch.choices[1]!.confirm?.title, "Undo the run's commits?");
    });

    it("skip needs a reason, done takes an optional note", () => {
      const [done, skip] = cardActions(card({ task: "A-01", state: "working" }));
      assert.deepEqual(done!.input, { prompt: "Note (optional)", required: false });
      assert.deepEqual(skip!.input, { prompt: "Reason", required: true });
    });
  });

  describe("cards that wait without a question", () => {
    it("a user task at its turn: done or skip", () => {
      const a = sheetOf(card({ task: "U-01", state: "your_turn", title: "Sign", detail: "Sign the form", user: true }))!;
      assert.equal(a.title, "Your turn: U-01 · Sign");
      assert.match(a.text, /^Sign the form\. This task is yours/);
      assert.deepEqual(labels(a.choices), ["Mark done", "Skip"]);
    });

    it("a blocked session says why in §10's words", () => {
      const a = sheetOf(card({ task: "A-01", state: "needs_you", reason: "blocked", cause: "permission", tool: "Bash" }))!;
      assert.equal(a.kind, "waits for a permission (Bash)");
      assert.deepEqual(labels(a.choices), ["Mark done", "Skip", "Stop igris"]);
      assert.equal(needsWords("idle", "", ""), "ended its turn without igris done");
      assert.equal(needsWords("nonsense", "", ""), "");
    });

    it("a lost session: retry fresh or continuing, done, skip, stop", () => {
      const a = sheetOf(card({ task: "A-01", state: "lost" }))!;
      assert.deepEqual(labels(a.choices), ["Start fresh", "Continue conversation", "Mark done", "Skip", "Stop igris"]);
    });

    it("a working card asks nothing; its actions are done, skip and retry", () => {
      assert.equal(sheetOf(card({ task: "A-01", state: "working" })), undefined);
      assert.deepEqual(labels(cardActions(card({ task: "A-01", state: "working" }))), ["Mark done", "Skip", "Retry fresh", "Retry, continuing the conversation"]);
      assert.deepEqual(labels(cardActions(card({ task: "U-01", state: "working", user: true }))), ["Mark done", "Skip"]);
    });
  });

  describe("what waits on the owner", () => {
    it("in the web UI's order, the run's questions last", () => {
      const run = recorded("parallel.jsonl").snapshot;
      const asks = asksOf(run);
      assert.deepEqual(
        asks.map((a) => notice(a)),
        ["P1-01 · commit?: Commit P1-01?", "budget reached: Budget reached"],
      );
      assert.deepEqual(asks.map((a) => a.key), ["q/P1-01/commit/" + Date.parse("2026-11-04T12:03:00Z"), "b/" + Date.parse("2026-11-04T12:04:00Z")]);
    });

    it("once the run ended, only the batch question", () => {
      const run = recorded("parallel.jsonl").snapshot;
      run.ended = true;
      assert.deepEqual(asksOf(run), []);
      run.batchQuestion = ev({ type: "asked", question: "commit_batch", detail: "Keep the 2 commits of this run?" });
      assert.deepEqual(asksOf(run).map((a) => a.title), ["Keep the 2 commits of this run?"]);
    });

    it("a question asked anew gets a new key, so it is told again", () => {
      const a = questionOf(ev({ type: "asked", task: "A-01", question: "commit", at: 1 }));
      const b = questionOf(ev({ type: "asked", task: "A-01", question: "commit", at: 2 }));
      assert.notEqual(a.key, b.key);
    });
  });

  describe("skip-permissions mode is never offered or accepted (§7.3)", () => {
    it("the pickers list every mode but yolo and plan:yolo", () => {
      assert.deepEqual(OFFERED_MODES, ["default", "accept", "auto", "plan", "manual", "plan:default", "plan:accept", "plan:auto"]);
      assert.deepEqual(
        MODES.filter((m) => !OFFERED_MODES.includes(m)),
        ["yolo", "plan:yolo"],
      );
      const run = recorded("parallel.jsonl").snapshot;
      assert.deepEqual(modeChoice(run).pick!.items, OFFERED_MODES);
      assert.deepEqual(taskModeChoice(run, "P1-03").pick!.items, OFFERED_MODES);
      assert.equal(taskModeChoice(run, "P1-03").pick!.picked, "plan");
    });

    it("a run in a skip-permissions mode says the terminal chose it", () => {
      const run = recorded("parallel.jsonl").snapshot;
      run.mode = "yolo";
      assert.match(modeChoice(run).pick!.note!, /chosen in the terminal/);
      assert.ok(!modeChoice(run).pick!.items.includes("yolo"));
    });

    it("guard refuses a command that would set one, whatever builds it", () => {
      for (const cmd of [
        { type: "mode", text: "yolo" },
        { type: "mode", text: " plan:yolo " },
        { type: "task_mode", task: "A-01", text: "yolo" },
        { type: "mode", text: "auto", yes: true },
        { type: "mode", text: "auto", yes: false },
        { type: "mode", text: "everything" },
        { type: "hello", yolo: true },
        { type: "answer", yes: true, yolo: false },
      ]) {
        assert.equal(guard(cmd), SKIP_PERMISSIONS_REFUSAL, JSON.stringify(cmd));
      }
      for (const cmd of [{ type: "mode", text: "auto" }, { type: "task_mode", task: "A-01", text: "plan:accept" }, { type: "answer", yes: true }, { type: "stop" }]) {
        assert.equal(guard(cmd), undefined, JSON.stringify(cmd));
      }
    });

    it("a picked mode outside the list is never sent", async () => {
      const run = recorded("parallel.jsonl").snapshot;
      assert.equal(await commandOf(modeChoice(run), new Scripted({ pick: "yolo" }), "t"), undefined);
      assert.deepEqual(await commandOf(modeChoice(run), new Scripted({ pick: "accept" }), "t"), { type: "mode", text: "accept" });
    });
  });

  describe("the run's actions", () => {
    it("pause or resume, stop with its confirmation, the mode; Start anyway while held", () => {
      const run = recorded("parallel.jsonl").snapshot;
      assert.deepEqual(labels(runActions(run)), ["Pause after this task", "Stop", "Mode of the next sessions…"]);
      run.paused = true;
      run.limited = { window: "5h", resetsAt: 1, percent: 90, task: "", detail: "" };
      assert.deepEqual(labels(runActions(run)), ["Start anyway", "Resume", "Stop", "Mode of the next sessions…"]);
      assert.deepEqual(runActions(run)[0]!.cmd, { type: "limit", text: "start" });
      assert.equal(runActions(run)[2]!.confirm?.title, "Stop igris?");
      run.ended = true;
      assert.deepEqual(runActions(run), []);
    });

    it("a task's mode: agent tasks of the run's phase still to do", () => {
      const run = recorded("parallel.jsonl").snapshot;
      assert.equal(taskModable(run, "P1-03"), true);
      assert.equal(taskModable(run, "P1-04"), false, "a user task");
      assert.equal(taskModable(run, "X-01"), false, "not in the plan");
      run.phases[0]!.tasks[2]!.status = "done";
      assert.equal(taskModable(run, "P1-03"), false, "done");
    });
  });

  describe("on the way to a command", () => {
    const skip = cardActions(card({ task: "A-01", state: "working" }))[1]!;
    const done = cardActions(card({ task: "A-01", state: "working" }))[0]!;

    it("skip asks the reason, then the second confirmation", async () => {
      const p = new Scripted({ input: "not needed", confirm: true });
      assert.deepEqual(await commandOf(skip, p, "t"), { type: "skip", task: "A-01", text: "not needed" });
      assert.deepEqual(p.asked, ["input Reason", "confirm Skip A-01?"]);
    });

    it("a declined confirmation, an empty reason or a dismissed box sends nothing", async () => {
      assert.equal(await commandOf(skip, new Scripted({ input: "x", confirm: false }), "t"), undefined);
      const empty = new Scripted({ input: "  ", confirm: true });
      assert.equal(await commandOf(skip, empty, "t"), undefined);
      assert.deepEqual(empty.asked, ["input Reason"]);
      assert.equal(await commandOf(done, new Scripted({ confirm: true }), "t"), undefined);
    });

    it("done without a note sends no text", async () => {
      assert.deepEqual(await commandOf(done, new Scripted({ input: "", confirm: true }), "t"), { type: "done", task: "A-01" });
    });
  });
});
