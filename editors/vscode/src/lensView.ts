// The plan's CodeLens in the editor (SPEC §15.9): lens.ts's lenses on the
// rows of the plan's files, and the run-this-task command that starts a
// headless `igris serve --only ID` as argv, never through a shell.

import { spawn } from "node:child_process";
import * as path from "node:path";
import * as vscode from "vscode";
import { lensesOf, serveArgs, type LensEnv } from "./lens";

/** Serves lenses for the files the run view says are the plan's. */
export class PlanLens implements vscode.CodeLensProvider {
  private root: string | undefined;
  private files = new Set<string>();
  private env: LensEnv = { tasks: new Map(), live: false };
  private readonly changed = new vscode.EventEmitter<void>();
  readonly onDidChangeCodeLenses = this.changed.event;

  /** What the run view knows now; `files` are relative to root and are kept
   * from the last time they were known. */
  set(root: string | undefined, files: string[] | undefined, env: LensEnv): void {
    this.root = root;
    if (files && files.length > 0) this.files = new Set(files.map((f) => path.resolve(root ?? "", f)));
    this.env = env;
    this.changed.fire();
  }

  dispose(): void {
    this.changed.dispose();
  }

  provideCodeLenses(doc: vscode.TextDocument): vscode.CodeLens[] {
    if (!this.root || doc.uri.scheme !== "file" || !this.files.has(path.resolve(doc.uri.fsPath))) return [];
    return lensesOf(doc.getText(), this.env).map((l) => {
      const range = new vscode.Range(l.line, 0, l.line, 0);
      switch (l.kind) {
        case "run":
          return new vscode.CodeLens(range, { title: l.title, command: "igris.runTask", arguments: [{ task: l.task }] });
        case "changes":
          return new vscode.CodeLens(range, { title: l.title, command: "igris.viewChanges", arguments: [{ task: l.task }] });
        default:
          return new vscode.CodeLens(range, { title: l.title, command: "igris.showRun" });
      }
    });
  }
}

/** Starts `igris serve --only task` in root, detached so it outlives the
 * extension host's window; resolves to what went wrong, if anything. */
export function startOnly(root: string, task: string): Promise<string | undefined> {
  return new Promise((resolve) => {
    const child = spawn("igris", serveArgs(task), { cwd: root, detached: true, stdio: "ignore", windowsHide: true });
    child.once("error", (e) => resolve(e.message));
    child.once("spawn", () => {
      child.unref();
      resolve(undefined);
    });
  });
}
