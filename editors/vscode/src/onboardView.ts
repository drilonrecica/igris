// Onboarding in the editor (SPEC §15.9): the first-run checklist as a
// QuickPick, an install command put in a terminal for the owner to run
// (never run by the extension), `igris init`, and a run started with one
// button. onboard.ts decides what is shown; this asks the editor.

import { execFile, spawn } from "node:child_process";
import * as fs from "node:fs";
import * as vscode from "vscode";
import { OFFERED_MODES } from "./answers";
import { readStatus } from "./idle";
import { checkInstall } from "./igris";
import { checklist, initArgs, parseDoctor, PLAN_CHOICES, planArgs, readyToStart, startArgs, type DoctorResult, type Row } from "./onboard";
import { line, short } from "./textsafe";

/** Runs igris with argv in dir and gives what it printed (stdout, stderr)
 * whether it exited 0 or not; `undefined` when it could not start. */
function runIgris(args: string[], dir: string, timeout: number): Promise<{ stdout: string; stderr: string; code: number | undefined } | undefined> {
  return new Promise((resolve) => {
    execFile("igris", args, { cwd: dir, timeout, maxBuffer: 16 << 20, windowsHide: true }, (err, stdout, stderr) => {
      if (err && (err as NodeJS.ErrnoException).code === "ENOENT") return resolve(undefined);
      const code = err ? (typeof (err as { code?: unknown }).code === "number" ? ((err as { code: number }).code) : undefined) : 0;
      resolve({ stdout: String(stdout), stderr: String(stderr), code });
    });
  });
}

/** `igris doctor --json` in dir: it exits 1 on a failed check and still
 * prints the document. */
export async function readDoctor(dir: string): Promise<DoctorResult> {
  const got = await runIgris(["doctor", "--json"], dir, 20_000);
  if (!got) return { ok: false, line: "igris is not in PATH" };
  const parsed = parseDoctor(got.stdout);
  if (parsed.ok) return parsed;
  const said = got.stderr.trim().split("\n").filter(Boolean).pop();
  return said ? { ok: false, line: short(said, 300) } : parsed;
}

/** Starts `igris serve …` in root, detached so it outlives the window, with
 * no terminal: its start-up questions get a closed stdin and decline, so a
 * start that needs the owner's answer (a typed phrase, an API key) ends
 * with its reason in .igris/serve.log. Resolves to what went wrong. */
export function startServe(root: string, args: string[]): Promise<string | undefined> {
  return new Promise((resolve) => {
    const child = spawn("igris", args, { cwd: root, detached: true, stdio: "ignore", windowsHide: true });
    child.once("error", (e) => resolve(e.message));
    child.once("spawn", () => {
      child.unref();
      resolve(undefined);
    });
  });
}

function osRelease(): string {
  try {
    return fs.readFileSync("/etc/os-release", "utf8");
  } catch {
    return "";
  }
}

const ICON: Record<Row["level"], string> = { ok: "$(pass)", warn: "$(warning)", fail: "$(error)" };

/** What the onboarding needs of the extension. */
export interface OnboardEnv {
  /** The project's root, or undefined when the workspace has none. */
  root: () => string | undefined;
  /** The first workspace folder on disk: where `igris init` runs. */
  folder: () => string | undefined;
  /** Called after init or a started run so the run view looks again. */
  changed: () => void;
}

/** Puts a command in the Igris terminal, not run: the owner reads it and
 * presses Enter. */
function offerInTerminal(command: string): void {
  const term = vscode.window.terminals.find((t) => t.name === "Igris setup") ?? vscode.window.createTerminal({ name: "Igris setup" });
  term.show();
  term.sendText(command, false);
}

/** The first-run checklist: reads the install and `igris doctor --json`,
 * lists each check, and lets the owner act on one; asks again after. */
export async function onboarding(env: OnboardEnv): Promise<void> {
  for (;;) {
    const dir = env.root() ?? env.folder();
    if (!dir) {
      void vscode.window.showErrorMessage("Igris: open a folder first.");
      return;
    }
    const rows = await vscode.window.withProgress({ location: vscode.ProgressLocation.Notification, title: "Igris: checking this machine…" }, async () => {
      const install = await checkInstall();
      const doctor = install.ok ? await readDoctor(dir) : undefined;
      return checklist({ install, doctor, hasProject: env.root() !== undefined, osRelease: osRelease() });
    });
    const ready = readyToStart(rows);
    // The first line is the thing to do: Start a run when ready, else the
    // first row the owner can act on (checklist() puts those first).
    const items: (vscode.QuickPickItem & { row?: Row; again?: boolean; start?: boolean })[] = [];
    if (ready) items.push({ label: "$(play) Start a run…", detail: "Choose a phase and a mode", start: true });
    for (const r of rows) {
      items.push({
        label: `${ICON[r.level]} ${line(r.label)}`,
        ...(r.detail ? { detail: line(r.detail) } : {}),
        description: r.action.kind === "install" ? "install" : r.action.kind === "init" ? "igris init" : r.action.kind === "docs" ? "open" : "",
        row: r,
      });
    }
    items.push({ label: "$(refresh) Check again", again: true });
    for (;;) {
      const pick = await vscode.window.showQuickPick(items, {
        title: ready ? "Igris: ready" : "Igris: set up",
        placeHolder: ready ? "Everything is in place" : "Pick a line to act on it: an install command goes to a terminal for you to run; ✓ lines are information",
        ignoreFocusOut: true,
      });
      if (!pick) return;
      if (pick.start) return startRun(env);
      if (pick.again) break;
      const a = pick.row?.action;
      if (a?.kind === "install") {
        offerInTerminal(a.command);
        void vscode.window.showInformationMessage(`Igris: run it in the terminal (press Enter), then choose Check again. ${a.command}`);
        return;
      }
      if (a?.kind === "docs") {
        await vscode.env.openExternal(vscode.Uri.parse(a.url));
        return;
      }
      if (a?.kind === "init") {
        await runInit(env);
        break;
      }
      if (a?.kind === "plan") {
        if (!(await createPlan(env))) return;
        break;
      }
      if (a?.kind === "terminal") {
        offerInTerminal(a.command);
        return;
      }
      // An information line: show the list again, nothing to check anew.
    }
  }
}

/** `igris init` in the first workspace folder, as the terminal command
 * does: it never overwrites. */
async function runInit(env: OnboardEnv): Promise<void> {
  const dir = env.folder();
  if (!dir) return;
  const got = await runIgris(initArgs(), dir, 30_000);
  if (!got || got.code !== 0) {
    const said = (got?.stderr ?? "").trim().split("\n").filter(Boolean).pop() ?? "igris init failed";
    void vscode.window.showErrorMessage(`Igris: ${short(said, 300)}`);
    return;
  }
  void vscode.window.setStatusBarMessage("$(check) igris init: done", 4000);
  env.changed();
}

/** The starter plan (SPEC §14): asks which, then `igris init --template X`
 * or `igris init --example` in the project, which never overwrites a plan.
 * True when a plan was written. */
export async function createPlan(env: OnboardEnv): Promise<boolean> {
  const dir = env.root() ?? env.folder();
  if (!dir) {
    void vscode.window.showErrorMessage("Igris: open a folder first.");
    return false;
  }
  const pick = await vscode.window.showQuickPick(
    PLAN_CHOICES.map((c) => ({ label: c.label, detail: c.detail, id: c.id as string })),
    { title: "Igris: which starter plan?", placeHolder: "Written to tasks.md, for you to edit", ignoreFocusOut: true },
  );
  if (!pick) return false;
  const args = planArgs(pick.id);
  if (!args) return false;
  const got = await runIgris(args, dir, 30_000);
  if (!got || got.code !== 0) {
    const said = (got?.stderr ?? "").trim().split("\n").filter(Boolean).pop() ?? `igris ${args.join(" ")} failed`;
    void vscode.window.showErrorMessage(`Igris: ${short(said, 300)}`);
    return false;
  }
  void vscode.window.setStatusBarMessage(`$(check) igris ${args.join(" ")}: done`, 4000);
  env.changed();
  return true;
}

/** The one button: pick a phase and a mode (never a skip-permissions one),
 * confirm once, start `igris serve`. */
export async function startRun(env: OnboardEnv): Promise<void> {
  const root = env.root();
  if (!root) {
    void vscode.window.showErrorMessage("Igris: this folder has no igris project yet; run Igris: Set up first.");
    return;
  }
  const status = await readStatus(root);
  if (!status.ok) {
    void vscode.window.showErrorMessage(`Igris: ${short(status.line, 300)}`);
    return;
  }
  const phases = status.idle.phases;
  const phaseItems = [
    { label: "Next phase with work", description: "igris decides", phase: undefined as string | undefined },
    ...phases.map((p) => ({ label: line(p.id), description: line(p.title), phase: p.id as string | undefined })),
  ];
  const ph = await vscode.window.showQuickPick(phaseItems, { title: "Igris: start a run", placeHolder: "Which phase?", ignoreFocusOut: true });
  if (!ph) return;
  const modeItems = [
    { label: "Project's mode", description: "as igris.toml says", mode: undefined as string | undefined },
    ...OFFERED_MODES.map((m) => ({ label: m, description: "", mode: m as string | undefined })),
  ];
  const md = await vscode.window.showQuickPick(modeItems, { title: "Igris: start a run", placeHolder: "Which mode?", ignoreFocusOut: true });
  if (!md) return;
  const built = startArgs(ph.phase, md.mode);
  if (!built.ok) {
    void vscode.window.showErrorMessage(built.line);
    return;
  }
  const what = `igris ${built.args.map(line).join(" ")}`;
  const ok = await vscode.window.showInformationMessage("Igris: start the run?", { modal: true, detail: `Runs ${what} in this project.` }, "Start");
  if (ok !== "Start") return;
  const err = await startServe(root, built.args);
  if (err !== undefined) void vscode.window.showErrorMessage(`Igris: couldn't start the run: ${short(err, 200)}`);
  else void vscode.window.setStatusBarMessage("$(check) igris: starting the run", 4000);
  env.changed();
}
