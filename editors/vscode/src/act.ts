// Answering from the editor (SPEC §15.9): an ask's answers in a QuickPick,
// a text in an InputBox, the second confirmation as a modal, a notification
// with the first answers as its buttons when something new waits on the
// owner. What is offered is answers.ts's; this file only asks and sends.
// Everything the editor shows passes through one Ui (prompts.ts draws it
// with the editor's own QuickPick, InputBox and notifications), so this
// file runs in the unit tests and the tests in a real VS Code can answer in
// its place.

import { commandOf, notice, type Ask, type Choice, type Command, type Prompts } from "./answers";
import type { Reply } from "./client";
import { short } from "./textsafe";

/** The editor's prompts, as the actions use them. */
export interface Ui extends Prompts {
  /** One of choices; undefined when dismissed. */
  choose(choices: Choice[], title: string, placeholder: string): Promise<Choice | undefined>;
  /** One of asks, when several wait. */
  chooseAsk(asks: Ask[]): Promise<Ask | undefined>;
  /** A notification with buttons; the button chosen, if any. */
  notify(line: string, buttons: string[]): Promise<string | undefined>;
  /** A plan's text, to read. */
  show(text: string, title: string): Promise<void>;
  /** What came of a command: sent, or the run's error. */
  told(ok: boolean, line: string): void;
}

/** What the actions need of the extension. */
export interface Deps {
  ui: () => Ui;
  /** Sends a command to the attached run. */
  send: (cmd: Command) => Promise<Reply>;
  /** The asks pending now: an answer is sent only while its ask is. */
  asks: () => Ask[];
}

/** The label a notification's More button has. */
export const MORE = "Answer…";

/** Asks what a choice needs and sends it; for an ask's choice only while
 * that ask still waits (else: answered elsewhere, as ntfy's buttons say). */
export async function perform(d: Deps, choice: Choice, title: string, askKey?: string): Promise<void> {
  const ui = d.ui();
  const cmd = await commandOf(choice, ui, title);
  if (!cmd) return;
  if (askKey !== undefined && !d.asks().some((a) => a.key === askKey)) {
    ui.told(false, `Igris: ${title} was already answered.`);
    return;
  }
  const r = await d.send(cmd);
  if (r.type === "ack") ui.told(true, `igris: ${choice.label} sent`);
  else ui.told(false, `Igris: ${short(r.error || "the run didn't take it", 300)}`);
}

/** Offers choices until one is sent or the owner backs out; a Read the
 * plan choice shows it and offers them again. */
export async function offer(d: Deps, choices: Choice[], title: string, placeholder: string, askKey?: string): Promise<void> {
  for (;;) {
    const c = await d.ui().choose(choices, title, placeholder);
    if (!c) return;
    if (c.show !== undefined) {
      await d.ui().show(c.show, title);
      continue;
    }
    await perform(d, c, title, askKey);
    return;
  }
}

/** Answers ask: its answers in a QuickPick. */
export function answer(d: Deps, ask: Ask): Promise<void> {
  return offer(d, ask.choices, ask.title, ask.text || ask.kind, ask.key);
}

/** The buttons of ask's notification: its first two answers (a plan to
 * read is not one), then Answer… for the rest. */
export function buttonsOf(ask: Ask): string[] {
  const answers = ask.choices.filter((c) => c.show === undefined);
  const out = answers.slice(0, 2).map((c) => c.label);
  if (answers.length > 2 || answers.length < ask.choices.length) out.push(MORE);
  return out;
}

/** Tells the owner about ask with a notification; a button answers it
 * while it still waits. */
export async function announce(d: Deps, ask: Ask): Promise<void> {
  const got = await d.ui().notify(`Igris: ${notice(ask)}`, buttonsOf(ask));
  if (got === undefined) return;
  const now = d.asks().find((a) => a.key === ask.key);
  if (!now) {
    d.ui().told(false, `Igris: ${ask.title} was already answered.`);
    return;
  }
  if (got === MORE) {
    await answer(d, now);
    return;
  }
  const c = now.choices.find((x) => x.label === got);
  if (c) await perform(d, c, now.title, now.key);
}
