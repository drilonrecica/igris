import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { parseStatus } from "../../idle";
import { fallbackName, findRoot, readSmall, socketPath, validFallback } from "../../socket";

describe("the project and its socket", () => {
  let dir: string;
  beforeEach(() => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), "igris-root-"));
  });
  afterEach(() => fs.rmSync(dir, { recursive: true, force: true }));

  it("finds the root as igris does, from a worktree too", () => {
    fs.writeFileSync(path.join(dir, "igris.toml"), "");
    fs.mkdirSync(path.join(dir, "src", "deep"), { recursive: true });
    assert.equal(findRoot(path.join(dir, "src", "deep")), dir);
    const wt = path.join(dir, ".igris", "worktrees", "run-1", "src");
    fs.mkdirSync(wt, { recursive: true });
    fs.writeFileSync(path.join(dir, ".igris", "worktrees", "run-1", "igris.toml"), "");
    assert.equal(findRoot(wt), dir, "a worktree's own igris.toml doesn't make it a project");
  });

  it("names the fallback socket after the root's sha256, as serve does", () => {
    assert.equal(fallbackName("/home/u/p"), "60ad706fe5335804.sock");
  });

  it("uses .igris/igris.sock.path only when it names the fallback for this root", () => {
    fs.mkdirSync(path.join(dir, ".igris"));
    const pathFile = path.join(dir, ".igris", "igris.sock.path");
    assert.equal(socketPath(dir), path.join(dir, ".igris", "igris.sock"));

    const good = path.join("/run/user/1000/igris", fallbackName(dir));
    fs.writeFileSync(pathFile, good + "\n");
    assert.equal(socketPath(dir), good);

    for (const bad of ["/tmp/elsewhere.sock", path.join("/tmp/evil", fallbackName(dir)), "relative/igris/" + fallbackName(dir), `/run/user/1000/igris/../igris/${fallbackName(dir)}`]) {
      fs.writeFileSync(pathFile, bad);
      assert.equal(socketPath(dir), path.join(dir, ".igris", "igris.sock"), bad);
    }
    assert.ok(validFallback(dir, path.join(os.tmpdir(), `igris-${process.getuid!()}`, fallbackName(dir))));
  });

  it("never reads the path file through a symlink, nor a large one", () => {
    fs.mkdirSync(path.join(dir, ".igris"));
    const real = path.join(dir, "real");
    fs.writeFileSync(real, path.join("/run/user/1000/igris", fallbackName(dir)));
    fs.symlinkSync(real, path.join(dir, ".igris", "igris.sock.path"));
    assert.equal(socketPath(dir), path.join(dir, ".igris", "igris.sock"));
    fs.writeFileSync(real, "x".repeat(5000));
    assert.equal(readSmall(real, 4096), undefined);
  });
});

describe("igris status --json", () => {
  it("refuses what it can't read", () => {
    assert.deepEqual(parseStatus("not json"), { ok: false, line: "igris status --json printed something that isn't JSON" });
    assert.equal(parseStatus('{"v":2,"phases":[]}').ok, false);
  });

  it("ignores fields it doesn't know and entries of the wrong shape", () => {
    const res = parseStatus('{"v":1,"plan":"tasks.md","future":true,"phases":[{"id":"A","title":"x","tasks":[{"id":"A-01","status":"ready"},{"title":"no id"},7]},"junk"]}');
    assert.ok(res.ok);
    assert.equal(res.idle.phases.length, 1);
    assert.deepEqual(
      res.idle.phases[0]!.tasks.map((t) => t.id),
      ["A-01"],
    );
    assert.deepEqual(res.idle.files, ["tasks.md"], "the plan when files is absent (before 1.2)");
  });
});
