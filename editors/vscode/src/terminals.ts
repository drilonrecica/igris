// The real session tabs (SPEC §15.9): a VS Code terminal per session whose
// process is the tmux client sessions.ts's argv names, started as argv,
// never through a shell. The editor's own terminal tab is the session's.

import * as vscode from "vscode";
import type { TabHandle, TabMaker } from "./tabs";

export class EditorTabs implements TabMaker {
  private readonly handles = new Map<vscode.Terminal, TabHandle>();

  /** icon: the tab's icon, the extension's own. */
  constructor(private readonly icon: vscode.Uri | undefined) {}

  open(name: string, argv: string[]): TabHandle {
    const [shellPath = "tmux", ...shellArgs] = argv;
    const term = vscode.window.createTerminal({
      name,
      shellPath,
      shellArgs,
      ...(this.icon ? { iconPath: this.icon } : {}),
      // Not restored with the window: the tmux client is gone with it.
      isTransient: true,
    });
    // Shown without taking the focus from the editor.
    term.show(true);
    const h: TabHandle = { show: () => term.show(false), dispose: () => term.dispose() };
    this.handles.set(term, h);
    return h;
  }

  /** The handle of a terminal that closed, if it was one of these tabs,
   * and the exit code when its tmux client ended with an error (so not a
   * closed tab, not a detach); the terminal is forgotten. */
  closed(term: vscode.Terminal): { handle: TabHandle; failed: number | undefined } | undefined {
    const h = this.handles.get(term);
    if (!h) return undefined;
    this.handles.delete(term);
    const exit = term.exitStatus;
    const failed = exit?.reason === vscode.TerminalExitReason.Process && exit.code !== undefined && exit.code !== 0 ? exit.code : undefined;
    return { handle: h, failed };
  }
}
