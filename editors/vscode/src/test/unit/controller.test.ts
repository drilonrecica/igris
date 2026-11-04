import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { Controller } from "../../controller";
import { parseStatus, type IdleResult } from "../../idle";
import { barOf, type ViewState } from "../../view";
import { FakeServe } from "../support/fakeServe";
import { testdata, transcripts } from "../support/transcripts";

describe("the run view's controller", () => {
  let dir: string;
  let sock: string;
  let fake: FakeServe | undefined;
  let c: Controller | undefined;
  let reads: number;
  const status = (): Promise<IdleResult> => {
    reads++;
    return Promise.resolve(parseStatus(fs.readFileSync(path.join(testdata, "status.json"), "utf8")));
  };

  beforeEach(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), "igris-ctl-"));
    sock = path.join(dir, "igris.sock");
    reads = 0;
  });
  afterEach(async () => {
    c?.dispose();
    c = undefined;
    await fake?.close();
    fake = undefined;
    fs.rmSync(dir, { recursive: true, force: true });
  });

  /** Resolves with the first state that passes ok. */
  function until(ok: (s: ViewState) => boolean): Promise<ViewState> {
    return new Promise((resolve) => {
      if (ok(c!.state)) resolve(c!.state);
      c!.onChange((s) => {
        if (ok(s)) resolve(s);
      });
    });
  }

  it("shows the plan without a run, attaches when one starts, and tells a serve that died", async () => {
    c = new Controller({ name: "t", socket: () => sock, readStatus: status, retryMs: 10, pollMs: 10 });
    c.start();
    const idle = await until((s) => s.mode === "idle" && s.idle !== undefined);
    assert.equal(idle.idle!.phases.length, 2);
    assert.equal(barOf(idle, Date.now())!.text, "igris Idle · no run");

    fake = await FakeServe.start(path.join(transcripts, "dropped.jsonl"), sock);
    const live = await until((s) => s.mode === "live");
    assert.equal(live.run!.phase, "S1");
    await until((s) => s.mode === "reconnecting");

    await fake.close(); // serve is gone without run_stopped
    fake = undefined;
    const after = await until((s) => s.mode === "idle" && s.idle !== undefined);
    assert.equal(after.ended, "interrupted");
    assert.equal(barOf(after, Date.now())!.text, "igris Done · ▲ interrupted");
    assert.equal(reads, 2, "the plan is read once per idle spell, not on every probe");
  });

  it("keeps a run's outcome once its serve has gone", async () => {
    fake = await FakeServe.start(["dropped.jsonl", "after-drop.jsonl"].map((n) => path.join(transcripts, n)), sock);
    c = new Controller({ name: "t", socket: () => sock, readStatus: status, retryMs: 10, pollMs: 10 });
    c.start();
    const s = await until((s) => s.mode === "idle" && s.idle !== undefined);
    assert.equal(s.ended, "completed");
    assert.equal(barOf(s, Date.now())!.text, "igris Done · ✓ completed");
  });

  it("holds a protocol refusal until a refresh", async () => {
    fake = await FakeServe.start(path.join(transcripts, "protocol-2.jsonl"), sock);
    c = new Controller({ name: "t", socket: () => sock, readStatus: status, retryMs: 10, pollMs: 10 });
    c.start();
    const s = await until((s) => s.mode === "refused");
    assert.match(s.refusal!, /speaks protocol 2;/);
    assert.equal(reads, 0, "nothing else runs");
    await fake.close();
    fake = undefined;
    c.refresh();
    await until((s) => s.mode === "idle" && s.idle !== undefined);
  });

  it("reads the plan again when its files change, only while idle", async () => {
    c = new Controller({ name: "t", socket: () => sock, readStatus: status, retryMs: 10, pollMs: 10 });
    c.start();
    await until((s) => s.mode === "idle" && s.idle !== undefined);
    c.filesChanged();
    c.filesChanged();
    await new Promise((r) => setTimeout(r, 400));
    assert.equal(reads, 2, "debounced into one read");
  });
});
