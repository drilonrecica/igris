import * as vscode from "vscode";
import { announce, answer, offer, perform, type Deps, type Ui } from "./act";
import { asksOf, cardActions, modeChoice, runActions, taskModable, taskModeChoice, type Ask, type Choice } from "./answers";
import { GitContent, SCHEME_NAME, viewChanges } from "./changesView";
import { Controller } from "./controller";
import { PlanLens, startOnly } from "./lensView";
import { editorUi } from "./prompts";
import { readStatus } from "./idle";
import { checkInstall } from "./igris";
import { createPlan, onboarding, readDoctor, startRun } from "./onboardView";
import { planMissing } from "./onboard";
import { attachCommand, whereIs } from "./sessions";
import { findRoot, socketPath } from "./socket";
import { SessionTabs, type TabMaker } from "./tabs";
import { EditorTabs } from "./terminals";
import { line, short } from "./textsafe";
import type { Task } from "./protocol";
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
  /** The sessions with a terminal tab open right now. */
  tabs(): { task: string; name: string }[];
  /** Answers through ui instead of the editor's prompts (tests). */
  useUi(ui: Ui | undefined): void;
  /** Opens the session tabs through maker instead of the editor's
   * terminals (tests: no tmux client is started). */
  useTabs(maker: TabMaker | undefined): void;
}

/** The one line of the current refusal, if any (SPEC §15.9): while it holds
 * nothing else of the extension runs. */
let refused: string | undefined;
let extensionVersion = "";
let controller: Controller | undefined;
let tree: RunTree;
let treeView: vscode.TreeView<Node>;
let bar: vscode.StatusBarItem;
let lastBar: Bar | undefined;
let watchers: vscode.Disposable[] = [];
let watchedFiles = "";
let fileWatcher: vscode.Disposable | undefined;
let ui: Ui = editorUi;
const gitContent = new GitContent();
const planLens = new PlanLens();
let editorTabs: EditorTabs;
let tabMaker: TabMaker;
/** The session tabs of the attached run (SPEC §15.9). */
let tabs: SessionTabs | undefined;
let projectDir: string | undefined;
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

/** What onboarding reads of the workspace; a started run or an init has the
 * run view look again. */
const onboardEnv = {
  root: () => projectRoot(),
  folder: () => vscode.workspace.workspaceFolders?.find((f) => f.uri.scheme === "file")?.uri.fsPath,
  changed: lookAgain,
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

/** Opens a task's changes: node's task, else the task that asks, else an
 * active one (SPEC §15.9). */
async function changesCommand(node?: Node): Promise<void> {
  const rootDir = projectDir;
  if (!rootDir) return ui.told(false, "Igris: no igris project in this workspace.");
  const run = liveRun();
  let task = node?.task;
  if (!task) {
    const tasks = [...new Set([...currentAsks().map((a) => a.task), ...(run?.active.map((c) => c.task) ?? [])].filter((t) => t !== ""))];
    task = tasks.length === 1 ? tasks[0] : tasks.length > 1 ? await vscode.window.showQuickPick(tasks, { title: "Igris: whose changes?", ignoreFocusOut: true }) : undefined;
  }
  if (!task) return ui.told(false, "Igris: choose a task in the run view to read its changes.");
  await viewChanges(rootDir, task, run?.run ? [run.run] : [], (l) => ui.told(false, l));
}

/** Starts a run of one task from its plan row (SPEC §15.9): asks once,
 * then `igris serve --only ID` in the project, in the project's own mode. */
async function runTaskCommand(node?: Node): Promise<void> {
  const rootDir = projectDir;
  const task = node?.task;
  if (!rootDir || !task) return ui.told(false, "Igris: choose a task row in the plan to run it.");
  if (liveRun() && !liveRun()?.ended) return ui.told(false, "Igris: a run is already attached; use the run view.");
  const ok = await vscode.window.showInformationMessage(`Igris: run ${line(task)} alone?`, { modal: true, detail: `Starts igris serve --only ${line(task)} in this project, in the mode igris.toml says.` }, "Run");
  if (ok !== "Run") return;
  const err = await startOnly(rootDir, task);
  if (err !== undefined) ui.told(false, `Igris: couldn't start the run: ${short(err, 200)}`);
  else ui.told(true, `igris: running ${line(task)}`);
  controller?.refresh();
}

/** Shows a task's session in its terminal tab, opening the tab again when
 * it was closed; when no tab can show it (herdr, another backend, an older
 * serve, no session yet) a line says where it is (SPEC §15.9). */
async function openSessionCommand(node?: Node): Promise<void> {
  const run = liveRun();
  if (!run || run.ended) return ui.told(false, "Igris: no run is attached.");
  let task = node?.task;
  if (!task) {
    const ids = run.active.map((c) => c.task);
    task = ids.length === 1 ? ids[0] : ids.length > 1 ? await vscode.window.showQuickPick(ids, { title: "Igris: whose session?", ignoreFocusOut: true }) : undefined;
  }
  const card = task !== undefined ? run.active.find((c) => c.task === task) : undefined;
  if (!card) return ui.told(false, task !== undefined ? `Igris: ${line(task)} is not active.` : "Igris: no task is active.");
  if (tabs?.focus(run, card.task)) return;
  await ui.notify(`Igris: ${whereIs(run, card)}`, []);
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
  extensionVersion = String((context.extension.packageJSON as { version?: unknown }).version ?? "");
  editorTabs = new EditorTabs(vscode.Uri.joinPath(context.extensionUri, "resources", "igris.svg"));
  tabMaker = editorTabs;
  context.subscriptions.push(
    tree,
    treeView,
    bar,
    { dispose: stopRunView },
    vscode.window.onDidCloseTerminal((t) => {
      const c = editorTabs.closed(t);
      if (c) tabs?.closed(c.handle, c.failed);
    }),
    vscode.commands.registerCommand("igris.openSession", openSessionCommand),
    vscode.commands.registerCommand("igris.checkInstall", async () => {
      await verify();
      lookAgain();
    }),
    vscode.commands.registerCommand("igris.refresh", lookAgain),
    vscode.commands.registerCommand("igris.showRun", showRun),
    vscode.workspace.registerTextDocumentContentProvider(SCHEME_NAME, gitContent),
    vscode.commands.registerCommand("igris.viewChanges", changesCommand),
    vscode.commands.registerCommand("igris.runTask", runTaskCommand),
    vscode.commands.registerCommand("igris.onboarding", async () => {
      await onboarding(onboardEnv);
      await verify();
      lookAgain();
    }),
    vscode.commands.registerCommand("igris.createPlan", async () => {
      if (refused) return onboarding(onboardEnv);
      await createPlan(onboardEnv);
    }),
    vscode.commands.registerCommand("igris.startRun", async () => {
      if (refused) return onboarding(onboardEnv);
      if (liveRun() && !liveRun()?.ended) return ui.told(false, "Igris: a run is already attached; use the run view.");
      await startRun(onboardEnv);
    }),
    planLens,
    vscode.languages.registerCodeLensProvider({ scheme: "file", language: "markdown" }, planLens),
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
    vscode.workspace.onDidChangeWorkspaceFolders(lookAgain),
  );
  await verify();
  lookAgain();
  return {
    refusal: () => refused,
    view: () => ({
      mode: controller?.state.mode ?? "none",
      bar: lastBar,
      tree: tree.nodes,
      message: treeView.message || undefined,
    }),
    asks: currentAsks,
    tabs: () => tabs?.tabs().map((s) => ({ task: s.task, name: s.name })) ?? [],
    useUi: (u) => {
      ui = u ?? editorUi;
    },
    useTabs: (m) => {
      tabMaker = m ?? editorTabs;
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

/** The refusal: the line as a notification, the view empty. VS Code shows a
 * view's welcome content (here *Set up igris*) only while its tree is empty
 * AND its message is empty, so the view sets no message. */
function showRefusal(line: string): void {
  treeView.message = "";
  tree.set([]);
  bar.hide();
  lastBar = undefined;
  void vscode.window.showWarningMessage(line);
}

let noPlanProbe = 0;
let noPlanShown = false;

/** The first failure a new user meets: `igris status` fails because the
 * project has no plan yet. Doctor says so (its plan-valid check); then the
 * view shows the welcome content with *Create a starter plan* instead of
 * igris's error line (no message: the content shows only without one). */
async function probeNoPlan(s: ViewState): Promise<void> {
  const root = projectDir;
  if (s.mode !== "idle" || s.idleError === undefined || root === undefined) {
    if (noPlanShown) {
      noPlanShown = false;
      void vscode.commands.executeCommand("setContext", "igris.noPlan", false);
    }
    return;
  }
  const n = ++noPlanProbe;
  const missing = planMissing(await readDoctor(root));
  if (n !== noPlanProbe || controller?.state !== s) return;
  noPlanShown = missing;
  void vscode.commands.executeCommand("setContext", "igris.noPlan", missing);
  if (missing) treeView.message = "";
}

/** Looks at the workspace again: while a refusal holds nothing runs; a
 * project that appeared (`igris init`) or went swaps the view; otherwise
 * the view it has refreshes. */
function lookAgain(): void {
  if (refused) return;
  if (controller && projectRoot() === projectDir) {
    controller.refresh();
    return;
  }
  stopRunView();
  startRunView(extensionVersion);
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
  gitContent.setRoot(root);
  projectDir = root;
  void vscode.commands.executeCommand("setContext", "igris.project", root !== undefined);
  if (!root) {
    // No message: the welcome content (*Set up igris*, package.json
    // viewsWelcome) shows only while the tree is empty and the message is.
    treeView.message = "";
    tree.set([]);
    bar.hide();
    lastBar = undefined;
    return;
  }
  const c = new Controller({
    name: `igris-vscode ${version}`.slice(0, 64),
    socket: () => socketPath(root),
    readStatus: () => readStatus(root),
  });
  controller = c;
  tabs = new SessionTabs(
    () => tabMaker,
    String(process.pid),
    (s, code) => ui.told(false, `Igris: the tab for ${s.name} couldn't attach to tmux session ${line(s.host.session)} (tmux exited with code ${code}); run ${attachCommand(s.host)} in a terminal to see why.`),
  );
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
  tabs?.dispose();
  tabs = undefined;
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

/** What the plan's lenses draw from: the tasks the view knows. */
function setLenses(s: ViewState): void {
  const run = s.mode === "live" || s.mode === "reconnecting" ? s.run : undefined;
  const phases = run ? run.phases : (s.idle?.phases ?? []);
  const tasks = new Map<string, Task>();
  for (const p of phases) for (const t of p.tasks) tasks.set(t.id, t);
  planLens.set(projectDir, s.idle?.files, { tasks, live: s.mode === "live" && run !== undefined && !run.ended });
}

function render(s: ViewState): void {
  setContext(s);
  setLenses(s);
  // The tabs follow the run while it is shown, through a reconnect too.
  tabs?.sync(s.mode === "live" || s.mode === "reconnecting" ? s.run : undefined);
  const now = Date.now();
  tree.set(treeOf(s, now));
  treeView.message = messageOf(s) ?? "";
  void probeNoPlan(s);
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
