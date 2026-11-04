import * as assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { contentAt, filesOf, gitIn, readLogged, sourcesOf } from "../../changes";

const RUN = "20261104-172159-6a62";

describe("a task's changes", () => {
  let root: string;
  const sh = (...args: string[]): string =>
    execFileSync("git", args, {
      cwd: root,
      encoding: "utf8",
      env: { ...process.env, GIT_AUTHOR_NAME: "t", GIT_AUTHOR_EMAIL: "t@example.com", GIT_COMMITTER_NAME: "t", GIT_COMMITTER_EMAIL: "t@example.com" },
    }).trim();
  const write = (f: string, s: string): void => {
    fs.mkdirSync(path.dirname(path.join(root, f)), { recursive: true });
    fs.writeFileSync(path.join(root, f), s);
  };
  const logLine = (o: Record<string, unknown>): string => JSON.stringify({ v: 1, at: "2026-11-04T10:00:00Z", run: RUN, ...o });
  let base: string;
  let t1: string;
  let branchTip: string;

  before(() => {
    root = fs.mkdtempSync(path.join(os.tmpdir(), "igris-changes-"));
    sh("init", "-q", "-b", "main");
    write("a.txt", "one\n");
    write("gone.txt", "bye\n");
    sh("add", ".");
    sh("commit", "-q", "-m", "base");
    base = sh("rev-parse", "HEAD");
    // T1 is committed on the run branch.
    write("a.txt", "one\ntwo\n");
    write("new.txt", "new\n");
    fs.rmSync(path.join(root, "gone.txt"));
    sh("add", "-A");
    sh("commit", "-q", "-m", "T1: change things");
    t1 = sh("rev-parse", "HEAD");
    // T2 lives on its worktree branch, not integrated.
    sh("branch", `igris/${RUN}-T2`, "HEAD");
    sh("checkout", "-q", `igris/${RUN}-T2`);
    write("b.txt", "bee\n");
    sh("add", ".");
    sh("commit", "-q", "-m", "T2: add b");
    branchTip = sh("rev-parse", "HEAD");
    sh("checkout", "-q", "main");
    write(".igris/runs.jsonl", [
      logLine({ type: "committed", task: "T1", commit: "f".repeat(40) }), // rewritten by a rebase: gone
      logLine({ type: "integrated", task: "T1", commit: t1, branch: "main" }),
      logLine({ type: "committed", task: "T1", commit: "../../etc" }),
      "not json at all",
      logLine({ type: "task_done", task: "T2" }),
      logLine({ type: "committed", task: "T10", commit: base }),
    ].join("\n") + "\n");
  });

  after(() => fs.rmSync(root, { recursive: true, force: true }));

  it("reads a task's commits and runs from the log, skipping what isn't an event or a SHA", () => {
    const l = readLogged(path.join(root, ".igris", "runs.jsonl"), "T1");
    assert.deepEqual(l.commits, [t1, "f".repeat(40)]);
    assert.deepEqual(l.runs, [RUN]);
    assert.deepEqual(readLogged(path.join(root, ".igris", "runs.jsonl"), "T10").commits, [base]);
    assert.deepEqual(readLogged(path.join(root, "missing.jsonl"), "T1"), { commits: [], runs: [] });
    assert.deepEqual(readLogged(path.join(root, ".igris"), "T1"), { commits: [], runs: [] });
    assert.deepEqual(readLogged(path.join(root, ".igris", "runs.jsonl"), "T1 --x"), { commits: [], runs: [] });
  });

  it("offers the commit git still has, and not the one a rebase rewrote", async () => {
    const s = await sourcesOf(root, gitIn(root), "T1");
    assert.equal(s.length, 1);
    assert.equal(s[0]!.kind, "commit");
    assert.equal(s[0]!.label, `${t1.slice(0, 8)} T1: change things`);
    assert.equal(s[0]!.right, t1);
    assert.equal(s[0]!.left, `${t1}^`);
  });

  it("offers the worktree branch, diffed from where it left the run branch", async () => {
    const s = await sourcesOf(root, gitIn(root), "T2");
    assert.equal(s.length, 1);
    assert.equal(s[0]!.kind, "branch");
    assert.equal(s[0]!.label, `igris/${RUN}-T2`);
    assert.equal(s[0]!.left, t1);
    assert.equal(sh("rev-parse", s[0]!.right), branchTip);
    assert.deepEqual(await filesOf(gitIn(root), s[0]!), [{ status: "A", path: "b.txt" }]);
  });

  it("finds the branch from the live run's ID when the log has no line for it yet", async () => {
    fs.rmSync(path.join(root, ".igris", "runs.jsonl"));
    const s = await sourcesOf(root, gitIn(root), "T2", [RUN]);
    assert.deepEqual(s.map((x) => x.kind), ["branch"]);
    assert.deepEqual(await sourcesOf(root, gitIn(root), "T2"), []);
  });

  it("lists a commit's files with git's status letters", async () => {
    write(".igris/runs.jsonl", logLine({ type: "integrated", task: "T1", commit: t1 }) + "\n");
    const [s] = await sourcesOf(root, gitIn(root), "T1");
    const files = await filesOf(gitIn(root), s!);
    assert.deepEqual(files, [
      { status: "M", path: "a.txt" },
      { status: "D", path: "gone.txt" },
      { status: "A", path: "new.txt" },
    ]);
    assert.equal(await contentAt(gitIn(root), s!.left, "a.txt"), "one\n");
    assert.equal(await contentAt(gitIn(root), s!.right, "a.txt"), "one\ntwo\n");
    // The missing side of an added or deleted file reads as empty.
    assert.equal(await contentAt(gitIn(root), s!.left, "new.txt"), "");
    assert.equal(await contentAt(gitIn(root), s!.right, "gone.txt"), "");
  });

  it("diffs a root commit against the empty tree", async () => {
    write(".igris/runs.jsonl", logLine({ type: "committed", task: "T0", commit: sh("rev-list", "--max-parents=0", "HEAD") }) + "\n");
    const [s] = await sourcesOf(root, gitIn(root), "T0");
    assert.ok(s);
    assert.deepEqual(await filesOf(gitIn(root), s), [
      { status: "A", path: "a.txt" },
      { status: "A", path: "gone.txt" },
    ]);
  });

  it("reads no ref that isn't a SHA or an igris branch", async () => {
    assert.equal(await contentAt(gitIn(root), "--output=/tmp/x", "a.txt"), "");
    assert.equal(await contentAt(gitIn(root), "HEAD", "a.txt"), "");
    assert.deepEqual(await sourcesOf(root, gitIn(root), "T1; rm -rf /"), []);
  });
});
