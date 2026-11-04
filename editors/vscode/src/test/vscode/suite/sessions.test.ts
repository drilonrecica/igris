import * as assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import * as vscode from "vscode";
import type { Api } from "../../../extension";
import type { TabHandle, TabMaker } from "../../../tabs";
import { EditorTabs } from "../../../terminals";
import { FakeServe } from "../../support/fakeServe";
import { workspaceDir } from "./workspace";

async function eventually<T>(what: string, f: () => T | undefined, ms = 15_000): Promise<T> {
  const end = Date.now() + ms;
  for (;;) {
    const v = f();
    if (v !== undefined) return v;
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 50));
  }
}

/** Stands in for the editor's terminals: records what the extension opens,
 * shows and closes, so no tmux client is started. */
class TabStandIn implements TabMaker {
  log: string[] = [];
  open(name: string, argv: string[]): TabHandle {
    const window = argv.at(-1)!;
    this.log.push(`open ${name} ${window} ${argv.join(" ")}`);
    return { show: () => this.log.push(`show ${name} ${window}`), dispose: () => this.log.push(`close ${name} ${window}`) };
  }
}

describe("sessions as terminal tabs", () => {
  let api: Api;
  let fake: FakeServe | undefined;
  const stand = new TabStandIn();

  before(async () => {
    api = await vscode.extensions.getExtension<Api>("drilonrecica.igris")!.activate();
    api.useTabs(stand);
    // The suites before removed .igris and refreshed: the view has no project.
    fs.mkdirSync(path.join(workspaceDir(), ".igris"), { recursive: true });
    await vscode.commands.executeCommand("igris.refresh");
    await eventually("the project", () => (api.view().mode !== "none" ? true : undefined));
    const transcript = path.resolve(__dirname, "../../../../testdata/transcripts/sessions.jsonl");
    fake = await FakeServe.start(transcript, path.join(workspaceDir(), ".igris", "igris.sock"));
  });

  after(async () => {
    api.useTabs(undefined);
    await fake?.close();
    fs.rmSync(path.join(workspaceDir(), ".igris"), { recursive: true, force: true });
  });

  it("opens a tab per active session on the run's tmux server, by the snapshot's tab_id, and follows the run", async () => {
    // The transcript ends with S1-01 on its second session (@3) and S1-02 done.
    await eventually("the tabs to settle", () => (stand.log.length >= 5 ? true : undefined));
    assert.deepEqual(
      stand.log.map((l) => l.split(" ").slice(0, 5).join(" ")),
      ["open S1-01 · sonnet @1", "open S1-02 · opus @2", "close S1-01 · sonnet @1", "open S1-01 · sonnet @3", "close S1-02 · opus @2"],
    );
    const argv = stand.log[0]!.split(" ").slice(5);
    assert.deepEqual(argv.slice(0, 7), ["tmux", "-S", "/nonexistent/igris-vscode-test/tmux.sock", "new-session", "-t", "=igris-demo", "-s"]);
    assert.match(argv[7]!, /^igris-tab-S1-01-\d+-1$/);
    assert.ok(argv.includes("destroy-unattached"), argv.join(" "));
    assert.deepEqual(argv.slice(-3), ["select-window", "-t", "@1"]);
    assert.deepEqual(api.tabs(), [{ task: "S1-01", name: "S1-01 · sonnet" }]);
  });

  it("shows the task's tab from the command", async () => {
    await vscode.commands.executeCommand("igris.openSession", { task: "S1-01" });
    assert.equal(stand.log.at(-1), "show S1-01 · sonnet @3");
    const n = stand.log.length;
    await vscode.commands.executeCommand("igris.openSession", { task: "S1-02" }); // not active: told, nothing opened
    assert.equal(stand.log.length, n);
  });

  it("closes the tabs when the run's serve has gone", async () => {
    await fake!.close();
    fake = undefined;
    await eventually("the tab closed", () => (stand.log.at(-1) === "close S1-01 · sonnet @3" ? true : undefined));
    assert.deepEqual(api.tabs(), []);
  });

  it("opens a real tab as a transient terminal whose process is the argv", () => {
    const tabs = new EditorTabs(undefined);
    tabs.open("Igris test tab", ["tmux", "-V"]);
    const term = vscode.window.terminals.find((t) => t.name === "Igris test tab");
    assert.ok(term, "the terminal exists");
    const opts = term.creationOptions as vscode.TerminalOptions;
    assert.equal(opts.shellPath, "tmux");
    assert.deepEqual(opts.shellArgs, ["-V"]);
    assert.equal(opts.isTransient, true);
    term.dispose();
  });
});
