// The terminal tabs of a run's sessions (SPEC §15.9), kept in step with the
// run: a tab for each active session that has none yet, none for a session
// the run no longer has (the task ended, a retry opened another), and a tab
// the owner closed stays closed until asked for again. No VS Code here:
// terminals.ts opens the real tabs, the unit tests a fake.

import type { Run } from "./run";
import { argvOf, groupedName, sessionsOf, type Session } from "./sessions";

/** One open tab. */
export interface TabHandle {
  /** Brings the tab to the front. */
  show(): void;
  /** Closes it; its tmux client ends, and with it the grouped session. */
  dispose(): void;
}

/** What opens a tab: a terminal named name whose process is argv. */
export interface TabMaker {
  open(name: string, argv: string[]): TabHandle;
}

export class SessionTabs {
  private readonly open = new Map<string, { session: Session; handle: TabHandle }>();
  /** The tabs the owner closed, or whose tmux client failed, by session
   * key: not opened again by sync. */
  private readonly closedByOwner = new Set<string>();
  /** Handles this class is closing, so their close isn't the owner's. */
  private readonly disposing = new Set<TabHandle>();
  private seq = 0;

  /** maker opens the tabs (read each time, so a test can stand in). tag is
   * this client's part of its grouped sessions' names, unique on the
   * machine (the extension host's pid); each tab adds a number of its own,
   * so a tab opened again never collides with one still ending. onFailed
   * hears of a tab whose tmux client ended with an error at once. */
  constructor(
    private readonly maker: () => TabMaker,
    private readonly tag: string,
    private readonly onFailed: (s: Session, code: number) => void = () => undefined,
  ) {}

  /** Brings the tabs in line with run (none without one, or once it ended). */
  sync(run: Run | undefined): void {
    const want = run && !run.ended ? sessionsOf(run) : [];
    const keys = new Set(want.map((s) => s.key));
    for (const [key, o] of this.open) {
      if (!keys.has(key)) {
        this.open.delete(key);
        this.close(o.handle);
      }
    }
    for (const key of this.closedByOwner) if (!keys.has(key)) this.closedByOwner.delete(key);
    for (const s of want) if (!this.open.has(s.key) && !this.closedByOwner.has(s.key)) this.start(s);
  }

  /** A tab closed (the editor's onDidCloseTerminal): one the owner closed
   * stays closed for that session, as does one whose tmux client exited
   * with failed (an error, reported, never retried in a loop); one this
   * class closed is forgotten. */
  closed(handle: TabHandle, failed?: number): void {
    if (this.disposing.delete(handle)) return;
    for (const [key, o] of this.open) {
      if (o.handle === handle) {
        this.open.delete(key);
        this.closedByOwner.add(key);
        if (failed !== undefined) this.onFailed(o.session, failed);
      }
    }
  }

  /** Shows task's tab, opening it again when the owner had closed it;
   * false when run has no session a tab can show for task. */
  focus(run: Run | undefined, task: string): boolean {
    const s = run && !run.ended ? sessionsOf(run).find((x) => x.task === task) : undefined;
    if (!s) return false;
    this.closedByOwner.delete(s.key);
    (this.open.get(s.key) ?? this.start(s)).handle.show();
    return true;
  }

  /** The sessions with a tab open now, in the run's order. */
  tabs(): Session[] {
    return [...this.open.values()].map((o) => o.session);
  }

  dispose(): void {
    for (const o of this.open.values()) this.close(o.handle);
    this.open.clear();
    this.closedByOwner.clear();
  }

  private start(s: Session): { session: Session; handle: TabHandle } {
    const o = { session: s, handle: this.maker().open(s.name, argvOf(s, groupedName(s.task, `${this.tag}-${++this.seq}`))) };
    this.open.set(s.key, o);
    return o;
  }

  private close(h: TabHandle): void {
    this.disposing.add(h);
    h.dispose();
  }
}
