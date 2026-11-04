import * as assert from "node:assert/strict";
import type { Card } from "../../protocol";
import { apply, type Run } from "../../run";
import { argvOf, groupedName, labelOf, sessionsOf, TRUST, whereIs } from "../../sessions";
import { recorded } from "../support/transcripts";

const SOCKET = "/nonexistent/igris-vscode-test/tmux.sock";

function card(fields: Partial<Card> & { task: string }): Card {
  return { title: "", rank: "", model: "", mode: "", user: false, state: "", since: 0, detail: "", reason: "", cause: "", tool: "", resolver: "", session: undefined, ...fields };
}

describe("sessions as terminal tabs", () => {
  it("reads session_host and each card's session from the snapshot", () => {
    const { snapshot } = recorded("sessions.jsonl");
    assert.deepEqual(snapshot.sessionHost, { backend: "tmux", socket: SOCKET, session: "igris-demo" });
    assert.deepEqual(snapshot.active[0]!.session, { backend: "tmux", tabId: "@1", paneId: "%1" });
    assert.equal(snapshot.active[1]!.session, undefined);
  });

  it("gives a tab to each active task whose tmux window the snapshot names", () => {
    const { snapshot } = recorded("sessions.jsonl");
    assert.deepEqual(
      sessionsOf(snapshot).map((s) => [s.key, s.task, s.name, s.tabId]),
      [["S1-01/@1", "S1-01", "S1-01 · sonnet", "@1"]],
    );
  });

  it("follows the events: a session opened, a retry, a task done", () => {
    const { snapshot: run, events } = recorded("sessions.jsonl");
    const keys = (): string[] => sessionsOf(run).map((s) => s.key);
    apply(run, events[0]!); // session_opened S1-02 @2
    assert.deepEqual(keys(), ["S1-01/@1", "S1-02/@2"]);
    apply(run, events[1]!); // retrying S1-01: its session is gone until the next opens
    assert.deepEqual(keys(), ["S1-02/@2"]);
    apply(run, events[2]!); // session_opened S1-01 @3: another key
    assert.deepEqual(keys(), ["S1-01/@3", "S1-02/@2"]);
    apply(run, events[3]!); // task_done S1-02
    assert.deepEqual(keys(), ["S1-01/@3"]);
  });

  it("gives no tab without session_host (an older serve), on herdr, or for a window id of another shape", () => {
    const older = recorded("working.jsonl").snapshot;
    assert.equal(older.sessionHost, undefined);
    assert.deepEqual(sessionsOf(older), []);
    const herdr: Run = { ...older, sessionHost: { backend: "herdr", socket: "", session: "" } };
    assert.deepEqual(sessionsOf(herdr), []);
    const { snapshot } = recorded("sessions.jsonl");
    snapshot.active[0]!.session = { backend: "tmux", tabId: "S1-01 · sonnet", paneId: "%1" };
    assert.deepEqual(sessionsOf(snapshot), []);
    snapshot.active[0]!.session = { backend: "tmux", tabId: "@1; kill-server", paneId: "%1" };
    assert.deepEqual(sessionsOf(snapshot), []);
  });

  it("attaches through a grouped session on the run's server that ends with the tab, selecting the window by tab_id", () => {
    const s = sessionsOf(recorded("sessions.jsonl").snapshot)[0]!;
    const g = "igris-tab-S1-01-4242-1";
    assert.deepEqual(argvOf(s, g), [
      "tmux", "-S", SOCKET,
      "new-session", "-t", "=igris-demo", "-s", g, ";",
      "set-option", "-t", g, "destroy-unattached", "on", ";",
      "set-option", "-t", g, "status-position", "top", ";",
      // The whole line as a session option: no window list after the text.
      "set-option", "-t", g, "status-format[0]", ` S1-01 · sonnet │ ${TRUST} `, ";",
      "select-window", "-t", "@1",
    ]);
    assert.match(TRUST, /folder.*Claude Code's/);
  });

  it("leaves -S out for a host without a socket path", () => {
    const s = sessionsOf(recorded("sessions.jsonl").snapshot)[0]!;
    const argv = argvOf({ ...s, host: { ...s.host, socket: "" } }, "g");
    assert.deepEqual(argv.slice(0, 4), ["tmux", "new-session", "-t", "=igris-demo"]);
  });

  it("writes the first line as tmux format text: # doubled, control characters cleaned", () => {
    const c = card({ task: "S1-01", rank: "son#{kill-server}net\u001b[31m" });
    const s = { key: "k", task: c.task, name: labelOf(c), host: { backend: "tmux", socket: "", session: "x" }, tabId: "@1" };
    const status = argvOf(s, "g")[argvOf(s, "g").indexOf("status-format[0]") + 1]!;
    assert.ok(status.includes("S1-01 · son##{kill-server}net"), status);
    assert.ok(!status.includes("\u001b"), status);
    assert.equal(labelOf(card({ task: "S1-02" })), "S1-02");
  });

  it("names the grouped session with tmux's characters only", () => {
    assert.equal(groupedName("S1-01", "4242-1"), "igris-tab-S1-01-4242-1");
    assert.equal(groupedName("S1.01:x y", "7"), "igris-tab-S1-01-x-y-7");
    assert.equal(groupedName("A".repeat(40), "7"), `igris-tab-${"A".repeat(32)}-7`);
  });

  it("says where a session is when no tab can show it", () => {
    const older = recorded("working.jsonl").snapshot;
    assert.match(whereIs(older, older.active[0]!), /^S1-01 · sonnet: this run's igris serve is older than 1\.9/);
    const herdr: Run = { ...older, sessionHost: { backend: "herdr", socket: "", session: "" } };
    assert.equal(whereIs(herdr, older.active[0]!), "Session: herdr tab S1-01 · sonnet");
    const { snapshot } = recorded("sessions.jsonl");
    assert.equal(whereIs(snapshot, snapshot.active[1]!), "S1-02 · opus has no session open right now.");
    assert.equal(whereIs(snapshot, snapshot.active[0]!), `Session: tmux session igris-demo — tmux -S ${SOCKET} attach -t igris-demo`);
    const dflt: Run = { ...snapshot, sessionHost: { backend: "tmux", socket: "/tmp/tmux-1000/default", session: "igris-demo" } };
    assert.equal(whereIs(dflt, snapshot.active[0]!), "Session: tmux session igris-demo — tmux attach -t igris-demo");
    const odd: Run = { ...snapshot, sessionHost: { backend: "tmux", socket: "/tmp/my sock/t", session: "igris-demo" } };
    assert.equal(whereIs(odd, snapshot.active[0]!), "Session: tmux session igris-demo — tmux -S '/tmp/my sock/t' attach -t igris-demo");
    const other: Run = { ...snapshot, sessionHost: { backend: "zellij\u001b[31m", socket: "", session: "" } };
    assert.equal(whereIs(other, snapshot.active[0]!), "Session: zellij");
  });
});
