import * as vscode from "vscode";
import { announce, answer, offer, perform, type Deps, type Ui } from "./act";
import { asksOf, cardActions, modeChoice, runActions, taskModable, taskModeChoice, type Ask, type Choice } from "./answers";
import { Controller } from "./controller";
import { editorUi } from "./prompts";
import { readStatus } from "./idle";
import { checkInstall } from "./igris";
import { findRoot, socketPath } from "./socket";
import { RunTree } from "./tree";
import { barOf, firstNeeds, messageOf, treeOf, type Bar, type Node, type ViewState } from "./view";

/** What the tests read of the extension. */
export interface Api {
  /** The one line of the refusal that holds, if any. */
  refusal(): string | undefined;
  /** What the run view draws right now. */
  view(): { mode: ViewState["mode"] | "none"; bar: Bar | undefined; tree: Node[]; message: string | undefined };
  /** What waits on the owner right now. */
  asks(): Ask[];
  /** Answers through ui instead of the editor's prompts (tests). */
  useUi(ui: Ui | undefined): void;
}

/** The one line of the current refusal, if any (SPEC §15.9): while it holds
 * nothing else of the extension runs. */
let refused: string | undefined;
let controller: Controller | undefined;
let tree: RunTree;
let treeView: vscode.TreeView<Node>;
let bar: vscode.StatusBarItem;
let lastBar: Bar | undefined;
let watchers: vscode.Disposable[] = [];
let watchedFiles = "";
let fileWatcher: vscode.Disposable | undefined;
let ui: Ui = editorUi;
/** The asks a notification has told the owner about, by key. */
let announced = new Set<string>();

/** The live run, while one is attached. */
function liveRun(): ViewState["run"] {
  const s = controller?.state;
  return s?.mode === "live" ? s.run : undefined;
}

function currentAsks(): Ask[] {
  const run = liveRun();
  return run ? asksOf(run) : [];
}

const deps: Deps = {
  ui: () => ui,
  send: (cmd) => controller?.send(cmd) ?? Promise.resolve({ type: "error", error: "not connected to the run" }),
  asks: currentAsks,
};

/** Answers what node asks, or the one thing that waits, or one of them. */
async function answerCommand(node?: Node): Promise<void> {
  const asks = currentAsks();
  let a = node?.ask !== undefined ? asks.find((x) => x.key === node.ask) : undefined;
  if (!a && node?.task) a = asks.find((x) => x.task === node.task);
  if (!a && !node) a = asks.length === 1 ? asks[0] : asks.length > 1 ? await ui.chooseAsk(asks) : undefined;
  if (!a) {
    ui.told(false, node ? "Igris: that was already answered." : "Igris: nothing needs you.");
    return;
  }
  await answer(deps, a);
}

/** A task's actions: its ask's answers when it asks something, else an
 * active task's done, skip and retry; and its next session's mode. */
async function taskActions(node?: Node): Promise<void> {
  const run = liveRun();
  const id = node?.task;
  if (!run || !id) return;
  const a = currentAsks().find((x) => x.task === id);
  if (a) {
    await answer(deps, a);
    return;
  }
  const choices: Choice[] = [];
  const card = run.active.find((c) => c.task === id);
  if (card && !run.ended) choices.push(...cardActions(card));
  if (taskModable(run, id)) choices.push(taskModeChoice(run, id));
  if (choices.length === 0) {
    ui.told(false, `Igris: nothing to do for ${id} from here.`);
    return;
  }
  await offer(deps, choices, id, card ? card.title || id : id);
}

/** Sends one of the run's actions by its command type. */
async function runAction(pickOf: (cs: Choice[]) => Choice | undefined): Promise<void> {
  const run = liveRun();
  const c = run ? pickOf(runActions(run)) : undefined;
  if (!run || !c) {
    ui.told(false, "Igris: no run is attached.");
    return;
  }
  await perform(deps, c, "Igris");
}

export async function activate(context: vscode.ExtensionContext): Promise<Api> {
  tree = new RunTree();
  treeView = vscode.window.createTreeView("igris.run", { treeDataProvider: tree, showCollapseAll: true });
  bar = vscode.window.createStatusBarItem("igris.status", vscode.StatusBarAlignment.Left, 10);
  bar.name = "Igris";
  bar.command = "igris.showRun";
  const version = String((context.extension.packageJSON as { version?: unknown }).version ?? "");
  context.subscriptions.push(
    tree,
    treeView,
    bar,
    { dispose: stopRunView },
    vscode.commands.registerCommand("igris.checkInstall", async () => {
      await verify();
      if (!refused && !controller) startRunView(version);
    }),
    vscode.commands.registerCommand("igris.refresh", () => {
      if (refused) return;
      if (controller) controller.refresh();
      else startRunView(version);
    }),
    vscode.commands.registerCommand("igris.showRun", showRun),
    vscode.commands.registerCommand("igris.answer", answerCommand),
    vscode.commands.registerCommand("igris.taskActions", taskActions),
    vscode.commands.registerCommand("igris.runActions", async () => {
      const run = liveRun();
      if (!run || run.ended) return ui.told(false, "Igris: no run is attached.");
      await offer(deps, runActions(run), "Igris: the run", "What to do with the run");
    }),
    vscode.commands.registerCommand("igris.pause", () => runAction((cs) => cs.find((c) => c.cmd.type === "pause" && c.label !== "Resume"))),
    vscode.commands.registerCommand("igris.resume", () => runAction((cs) => cs.find((c) => c.label === "Resume"))),
    vscode.commands.registerCommand("igris.stop", () => runAction((cs) => cs.find((c) => c.cmd.type === "stop"))),
    vscode.commands.registerCommand("igris.startAnyway", () => runAction((cs) => cs.find((c) => c.cmd.type === "limit"))),
    vscode.commands.registerCommand("igris.setMode", async () => {
      const run = liveRun();
      if (!run || run.ended) return ui.told(false, "Igris: no run is attached.");
      await perform(deps, modeChoice(run), "Igris");
    }),
    vscode.commands.registerCommand("igris.setTaskMode", async (node?: Node) => {
      const run = liveRun();
      if (!run || !node?.task || !taskModable(run, node.task)) return;
      await perform(deps, taskModeChoice(run, node.task), node.task);
    }),
    vscode.workspace.onDidChangeWorkspaceFolders(() => {
      stopRunView();
      if (!refused) startRunView(version);
    }),
  );
  await verify();
  if (!refused) startRunView(version);
  return {
    refusal: () => refused,
    view: () => ({
      mode: controller?.state.mode ?? "none",
      bar: lastBar,
      tree: tree.nodes,
      message: treeView.message || undefined,
    }),
    asks: currentAsks,
    useUi: (u) => {
      ui = u ?? editorUi;
    },
  };
}

/** The activation check (SPEC §15.9). */
async function verify(): Promise<void> {
  const res = await checkInstall();
  refused = res.ok ? undefined : res.line;
  await vscode.commands.executeCommand("setContext", "igris.refused", refused !== undefined);
  if (refused !== undefined) {
    stopRunView();
    showRefusal(refused);
  }
}

function showRefusal(line: string): void {
  treeView.message = line;
  tree.set([]);
  bar.hide();
  lastBar = undefined;
  void vscode.window.showWarningMessage(line);
}

/** The project of the first workspace folder that has one. */
function projectRoot(): string | undefined {
  for (const f of vscode.workspace.workspaceFolders ?? []) {
    if (f.uri.scheme !== "file") continue;
    const root = findRoot(f.uri.fsPath);
    if (root) return root;
  }
  return undefined;
}

function startRunView(version: string): void {
  const root = projectRoot();
  if (!root) {
    treeView.message = "No igris project in this workspace: open a folder with an igris.toml, or run igris init in one.";
    tree.set([]);
    bar.hide();
    return;
  }
  const c = new Controller({
    name: `igris-vscode ${version}`.slice(0, 64),
    socket: () => socketPath(root),
    readStatus: () => readStatus(root),
  });
  controller = c;
  announced = new Set();
  let wasRefused = false;
  c.onChange((s) => {
    render(s);
    announceNew(s);
    if (s.mode === "refused" && !wasRefused) void vscode.window.showWarningMessage(s.refusal ?? "");
    wasRefused = s.mode === "refused";
    watchFiles(root, s);
  });
  const base = vscode.workspace.createFileSystemWatcher(new vscode.RelativePattern(root, "{igris.toml,.igris/state.json,.igris/igris.lock}"));
  base.onDidChange(() => c.filesChanged());
  base.onDidCreate(() => c.filesChanged());
  base.onDidDelete(() => c.filesChanged());
  watchers.push(base);
  render(c.state);
  c.start();
}

function stopRunView(): void {
  controller?.dispose();
  controller = undefined;
  for (const w of watchers) w.dispose();
  watchers = [];
  fileWatcher?.dispose();
  fileWatcher = undefined;
  watchedFiles = "";
}

/** Watches the plan's files once `igris status` named them, so an edit or
 * a run without a socket (`arise --no-tui`) shows without a refresh. */
function watchFiles(root: string, s: ViewState): void {
  const files = (s.idle?.files ?? []).filter((f) => !/[{}[\],*?!]/.test(f));
  const key = files.join("\n");
  if (s.mode !== "idle" || files.length === 0 || key === watchedFiles) return;
  watchedFiles = key;
  const w = vscode.workspace.createFileSystemWatcher(new vscode.RelativePattern(root, files.length === 1 ? files[0]! : `{${files.join(",")}}`));
  w.onDidChange(() => controller?.filesChanged());
  w.onDidCreate(() => controller?.filesChanged());
  fileWatcher?.dispose();
  fileWatcher = w;
}

/** A notification for each ask that is new since the last change; one
 * that keeps waiting, or comes back with a reconnect, isn't told again. */
function announceNew(s: ViewState): void {
  if (s.mode !== "live" || !s.run) return;
  for (const a of asksOf(s.run)) {
    if (announced.has(a.key)) continue;
    announced.add(a.key);
    void announce(deps, a);
  }
}

function setContext(s: ViewState): void {
  const run = s.mode === "live" ? s.run : undefined;
  const live = run !== undefined && !run.ended;
  void vscode.commands.executeCommand("setContext", "igris.live", live);
  void vscode.commands.executeCommand("setContext", "igris.paused", live && run.paused);
  void vscode.commands.executeCommand("setContext", "igris.limited", live && run.limited !== undefined);
  void vscode.commands.executeCommand("setContext", "igris.needs", run !== undefined && asksOf(run).length > 0);
}

function render(s: ViewState): void {
  setContext(s);
  const now = Date.now();
  tree.set(treeOf(s, now));
  treeView.message = messageOf(s) ?? "";
  lastBar = barOf(s, now);
  if (!lastBar) {
    bar.hide();
    return;
  }
  bar.text = lastBar.text;
  bar.tooltip = lastBar.tooltip;
  bar.backgroundColor =
    lastBar.tone === "needs"
      ? new vscode.ThemeColor("statusBarItem.errorBackground")
      : lastBar.tone === "warn"
        ? new vscode.ThemeColor("statusBarItem.warningBackground")
        : undefined;
  bar.show();
}

/** The status bar's click: the run view, at what waits on the owner. */
async function showRun(): Promise<void> {
  await vscode.commands.executeCommand("igris.run.focus");
  const n = firstNeeds(tree.nodes);
  if (n) await treeView.reveal(n, { select: true, focus: true, expand: true });
}

export function deactivate(): void {
  stopRunView();
}
