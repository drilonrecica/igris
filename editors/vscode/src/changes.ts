// A task's changes (SPEC §15.9): which commits and which branch hold them,
// and the files each changed, read with `git` in the project. No VS Code
// here, so the unit tests run it against a real scratch repository.
//
// Commits come from the run log's `committed` and `integrated` events
// (`commit`, SPEC §13), newest first; one that git no longer has (a
// parallel phase's commit before the rebase rewrote it) is dropped. The
// branch is `igris/<run-id>-<ID>` (§6.11) while it exists. Everything that
// reaches git is argv, never a shell, and an ID, run ID or SHA that is not
// of the shape igris writes is not used at all.

import { execFile } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";
import { line } from "./textsafe";

/** Runs git in the project; the output, or a rejection with its message. */
export type Git = (args: string[]) => Promise<string>;

/** One thing to read the changes of: a commit or a branch. */
export interface Source {
  kind: "commit" | "branch";
  /** What the picker shows. */
  label: string;
  description: string;
  /** The two sides of the diff, as refs git resolves; left is "" for a
   * root commit. */
  left: string;
  right: string;
}

export interface FileChange {
  /** A, M, D, R, C or T (git's letters). */
  status: string;
  path: string;
  /** The path on the left side of a rename or copy. */
  oldPath?: string;
}

/** The most the log reader takes, from its end (SPEC §13). */
const MAX_LOG = 16 << 20;

const idShape = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;
const shaShape = /^[0-9a-f]{40}([0-9a-f]{24})?$/;

export interface Logged {
  /** The task's commit SHAs, newest first. */
  commits: string[];
  /** The runs the task appears in, newest first. */
  runs: string[];
}

/** Reads what the run log (`runs.jsonl`) says of task: its commits and its
 * runs. Reads only a regular file and only its last 16 MiB; a line that
 * isn't an event is skipped. */
export function readLogged(file: string, task: string): Logged {
  const out: Logged = { commits: [], runs: [] };
  if (!idShape.test(task)) return out;
  let text: string;
  try {
    const st = fs.lstatSync(file);
    if (!st.isFile()) return out;
    const fd = fs.openSync(file, "r");
    try {
      const n = Math.min(st.size, MAX_LOG);
      const buf = Buffer.alloc(n);
      let got = 0;
      while (got < n) {
        const r = fs.readSync(fd, buf, got, n - got, st.size - n + got);
        if (r <= 0) break;
        got += r;
      }
      text = buf.subarray(0, got).toString("utf8");
      // A read from the middle starts inside a line: from the first whole one.
      if (st.size > MAX_LOG) text = text.slice(text.indexOf("\n") + 1);
    } finally {
      fs.closeSync(fd);
    }
  } catch {
    return out;
  }
  const lines = text.split("\n");
  for (let i = lines.length - 1; i >= 0; i--) {
    const l = lines[i]!;
    if (l === "" || !l.includes(`"${task}"`)) continue;
    let ev: unknown;
    try {
      ev = JSON.parse(l);
    } catch {
      continue;
    }
    if (typeof ev !== "object" || ev === null) continue;
    const o = ev as Record<string, unknown>;
    if (o["task"] !== task) continue;
    const run = o["run"];
    if (typeof run === "string" && idShape.test(run) && !out.runs.includes(run)) out.runs.push(run);
    const commit = o["commit"];
    if ((o["type"] === "committed" || o["type"] === "integrated") && typeof commit === "string" && shaShape.test(commit) && !out.commits.includes(commit)) {
      out.commits.push(commit);
    }
  }
  return out;
}

/** git in root, as argv with a timeout; no shell. */
export function gitIn(root: string, env: NodeJS.ProcessEnv = process.env): Git {
  return (args) =>
    new Promise((resolve, reject) => {
      execFile("git", args, { cwd: root, env, timeout: 30_000, maxBuffer: 64 << 20, windowsHide: true }, (err, stdout, stderr) => {
        if (err) reject(new Error(line(String(stderr).trim().split("\n")[0] || err.message)));
        else resolve(stdout);
      });
    });
}

async function ok(git: Git, args: string[]): Promise<boolean> {
  try {
    await git(args);
    return true;
  } catch {
    return false;
  }
}

/** What task changed: its commits and its branch, newest first and the
 * branch last. `runIds` are runs known to the caller (the live one); the
 * log adds the rest. */
export async function sourcesOf(root: string, git: Git, task: string, runIds: string[] = []): Promise<Source[]> {
  if (!idShape.test(task)) return [];
  const logged = readLogged(path.join(root, ".igris", "runs.jsonl"), task);
  const out: Source[] = [];
  for (const sha of logged.commits) {
    // A merge or a root commit has no single parent to compare with.
    if (!(await ok(git, ["cat-file", "-e", `${sha}^{commit}`]))) continue;
    let subject = "";
    try {
      subject = line((await git(["show", "-s", "--format=%s", sha, "--"])).trim());
    } catch {
      continue;
    }
    const hasParent = await ok(git, ["rev-parse", "--verify", "--quiet", `${sha}^`]);
    out.push({
      kind: "commit",
      label: `${sha.slice(0, 8)} ${subject}`,
      description: "commit",
      left: hasParent ? `${sha}^` : "",
      right: sha,
    });
  }
  const runs = [...runIds.filter((r) => idShape.test(r)), ...logged.runs];
  const seen = new Set<string>();
  for (const run of runs) {
    const branch = `igris/${run}-${task}`;
    if (seen.has(branch)) continue;
    seen.add(branch);
    if (!(await ok(git, ["rev-parse", "--verify", "--quiet", `refs/heads/${branch}^{commit}`]))) continue;
    let base: string;
    try {
      base = (await git(["merge-base", "HEAD", `refs/heads/${branch}`])).trim();
    } catch {
      continue;
    }
    if (!shaShape.test(base)) continue;
    out.push({ kind: "branch", label: branch, description: "worktree branch", left: base, right: `refs/heads/${branch}` });
  }
  return out;
}

/** The files a source changed. Renames and copies are detected; paths come
 * as git writes them with -z, cleaned before they are shown. */
export async function filesOf(git: Git, s: Source): Promise<FileChange[]> {
  // An empty left side is a root commit: everything in it is new.
  const raw =
    s.left === ""
      ? await git(["diff-tree", "-r", "--root", "--no-commit-id", "--name-status", "-z", "--no-ext-diff", s.right, "--"])
      : await git(["diff", "--name-status", "-z", "-M", "-C", "--no-ext-diff", "--no-color", s.left, s.right, "--"]);
  const parts = raw.split("\0");
  const out: FileChange[] = [];
  for (let i = 0; i < parts.length; ) {
    const st = parts[i++] ?? "";
    if (st === "") continue;
    const letter = st[0]!;
    if (letter === "R" || letter === "C") {
      const from = parts[i++] ?? "";
      const to = parts[i++] ?? "";
      if (to !== "") out.push({ status: letter, path: to, oldPath: from });
    } else {
      const p = parts[i++] ?? "";
      if (p !== "") out.push({ status: letter, path: p });
    }
  }
  return out;
}

/** The content of file at ref, "" when it isn't there (an added file's
 * left side, a deleted file's right, a root commit's left side ""). */
export async function contentAt(git: Git, ref: string, file: string): Promise<string> {
  if (!/^[0-9a-f]{40,64}\^?$|^refs\/heads\/igris\/[A-Za-z0-9._/-]+$/.test(ref)) return "";
  try {
    return await git(["show", `${ref}:${file}`]);
  } catch {
    return "";
  }
}
