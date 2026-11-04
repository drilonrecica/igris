// What the run view draws (SPEC §15.9), as plain data: the sidebar tree and
// the status bar item, from the live run or, without one, from `igris status
// --json`. No VS Code here, so the unit tests check exactly what is drawn;
// tree.ts and statusBar.ts hand it to the editor. Every text that came from
// the plan, a session or igris passes textsafe on its way out.

import type { Idle } from "./idle";
import { cardNeeds, Glyph, idleStatus, label, liveStatus, stateText, taskGlyph, type Status, type Tone } from "./look";
import type { Phase, Task } from "./protocol";
import { State, type Run } from "./run";
import { line, short } from "./textsafe";

/** Where the view is (the controller's state). */
export type Mode =
  /** Activation, before the first answer. */
  | "starting"
  /** Attached to a run's serve. */
  | "live"
  /** The connection dropped; the last run shown while it reconnects. */
  | "reconnecting"
  /** No run answers: the plan from `igris status --json`. */
  | "idle"
  /** A refusal holds (SPEC §15.9): nothing else runs. */
  | "refused";

export interface ViewState {
  mode: Mode;
  /** The refusal's one line, in mode refused. */
  refusal?: string;
  run?: Run;
  idle?: Idle;
  /** Why idle has nothing: `igris status` failed (an invalid plan, …). */
  idleError?: string;
  /** The outcome of the live run this window saw last, once it ended. */
  ended?: string;
}

/** The icons the tree uses; tree.ts maps them to the editor's. */
export type Icon = "done" | "running" | "needs" | "ready" | "blocked" | "skipped" | "warn" | "fail" | "waiting" | "phase";

export interface Node {
  /** Stable across updates: the editor keeps expansion and selection. */
  id: string;
  label: string;
  description: string;
  tooltip: string;
  icon: Icon;
  /** Waits on the owner: drawn red, the status bar's click goes to it. */
  needs: boolean;
  /** undefined: a leaf. */
  expanded?: boolean;
  children: Node[];
  /** The kind of row, for menus: run, active, question, phase, task. */
  kind: "run" | "active" | "question" | "phase" | "task";
  task?: string;
}

/** The status the status bar shows; undefined while a refusal holds. */
export function statusOf(v: ViewState, now: number): Status | undefined {
  switch (v.mode) {
    case "refused":
      return undefined;
    case "starting":
      return { word: "Idle", glyph: "", line: "connecting…", tone: "dim" };
    case "reconnecting": {
      const s = v.run ? liveStatus(v.run, now) : undefined;
      return { word: s?.word ?? "Working", glyph: s?.glyph ?? "", line: `${Glyph.warn} reconnecting…`, tone: "warn" };
    }
    case "live":
      return liveStatus(v.run!, now);
  }
  if (v.idleError !== undefined) return { word: "Idle", glyph: "", line: short(v.idleError, 120), tone: "dim" };
  return idleStatus(v.idle?.run, v.ended);
}

export interface Bar {
  text: string;
  tooltip: string;
  tone: Tone;
}

/** The status bar item: the status word and what waits on the owner. */
export function barOf(v: ViewState, now: number): Bar | undefined {
  const s = statusOf(v, now);
  if (!s) return undefined;
  const quiet = s.word === "Working" && s.tone === "accent";
  const text = quiet ? `igris ${label(s)}` : `igris ${label(s)} · ${s.line}`;
  const tooltip = `${label(s)} · ${s.line}\n${s.tone === "needs" ? "Click to see what needs you" : "Click to open the run view"}`;
  return { text, tooltip, tone: s.tone };
}

function toneIcon(t: Tone): Icon {
  switch (t) {
    case "needs":
      return "needs";
    case "accent":
      return "running";
    case "warn":
      return "warn";
  }
  return "phase";
}

function words(...parts: (string | false | undefined)[]): string {
  return parts.filter((p): p is string => typeof p === "string" && p !== "").join(" · ");
}

function statusIcon(status: string): Icon {
  switch (taskGlyph(status)) {
    case Glyph.done:
      return "done";
    case Glyph.running:
      return "running";
    case Glyph.ready:
      return "ready";
    case Glyph.blocked:
      return "blocked";
  }
  return "skipped";
}

function title(id: string, t: string): string {
  return short(t ? `${id} ${t}` : id, 120);
}

/** The tree: the live run on top, then the plan's phases and tasks. */
export function treeOf(v: ViewState, now: number): Node[] {
  if (v.mode === "refused" || v.mode === "starting") return [];
  const run = v.mode === "idle" ? undefined : v.run;
  const out: Node[] = [];
  if (run) out.push(runNode(v, run, now));
  const phases = run ? run.phases : (v.idle?.phases ?? []);
  const expand = run && !run.ended ? run.phase : phases.find((p) => p.tasks.some((t) => open(t.status)))?.id;
  for (const ph of phases) out.push(phaseNode(ph, run, ph.id === expand));
  return out;
}

/** What the tree's place says instead of rows, if anything. */
export function messageOf(v: ViewState): string | undefined {
  switch (v.mode) {
    case "refused":
      return v.refusal;
    case "starting":
      return "Connecting to igris…";
  }
  if (v.mode === "idle" && v.idleError !== undefined) return short(v.idleError, 300);
  const phases = v.mode === "idle" ? v.idle?.phases : v.run?.phases;
  if (!phases || phases.length === 0) {
    if (v.run?.planError) return short(v.run.planError, 300);
    return "The plan has no phases yet.";
  }
  return undefined;
}

function open(status: string): boolean {
  const s = status.toLowerCase();
  return s !== "done" && s !== "skipped";
}

/** The tasks of the run that wait on the owner and the run's questions. */
function needsOf(run: Run): Set<string> {
  const ids = new Set<string>();
  for (const c of run.active) if (cardNeeds(c.state) || c.state === State.question) ids.add(c.task);
  for (const q of run.questions) ids.add(q.task);
  if (run.reset) ids.add(run.reset.task);
  return ids;
}

function runNode(v: ViewState, run: Run, now: number): Node {
  const s = statusOf(v, now)!;
  const needs = needsOf(run);
  const children: Node[] = [];
  for (const c of run.active) {
    const n = needs.has(c.task);
    const state = run.questions.some((q) => q.task === c.task) ? State.question : c.state;
    children.push({
      id: `run/active/${c.task}`,
      label: title(c.task, line(c.title)),
      description: words(stateText(state), c.resolver && `resolver ${line(c.resolver)}`, c.user ? "your task" : c.rank && `rank ${line(c.rank)}`, c.mode && `mode ${line(c.mode)}`),
      tooltip: words(title(c.task, line(c.title)), short(c.detail, 400)),
      icon: n ? "needs" : c.state === State.waiting || c.state === State.limited ? "waiting" : c.state === State.lost ? "warn" : "running",
      needs: n,
      children: [],
      kind: "active",
      task: c.task,
    });
  }
  const ask = (id: string, label: string, detail: string, task?: string): void => {
    children.push({ id, label, description: short(detail, 200), tooltip: short(detail, 600), icon: "needs", needs: true, children: [], kind: "question", ...(task ? { task } : {}) });
  };
  for (const q of run.questions) {
    if (!run.active.some((c) => c.task === q.task)) ask(`run/question/${q.task}`, `${line(q.task)} asks you`, q.detail, q.task);
  }
  if (run.reset) ask("run/reset", `Reset ${line(run.reset.task)}?`, run.reset.detail, run.reset.task);
  if (run.budgetQuestion) ask("run/budget", "Budget reached", run.budgetQuestion.detail);
  if (run.batchQuestion) ask("run/batch", "Keep this run's commits?", run.batchQuestion.detail);
  return {
    id: "run",
    label: label(s),
    description: words(s.line, run.phase && `phase ${line(run.phase)}`, run.mode && `mode ${line(run.mode)}`),
    tooltip: words(label(s), s.line, run.run && `run ${line(run.run)}`),
    icon: toneIcon(s.tone),
    needs: s.tone === "needs",
    expanded: true,
    children,
    kind: "run",
  };
}

function phaseNode(ph: Phase, run: Run | undefined, expanded: boolean): Node {
  const total = ph.tasks.length;
  const finished = ph.tasks.filter((t) => !open(t.status)).length;
  const current = run !== undefined && !run.ended && run.phase === ph.id;
  const needs = run ? needsOf(run) : new Set<string>();
  const children = ph.tasks.map((t) => taskNode(t, run, needs));
  return {
    id: `phase/${ph.id}`,
    label: title(line(ph.id), line(ph.title)),
    description: words(`${finished}/${total} done`, ph.parallel && "parallel"),
    tooltip: words(title(line(ph.id), line(ph.title)), `${finished}/${total} done`),
    icon: current ? "running" : total > 0 && finished === total ? "done" : "phase",
    needs: children.some((c) => c.needs),
    expanded,
    children,
    kind: "phase",
  };
}

function taskNode(t: Task, run: Run | undefined, needs: Set<string>): Node {
  const card = run?.active.find((c) => c.task === t.id);
  const n = needs.has(t.id);
  const mode = card?.mode || run?.overrides[t.id] || t.mode;
  const status = n ? "needs you" : card ? stateText(card.state) : t.status.toLowerCase();
  const owner = t.owner === "user" ? "your task" : t.rank && `rank ${line(t.rank)}`;
  return {
    id: `task/${t.id}`,
    label: title(line(t.id), line(t.title)),
    description: words(line(status), owner, mode && `mode ${line(mode)}`),
    tooltip: words(title(line(t.id), line(t.title)), line(t.status), owner, mode && `mode ${line(mode)}`),
    icon: n ? "needs" : card ? "running" : statusIcon(t.status),
    needs: n,
    children: [],
    kind: "task",
    task: t.id,
  };
}

/** The first row that waits on the owner, depth first: where the status
 * bar's click goes. */
export function firstNeeds(nodes: Node[]): Node | undefined {
  for (const n of nodes) {
    if (n.needs && n.children.length === 0) return n;
    const inner = firstNeeds(n.children);
    if (inner) return inner;
    if (n.needs) return n;
  }
  return undefined;
}
