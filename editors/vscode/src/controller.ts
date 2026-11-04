// Drives the run view (SPEC §15.9): attached to the run's serve while one
// answers, reconnecting with a fresh snapshot when the connection drops, and
// without a run the plan from `igris status --json`, watching for a run to
// start. No VS Code here; extension.ts draws what it says.

import * as fs from "node:fs";
import type { Command } from "./answers";
import { Client, type End, type Reply } from "./client";
import type { IdleResult } from "./idle";
import type { Snapshot } from "./protocol";
import { apply } from "./run";
import type { ViewState } from "./view";

export interface Deps {
  /** This client in the hello. */
  name: string;
  /** The run's socket path, read again each time. */
  socket: () => string;
  /** `igris status --json`. */
  readStatus: () => Promise<IdleResult>;
  /** Reconnect wait; 1 s. */
  retryMs?: number;
  /** How often to look for a run while none answers; 2 s. */
  pollMs?: number;
  /** Hello to snapshot; 10 s. */
  snapshotMs?: number;
}

export class Controller {
  state: ViewState = { mode: "starting" };
  private client: Client | undefined;
  private listeners: ((s: ViewState) => void)[] = [];
  private poll: NodeJS.Timeout | undefined;
  private tick: NodeJS.Timeout | undefined;
  private reread: NodeJS.Timeout | undefined;
  private reading = 0; // the latest idle read; older answers are dropped
  private disposed = false;

  constructor(private readonly deps: Deps) {}

  /** Calls f on every change. */
  onChange(f: (s: ViewState) => void): void {
    this.listeners.push(f);
  }

  private emit(): void {
    for (const f of this.listeners) f(this.state);
  }

  /** Attaches to the run if one answers, else shows the plan. */
  start(): void {
    this.attach(false);
  }

  /** Starts over: a refusal is dropped and everything is read again. */
  refresh(): void {
    if (this.disposed) return;
    this.client?.close();
    this.client = undefined;
    // Closed here, not by serve: what follows is never "interrupted".
    const { ended } = this.state;
    this.state = { mode: "starting", ...(ended !== undefined ? { ended } : {}) };
    this.emit();
    this.attach(false);
  }

  /** The plan or .igris/ changed on disk: read it again when no run is
   * attached (the live run's events say it otherwise). */
  filesChanged(): void {
    if (this.state.mode !== "idle") return;
    clearTimeout(this.reread);
    this.reread = setTimeout(() => void this.readIdle(), 300);
  }

  /** Sends cmd to the run this view is attached to. */
  send(cmd: Command): Promise<Reply> {
    if (!this.client || this.state.mode !== "live") return Promise.resolve({ type: "error", error: "not connected to the run" });
    return this.client.send({ ...cmd });
  }

  dispose(): void {
    this.disposed = true;
    this.client?.close();
    clearInterval(this.poll);
    clearInterval(this.tick);
    clearTimeout(this.reread);
    this.listeners = [];
  }

  /** Connects; probing says the view is idle and only looks for a run. */
  private attach(probing: boolean): void {
    if (this.disposed) return;
    const c = new Client(
      { socket: this.deps.socket, name: this.deps.name, ...opt("retryMs", this.deps.retryMs), ...opt("snapshotMs", this.deps.snapshotMs) },
      {
        snapshot: (s) => this.live(s),
        event: (ev) => {
          if (!this.state.run) return;
          apply(this.state.run, ev);
          if (ev.type === "run_stopped") this.state.ended = this.state.run.end;
          this.emit();
        },
        lost: () => {
          this.state = { ...this.state, mode: "reconnecting" };
          this.emit();
        },
        end: (why) => {
          if (this.client === c) this.client = undefined;
          this.ended(why, probing);
        },
      },
    );
    this.client = c;
    c.start();
  }

  private live(s: Snapshot): void {
    clearInterval(this.poll);
    this.poll = undefined;
    this.state = { mode: "live", run: s, ...(s.ended ? { ended: s.end } : {}) };
    clearInterval(this.tick);
    // The usage hold's countdown moves while nothing else does.
    this.tick = setInterval(() => {
      if (this.state.mode === "live" && this.state.run?.limited) this.emit();
    }, 30_000);
    this.emit();
  }

  private ended(why: End, probing: boolean): void {
    clearInterval(this.tick);
    if (why.kind === "refused") {
      clearInterval(this.poll);
      this.poll = undefined;
      this.state = { mode: "refused", refusal: why.line };
      this.emit();
      return;
    }
    if (probing && this.state.mode === "idle") return; // still no run
    let ended = this.state.ended;
    const run = this.state.run;
    if (run && (this.state.mode === "live" || this.state.mode === "reconnecting")) {
      // A serve that went away without run_stopped died: Done ▲ interrupted.
      ended = run.ended ? run.end : "interrupted";
    }
    this.state = { mode: "idle", ...(ended !== undefined ? { ended } : {}) };
    this.emit();
    void this.readIdle();
    this.watch();
  }

  private async readIdle(): Promise<void> {
    const n = ++this.reading;
    const res = await this.deps.readStatus();
    if (n !== this.reading || this.disposed || this.state.mode !== "idle") return;
    const { ended } = this.state;
    this.state = res.ok
      ? { mode: "idle", idle: res.idle, ...(ended !== undefined ? { ended } : {}) }
      : { mode: "idle", idleError: res.line, ...(ended !== undefined ? { ended } : {}) };
    this.emit();
  }

  /** Looks for a run to start while none answers: a socket appearing. */
  private watch(): void {
    clearInterval(this.poll);
    this.poll = setInterval(() => {
      if (this.state.mode !== "idle" || this.client) return;
      if (fs.existsSync(this.deps.socket())) this.attach(true);
    }, this.deps.pollMs ?? 2000);
  }
}

function opt<K extends string>(k: K, v: number | undefined): { [P in K]?: number } {
  return (v === undefined ? {} : { [k]: v }) as { [P in K]?: number };
}
