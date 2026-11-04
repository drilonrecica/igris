import * as assert from "node:assert/strict";
import { apply } from "../../run";
import { SessionTabs, type TabHandle, type TabMaker } from "../../tabs";
import { recorded } from "../support/transcripts";

/** A maker that records what it was asked to open, show and close. */
class FakeMaker implements TabMaker {
  log: string[] = [];
  handles = new Map<string, TabHandle>();
  open(name: string, argv: string[]): TabHandle {
    const grouped = argv[argv.indexOf("-s") + 1]!;
    const window = argv.at(-1)!;
    this.log.push(`open ${name} ${grouped} ${window}`);
    const h: TabHandle = { show: () => this.log.push(`show ${name} ${window}`), dispose: () => this.log.push(`close ${name} ${window}`) };
    this.handles.set(`${name} ${window}`, h);
    return h;
  }
}

describe("the session tabs", () => {
  it("opens a tab per session, closes a tab for a session the run no longer has", () => {
    const { snapshot: run, events } = recorded("sessions.jsonl");
    const m = new FakeMaker();
    const tabs = new SessionTabs(() => m, "4242");
    tabs.sync(run);
    tabs.sync(run); // the same run again opens nothing more
    assert.deepEqual(m.log, ["open S1-01 · sonnet igris-tab-S1-01-4242-1 @1"]);
    apply(run, events[0]!); // session_opened S1-02
    tabs.sync(run);
    apply(run, events[1]!); // retrying S1-01
    tabs.sync(run);
    apply(run, events[2]!); // session_opened S1-01 @3
    tabs.sync(run);
    apply(run, events[3]!); // task_done S1-02
    tabs.sync(run);
    assert.deepEqual(m.log, [
      "open S1-01 · sonnet igris-tab-S1-01-4242-1 @1",
      "open S1-02 · opus igris-tab-S1-02-4242-2 @2",
      "close S1-01 · sonnet @1",
      "open S1-01 · sonnet igris-tab-S1-01-4242-3 @3",
      "close S1-02 · opus @2",
    ]);
    assert.deepEqual(tabs.tabs().map((s) => s.key), ["S1-01/@3"]);
  });

  it("closes every tab without a run, once the run ended, and on dispose", () => {
    const { snapshot: run } = recorded("sessions.jsonl");
    const m = new FakeMaker();
    const tabs = new SessionTabs(() => m, "1");
    tabs.sync(run);
    tabs.sync(undefined);
    assert.deepEqual(m.log, ["open S1-01 · sonnet igris-tab-S1-01-1-1 @1", "close S1-01 · sonnet @1"]);
    tabs.sync(run);
    run.ended = true;
    tabs.sync(run);
    assert.equal(m.log.at(-1), "close S1-01 · sonnet @1");
    run.ended = false;
    tabs.sync(run);
    tabs.dispose();
    assert.equal(m.log.at(-1), "close S1-01 · sonnet @1");
    assert.deepEqual(tabs.tabs(), []);
  });

  it("leaves a tab the owner closed closed, until focus opens it again", () => {
    const { snapshot: run } = recorded("sessions.jsonl");
    const m = new FakeMaker();
    const tabs = new SessionTabs(() => m, "1");
    tabs.sync(run);
    tabs.closed(m.handles.get("S1-01 · sonnet @1")!);
    tabs.sync(run);
    assert.deepEqual(m.log, ["open S1-01 · sonnet igris-tab-S1-01-1-1 @1"]);
    assert.deepEqual(tabs.tabs(), []);
    assert.equal(tabs.focus(run, "S1-01"), true);
    assert.deepEqual(m.log.slice(1), ["open S1-01 · sonnet igris-tab-S1-01-1-2 @1", "show S1-01 · sonnet @1"]);
    assert.equal(tabs.focus(run, "S1-01"), true); // open already: shown, not opened again
    assert.equal(m.log.at(-1), "show S1-01 · sonnet @1");
    assert.equal(m.log.length, 4);
    assert.equal(tabs.focus(run, "S1-02"), false); // no session yet
    assert.equal(tabs.focus(undefined, "S1-01"), false);
  });

  it("takes a close it asked for itself as its own, not the owner's", () => {
    const { snapshot: run, events } = recorded("sessions.jsonl");
    const m = new FakeMaker();
    const tabs = new SessionTabs(() => m, "1");
    tabs.sync(run);
    const first = m.handles.get("S1-01 · sonnet @1")!;
    apply(run, events[1]!); // retrying: the tab goes
    tabs.sync(run);
    tabs.closed(first); // the editor reports the close this class asked for
    // The same session again (as a reconnect's snapshot could show it): opened, not held closed.
    run.active[0]!.session = { backend: "tmux", tabId: "@1", paneId: "%1" };
    tabs.sync(run);
    assert.equal(m.log.at(-1), "open S1-01 · sonnet igris-tab-S1-01-1-2 @1");
  });

  it("ignores a close of a terminal that is not one of its tabs", () => {
    const tabs = new SessionTabs(() => new FakeMaker(), "1");
    tabs.closed({ show: () => undefined, dispose: () => undefined });
    assert.deepEqual(tabs.tabs(), []);
  });

  it("reports a tab whose tmux client failed, once, and doesn't open it again", () => {
    const { snapshot: run } = recorded("sessions.jsonl");
    const m = new FakeMaker();
    const failed: string[] = [];
    const tabs = new SessionTabs(() => m, "1", (s, code) => failed.push(`${s.name} ${s.host.session} ${code}`));
    tabs.sync(run);
    tabs.closed(m.handles.get("S1-01 · sonnet @1")!, 1);
    tabs.sync(run);
    assert.deepEqual(failed, ["S1-01 · sonnet igris-demo 1"]);
    assert.deepEqual(m.log, ["open S1-01 · sonnet igris-tab-S1-01-1-1 @1"]);
    assert.equal(tabs.focus(run, "S1-01"), true); // asked for: tried again
    assert.equal(m.log[1], "open S1-01 · sonnet igris-tab-S1-01-1-2 @1");
  });
});
