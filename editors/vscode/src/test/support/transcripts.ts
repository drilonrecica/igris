// The recorded transcripts as data, for tests of the fold and the view that
// need no socket: the snapshot a transcript sends and the events after it.
import * as path from "node:path";
import { obj, parseEvent, parseSnapshot, type Event, type Snapshot } from "../../protocol";
import { loadTranscript } from "./fakeServe";

// out/test/unit → the extension's testdata/
export const transcripts = path.resolve(__dirname, "../../../testdata/transcripts");
export const testdata = path.resolve(__dirname, "../../../testdata");

export function recorded(name: string): { snapshot: Snapshot; events: Event[] } {
  const sent = loadTranscript(path.join(transcripts, name))
    .filter((s): s is { send: Record<string, unknown> } => "send" in s)
    .map((s) => s.send);
  const snap = sent.find((m) => m["type"] === "snapshot");
  if (!snap) throw new Error(`${name} sends no snapshot`);
  const events = sent.filter((m) => m["type"] !== "snapshot").map(parseEvent).filter((e): e is Event => e !== undefined);
  return { snapshot: parseSnapshot(obj(snap)!), events };
}

/** An event with the fields a test names; the rest empty. */
export function ev(fields: Partial<Event> & { type: string }): Event {
  return {
    at: Date.parse("2026-11-04T12:00:00Z"),
    run: "",
    phase: "",
    task: "",
    title: "",
    rank: "",
    model: "",
    mode: "",
    detail: "",
    reason: "",
    question: "",
    changes: [],
    limit: undefined,
    budgets: undefined,
    cause: "",
    tool: "",
    resolve: false,
    undo: false,
    plan: "",
    then: "",
    budget: undefined,
    ...fields,
  };
}
