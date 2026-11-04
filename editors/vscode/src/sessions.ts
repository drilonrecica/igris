// Sessions as terminal tabs (SPEC §15.9), as plain data: which of the run's
// active sessions get a tab, the tmux argv that attaches one, and the line
// that says where a session is when no tab can show it. No VS Code here:
// tabs.ts keeps the tabs in step with the run, terminals.ts opens them.
//
// The extension host runs outside herdr and tmux, so a run started from the
// editor is served headless on tmux (§11.3, §11.5): its windows live in the
// detached session the snapshot's `session_host` names, `igris-<project>`,
// on the server whose socket it names. A tab is a tmux client of its own in
// a grouped session on that server (`new-session -t <session>`): it shares
// the windows but has a current window of its own, so each tab selects its
// task's window by the snapshot's exact `tab_id` (`select-window -t @N`)
// without moving the others (decisions.md V19-P2). The grouped session is
// `destroy-unattached on`, so a closed tab leaves nothing behind. Its
// status line is at the top, the tab's first line: the task's label and
// that a folder-trust question is Claude Code's, not igris's. The owner
// never handles tmux; side sessions stay in the terminal (§6.10).

import type { Card, SessionHost } from "./protocol";
import type { Run } from "./run";
import { line } from "./textsafe";

/** The backend names the snapshot uses (internal/backend). */
export const TMUX = "tmux";
const HERDR = "herdr";

/** A tmux window id, the only shape select-window gets (SPEC §11.5). */
const windowID = /^@[0-9]+$/;

/** What the tab's first line says after the task's label. */
export const TRUST = "If Claude Code asks to trust this folder, that question is Claude Code's: answer it here";

/** An active task's session that a tab can show. */
export interface Session {
  /** Stable for one session: the task and its window. A new session of the
   * same task (a retry) is another key. */
  key: string;
  task: string;
  /** The tab's name: the pane's label, `<ID> · <rank>`. */
  name: string;
  host: SessionHost;
  tabId: string;
}

/** The sessions of run that get a tab: each active task whose tmux window
 * the snapshot names, when the run's sessions live in a tmux session (SPEC
 * §15.9). None on herdr, none from an older serve without `session_host`,
 * none for a task whose session hasn't opened (or was lost). */
export function sessionsOf(run: Run): Session[] {
  const h = run.sessionHost;
  if (!h || h.backend !== TMUX || h.session === "") return [];
  const out: Session[] = [];
  for (const c of run.active) {
    const s = c.session;
    if (!s || s.backend !== TMUX || !windowID.test(s.tabId)) continue;
    out.push({ key: `${c.task}/${s.tabId}`, task: c.task, name: labelOf(c), host: h, tabId: s.tabId });
  }
  return out;
}

/** The pane's label, `<ID> · <rank>` (internal/backend), cleaned. */
export function labelOf(c: Card): string {
  return line(c.rank ? `${c.task} · ${c.rank}` : c.task);
}

/** The grouped session's name for task's tab: tmux's characters only (as
 * igris's own session name, SPEC §11.5), with tag making it this tab's. */
export function groupedName(task: string, tag: string): string {
  return `igris-tab-${task.replace(/[^A-Za-z0-9_-]/g, "-").slice(0, 32)}-${tag}`;
}

/** The tmux argv that attaches a tab to s through the grouped session
 * named grouped: one tmux process, its commands separated by `;` arguments,
 * never a shell. An error in the sequence drops the rest of it, so every
 * option target is the grouped session's plain name. The status line is
 * `status-format[0]`, the whole line as a session option: a window option
 * (`window-status-format`) set on the grouped session would land on its
 * current window at that moment, not on the task's window selected after,
 * and tmux's window list would show after the text. */
export function argvOf(s: Session, grouped: string): string[] {
  const opt = (name: string, value: string): string[] => ["set-option", "-t", grouped, name, value, ";"];
  return [
    TMUX,
    ...(s.host.socket !== "" ? ["-S", s.host.socket] : []),
    "new-session",
    "-t",
    `=${s.host.session}`,
    "-s",
    grouped,
    ";",
    ...opt("destroy-unattached", "on"),
    ...opt("status-position", "top"),
    ...opt("status-format[0]", format(` ${s.name} │ ${TRUST} `)),
    "select-window",
    "-t",
    s.tabId,
  ];
}

/** s as tmux format text (SPEC §11.5): every `#` doubled, so nothing in
 * it expands or runs. */
function format(s: string): string {
  return s.replace(/#/g, "##");
}

/** Where c's session is when no tab can show it, as the TUI's card says
 * it (SPEC §15.3): the herdr tab, the tmux attach command for a window the
 * snapshot doesn't name, what an older serve leaves unsaid, or that the
 * task has no session right now. */
export function whereIs(run: Run, c: Card): string {
  const h = run.sessionHost;
  const name = labelOf(c);
  if (!h) return `${name}: this run's igris serve is older than 1.9 and doesn't say where its sessions are; open it from the TUI (igris attach).`;
  switch (h.backend) {
    case TMUX: {
      if (!c.session) return `${name} has no session open right now.`;
      const socket = line(h.socket);
      const session = line(h.session);
      return `Session: ${session !== "" ? `tmux session ${session}` : `tmux server ${socket}`} — ${attachCommand(h)}`;
    }
    case HERDR:
      return `Session: herdr tab ${name}`;
  }
  return `Session: ${line(h.backend)}`;
}

/** The command that shows h's tmux session in a terminal, as the TUI's
 * card gives it (SPEC §15.3): `tmux attach -t <session>`, with `-S
 * <socket>` when the server is not the default one. */
export function attachCommand(h: SessionHost): string {
  const socket = line(h.socket);
  const session = line(h.session);
  let attach = socket !== "" && !defaultSocket(socket) ? `tmux -S ${shellWord(socket)} attach` : "tmux attach";
  if (session !== "") attach += ` -t ${shellWord(session)}`;
  return attach;
}

/** A default tmux server's socket, the one a plain `tmux` reaches:
 * tmux-<uid>/default in tmux's directory (internal/backend). */
function defaultSocket(socket: string): boolean {
  const parts = socket.split("/");
  return parts.at(-1) === "default" && (parts.at(-2) ?? "").startsWith("tmux-");
}

/** s as one shell word: as it is when plain, else in single quotes. */
function shellWord(s: string): string {
  return s !== "" && /^[A-Za-z0-9_\-./:@%+=,]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`;
}
