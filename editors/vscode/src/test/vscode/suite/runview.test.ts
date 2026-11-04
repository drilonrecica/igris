import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
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
