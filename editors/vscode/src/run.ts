// The live run as the extension holds it: the snapshot, continued by the
// events that follow it. A port of internal/serve's tracker (snapshot.go),
// which folds the same events into the snapshot serve hands the next
// client, so this view and a fresh snapshot agree. On top of it the plan's
// Status cells follow the events' `changes` (serve rereads the plan; a
// client only has the events).

import type { Card, Event, Snapshot } from "./protocol";

/** The card states (internal/serve). */
export const State = {
  working: "working",
  yourTurn: "your_turn",
  waiting: "waiting",
  needsYou: "needs_you",
  verifying: "verifying",
  lost: "lost",
  question: "question",
  limited: "limited",
} as const;

export type Run = Snapshot;

function setState(c: Card | undefined, state: string, ev: Event): void {
  if (!c) return;
  c.state = state;
  c.since = ev.at;
  c.detail = ev.detail;
  c.reason = ev.reason;
  c.cause = ev.cause;
  c.tool = ev.tool;
}

/** Applies one event to run, as serve's tracker does. */
export function apply(run: Run, ev: Event): void {
  if (ev.type === "run_started" && ev.run !== "") {
    run.run = ev.run;
    run.started = ev.at;
  }
  if (ev.phase !== "") run.phase = ev.phase;
  applyChanges(run, ev);

  // The run's questions and counters: no task's card (SPEC §6.9, §6.5).
  switch (ev.type) {
    case "budgets":
      return;
    case "budget_raised":
      run.budgetQuestion = undefined;
      return;
    case "budget_exceeded":
    case "commit_policy":
      return;
    case "commits_undone":
      run.batchQuestion = undefined;
      return;
    case "asked":
      if (ev.question === "budget") {
        run.budgetQuestion = ev;
        return;
      }
      if (ev.question === "commit_batch") {
        run.batchQuestion = ev;
        return;
      }
  }

  let task = ev.task;
  if (task === "" && run.active.length === 1) task = run.active[0]!.task; // a task's event, from a sequential run
  let c = run.active.find((a) => a.task === task);

  switch (ev.type) {
    case "task_started":
    case "task_resumed":
      if (!c) {
        c = { task, title: "", rank: "", model: "", mode: "", user: false, state: "", since: 0, detail: "", reason: "", cause: "", tool: "", resolver: "", session: undefined };
        run.active.push(c);
        sortActive(run);
      }
      c.title = ev.title;
      c.rank = ev.rank;
      c.model = ev.model;
      c.mode = ev.mode;
      c.user = ev.model === "";
      if (ev.session) c.session = ev.session; // a resume reattached it
      setState(c, State.working, ev);
      run.holding = false;
      settle(run, task);
      break;
    case "session_opened":
      if (c) {
        c.mode = ev.mode;
        c.session = ev.session;
      }
      break;
    case "your_turn":
      setState(c, State.yourTurn, ev);
      break;
    case "agent_waiting":
      setState(c, State.waiting, ev);
      break;
    case "agent_waiting_over":
      if (c && c.state === State.waiting) setState(c, State.working, ev);
      break;
    case "session_limited":
      if (c) {
        if (c.state !== State.limited) setState(c, State.limited, ev);
        else c.detail = ev.detail;
      }
      break;
    case "session_limit_over":
      if (c && c.state === State.limited) setState(c, State.working, ev);
      break;
    case "limit_hold":
      if (ev.limit) run.limited = { ...ev.limit, task: ev.limit.task || ev.task, detail: ev.detail };
      break;
    case "limit_resume":
      run.limited = undefined;
      break;
    case "needs_you":
      if (c && c.state === State.waiting) {
        // The wait counts from its start.
        c.state = State.needsYou;
        c.detail = ev.detail;
        c.reason = ev.reason;
        c.cause = ev.cause;
        c.tool = ev.tool;
        break;
      }
      setState(c, State.needsYou, ev);
      break;
    case "needs_you_clear":
    case "verify_failed":
      setState(c, State.working, ev);
      break;
    case "resolver_opened":
      setState(c, State.working, ev);
      if (c) c.resolver = ev.reason;
      break;
    case "resolver_done":
    case "resolver_failed":
      if (c) c.resolver = "";
      break;
    case "verify_started":
      setState(c, State.verifying, ev);
      break;
    case "session_lost":
      setState(c, State.lost, ev);
      if (c) c.session = undefined;
      break;
    case "asked":
      if (ev.question === "confirm_reset") {
        run.reset = ev;
        break;
      }
      run.questions = run.questions.filter((q) => q.task !== task);
      run.questions.push({ ...ev, task });
      sortQuestions(run);
      setState(c, State.question, ev);
      break;
    case "plan_closed":
      run.questions = run.questions.filter((q) => !(q.task === task && q.question === "plan_approval"));
      if (c && c.state === State.question) setState(c, State.needsYou, ev);
      break;
    case "reset_dropped":
      dropReset(run, task);
      break;
    case "retrying":
      if (c) c.session = undefined; // a new attempt opens its own
      setState(c, State.working, ev);
      settle(run, task);
      break;
    case "committed":
    case "not_committed":
      settle(run, task);
      break;
    case "task_reset":
      dropReset(run, task);
      drop(run, task);
      settle(run, task);
      break;
    case "task_done":
    case "task_skipped":
      drop(run, task);
      settle(run, task);
      break;
    case "pause_on":
      run.paused = true;
      break;
    case "pause_off":
      run.paused = false;
      run.holding = false;
      break;
    case "paused":
      run.holding = true;
      break;
    case "mode_changed":
      run.mode = ev.detail;
      break;
    case "task_mode_changed":
      run.overrides[task] = ev.detail;
      break;
    case "run_stopped":
      run.ended = true;
      run.end = ev.detail;
      run.questions = [];
      run.reset = undefined;
      run.limited = undefined;
      run.budgetQuestion = undefined;
      run.batchQuestion = undefined;
      break;
  }
}

/** The Status cells an event wrote (task_started, task_done, task_skipped,
 * task_reset carry them). */
function applyChanges(run: Run, ev: Event): void {
  if (ev.changes.length === 0) return;
  const to = new Map(ev.changes.map((c) => [c.id, c.to]));
  for (const ph of run.phases) {
    for (const t of ph.tasks) {
      const s = to.get(t.id);
      if (s !== undefined) t.status = s;
    }
  }
}

function drop(run: Run, task: string): void {
  run.active = run.active.filter((a) => a.task !== task);
}

/** A later event answered task's question. */
function settle(run: Run, task: string): void {
  run.questions = run.questions.filter((q) => q.task !== task);
}

function dropReset(run: Run, task: string): void {
  if (run.reset && (task === "" || run.reset.task === task)) run.reset = undefined;
}

/** Plan order of the run's tasks; tasks the plan doesn't list come after. */
function order(run: Run): (id: string) => number {
  const at = new Map<string, number>();
  for (const ph of run.phases) for (const t of ph.tasks) if (!at.has(t.id)) at.set(t.id, at.size);
  return (id) => at.get(id) ?? at.size;
}

function sortActive(run: Run): void {
  const rank = order(run);
  run.active.sort((a, b) => rank(a.task) - rank(b.task));
}

function sortQuestions(run: Run): void {
  const rank = order(run);
  run.questions.sort((a, b) => rank(a.task) - rank(b.task) || a.at - b.at);
}
