import * as assert from "node:assert/strict";
import { announce, answer, buttonsOf, MORE, perform, type Deps, type Ui } from "../../act";
import { questionOf, type Ask, type Choice, type Command } from "../../answers";
import { Client } from "../../client";
import { ev } from "../support/transcripts";

/** A Ui that answers from a script and records what it showed. */
class FakeUi implements Ui {
  shown: string[] = [];
  constructor(
    private readonly script: {
      choose?: (string | undefined)[];
      input?: string;
      confirm?: boolean;
      button?: string;
    } = {},
  ) {}
  async choose(choices: Choice[], title: string): Promise<Choice | undefined> {
    const want = this.script.choose?.shift();
    this.shown.push(`choose ${title}: ${choices.map((c) => c.label).join(" | ")}`);
    return choices.find((c) => c.label === want);
  }
  async chooseAsk(asks: Ask[]): Promise<Ask | undefined> {
    return asks[0];
  }
  async input(): Promise<string | undefined> {
    return this.script.input;
  }
  async pick(): Promise<string | undefined> {
    return undefined;
  }
  async confirm(): Promise<boolean> {
    return this.script.confirm ?? false;
  }
  async notify(line: string, buttons: string[]): Promise<string | undefined> {
    this.shown.push(`notify ${line} [${buttons.join(" | ")}]`);
    return this.script.button;
  }
  async show(text: string): Promise<void> {
    this.shown.push(`show ${text}`);
  }
  told(ok: boolean, line: string): void {
    this.shown.push(`${ok ? "ok" : "told"} ${line}`);
  }
}

function deps(ui: Ui, asks: Ask[], reply: { type: string; error: string } = { type: "ack", error: "" }): Deps & { sent: Command[] } {
  const sent: Command[] = [];
  return {
    sent,
    ui: () => ui,
    send: async (c) => {
      sent.push(c);
      return reply;
    },
    asks: () => asks,
  };
}

const commit = questionOf(ev({ type: "asked", task: "A-01", question: "commit", detail: "Commit A-01's changes?" }));
const lost = questionOf(ev({ type: "asked", task: "A-01", question: "session_lost" }));
const plan = questionOf(ev({ type: "asked", task: "A-01", question: "plan_approval", plan: "1. edit" }));

describe("acting from the editor", () => {
  it("answers a question from its QuickPick", async () => {
    const ui = new FakeUi({ choose: ["Commit"] });
    const d = deps(ui, [commit]);
    await answer(d, commit);
    assert.deepEqual(d.sent, [{ type: "answer", task: "A-01", yes: true }]);
    assert.deepEqual(ui.shown, ["choose Commit A-01?: Commit | Leave uncommitted | Commit all (no more commit questions this run)", "ok igris: Commit sent"]);
  });

  it("sends nothing once the question was answered elsewhere", async () => {
    const ui = new FakeUi({ choose: ["Commit"] });
    const d = deps(ui, []);
    await answer(d, commit);
    assert.deepEqual(d.sent, []);
    assert.equal(ui.shown.at(-1), "told Igris: Commit A-01? was already answered.");
  });

  it("asks twice before skipping, and a no sends nothing", async () => {
    const no = deps(new FakeUi({ choose: ["Skip"], input: "later", confirm: false }), [lost]);
    await answer(no, lost);
    assert.deepEqual(no.sent, []);
    const yes = deps(new FakeUi({ choose: ["Skip"], input: "later", confirm: true }), [lost]);
    await answer(yes, lost);
    assert.deepEqual(yes.sent, [{ type: "skip", task: "A-01", text: "later" }]);
  });

  it("says the run's error", async () => {
    const ui = new FakeUi({ choose: ["Start fresh"] });
    await answer(deps(ui, [lost], { type: "error", error: "A-01 is not active" }), lost);
    assert.equal(ui.shown.at(-1), "told Igris: A-01 is not active");
  });

  it("shows a plan to read and offers the answers again", async () => {
    const ui = new FakeUi({ choose: ["Read the plan", "Approve: the session switches to the mode after approval"] });
    const d = deps(ui, [plan]);
    await answer(d, plan);
    assert.equal(ui.shown[1], "show 1. edit");
    assert.deepEqual(d.sent, [{ type: "plan", task: "A-01", plan: "allow" }]);
  });

  describe("notifications", () => {
    it("carry the first two answers and Answer… for the rest", () => {
      assert.deepEqual(buttonsOf(commit), ["Commit", "Leave uncommitted", MORE]);
      assert.deepEqual(buttonsOf(plan), ["Approve: the session switches to the mode after approval", "Reject with feedback", MORE]);
      const reset = questionOf(ev({ type: "asked", task: "A-01", question: "confirm_reset" }));
      assert.deepEqual(buttonsOf(reset), ["Keep it as it is", "Reset A-01"]);
    });

    it("a button answers while the question waits", async () => {
      const ui = new FakeUi({ button: "Leave uncommitted" });
      const d = deps(ui, [commit]);
      await announce(d, commit);
      assert.equal(ui.shown[0], "notify Igris: A-01 · commit?: Commit A-01? [Commit | Leave uncommitted | Answer…]");
      assert.deepEqual(d.sent, [{ type: "answer", task: "A-01", yes: false }]);
    });

    it("a button for a question answered meanwhile sends nothing", async () => {
      const ui = new FakeUi({ button: "Commit" });
      const d = deps(ui, []);
      await announce(d, commit);
      assert.deepEqual(d.sent, []);
      assert.equal(ui.shown.at(-1), "told Igris: Commit A-01? was already answered.");
    });

    it("Answer… opens the QuickPick", async () => {
      const ui = new FakeUi({ button: MORE, choose: ["Commit all (no more commit questions this run)"] });
      const d = deps(ui, [commit]);
      await announce(d, commit);
      assert.deepEqual(d.sent, [{ type: "answer", task: "A-01", yes: true, text: "all" }]);
    });
  });

  it("a choice outside an ask is sent without the pending check", async () => {
    const ui = new FakeUi();
    const d = deps(ui, []);
    await perform(d, { label: "Pause after this task", cmd: { type: "pause" } }, "Igris");
    assert.deepEqual(d.sent, [{ type: "pause" }]);
  });

  it("the client never sends a skip-permissions mode, connected or not", async () => {
    const c = new Client({ socket: () => "/nonexistent/igris.sock", name: "t" }, { snapshot() {}, event() {}, lost() {}, end() {} });
    const r = await c.send({ type: "mode", text: "yolo" });
    assert.equal(r.type, "error");
    assert.match(r.error, /terminal only/);
  });
});
