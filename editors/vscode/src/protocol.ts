// Protocol 1 as the extension reads it (SPEC §13): the snapshot and the
// events, the fields the run view draws. Everything is shape-checked as it
// is read: a field of the wrong type is absent, never trusted, and fields
// and message types the extension doesn't know are ignored (§19).

/** The protocol the extension speaks (SPEC §13, §15.9). */
export const PROTOCOL = 1;

/** The longest line either side sends, newline excluded (SPEC §13). */
export const MAX_LINE = 1 << 20;

export interface Task {
  id: string;
  title: string;
  status: string;
  owner: string;
  rank: string;
  mode: string;
}

export interface Phase {
  id: string;
  title: string;
  parallel: boolean;
  tasks: Task[];
}

export interface Limited {
  window: string;
  resetsAt: number; // ms since the epoch
  percent: number;
  task: string;
  detail: string;
}

/** A session's identity (SPEC §13 `session`): its backend and, on tmux,
 * its window and pane ids (`@N`, `%N`). */
export interface SessionRef {
  backend: string;
  tabId: string;
  paneId: string;
}

/** Where the run's sessions live (SPEC §13 `session_host`, since 1.9): the
 * backend and, for tmux, the server's socket path and the session the
 * windows open in. Absent from an older serve. */
export interface SessionHost {
  backend: string;
  socket: string;
  session: string;
}

/** An active task, as its card shows it (the snapshot's `active`). */
export interface Card {
  task: string;
  title: string;
  rank: string;
  model: string;
  mode: string;
  user: boolean;
  state: string;
  since: number;
  detail: string;
  reason: string;
  /** Why a blocked session is blocked (SPEC §6.3), since 1.8. */
  cause: string;
  tool: string;
  resolver: string;
  /** The task's session while it has one (SPEC §13); its tmux window is
   * what a terminal tab shows (§15.9). */
  session: SessionRef | undefined;
}

/** An event: the run log's v1 envelope and the socket-only fields the
 * run view uses. */
export interface Event {
  type: string;
  at: number;
  run: string;
  phase: string;
  task: string;
  title: string;
  rank: string;
  model: string;
  mode: string;
  detail: string;
  reason: string;
  question: string;
  changes: { id: string; to: string }[];
  limit: Limited | undefined;
  budgets: unknown[] | undefined;
  cause: string;
  tool: string;
  /** An `asked` integration question: a resolver may be opened (§6.11). */
  resolve: boolean;
  /** The batch question: Undo is offered (§6.5). */
  undo: boolean;
  /** A plan shown for approval and the mode an approval switches to. */
  plan: string;
  then: string;
  /** The budget question's budget (§6.9). */
  budget: { budget: string; limit: string } | undefined;
  /** The session a `session_opened` or a resumed task carries (§13). */
  session: SessionRef | undefined;
}

export interface Snapshot {
  protocol: unknown;
  server: string;
  project: string;
  backend: string;
  sessionHost: SessionHost | undefined;
  run: string;
  started: number;
  phase: string;
  mode: string;
  overrides: Record<string, string>;
  paused: boolean;
  holding: boolean;
  ended: boolean;
  end: string;
  phases: Phase[];
  planError: string;
  active: Card[];
  questions: Event[];
  reset: Event | undefined;
  limited: Limited | undefined;
  budgetQuestion: Event | undefined;
  batchQuestion: Event | undefined;
}

type Obj = Record<string, unknown>;

function obj(v: unknown): Obj | undefined {
  return v !== null && typeof v === "object" && !Array.isArray(v) ? (v as Obj) : undefined;
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function bool(v: unknown): boolean {
  return v === true;
}

function num(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

/** An RFC 3339 time as ms since the epoch; 0 for none or garbage. */
function time(v: unknown): number {
  if (typeof v !== "string") return 0;
  const t = Date.parse(v);
  return Number.isFinite(t) && t > 0 ? t : 0;
}

function list(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}

export function parseTask(v: unknown): Task | undefined {
  const o = obj(v);
  if (!o || str(o["id"]) === "") return undefined;
  return {
    id: str(o["id"]),
    title: str(o["title"]),
    status: str(o["status"]),
    owner: str(o["owner"]),
    rank: str(o["rank"]),
    mode: str(o["mode"]),
  };
}

export function parsePhase(v: unknown): Phase | undefined {
  const o = obj(v);
  if (!o || str(o["id"]) === "") return undefined;
  return {
    id: str(o["id"]),
    title: str(o["title"]),
    parallel: bool(o["parallel"]),
    tasks: list(o["tasks"]).map(parseTask).filter((t): t is Task => t !== undefined),
  };
}

function parseLimited(v: unknown): Limited | undefined {
  const o = obj(v);
  if (!o) return undefined;
  const resetsAt = time(o["resets_at"]);
  if (str(o["window"]) === "" || resetsAt === 0) return undefined;
  return { window: str(o["window"]), resetsAt, percent: num(o["percent"]), task: str(o["task"]), detail: str(o["detail"]) };
}

function parseSessionRef(v: unknown): SessionRef | undefined {
  const o = obj(v);
  if (!o || str(o["backend"]) === "") return undefined;
  return { backend: str(o["backend"]), tabId: str(o["tab_id"]), paneId: str(o["pane_id"]) };
}

function parseSessionHost(v: unknown): SessionHost | undefined {
  const o = obj(v);
  if (!o || str(o["backend"]) === "") return undefined;
  return { backend: str(o["backend"]), socket: str(o["socket_path"]), session: str(o["session"]) };
}

function parseCard(v: unknown): Card | undefined {
  const o = obj(v);
  if (!o || str(o["task"]) === "") return undefined;
  return {
    task: str(o["task"]),
    title: str(o["title"]),
    rank: str(o["rank"]),
    model: str(o["model"]),
    mode: str(o["mode"]),
    user: bool(o["user"]),
    state: str(o["state"]),
    since: time(o["since"]),
    detail: str(o["detail"]),
    reason: str(o["reason"]),
    cause: str(o["cause"]),
    tool: str(o["tool"]),
    resolver: str(o["resolver"]),
    session: parseSessionRef(o["session"]),
  };
}

/** One event line; undefined when it isn't an object with a type. */
export function parseEvent(v: unknown): Event | undefined {
  const o = obj(v);
  if (!o || str(o["type"]) === "") return undefined;
  const changes: { id: string; to: string }[] = [];
  for (const c of list(o["changes"])) {
    const co = obj(c);
    if (co && str(co["id"]) !== "" && str(co["to"]) !== "") changes.push({ id: str(co["id"]), to: str(co["to"]) });
  }
  return {
    type: str(o["type"]),
    at: time(o["at"]),
    run: str(o["run"]),
    phase: str(o["phase"]),
    task: str(o["task"]),
    title: str(o["title"]),
    rank: str(o["rank"]),
    model: str(o["model"]),
    mode: str(o["mode"]),
    detail: str(o["detail"]),
    reason: str(o["reason"]),
    question: str(o["question"]),
    changes,
    limit: parseLimited(o["limit"]),
    budgets: Array.isArray(o["budgets"]) ? o["budgets"] : undefined,
    cause: str(o["cause"]),
    tool: str(o["tool"]),
    resolve: bool(o["resolve"]),
    undo: bool(o["undo"]),
    plan: str(o["plan"]),
    then: str(o["then"]),
    budget: parseBudget(o["budget"]),
    session: parseSessionRef(o["session"]),
  };
}

function parseBudget(v: unknown): { budget: string; limit: string } | undefined {
  const o = obj(v);
  if (!o || str(o["budget"]) === "") return undefined;
  return { budget: str(o["budget"]), limit: str(o["limit"]) };
}

function parseEventOpt(v: unknown): Event | undefined {
  return v === undefined ? undefined : parseEvent(v);
}

export function parseSnapshot(o: Obj): Snapshot {
  const overrides: Record<string, string> = {};
  const ov = obj(o["overrides"]);
  if (ov) {
    for (const [k, v] of Object.entries(ov)) if (typeof v === "string") overrides[k] = v;
  }
  let active = list(o["active"]).map(parseCard).filter((c): c is Card => c !== undefined);
  // Before 1.5 there is no active: one current task (SPEC §15.9).
  if (o["active"] === undefined) {
    const cur = parseCard(o["current"]);
    active = cur ? [cur] : [];
  }
  let questions = list(o["questions"]).map(parseEvent).filter((e): e is Event => e !== undefined);
  if (o["questions"] === undefined) {
    const q = parseEventOpt(o["question"]);
    questions = q ? [q] : [];
  }
  return {
    protocol: o["protocol"],
    server: str(o["server"]),
    project: str(o["project"]),
    backend: str(o["backend"]),
    sessionHost: parseSessionHost(o["session_host"]),
    run: str(o["run"]),
    started: time(o["started"]),
    phase: str(o["phase"]),
    mode: str(o["mode"]),
    overrides,
    paused: bool(o["paused"]),
    holding: bool(o["holding"]),
    ended: bool(o["ended"]),
    end: str(o["end"]),
    phases: list(o["phases"]).map(parsePhase).filter((p): p is Phase => p !== undefined),
    planError: str(o["plan_error"]),
    active,
    questions,
    reset: parseEventOpt(o["reset"]),
    limited: parseLimited(o["limited"]),
    budgetQuestion: parseEventOpt(o["budget_question"]),
    batchQuestion: parseEventOpt(o["batch_question"]),
  };
}

/** What a socket line is: the type and, for replies, the id. */
export function head(v: unknown): { type: string; id: string; error: string } | undefined {
  const o = obj(v);
  if (!o) return undefined;
  return { type: str(o["type"]), id: str(o["id"]), error: str(o["error"]) };
}

export { obj };
