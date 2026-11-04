// Where a project's run answers (SPEC §13): the project root as igris finds
// it (internal/state FindRoot) and the socket serve listens on
// (internal/serve SocketPath).

import { createHash } from "node:crypto";
import * as fs from "node:fs";
import * as path from "node:path";

/** The nearest directory from start up holding igris.toml or .igris/; inside
 * a run's worktree (<root>/.igris/worktrees/<name>) its <root>. undefined
 * when there is none. */
export function findRoot(start: string): string | undefined {
  let dir = path.resolve(start);
  const marker = `${path.sep}.igris${path.sep}worktrees${path.sep}`;
  const i = (dir + path.sep).lastIndexOf(marker);
  if (i >= 0) {
    const root = dir.slice(0, i) || path.sep;
    if (isDir(path.join(root, ".igris"))) return root;
  }
  for (;;) {
    if (fs.existsSync(path.join(dir, "igris.toml")) || fs.existsSync(path.join(dir, ".igris"))) return dir;
    const parent = path.dirname(dir);
    if (parent === dir) return undefined;
    dir = parent;
  }
}

function isDir(p: string): boolean {
  try {
    return fs.statSync(p).isDirectory();
  } catch {
    return false;
  }
}

/** The socket's file name outside .igris/: the first 16 hex characters of
 * the sha256 of the root. */
export function fallbackName(root: string): string {
  return createHash("sha256").update(root).digest("hex").slice(0, 16) + ".sock";
}

function tempDirName(): string {
  return `igris-${process.getuid?.() ?? 0}`;
}

/** Whether p is a fallback socket path for root: absolute and clean, named
 * after root's hash, in an igris/ or igris-<uid>/ directory. */
export function validFallback(root: string, p: string): boolean {
  if (!path.isAbsolute(p) || path.resolve(p) !== p || path.basename(p) !== fallbackName(root)) return false;
  const d = path.basename(path.dirname(p));
  return d === "igris" || d === tempDirName();
}

/** Reads a small regular file that a session may have replaced (SPEC §16):
 * never through a symlink, never blocking on a FIFO, at most max bytes. */
export function readSmall(file: string, max: number): string | undefined {
  let fd: number | undefined;
  try {
    fd = fs.openSync(file, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW | fs.constants.O_NONBLOCK);
    const st = fs.fstatSync(fd);
    if (!st.isFile() || st.size > max) return undefined;
    const buf = Buffer.alloc(max + 1);
    const n = fs.readSync(fd, buf, 0, max + 1, 0);
    return n > max ? undefined : buf.subarray(0, n).toString("utf8");
  } catch {
    return undefined;
  } finally {
    if (fd !== undefined) fs.closeSync(fd);
  }
}

/** The socket of the project at root as serve made it: the path
 * .igris/igris.sock.path names when it names the fallback for this root,
 * else .igris/igris.sock. Any other path in the file is ignored. */
export function socketPath(root: string): string {
  const named = readSmall(path.join(root, ".igris", "igris.sock.path"), 4 << 10)?.trim();
  if (named !== undefined && validFallback(root, named)) return named;
  return path.join(root, ".igris", "igris.sock");
}
