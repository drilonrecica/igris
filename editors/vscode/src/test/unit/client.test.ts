import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as net from "node:net";
import * as os from "node:os";
import * as path from "node:path";
import { Client, type End, type Sink } from "../../client";
import type { Event, Snapshot } from "../../protocol";
import { FakeServe } from "../support/fakeServe";
import { transcripts } from "../support/transcripts";

/** A sink that records what the client said, in order. */
class Recorder implements Sink {
  readonly log: string[] = [];
  readonly snapshots: Snapshot[] = [];
  readonly events: Event[] = [];
  why: End | undefined;
  private waiters: [(l: string) => boolean, () => void][] = [];

  snapshot(s: Snapshot): void {
    this.snapshots.push(s);
    this.note(`snapshot ${s.run}`);
  }
  event(ev: Event): void {
    this.events.push(ev);
    this.note(`event ${ev.type}`);
  }
  lost(): void {
    this.note("lost");
  }
  end(why: End): void {
    this.why = why;
    this.note(`end ${why.kind}`);
  }
  private note(l: string): void {
    this.log.push(l);
    this.waiters = this.waiters.filter(([match, resolve]) => {
      if (!match(l)) return true;
      resolve();
      return false;
    });
  }
  /** Resolves once the client said a line starting with prefix. */
  until(prefix: string): Promise<void> {
    if (this.log.some((l) => l.startsWith(prefix))) return Promise.resolve();
    return new Promise((r) => this.waiters.push([(l) => l.startsWith(prefix), r]));
  }
}

const t = (name: string): string => path.join(transcripts, name);

describe("the protocol client", () => {
  const fakes: FakeServe[] = [];
  let client: Client | undefined;
  afterEach(async () => {
    client?.close();
    client = undefined;
    for (const f of fakes.splice(0)) await f.close();
  });

  async function serve(names: string | string[], at?: string): Promise<FakeServe> {
    const f = await FakeServe.start(Array.isArray(names) ? names.map(t) : t(names), at);
    fakes.push(f);
    return f;
  }

  it("says hello without yolo, then hands over the snapshot and the events", async () => {
    const fake = await serve("working.jsonl");
    const rec = new Recorder();
    client = new Client({ socket: () => fake.socketPath, name: "igris-vscode test", retryMs: 10 }, rec);
    client.start();
    await rec.until("event task_done");
    assert.deepEqual(fake.received[0], { v: 1, type: "hello", client: "igris-vscode test" });
    assert.equal(rec.snapshots[0]!.active[0]!.task, "S1-01");
    assert.deepEqual(rec.log.slice(0, 3), ["snapshot 20261104-090000-ab12", "event tokens", "event task_done"]);
  });

  it("refuses a snapshot of another protocol with one line, and stops", async () => {
    const fake = await serve("protocol-2.jsonl");
    const rec = new Recorder();
    client = new Client({ socket: () => fake.socketPath, name: "t", retryMs: 10 }, rec);
    client.start();
    await rec.until("end");
    assert.deepEqual(rec.why, { kind: "refused", line: "This run's igris serve speaks protocol 2; this Igris extension speaks protocol 1. Update the extension." });
    assert.deepEqual(rec.snapshots, []);
    assert.equal(fake.connections, 1, "no retry");
  });

  it("reconnects with a fresh snapshot after a dropped connection", async () => {
    const fake = await serve(["dropped.jsonl", "after-drop.jsonl"]);
    const rec = new Recorder();
    client = new Client({ socket: () => fake.socketPath, name: "t", retryMs: 10 }, rec);
    client.start();
    await rec.until("end");
    assert.deepEqual(rec.log, [
      "snapshot 20261104-110000-ef56",
      "event task_done",
      "lost",
      "snapshot 20261104-110000-ef56",
      "event task_done",
      "event run_stopped",
      "end closed",
    ]);
    assert.equal(rec.snapshots[1]!.active[0]!.task, "S1-02");
  });

  it("ends when nothing answers on the socket any more", async () => {
    const fake = await serve("dropped.jsonl");
    const rec = new Recorder();
    client = new Client({ socket: () => fake.socketPath, name: "t", retryMs: 10 }, rec);
    client.start();
    await rec.until("lost");
    await fake.close();
    fakes.length = 0;
    await rec.until("end");
    assert.deepEqual(rec.why, { kind: "gone" });
  });

  it("ends at once when no run answers to start with", async () => {
    const rec = new Recorder();
    client = new Client({ socket: () => path.join(os.tmpdir(), "igris-no-such.sock"), name: "t" }, rec);
    client.start();
    await rec.until("end");
    assert.deepEqual(rec.log, ["end gone"]);
  });

  it("matches a command's ack by its id", async () => {
    const fake = await serve("needs-you.jsonl");
    const rec = new Recorder();
    client = new Client({ socket: () => fake.socketPath, name: "t", retryMs: 10 }, rec);
    client.start();
    await rec.until("snapshot");
    const reply = await client.send({ type: "answer", yes: true, task: "S1-01" });
    assert.deepEqual(reply, { type: "ack", error: "" });
    assert.equal(fake.failure, undefined);
    assert.match(String(fake.received[1]!["id"]), /^vscode-\d+$/);
  });

  it("drops a connection whose line passes the protocol's limit", async () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), "igris-big-"));
    const sock = path.join(dir, "igris.sock");
    let connections = 0;
    const server = net.createServer((c) => {
      connections++;
      c.write('{"v":1,"type":"snapshot","protocol":1,"run":"r"}\n');
      c.write("x".repeat((1 << 20) + 10)); // no newline
    });
    await new Promise<void>((r) => server.listen(sock, r));
    const rec = new Recorder();
    client = new Client({ socket: () => sock, name: "t", retryMs: 10 }, rec);
    client.start();
    await rec.until("lost");
    client.close();
    server.close();
    fs.rmSync(dir, { recursive: true, force: true });
    assert.ok(connections >= 1);
  });
});
