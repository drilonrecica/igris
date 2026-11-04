// The run view without a live run (SPEC §15.9): the plan's phases and tasks
// and the last run's state from `igris status --json` (status.schema.json),
// which holds everything of `igris phases --json` the tree draws. Run as
// argv in the project root, never through a shell, with a timeout.

import { execFile } from "node:child_process";
import type { StatusRun } from "./look";
import { obj, parsePhase, type Phase } from "./protocol";
import { short } from "./textsafe";

export interface Idle {
  phases: Phase[];
  run: StatusRun | undefined;
  /** The plan's files relative to the root: the main plan first. */
  files: string[];
}

export type IdleResult = { ok: true; idle: Idle } | { ok: false; line: string };

/** Reads `igris status --json`'s output. */
export function parseStatus(out: string): IdleResult {
  let doc: unknown;
  try {
    doc = JSON.parse(out);
  } catch {
    return { ok: false, line: "igris status --json printed something that isn't JSON" };
  }
  const o = obj(doc);
  if (!o || o["v"] !== 1) return { ok: false, line: "igris status --json printed a document this extension doesn't know" };
  const phases = (Array.isArray(o["phases"]) ? o["phases"] : []).map(parsePhase).filter((p): p is Phase => p !== undefined);
  const r = obj(o["run"]);
  const run: StatusRun | undefined = r
    ? {
        task: typeof r["task"] === "string" ? r["task"] : "",
        lock: typeof r["lock"] === "string" ? r["lock"] : "",
        lockDetail: typeof r["lock_detail"] === "string" ? r["lock_detail"] : "",
      }
    : undefined;
  let files = Array.isArray(o["files"]) ? o["files"].filter((f): f is string => typeof f === "string") : [];
  if (files.length === 0 && typeof o["plan"] === "string") files = [o["plan"]];
  return { ok: true, idle: { phases, run, files } };
}

/** Runs `igris status --json` in root. A failure comes back as the line
 * igris printed (its errors say what to do next), cleaned and cut. */
export function readStatus(root: string, env: NodeJS.ProcessEnv = process.env): Promise<IdleResult> {
  return new Promise((resolve) => {
    execFile(
      "igris",
      ["status", "--json"],
      { cwd: root, env, timeout: 15_000, maxBuffer: 64 << 20, windowsHide: true },
      (err, stdout, stderr) => {
        if (!err) {
          resolve(parseStatus(stdout));
          return;
        }
        const said = String(stderr).trim().split("\n").filter(Boolean).pop() ?? "";
        resolve({ ok: false, line: short(said || `igris status failed: ${err.message}`, 300) });
      },
    );
  });
}
