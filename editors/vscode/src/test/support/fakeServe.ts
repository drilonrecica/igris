// A fake `igris serve` socket (AGENTS §5: tests never start the real igris).
// It replays a recorded protocol-1 transcript (testdata/transcripts/*.jsonl)
// to the one client that connects: newline-delimited JSON, one step a line.
//
//   {"expect": {...}}  wait for a client message containing these fields
//   {"send": {...}}    send this message to the client
//   {"close": true}    end the connection
//
// A client message that doesn't match the next `expect` fails the replay.
import * as fs from "node:fs";
import * as net from "node:net";
import * as os from "node:os";
import * as path from "node:path";

type Step = { expect: Record<string, unknown> } | { send: Record<string, unknown> } | { close: true };

export function loadTranscript(file: string): Step[] {
  return fs
    .readFileSync(file, "utf8")
    .split("\n")
    .filter((l) => l.trim() !== "")
    .map((l) => JSON.parse(l) as Step);
}

function contains(got: unknown, want: unknown): boolean {
  if (want === null || typeof want !== "object") return got === want;
  if (got === null || typeof got !== "object") return false;
  return Object.entries(want).every(([k, v]) => contains((got as Record<string, unknown>)[k], v));
}

export class FakeServe {
  readonly dir = fs.mkdtempSync(path.join(os.tmpdir(), "igris-fake-"));
  readonly socketPath = path.join(this.dir, "igris.sock");
  /** Client messages received, in order. */
  readonly received: Record<string, unknown>[] = [];
  /** Set when the client sent something the transcript didn't expect. */
  failure: string | undefined;
  /** Resolves when the whole transcript has been replayed. */
  readonly done: Promise<void>;
  private server = net.createServer();
  private finish!: () => void;

  constructor(private readonly steps: Step[]) {
    this.done = new Promise((r) => (this.finish = r));
    this.server.once("connection", (sock) => this.replay(sock));
  }

  static async start(transcript: string): Promise<FakeServe> {
    const f = new FakeServe(loadTranscript(transcript));
    await new Promise<void>((resolve, reject) => {
      f.server.once("error", reject);
      f.server.listen(f.socketPath, resolve);
    });
    return f;
  }

  private replay(sock: net.Socket): void {
    let i = 0;
    let buf = "";
    const advance = (): void => {
      while (i < this.steps.length) {
        const step = this.steps[i]!;
        if ("expect" in step) return;
        i++;
        if ("send" in step) sock.write(JSON.stringify(step.send) + "\n");
        else sock.end();
      }
      this.finish();
    };
    sock.setEncoding("utf8");
    sock.on("data", (chunk: string) => {
      buf += chunk;
      for (let nl = buf.indexOf("\n"); nl >= 0; nl = buf.indexOf("\n")) {
        const line = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        const msg = JSON.parse(line) as Record<string, unknown>;
        this.received.push(msg);
        const step = this.steps[i];
        if (!step || !("expect" in step) || !contains(msg, step.expect)) {
          this.failure = `unexpected client message ${line} at step ${i}`;
          sock.destroy();
          this.finish();
          return;
        }
        i++;
        advance();
      }
    });
    advance();
  }

  async close(): Promise<void> {
    await new Promise<void>((r) => this.server.close(() => r()));
    fs.rmSync(this.dir, { recursive: true, force: true });
  }
}
