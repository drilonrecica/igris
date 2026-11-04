import * as vscode from "vscode";
import { Controller } from "./controller";
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
  let wasRefused = false;
  c.onChange((s) => {
    render(s);
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

function render(s: ViewState): void {
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
