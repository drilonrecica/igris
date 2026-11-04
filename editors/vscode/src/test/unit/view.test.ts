import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { parseStatus } from "../../idle";
import { idleStatus, liveStatus, minutes, needYou, RESUME_GRACE } from "../../look";
import { apply } from "../../run";
import { barOf, firstNeeds, messageOf, statusOf, treeOf, type Node, type ViewState } from "../../view";
import { ev, recorded, testdata } from "../support/transcripts";

const now = Date.parse("2026-11-04T12:10:00Z");

function live(name: string): ViewState {
  return { mode: "live", run: recorded(name).snapshot };
}

/** A row as one line: label — description. */
function rows(nodes: Node[], depth = 0): string[] {
  return nodes.flatMap((n) => [`${"  ".repeat(depth)}${n.label} — ${n.description}${n.needs ? " [needs]" : ""}`, ...rows(n.children, depth + 1)]);
}

describe("the run view", () => {
  it("draws a working run: the run on top, its phase open", () => {
    const v = live("working.jsonl");
    const tree = treeOf(v, now);
    assert.deepEqual(rows(tree), [
      "● Working — nothing needs you · phase S1 · mode manual",
      "  S1-01 First task — working · rank sonnet · mode manual",
      "S1 Scratch phase — 0/2 done",
      "  S1-01 First task — working · rank sonnet · mode manual",
      "  S1-02 Second task — ready · rank opus",
    ]);
    assert.equal(tree[1]!.expanded, true);
    assert.deepEqual(barOf(v, now), { text: "igris ● Working", tooltip: "● Working · nothing needs you\nClick to open the run view", tone: "accent" });
    assert.equal(messageOf(v), undefined);
  });

  it("names a task and the run that need the owner, red, and goes to the first", () => {
    const v = live("parallel.jsonl");
    const tree = treeOf(v, now);
    assert.deepEqual(rows(tree), [
      "! Needs you — 2 questions · phase P1 · mode auto [needs]",
      "  P1-01 Left — asks you · rank sonnet · mode auto [needs]",
      "  P1-02 Right side — working · rank opus · mode auto",
      "  Budget reached — The tokens budget of 2M is reached. [needs]",
      "P1 Parallel phase — 0/4 done · parallel [needs]",
      "  P1-01 Left — needs you · rank sonnet · mode auto [needs]",
      "  P1-02 Right side — working · rank opus · mode auto",
      "  P1-03 Join — blocked · rank sonnet · mode plan",
      "  P1-04 Check by hand — blocked · your task",
    ]);
    const bar = barOf(v, now)!;
    assert.equal(bar.text, "igris ! Needs you · 2 questions");
    assert.equal(bar.tone, "needs");
    assert.equal(firstNeeds(tree)?.id, "run/active/P1-01");
  });

  it("cleans the plan's text before drawing it", () => {
    const tree = treeOf(live("parallel.jsonl"), now);
    const all = JSON.stringify(tree);
    assert.ok(!all.includes("\\u001b") && !all.includes("\u202e"), all);
  });

  it("follows the events", () => {
    const v = live("dropped.jsonl");
    for (const e of recorded("dropped.jsonl").events) apply(v.run!, e);
    assert.deepEqual(rows(treeOf(v, now)), [
      "● Working — nothing running yet · phase S1 · mode auto",
      "S1 Scratch phase — 1/2 done",
      "  S1-01 First task — done · rank sonnet",
      "  S1-02 Second task — ready · rank opus · mode plan",
    ]);
    apply(v.run!, ev({ type: "run_stopped", detail: "completed" }));
    assert.equal(barOf(v, now)!.text, "igris Done · ✓ completed");
  });

  it("marks reconnecting in Ember and keeps the last run", () => {
    const v: ViewState = { ...live("working.jsonl"), mode: "reconnecting" };
    assert.deepEqual(barOf(v, now), { text: "igris ● Working · ▲ reconnecting…", tooltip: "● Working · ▲ reconnecting…\nClick to open the run view", tone: "warn" });
    assert.equal(treeOf(v, now)[0]!.id, "run");
  });

  it("puts a refusal in the tree's place and hides the status bar", () => {
    const v: ViewState = { mode: "refused", refusal: "This run's igris serve speaks protocol 2; this Igris extension speaks protocol 1. Update the extension." };
    assert.equal(barOf(v, now), undefined);
    assert.deepEqual(treeOf(v, now), []);
    assert.equal(messageOf(v), v.refusal);
  });

  it("draws the plan from igris status --json without a run", () => {
    const res = parseStatus(fs.readFileSync(path.join(testdata, "status.json"), "utf8"));
    assert.ok(res.ok);
    const v: ViewState = { mode: "idle", idle: res.idle };
    const tree = treeOf(v, now);
    assert.deepEqual(rows(tree), [
      "S1 Scratch phase — 1/2 done",
      "  S1-01 First task — done · rank sonnet",
      "  S1-02 Second task — ready · rank opus · mode plan",
      "S2 Later phase — 0/1 done",
      "  S2-01 Third task — blocked · rank sonnet",
    ]);
    assert.equal(tree[0]!.expanded, true, "the first phase with work is open");
    assert.equal(tree[1]!.expanded, false);
    assert.equal(barOf(v, now)!.text, "igris Idle · no run");
    assert.deepEqual(res.idle.files, ["tasks.md"]);
  });

  it("says why there is no plan", () => {
    const v: ViewState = { mode: "idle", idleError: "tasks.md:12: S1-02 depends on S9-01, which doesn't exist; fix the Depends cell" };
    assert.equal(messageOf(v), v.idleError);
    assert.equal(statusOf(v, now)!.word, "Idle");
  });
});

describe("the status word", () => {
  it("counts tasks and names the run (SPEC §15.4)", () => {
    assert.equal(needYou(1, false), "1 task needs you");
    assert.equal(needYou(2, false), "2 tasks need you");
    assert.equal(needYou(1, true), "1 task and the run need you");
    assert.equal(needYou(0, true), "the run needs you");
    assert.equal(needYou(0, false), "");
  });

  it("is the web UI's for a live run", () => {
    const run = recorded("working.jsonl").snapshot;
    assert.deepEqual(liveStatus(run, now), { word: "Working", glyph: "●", line: "nothing needs you", tone: "accent" });
    run.paused = true;
    assert.equal(liveStatus(run, now).line, "pauses after S1-01");
    run.holding = true;
    assert.deepEqual(liveStatus(run, now), { word: "Paused", glyph: "", line: "holding", tone: "plain" });
    run.questions = [ev({ type: "asked", task: "S1-01", question: "commit" })];
    assert.deepEqual(liveStatus(run, now), { word: "Needs you", glyph: "!", line: "1 question", tone: "needs" });
    run.questions = [];
    run.budgetQuestion = ev({ type: "asked", question: "budget" });
    assert.equal(liveStatus(run, now).line, "1 question");
    run.active[0]!.state = "lost";
    assert.equal(liveStatus(run, now).line, "1 task and the run need you");
  });

  it("shows when igris resumes a usage hold, with the countdown", () => {
    const run = recorded("working.jsonl").snapshot;
    const reset = now + 3 * 3600_000 + 11 * 60_000;
    run.limited = { window: "five_hour", resetsAt: reset, percent: 96.4, task: "S1-02", detail: "" };
    const s = liveStatus(run, now);
    assert.equal(s.word, "Paused");
    assert.match(s.line, /^5h limit 96% · resumes \d\d:\d\d \(≈ 3h12m\)$/);
    assert.equal(minutes(reset + RESUME_GRACE - now), "3h12m");
  });

  it("reads a run without a socket from the lock", () => {
    assert.deepEqual(idleStatus({ task: "S1-02", lock: "running here", lockDetail: "pid 42" }, undefined), { word: "Working", glyph: "●", line: "pid 42", tone: "accent" });
    assert.deepEqual(idleStatus({ task: "S1-02", lock: "stale", lockDetail: "" }, undefined), { word: "Interrupted", glyph: "▲", line: "during S1-02", tone: "warn" });
    assert.deepEqual(idleStatus({ task: "", lock: "none", lockDetail: "" }, "interrupted"), { word: "Done", glyph: "", line: "▲ interrupted", tone: "warn" });
    assert.deepEqual(idleStatus({ task: "S1-02", lock: "none", lockDetail: "" }, undefined), { word: "Idle", glyph: "", line: "S1-02 to resume", tone: "dim" });
    assert.deepEqual(idleStatus(undefined, undefined), { word: "Idle", glyph: "", line: "no run", tone: "dim" });
  });
});
