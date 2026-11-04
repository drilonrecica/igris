import * as assert from "node:assert/strict";
import * as net from "node:net";
import * as path from "node:path";
import { FakeServe } from "../support/fakeServe";

// out/test/unit → the extension's testdata/
const transcripts = path.resolve(__dirname, "../../../testdata/transcripts");

function converse(sock: string, lines: string[]): Promise<string[]> {
  return new Promise((resolve, reject) => {
    const got: string[] = [];
    let buf = "";
    const c = net.createConnection(sock);
    c.setEncoding("utf8");
    c.on("connect", () => lines.forEach((l) => c.write(l + "\n")));
    c.on("data", (d: string) => {
      buf += d;
      const parts = buf.split("\n");
      buf = parts.pop() ?? "";
      got.push(...parts);
    });
    c.on("close", () => resolve(got));
    c.on("error", reject);
  });
}

describe("FakeServe", () => {
  let fake: FakeServe | undefined;
  afterEach(async () => {
    await fake?.close();
    fake = undefined;
  });

  it("replays a recorded transcript after the hello", async () => {
    fake = await FakeServe.start(path.join(transcripts, "working.jsonl"));
    const got = await converse(fake.socketPath, [JSON.stringify({ v: 1, type: "hello", client: "igris-vscode test" })]);
    await fake.done;
    assert.equal(fake.failure, undefined);
    const snap = JSON.parse(got[0]!) as { type: string; protocol: number; active: { task: string }[] };
    assert.equal(snap.type, "snapshot");
    assert.equal(snap.protocol, 1);
    assert.equal(snap.active[0]!.task, "S1-01");
    assert.deepEqual(got.map((l) => (JSON.parse(l) as { type: string }).type), ["snapshot", "tokens", "task_done"]);
  });

  it("serves a snapshot of another protocol for the refusal path", async () => {
    fake = await FakeServe.start(path.join(transcripts, "protocol-2.jsonl"));
    const got = await converse(fake.socketPath, [JSON.stringify({ v: 1, type: "hello" })]);
    assert.equal((JSON.parse(got[0]!) as { protocol: number }).protocol, 2);
  });

  it("checks the client's commands against the transcript", async () => {
    fake = await FakeServe.start(path.join(transcripts, "needs-you.jsonl"));
    const got = await converse(fake.socketPath, [
      JSON.stringify({ v: 1, type: "hello" }),
      JSON.stringify({ v: 1, type: "answer", yes: true, task: "S1-01" }),
    ]);
    await fake.done;
    assert.equal(fake.failure, undefined);
    assert.equal((JSON.parse(got[1]!) as { type: string }).type, "ack");
  });

  it("fails the replay on a message the transcript didn't expect", async () => {
    fake = await FakeServe.start(path.join(transcripts, "needs-you.jsonl"));
    await converse(fake.socketPath, [JSON.stringify({ v: 1, type: "hello" }), JSON.stringify({ v: 1, type: "stop" })]);
    await fake.done;
    assert.match(fake.failure ?? "", /unexpected client message/);
  });
});
