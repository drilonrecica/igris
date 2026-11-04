// CodeLens on the plan's task rows (SPEC §15.9), as plain data: which line
// of which plan file gets which lenses. No VS Code here, so the unit tests
// check exactly what is drawn; lensView.ts hands it to the editor. A row
// gets lenses only when igris itself knows its ID (the run view's tasks), so
// a table cell that merely looks like an ID gets nothing, and every word
// drawn from the plan passes textsafe.

import { taskGlyph } from "./look";
import type { Task } from "./protocol";
import { line } from "./textsafe";

/** internal/plan's ID syntax. */
const idPattern = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

export type LensKind = "status" | "run" | "changes";

export interface Lens {
  /** 0-based line of the row. */
  line: number;
  kind: LensKind;
  task: string;
  title: string;
}

/** The task a plan row is for: the first cell, with the emphasis and code
 * marks a plan may put round an ID taken off. undefined for any other line
 * (a header, a separator, text, a row inside a code fence). */
export function rowId(text: string): string | undefined {
  const t = text.trim();
  if (!t.startsWith("|")) return undefined;
  const cell = (t.slice(1).split("|")[0] ?? "").trim().replace(/^(\*\*|__|`)+|(\*\*|__|`)+$/g, "").trim();
  return idPattern.test(cell) ? cell : undefined;
}

/** What the lenses need to know of the run: whether one is live, since a
 * second `serve` can't start next to it. */
export interface LensEnv {
  tasks: Map<string, Task>;
  live: boolean;
}

/** The statuses a task has something to run for / something changed in. */
function runnable(status: string): boolean {
  const s = status.toLowerCase();
  return s === "ready" || s === "in progress";
}

function changed(status: string): boolean {
  const s = status.toLowerCase();
  return s === "done" || s === "in progress";
}

/** The lenses of a plan file's text. */
export function lensesOf(text: string, env: LensEnv): Lens[] {
  const out: Lens[] = [];
  let fence = false;
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i]!;
    if (/^\s*(```|~~~)/.test(l)) {
      fence = !fence;
      continue;
    }
    if (fence) continue;
    const id = rowId(l);
    const task = id === undefined ? undefined : env.tasks.get(id);
    if (!task || id === undefined) continue;
    const status = line(task.status);
    out.push({ line: i, kind: "status", task: id, title: `igris · ${taskGlyph(task.status)} ${status || "no status"}` });
    if (!env.live && runnable(task.status)) out.push({ line: i, kind: "run", task: id, title: "Run this task" });
    if (changed(task.status)) out.push({ line: i, kind: "changes", task: id, title: "Open its changes" });
  }
  return out;
}

/** The argv of the run a lens starts: `igris serve --only ID`. The mode is
 * the project's own; the extension never passes one, so never yolo (§7.3). */
export function serveArgs(task: string): string[] {
  return ["serve", "--only", task];
}
