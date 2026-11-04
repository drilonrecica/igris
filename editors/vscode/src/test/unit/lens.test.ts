import * as assert from "node:assert/strict";
import { lensesOf, rowId, serveArgs } from "../../lens";
import type { Task } from "../../protocol";

const task = (id: string, status: string): Task => ({ id, title: "", status, owner: "agent", rank: "sonnet", mode: "" });
const env = (live: boolean, ...ts: Task[]) => ({ live, tasks: new Map(ts.map((t) => [t.id, t])) });

const plan = [
  "# Plan",
  "| ID | Task | Status |",
  "|---|---|---|",
  "| V1-01 | **A** | done |",
  "| **V1-02** | B | ready |",
  "| `V1-03` | C | blocked |",
  "```",
  "| V1-02 | in a fence | ready |",
  "```",
  "| X-9 | not igris's | ready |",
].join("\n");

describe("plan CodeLens", () => {
  it("reads the ID of a row", () => {
    assert.equal(rowId("| V1-01 | a | done |"), "V1-01");
    assert.equal(rowId("| **V1-02** | a |"), "V1-02");
    assert.equal(rowId("| `V1-03` | a |"), "V1-03");
    assert.equal(rowId("|---|---|"), undefined);
    assert.equal(rowId("| ID | Task |"), "ID"); // lensesOf asks igris whether it is a task
    assert.equal(rowId("V1-01 outside a table"), undefined);
  });

  it("draws status, run and changes by status, only for known tasks, outside fences", () => {
    const ls = lensesOf(plan, env(false, task("V1-01", "done"), task("V1-02", "ready"), task("V1-03", "blocked")));
    assert.deepEqual(
      ls.map((l) => `${l.line}:${l.kind}:${l.task}`),
      ["3:status:V1-01", "3:changes:V1-01", "4:status:V1-02", "4:run:V1-02", "5:status:V1-03"],
    );
  });

  it("offers no run while one is live", () => {
    const ls = lensesOf(plan, env(true, task("V1-02", "ready")));
    assert.deepEqual(ls.map((l) => l.kind), ["status"]);
  });

  it("cleans the status it draws of escape sequences", () => {
    const ls = lensesOf("| V1-01 | a | x |", env(false, task("V1-01", "re\u001b[31mady‮")));
    assert.match(ls[0]!.title, /ready$/);
    assert.doesNotMatch(ls[0]!.title, /\u001b|‮/);
  });

  it("starts serve with --only and nothing about the mode", () => {
    assert.deepEqual(serveArgs("V1-02"), ["serve", "--only", "V1-02"]);
  });
});
