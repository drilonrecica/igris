// The client side of protocol 1 (SPEC §13, §15.9), as internal/serve's
// Client: hello, the snapshot, then the run's events; a dropped connection
// is reconnected with a fresh snapshot, as `igris attach` does. Node's `net`
// only, no runtime dependency. Its hello never carries `yolo` (SPEC §7.3).

import * as net from "node:net";
import { guard } from "./answers";
import { head, MAX_LINE, obj, parseEvent, parseSnapshot, PROTOCOL, type Event, type Snapshot } from "./protocol";
import { short } from "./textsafe";

/** Why a client stopped for good. */
export type End =
  /** Nothing answers on the socket (no serve, or it has exited). */
  | { kind: "gone" }
  /** The connection closed after the run ended (run_stopped). */
  | { kind: "closed" }
  /** Retrying won't help: another protocol, or serve refused this client. */
  | { kind: "refused"; line: string };

export interface Sink {
  /** The run starts over from s: on connecting and after each reconnect. */
  snapshot(s: Snapshot): void;
  /** One event of the run. */
  event(ev: Event): void;
  /** The connection dropped; a snapshot follows once reconnected. */
  lost(): void;
  /** The client has stopped, once. */
  end(why: End): void;
}

export interface Options {
  /** The socket's path, read again before each attempt (it can move). */
  socket: () => string;
  /** This client in the hello, e.g. "igris-vscode 1.9.0". */
  name: string;
  /** The wait between reconnect attempts; 1 s. */
  retryMs?: number;
  /** From hello to the snapshot; 10 s. */
  snapshotMs?: number;
}

/** A command's answer: ack, or error with the run's words. */
export interface Reply {
  type: string;
  error: string;
}

/** The line a snapshot of another protocol is refused with (SPEC §15.9). */
export function protocolRefusal(p: unknown): string {
  const n = p === undefined ? "none" : short(typeof p === "string" ? p : JSON.stringify(p), 16);
  return `This run's igris serve speaks protocol ${n}; this Igris extension speaks protocol ${PROTOCOL}. Update the extension.`;
}

/** Whether a connect error means nothing serves there. */
function notServed(err: NodeJS.ErrnoException): boolean {
  return err.code === "ENOENT" || err.code === "ECONNREFUSED";
}

export class Client {
  private sock: net.Socket | undefined;
  private connected = false;
  private stopped = false;
  private ended = false; // the run ended (run_stopped or an ended snapshot)
  private retry: NodeJS.Timeout | undefined;
  private seq = 0;
  private waiting = new Map<string, (r: Reply) => void>();

  constructor(
    private readonly opts: Options,
    private readonly sink: Sink,
  ) {}

  /** Connects; the sink hears the rest. */
  start(): void {
    this.attempt(true);
  }

  /** Detaches: closes the connection without telling the sink. */
  close(): void {
    this.stopped = true;
    clearTimeout(this.retry);
    this.sock?.destroy();
    this.sock = undefined;
    this.failWaiting("detached");
  }

  /** Whether the connection is up and has had its snapshot. */
  get live(): boolean {
    return this.connected;
  }

  /** Sends a command with a fresh id and resolves with its ack or error
   * (an error when the connection drops first). A command that would set a
   * skip-permissions mode is never sent (SPEC §7.3). */
  send(cmd: Record<string, unknown>): Promise<Reply> {
    const refused = guard(cmd);
    if (refused !== undefined) return Promise.resolve({ type: "error", error: refused });
    if (!this.sock || !this.connected) return Promise.resolve({ type: "error", error: "not connected to the run" });
    const id = `vscode-${++this.seq}`;
    const sock = this.sock;
    return new Promise((resolve) => {
      this.waiting.set(id, resolve);
      sock.write(JSON.stringify({ ...cmd, v: PROTOCOL, id }) + "\n");
    });
  }

  private stop(why: End): void {
    if (this.stopped) return;
    this.close();
    this.sink.end(why);
  }

  private failWaiting(error: string): void {
    for (const resolve of this.waiting.values()) resolve({ type: "error", error });
    this.waiting.clear();
  }

  private attempt(first: boolean): void {
    if (this.stopped) return;
    const sock = net.createConnection(this.opts.socket());
    this.sock = sock;
    let opened = false;
    let gotSnapshot = false;
    let buf = "";
    const timer = setTimeout(() => sock.destroy(new Error("no snapshot")), this.opts.snapshotMs ?? 10_000);

    const onLine = (text: string): boolean => {
      let msg: unknown;
      try {
        msg = JSON.parse(text);
      } catch {
        return false; // not a protocol message: the connection is dropped
      }
      const h = head(msg);
      if (!h) return false;
      if (!gotSnapshot) {
        if (h.type === "error") {
          this.stop({ kind: "refused", line: `The run refused this client: ${short(h.error, 200)}` });
          return false;
        }
        if (h.type !== "snapshot") return false;
        const s = parseSnapshot(obj(msg)!);
        if (s.protocol !== PROTOCOL) {
          this.stop({ kind: "refused", line: protocolRefusal(s.protocol) });
          return false;
        }
        gotSnapshot = true;
        this.connected = true;
        clearTimeout(timer);
        this.ended = s.ended;
        this.sink.snapshot(s);
        return true;
      }
      switch (h.type) {
        case "ack":
        case "error":
        case "tail": {
          if (h.type === "error" && h.id === "") {
            this.stop({ kind: "refused", line: `The run refused this client: ${short(h.error, 200)}` });
            return false;
          }
          const resolve = this.waiting.get(h.id);
          this.waiting.delete(h.id);
          resolve?.({ type: h.type, error: h.error });
          return true;
        }
        case "snapshot":
          return true; // only after hello; a later one is not part of protocol 1
      }
      const ev = parseEvent(msg);
      if (!ev) return true;
      if (ev.type === "run_stopped") this.ended = true;
      this.sink.event(ev);
      return true;
    };

    sock.setEncoding("utf8");
    sock.on("connect", () => {
      opened = true;
      sock.write(JSON.stringify({ v: PROTOCOL, type: "hello", client: this.opts.name }) + "\n");
    });
    sock.on("data", (chunk: string) => {
      buf += chunk;
      for (let nl = buf.indexOf("\n"); nl >= 0; nl = buf.indexOf("\n")) {
        const text = buf.slice(0, nl);
        buf = buf.slice(nl + 1);
        if (!onLine(text)) {
          sock.destroy();
          return;
        }
        if (this.sock !== sock) return; // stopped while handling it
      }
      // A line longer than the protocol allows ends the connection, so a
      // misbehaving server can't make the editor hold an unbounded line.
      if (Buffer.byteLength(buf) > MAX_LINE) sock.destroy();
    });
    sock.on("error", (err: NodeJS.ErrnoException) => {
      if (!opened && (first || notServed(err))) {
        clearTimeout(timer);
        this.stop({ kind: "gone" });
      }
      // Anything else: "close" follows and decides.
    });
    sock.on("close", () => {
      clearTimeout(timer);
      if (this.sock !== sock || this.stopped) return;
      this.sock = undefined;
      const wasConnected = this.connected;
      this.connected = false;
      this.failWaiting("the connection to the run was lost");
      if (this.ended) {
        this.stop({ kind: "closed" });
        return;
      }
      if (wasConnected) this.sink.lost();
      this.retry = setTimeout(() => this.attempt(false), this.opts.retryMs ?? 1000);
    });
  }
}
