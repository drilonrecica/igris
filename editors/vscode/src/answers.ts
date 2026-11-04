// What the owner can answer and do from the editor (SPEC §15.9), as plain
// data: each question with the answers the web UI offers for it
// (internal/serve/ui.go: questionOf, sheetOf, cardActions, confirmOf), the
// run's own actions, and the modes the pickers list. No VS Code here; act.ts
// asks the owner and sends what they chose. Skip-permissions mode is never
// offered (§7.3): the modes omit yolo and plan:yolo, and guard() refuses a
// command that would set one, whatever builds it.

import type { Card, Event } from "./protocol";
import { State, type Run } from "./run";
import { line, short } from "./textsafe";

/** A protocol-1 command (SPEC §13), without v and id. */
export interface Command {
  type: string;
  task?: string;
  text?: string;
  question?: string;
  plan?: string;
  continue?: boolean;
  yes?: boolean;
}

/** The second confirmation stop, skip, done and Undo ask (§15.8). */
export interface Confirm {
  title: string;
  text: string;
  label: string;
}

/** A text the owner types for a choice: its text becomes the command's. */
export interface Input {
  prompt: string;
  required: boolean;
  placeholder?: string;
}

export interface Choice {
  label: string;
  cmd: Command;
  danger?: boolean;
  input?: Input;
  /** The command's text is one of these (a mode), picked. */
  pick?: { prompt: string; items: string[]; picked: string; note?: string };
  confirm?: Confirm;
  /** Not a command: shows this text (a plan) and asks again. */
  show?: string;
}

/** One thing that waits on the owner, with its answers. */
export interface Ask {
  /** Stable while it is the same asking; a new asking gets a new key. */
  key: string;
  task: string;
  /** Its small label: commit?, plan, your turn, … */
  kind: string;
  title: string;
  text: string;
  choices: Choice[];
}

/** The run modes in the order igris lists them (internal/engine). */
export const MODES = ["default", "accept", "auto", "plan", "yolo", "manual", "plan:default", "plan:accept", "plan:auto", "plan:yolo"];

/** Whether mode m skips permissions (SPEC §7.3). */
export function skipsPermissions(m: string): boolean {
  return m === "yolo" || m === "plan:yolo";
}

/** The modes the editor offers: none that skips permissions (§7.3). */
export const OFFERED_MODES = MODES.filter((m) => !skipsPermissions(m));

/** What guard() says to a command that would set a skip-permissions mode. */
export const SKIP_PERMISSIONS_REFUSAL = "Skip-permissions mode is chosen in the terminal only: start or change the run there (igris arise).";

/** Why cmd must not be sent, or undefined: the editor never sets a
 * skip-permissions mode, nor says its owner typed the phrase (§7.3). */
export function guard(cmd: Record<string, unknown>): string | undefined {
  if ("yolo" in cmd) return SKIP_PERMISSIONS_REFUSAL;
  if (cmd["type"] !== "mode" && cmd["type"] !== "task_mode") return undefined;
  if (cmd["yes"] !== undefined) return SKIP_PERMISSIONS_REFUSAL;
  const text = typeof cmd["text"] === "string" ? cmd["text"].trim() : "";
  if (!OFFERED_MODES.includes(text)) return SKIP_PERMISSIONS_REFUSAL;
  return undefined;
}

/** The second confirmation of a command type, as the web UI's confirmOf. */
export function confirmOf(type: "stop" | "skip" | "done", task = ""): Confirm {
  const t = task || "the task";
  switch (type) {
    case "stop":
      return { title: "Stop igris?", text: "The session stays open; igris arise picks the run up again.", label: "Stop igris" };
    case "skip":
      return { title: `Skip ${t}?`, text: "The reason is recorded with the skip; an open session is closed.", label: `Skip ${t}` };
    case "done":
      return { title: `Mark ${t} done?`, text: "It's your decision, so igris doesn't verify it.", label: `Mark ${t} done` };
  }
}

/** The second confirmation of the batch question's Undo. */
export const UNDO_CONFIRM: Confirm = {
  title: "Undo the run's commits?",
  text: "git reset --soft to where the run began: the commits go, every change stays in the tree and the index, uncommitted.",
  label: "Undo the commits",
};

const stop = (): Choice => ({ label: "Stop igris", cmd: { type: "stop" }, danger: true, confirm: confirmOf("stop") });
const done = (t: string): Choice => ({ label: "Mark done", cmd: { type: "done", task: t }, input: { prompt: "Note (optional)", required: false }, confirm: confirmOf("done", t) });
const skip = (t: string): Choice => ({ label: "Skip", cmd: { type: "skip", task: t }, danger: true, input: { prompt: "Reason", required: true }, confirm: confirmOf("skip", t) });
const fresh = (t: string, label = "Start fresh"): Choice => ({ label, cmd: { type: "retry", task: t } });
const cont = (t: string, label = "Continue conversation"): Choice => ({ label, cmd: { type: "retry", task: t, continue: true } });

/** The words of a Needs you (SPEC §10), as internal/engine's NeedsWords. */
export function needsWords(reason: string, cause: string, tool: string): string {
  switch (reason) {
    case "blocked":
      switch (cause) {
        case "question":
          return "asks you a question in its pane";
        case "plan":
          return "plan to approve";
        case "permission":
          return tool ? `waits for a permission (${tool})` : "waits for a permission";
      }
      return "waits for you in its pane";
    case "idle":
      return "ended its turn without igris done";
    case "usage_limit":
      return "usage limit reset; no response";
    case "plan_approval":
      return "plan to approve";
    case "commit":
      return "commit question";
    case "skip_request":
      return "wants to skip";
    case "integration":
      return "didn't integrate";
    case "budget":
      return "budget reached";
  }
  return "";
}

/** A question's small label, as the web UI's questionKind. */
export function questionKind(q: string): string {
  switch (q) {
    case "commit":
      return "commit?";
    case "confirm_skip":
      return "skip?";
    case "session_lost":
      return "session lost";
    case "hook_failed":
      return "hook failed";
    case "integration":
    case "files_in_the_way":
      return "didn't integrate";
    case "confirm_reset":
      return "reset?";
    case "plan_approval":
      return "plan";
    case "budget":
      return "budget reached";
    case "commit_batch":
      return "keep commits?";
  }
  return "question";
}

/** The batch question's title and what follows it (engine.BatchTitle). */
function batchTitle(detail: string): [string, string] {
  const i = detail.indexOf("? ");
  return i < 0 ? [detail, ""] : [detail.slice(0, i + 1), detail.slice(i + 2)];
}

/** A pending question with the web UI's answers, less the pane (opening
 * one stays in the terminal). */
export function questionOf(q: Event): Ask {
  const t = q.task;
  const tl = line(t);
  const ask: Ask = { key: `q/${t}/${q.question}/${q.at}`, task: t, kind: questionKind(q.question), title: "Question", text: line(q.detail), choices: [] };
  const answer = (label: string, yes: boolean, extra: Partial<Command> = {}): Choice => ({ label, cmd: { type: "answer", task: t, yes, ...extra } });
  switch (q.question) {
    case "commit":
      ask.title = `Commit ${tl}?`;
      ask.choices = [answer("Commit", true), answer("Leave uncommitted", false), answer("Commit all (no more commit questions this run)", true, { text: "all" })];
      break;
    case "confirm_skip":
      ask.title = `${tl} asks to skip`;
      ask.choices = [answer("Keep working", false), { ...answer("Skip the task", true), danger: true }];
      break;
    case "session_lost":
      ask.title = `${tl}: session lost`;
      ask.choices = [fresh(t), cont(t), done(t), skip(t), stop()];
      break;
    case "hook_failed":
      ask.title = `${tl}: before_task hook failed`;
      ask.choices = [fresh(t, "Retry"), done(t), skip(t), stop()];
      break;
    case "integration":
    case "files_in_the_way": {
      ask.title = `${tl} didn't integrate`;
      const d = { ...done(t), label: "Mark done (integrated by hand)" };
      ask.choices = [fresh(t, q.question === "integration" ? "Rerun fresh" : "Try again"), d, skip(t), stop()];
      if (q.question === "integration" && q.resolve) ask.choices.unshift({ label: "Resolve (a resolver session)", cmd: { type: "resolve", task: t } });
      break;
    }
    case "confirm_reset":
      ask.key = `r/${t}/${q.at}`;
      ask.title = `Reset ${tl}?`;
      ask.choices = [answer("Keep it as it is", false, { question: "confirm_reset" }), { ...answer(`Reset ${tl}`, true, { question: "confirm_reset" }), danger: true }];
      break;
    case "budget":
      // The run's, not a task's (SPEC §6.9).
      ask.key = `b/${q.at}`;
      ask.title = q.budget ? `${budgetName(q.budget.budget)} ${line(q.budget.limit)} reached` : "Budget reached";
      ask.choices = [
        { label: "Raise", cmd: { type: "budget" }, input: { prompt: "New value for this run", placeholder: "30M, $60", required: true } },
        { label: "Continue (it no longer holds this run)", cmd: { type: "budget", text: "continue" } },
        stop(),
      ];
      break;
    case "commit_batch": {
      // The run's, once it ended (SPEC §6.5).
      ask.key = `c/${q.at}`;
      const [title, rest] = batchTitle(line(q.detail));
      ask.title = title;
      ask.text = rest;
      ask.choices = [{ label: "Keep", cmd: { type: "answer", question: "commit_batch", yes: true } }];
      if (q.undo) ask.choices.push({ label: "Undo (git reset --soft; the changes stay)", cmd: { type: "answer", question: "commit_batch", yes: false }, danger: true, confirm: UNDO_CONFIRM });
      break;
    }
    case "plan_approval": {
      ask.title = `${tl} presents a plan`;
      const then = q.then ? line(q.then) : "the mode after approval";
      ask.choices = [
        { label: `Approve: the session switches to ${then}`, cmd: { type: "plan", task: t, plan: "allow" } },
        { label: "Reject with feedback", cmd: { type: "plan", task: t, plan: "deny" }, danger: true, input: { prompt: "Feedback for the session", required: true } },
      ];
      if (q.plan) ask.choices.push({ label: "Read the plan", cmd: { type: "" }, show: q.plan });
      else ask.text = ask.text || "The plan is too long to show here; read it in the session's pane.";
      break;
    }
    default:
      ask.choices = [answer("Yes", true), answer("No", false)];
  }
  return ask;
}

/** budget key in words: run budget, phase V18 budget (engine.BudgetName). */
function budgetName(key: string): string {
  const i = key.indexOf(":");
  return line(i < 0 ? `${key} budget` : `${key.slice(0, i)} ${key.slice(i + 1)} budget`);
}

/** The ask of a card that waits on the owner without a question event: a
 * user task at its turn, a session blocked in its pane, a lost session. So
 * Needs you never shows with nothing to answer. */
export function sheetOf(c: Card): Ask | undefined {
  const t = c.task;
  const tl = line(t);
  const key = `s/${t}/${c.state}/${c.since}`;
  switch (c.state) {
    case State.yourTurn: {
      let text = "This task is yours. Do it outside igris, then mark it done here or run igris done in a terminal.";
      if (c.detail) text = `${line(c.detail)}. ${text}`;
      return { key, task: t, kind: "your turn", title: `Your turn: ${tl}${c.title ? ` · ${line(c.title)}` : ""}`, text, choices: [done(t), skip(t)] };
    }
    case State.needsYou:
      return {
        key,
        task: t,
        kind: needsWords(c.reason, c.cause, c.tool) || "blocked",
        title: `${tl} waits for you in its pane`,
        text: line(c.detail) || "The session waits for a permission or an answer; open its pane in the terminal.",
        choices: [done(t), skip(t), stop()],
      };
    case State.lost:
      return { key, task: t, kind: "session lost", title: `${tl}: session lost`, text: line(c.detail), choices: [fresh(t), cont(t), done(t), skip(t), stop()] };
  }
  return undefined;
}

/** An active task's actions when nothing is asked of it: done, skip, and
 * for an agent task retry fresh or continuing. */
export function cardActions(c: Card): Choice[] {
  const out = [done(c.task), skip(c.task)];
  if (!c.user) out.push(fresh(c.task, "Retry fresh"), cont(c.task, "Retry, continuing the conversation"));
  return out;
}

/** Everything that waits on the owner, in the web UI's order: the active
 * tasks' questions and sheets in plan order, the questions of tasks no
 * longer active, the reset request, the budget and the batch question.
 * None once the run ended but the batch question (asked after it). */
export function asksOf(run: Run): Ask[] {
  const out: Ask[] = [];
  const asked = new Map(run.questions.map((q) => [q.task, q]));
  if (!run.ended) {
    for (const c of run.active) {
      const q = asked.get(c.task);
      if (q) {
        out.push(questionOf(q));
        asked.delete(c.task);
        continue;
      }
      const s = sheetOf(c);
      if (s) out.push(s);
    }
    for (const q of asked.values()) out.push(questionOf(q));
    if (run.reset) out.push(questionOf({ ...run.reset, question: "confirm_reset" }));
    if (run.budgetQuestion) out.push(questionOf({ ...run.budgetQuestion, question: "budget" }));
  }
  if (run.batchQuestion) out.push(questionOf({ ...run.batchQuestion, question: "commit_batch" }));
  return out;
}

/** The run's own actions while it runs: Start anyway while the usage hold
 * holds, Pause after this task or Resume, Stop, the next sessions' mode. */
export function runActions(run: Run): Choice[] {
  if (run.ended) return [];
  const out: Choice[] = [];
  if (run.limited) out.push({ label: "Start anyway", cmd: { type: "limit", text: "start" } });
  out.push(run.paused ? { label: "Resume", cmd: { type: "pause" } } : { label: "Pause after this task", cmd: { type: "pause" } });
  out.push({ ...stop(), label: "Stop" });
  out.push(modeChoice(run));
  return out;
}

/** The note a mode picker shows when the current mode skips permissions:
 * the terminal chose it, and only the terminal sets it. */
function skipNote(mode: string): string | undefined {
  return skipsPermissions(mode) ? `Now ${mode}, chosen in the terminal; a skip-permissions mode is set there only.` : undefined;
}

/** Mode of the next sessions, without a skip-permissions mode. */
export function modeChoice(run: Run): Choice {
  const note = skipNote(run.mode);
  return {
    label: "Mode of the next sessions…",
    cmd: { type: "mode" },
    pick: { prompt: "Mode of the next sessions", items: OFFERED_MODES, picked: run.mode, ...(note ? { note } : {}) },
  };
}

/** Whether task t's mode can be set, as the TUI's M and the web UI's plan
 * page: an agent task of the run's phase that is still to do. */
export function taskModable(run: Run, id: string): boolean {
  if (run.ended) return false;
  const ph = run.phases.find((p) => p.id === run.phase);
  const t = ph?.tasks.find((x) => x.id === id);
  if (!t) return false;
  const agent = t.owner === "" || t.owner === "agent" || t.owner === "agent + user";
  const s = t.status.toLowerCase();
  return agent && s !== "done" && s !== "skipped";
}

/** Mode of task id's next session, without a skip-permissions mode. */
export function taskModeChoice(run: Run, id: string): Choice {
  const ph = run.phases.find((p) => p.id === run.phase);
  const t = ph?.tasks.find((x) => x.id === id);
  const current = run.overrides[id] || t?.mode || run.mode;
  const note = skipNote(current);
  return {
    label: `Mode of ${line(id)}'s next session…`,
    cmd: { type: "task_mode", task: id },
    pick: { prompt: `Mode of ${line(id)}'s next session`, items: OFFERED_MODES, picked: current, ...(note ? { note } : {}) },
  };
}

/** What the owner is asked on the way to a command. */
export interface Prompts {
  /** A text; undefined when dismissed. */
  input(i: Input, title: string): Promise<string | undefined>;
  /** One of items; undefined when dismissed. */
  pick(p: NonNullable<Choice["pick"]>, title: string): Promise<string | undefined>;
  /** The second confirmation: true to go on. */
  confirm(c: Confirm): Promise<boolean>;
}

/** The command choice sends once the owner has typed, picked and
 * confirmed what it asks; undefined when they backed out. */
export async function commandOf(choice: Choice, p: Prompts, title: string): Promise<Command | undefined> {
  const cmd: Command = { ...choice.cmd };
  if (choice.input) {
    const text = await p.input(choice.input, title);
    if (text === undefined) return undefined;
    if (choice.input.required && text.trim() === "") return undefined;
    if (text.trim() !== "") cmd.text = text;
  }
  if (choice.pick) {
    const m = await p.pick(choice.pick, title);
    if (m === undefined || !choice.pick.items.includes(m)) return undefined;
    cmd.text = m;
  }
  if (choice.confirm && !(await p.confirm(choice.confirm))) return undefined;
  return cmd;
}

/** A notification's line for ask: task, kind and title, cut short. */
export function notice(a: Ask): string {
  const who = a.task ? `${line(a.task)} · ` : "";
  return short(`${who}${a.kind}: ${a.title}`, 200);
}
