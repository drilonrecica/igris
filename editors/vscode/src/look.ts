// The parts of internal/look the run view draws (SPEC §15.4): the glyphs,
// the seven status words, counts, durations and the usage hold's line, and
// the status word of a run, as the web UI (internal/serve statusOf) and
// `igris status` (cmd/igris stateLine) choose it.

import type { Run } from "./run";
import { State } from "./run";

export const Glyph = {
  done: "✓",
  running: "●",
  needsYou: "!",
  ready: "·",
  blocked: "⨯",
  skipped: "–",
  warn: "▲",
  fail: "✗",
  waiting: "◌",
} as const;

export type Word = "Working" | "Needs you" | "Paused" | "Stopping" | "Done" | "Idle" | "Interrupted";

/** How loud a status is drawn: red is for Needs you only, Ember for a
 * warning or a failure (SPEC §15.4). */
export type Tone = "needs" | "accent" | "warn" | "plain" | "dim";

/** The status word and what follows it. */
export interface Status {
  word: Word;
  /** The glyph drawn before the word ("" for none). */
  glyph: string;
  /** What follows the word: what waits on the owner, the outcome, … */
  line: string;
  tone: Tone;
}

/** The glyph of a plan status; – for one igris doesn't know. */
export function taskGlyph(status: string): string {
  switch (status.toLowerCase()) {
    case "done":
      return Glyph.done;
    case "in progress":
      return Glyph.running;
    case "ready":
      return Glyph.ready;
    case "blocked":
      return Glyph.blocked;
    case "skipped":
      return Glyph.skipped;
  }
  return Glyph.skipped;
}

export function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}

/** n and its noun: "1 task", "3 questions". */
export function count(n: number, one: string, many: string): string {
  return `${n} ${plural(n, one, many)}`;
}

/** What waits on the owner, counting tasks only (SPEC §15.4). */
export function needYou(tasks: number, run: boolean): string {
  if (tasks > 0 && run) return `${count(tasks, "task", "tasks")} and the run need you`;
  if (tasks > 0) return `${count(tasks, "task needs", "tasks need")} you`;
  if (run) return "the run needs you";
  return "";
}

const MINUTE = 60_000;

/** An estimate in minutes, at least 1m: 14m, 1h05m. */
export function minutes(ms: number): string {
  const n = Math.max(Math.round(ms / MINUTE), 1);
  return n < 60 ? `${n}m` : `${Math.floor(n / 60)}h${String(n % 60).padStart(2, "0")}m`;
}

/** A time of day, local: 09:00. */
export function clock(t: number): string {
  const d = new Date(t);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** A reset as the screens show it: "09:00 (≈ 3h12m)" while ahead. */
export function until(t: number, now: number): string {
  const s = clock(t);
  return t > now ? `${s} (≈ ${minutes(t - now)})` : s;
}

/** The grace after a window's reset before igris resumes (V18-P1). */
export const RESUME_GRACE = 60_000;

/** A quota window's short label; "outside hours" for the working hours. */
export function windowLabel(w: string): string {
  switch (w) {
    case "hours":
      return "outside hours";
    case "five_hour":
      return "5h";
    case "seven_day":
      return "7d";
    case "spend_limit":
      return "spend";
  }
  return "limit";
}

/** A card's state in words (the web UI's stateText). */
export function stateText(s: string): string {
  switch (s) {
    case State.working:
      return "working";
    case State.yourTurn:
      return "your turn";
    case State.waiting:
      return "waiting on background work";
    case State.limited:
      return "usage limit";
    case State.needsYou:
      return "needs you";
    case State.verifying:
      return "verifying";
    case State.lost:
      return "session lost";
    case State.question:
      return "asks you";
  }
  return s;
}

/** A card that waits on the owner without a question pending. */
export function cardNeeds(state: string): boolean {
  return state === State.yourTurn || state === State.needsYou || state === State.lost;
}

/** The outcome after Done, from run_stopped's detail. */
export function outcome(end: string): { glyph: string; word: string; tone: Tone } {
  switch (end) {
    case "completed":
      return { glyph: Glyph.done, word: "completed", tone: "accent" };
    case "stuck":
      return { glyph: Glyph.warn, word: "stuck", tone: "warn" };
    case "failed":
    case "error":
      return { glyph: Glyph.fail, word: "failed", tone: "warn" };
    case "interrupted":
      return { glyph: Glyph.warn, word: "interrupted", tone: "warn" };
  }
  return { glyph: Glyph.skipped, word: "stopped", tone: "plain" };
}

export function done(end: string): Status {
  const o = outcome(end);
  return { word: "Done", glyph: "", line: `${o.glyph} ${o.word}`, tone: o.tone };
}

/** The status of a live run (the web UI's statusOf, SPEC §15.4). */
export function liveStatus(run: Run, now: number): Status {
  if (run.ended) return done(run.end);
  let questions = run.questions.length + (run.reset ? 1 : 0);
  let runQuestions = 0;
  if (run.budgetQuestion) runQuestions++;
  if (run.batchQuestion) runQuestions++;
  questions += runQuestions;
  const needs = run.active.filter((c) => cardNeeds(c.state)).length;
  const working = (line: string): Status => ({ word: "Working", glyph: Glyph.running, line, tone: "accent" });
  const paused = (line: string): Status => ({ word: "Paused", glyph: "", line, tone: "plain" });
  if (needs > 0) {
    return { word: "Needs you", glyph: Glyph.needsYou, line: needYou(needs + questions - runQuestions, runQuestions > 0), tone: "needs" };
  }
  if (questions > 0) {
    return { word: "Needs you", glyph: Glyph.needsYou, line: count(questions, "question", "questions"), tone: "needs" };
  }
  if (run.limited) {
    const l = run.limited;
    // The working hours ([run] hours, since 1.10) resume at the opening.
    if (l.window === "hours") return paused(`${windowLabel(l.window)} · resumes ${until(l.resetsAt, now)}`);
    const pct = l.percent > 0 ? ` ${Math.round(l.percent)}%` : "";
    return paused(`${windowLabel(l.window)} limit${pct} · resumes ${until(l.resetsAt + RESUME_GRACE, now)}`);
  }
  if (run.paused && run.holding) return paused("holding");
  if (run.paused && run.active.length === 1) return working(`pauses after ${run.active[0]!.task}`);
  if (run.paused && run.active.length > 1) return working("pauses after these tasks");
  if (run.paused) return paused("after this task");
  if (run.active.length > 0) return working("nothing needs you");
  if (run.run !== "") return working("nothing running yet");
  return { word: "Idle", glyph: "", line: "no run yet", tone: "dim" };
}

/** The run `igris status --json` describes when no run answers on the
 * socket (`run` of status.schema.json). */
export interface StatusRun {
  task: string;
  lock: string;
  lockDetail: string;
}

/** The status without a live run, from `igris status --json`'s run (cmd/igris
 * stateLine): a lock held by a live igris is a run going (one without a
 * socket, `arise --no-tui`, or one starting); a stale or unreadable lock is a
 * run that ended without ending itself. `ended` is the outcome of the live
 * run this window last saw, if any. */
export function idleStatus(run: StatusRun | undefined, ended: string | undefined): Status {
  switch (run?.lock) {
    case "running here":
    case "running elsewhere":
      return { word: "Working", glyph: Glyph.running, line: run.lockDetail || "not on this socket", tone: "accent" };
    case "stale":
    case "unreadable":
      return { word: "Interrupted", glyph: Glyph.warn, line: run.task ? `during ${run.task}` : "igris arise picks it up", tone: "warn" };
  }
  if (ended !== undefined) return done(ended);
  if (run?.task) return { word: "Idle", glyph: "", line: `${run.task} to resume`, tone: "dim" };
  return { word: "Idle", glyph: "", line: "no run", tone: "dim" };
}

/** The glyph and the word: "! Needs you", "● Working", "Done". */
export function label(s: Status): string {
  return s.glyph ? `${s.glyph} ${s.word}` : s.word;
}
