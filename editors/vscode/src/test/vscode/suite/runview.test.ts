import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import type { Ui } from "../../../act";
import type { Ask, Choice } from "../../../answers";
import type { Api } from "../../../extension";
import { FakeServe } from "../../support/fakeServe";
import { workspaceDir } from "./workspace";

/** Polls f until it returns something, or fails after ms. */
async function eventually<T>(what: string, f: () => T | undefined, ms = 15_000): Promise<T> {
  const end = Date.now() + ms;
  for (;;) {
    const v = f();
    if (v !== undefined) return v;
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 50));
  }
}

describe("the run view", () => {
  let api: Api;
  let fake: FakeServe | undefined;

  before(async () => {
    const ext = vscode.extensions.getExtension<Api>("drilonrecica.igris");
    assert.ok(ext);
    api = await ext.activate();
  });

  after(async () => {
    await fake?.close();
    fs.rmSync(path.join(workspaceDir(), ".igris"), { recursive: true, force: true });
  });

  it("shows the plan from igris status --json without a run", async () => {
    const v = await eventually("the idle tree", () => {
      const v = api.view();
      return v.mode === "idle" && v.tree.length > 0 ? v : undefined;
    });
    assert.deepEqual(
      v.tree.map((n) => n.label),
      ["S1 Scratch phase", "S2 Later phase"],
    );
    assert.equal(v.bar?.text, "igris Idle · no run");
  });

  it("attaches to the run once its serve answers, the run on top", async () => {
    const transcript = path.resolve(__dirname, "../../../../testdata/transcripts/parallel.jsonl");
    fake = await FakeServe.start(transcript, path.join(workspaceDir(), ".igris", "igris.sock"));
    const v = await eventually("the live tree", () => {
      const v = api.view();
      return v.mode === "live" ? v : undefined;
    });
    assert.equal(v.tree[0]!.id, "run");
    assert.equal(v.bar?.text, "igris ! Needs you · 2 questions");
    assert.equal(v.bar?.tone, "needs");
    await vscode.commands.executeCommand("igris.showRun");
  });

  it("goes back to the plan when the run's serve has gone", async () => {
    await fake!.close();
    fake = undefined;
    const v = await eventually("the idle tree again", () => {
      const v = api.view();
      return v.mode === "idle" && v.tree.length > 0 ? v : undefined;
    });
    assert.equal(v.bar?.text, "igris Done · ▲ interrupted");
  });
});

/** A Ui that answers from queues in place of the owner, recording what the
 * extension showed. */
class OwnerStandIn implements Ui {
  shown: string[] = [];
  buttons: (string | undefined)[] = [];
  picks: (string | undefined)[] = [];
  confirms: boolean[] = [];
  async choose(_choices: Choice[], title: string): Promise<Choice | undefined> {
    this.shown.push(`choose ${title}`);
    return undefined;
  }
  async chooseAsk(asks: Ask[]): Promise<Ask | undefined> {
    return asks[0];
  }
  async input(): Promise<string | undefined> {
    return undefined;
  }
  async pick(p: { items: string[] }): Promise<string | undefined> {
    this.shown.push(`pick ${p.items.join(",")}`);
    return this.picks.shift();
  }
  async confirm(c: { title: string }): Promise<boolean> {
    this.shown.push(`confirm ${c.title}`);
    return this.confirms.shift() ?? false;
  }
  async notify(line: string, buttons: string[]): Promise<string | undefined> {
    this.shown.push(`notify ${line} [${buttons.join(" | ")}]`);
    return this.buttons.shift();
  }
  async show(): Promise<void> {}
  told(ok: boolean, line: string): void {
    this.shown.push(`${ok ? "ok" : "told"} ${line}`);
  }
}

describe("answering from the editor", () => {
  let api: Api;
  let fake: FakeServe | undefined;
  const owner = new OwnerStandIn();

  before(async () => {
    api = await vscode.extensions.getExtension<Api>("drilonrecica.igris")!.activate();
    api.useUi(owner);
    owner.buttons.push("Commit");
    fs.mkdirSync(path.join(workspaceDir(), ".igris"), { recursive: true });
    const transcript = path.resolve(__dirname, "../../../../testdata/transcripts/answering.jsonl");
    fake = await FakeServe.start(transcript, path.join(workspaceDir(), ".igris", "igris.sock"));
  });

  after(async () => {
    api.useUi(undefined);
    await fake?.close();
    fs.rmSync(path.join(workspaceDir(), ".igris"), { recursive: true, force: true });
  });

  it("tells about a question with a notification whose button answers it", async () => {
    await eventually("the answer", () => (fake!.received.some((m) => m["type"] === "answer") ? true : undefined));
    assert.equal(owner.shown[0], "notify Igris: S1-01 · commit?: Commit S1-01? [Commit | Leave uncommitted | Answer…]");
    await eventually("the question settled", () => (api.asks().length === 0 ? true : undefined));
  });

  it("pauses after this task", async () => {
    await vscode.commands.executeCommand("igris.pause");
    await eventually("paused", () => (api.view().bar?.text.includes("Paused") ? true : undefined));
  });

  it("never offers a skip-permissions mode, and sends the mode picked", async () => {
    owner.picks.push("yolo", "accept");
    await vscode.commands.executeCommand("igris.setMode");
    assert.ok(!owner.shown.at(-1)!.includes("yolo"), owner.shown.at(-1));
    assert.ok(!fake!.received.some((m) => m["type"] === "mode"), "a skip-permissions mode was sent");
    await vscode.commands.executeCommand("igris.setMode");
    await eventually("the mode sent", () => (fake!.received.some((m) => m["type"] === "mode" && m["text"] === "accept") ? true : undefined));
    assert.ok(!fake!.received.some((m) => "yolo" in m), "a message carried yolo");
  });

  it("stops only after the second confirmation", async () => {
    owner.confirms.push(false, true);
    await vscode.commands.executeCommand("igris.stop");
    assert.equal(owner.shown.at(-1), "confirm Stop igris?");
    assert.ok(!fake!.received.some((m) => m["type"] === "stop"));
    await vscode.commands.executeCommand("igris.stop");
    await fake!.done;
    assert.equal(fake!.failure, undefined);
    await eventually("the run's end", () => (api.view().bar?.text.includes("stopped") ? true : undefined));
  });
});

describe("without a project", () => {
  it("sets no message, so the view's welcome content with Set up igris shows", async () => {
    const api = await vscode.extensions.getExtension<Api>("drilonrecica.igris")!.activate();
    // The suites above removed .igris; refresh has the view look at the
    // workspace again. VS Code shows a view's welcome content (the Set up
    // button) only while the tree is empty AND its message is empty.
    fs.rmSync(path.join(workspaceDir(), ".igris"), { recursive: true, force: true });
    await vscode.commands.executeCommand("igris.refresh");
    await eventually("the empty view", () => (api.view().mode === "none" ? true : undefined));
    const v = api.view();
    assert.deepEqual(v.tree, []);
    assert.equal(v.message, undefined);
  });

  it("picks the project up again on refresh once it exists", async () => {
    const api = await vscode.extensions.getExtension<Api>("drilonrecica.igris")!.activate();
    fs.mkdirSync(path.join(workspaceDir(), ".igris"), { recursive: true });
    await vscode.commands.executeCommand("igris.refresh");
    await eventually("the plan", () => (api.view().mode !== "none" ? true : undefined));
    fs.rmSync(path.join(workspaceDir(), ".igris"), { recursive: true, force: true });
    await vscode.commands.executeCommand("igris.refresh");
    await eventually("the empty view again", () => (api.view().mode === "none" ? true : undefined));
  });
});

describe("without a plan", () => {
  it("shows the Create a starter plan content instead of igris's error line", async () => {
    const api = await vscode.extensions.getExtension<Api>("drilonrecica.igris")!.activate();
    fs.mkdirSync(path.join(workspaceDir(), ".igris"), { recursive: true });
    fs.writeFileSync(path.join(workspaceDir(), ".igris", "fake-no-plan"), "");
    await vscode.commands.executeCommand("igris.refresh");
    // The stand-in's status fails as igris does without a plan; doctor says
    // the plan is missing; the view then clears its message so the welcome
    // content (Create a starter plan) shows.
    await eventually("the no-plan view", () => (api.view().mode === "idle" && api.view().message === undefined ? true : undefined));
    assert.deepEqual(api.view().tree, []);
    fs.rmSync(path.join(workspaceDir(), ".igris"), { recursive: true, force: true });
    await vscode.commands.executeCommand("igris.refresh");
    await eventually("the empty view", () => (api.view().mode === "none" ? true : undefined));
  });
});
